package consumer

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	toolsozone "github.com/bluesky-social/indigo/api/ozone"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/bluesky-social/indigo/automod"
	"github.com/bluesky-social/indigo/xrpc"

	"github.com/redis/go-redis/v9"
)

// TODO: should probably make this not hepa-specific; or even configurable
var ozoneCursorKey = "hepa/ozoneTimestamp"

type OzoneConsumer struct {
	Logger      *slog.Logger
	RedisClient *redis.Client
	OzoneClient *xrpc.Client
	Engine      *automod.Engine

	// same as lastSeq, but for Ozone timestamp cursor. the value is a string.
	lastCursor atomic.Value
}

func (oc *OzoneConsumer) Run(ctx context.Context) error {

	if oc.Engine == nil {
		return fmt.Errorf("nil engine")
	}
	if oc.OzoneClient == nil {
		return fmt.Errorf("nil ozoneclient")
	}

	cur, err := oc.ReadLastCursor(ctx)
	if err != nil {
		return err
	}

	if cur == "" {
		cur = syntax.DatetimeNow().String()
	}
	since, err := syntax.ParseDatetime(cur)
	if err != nil {
		return err
	}

	oc.Logger.Info("subscribing to ozone event log", "upstream", oc.OzoneClient.Host, "cursor", cur, "since", since)
	var limit int64 = 50
	period := time.Second * 5

	for {
		//func ModerationQueryEvents(ctx context.Context, c *xrpc.Client, addedLabels []string, addedTags []string, comment string, createdAfter string, createdBefore string, createdBy string, cursor string, hasComment bool, includeAllUserRecords bool, limit int64, removedLabels []string, removedTags []string, reportTypes []string, sortDirection string, subject string, types []string) (*ModerationQueryEvents_Output, error) {
		me, err := toolsozone.ModerationQueryEvents(
			ctx,
			oc.OzoneClient,
			nil,            // addedLabels: If specified, only events where all of these labels were added are returned
			nil,            // addedTags: If specified, only events where all of these tags were added are returned
			"",             // comment: If specified, only events with comments containing the keyword are returned
			since.String(), // createdAfter: Retrieve events created after a given timestamp
			"",             // createdBefore: Retrieve events created before a given timestamp
			"",             // createdBy
			"",             // cursor
			false,          // hasComment: If true, only events with comments are returned
			true,           // includeAllUserRecords: If true, events on all record types (posts, lists, profile etc.) owned by the did are returned
			limit,
			nil,   // removedLabels: If specified, only events where all of these labels were removed are returned
			nil,   // removedTags
			nil,   // reportTypes
			"asc", // sortDirection: Sort direction for the events. Defaults to descending order of created at timestamp.
			"",    // subject
			nil,   // types: The types of events (fully qualified string in the format of tools.ozone.moderation.defs#modEvent<name>) to filter by. If not specified, all events are returned.
		)
		if err != nil {
			oc.Logger.Warn("ozone query events failed; sleeping then will retrying", "err", err, "period", period.String())
			time.Sleep(period)
			continue
		}

		// track if the response contained anything new
		anyNewEvents := false
		for _, evt := range me.Events {
			createdAt, err := syntax.ParseDatetime(evt.CreatedAt)
			if err != nil {
				return fmt.Errorf("invalid time format for ozone 'createdAt': %w", err)
			}
			// skip if the timestamp is the exact same
			if createdAt == since {
				continue
			}
			anyNewEvents = true
			// TODO: is there a race condition here?
			if !createdAt.Time().After(since.Time()) {
				oc.Logger.Error("out of order ozone event", "createdAt", createdAt, "since", since)
				return fmt.Errorf("out of order ozone event")
			}
			if err = oc.HandleOzoneEvent(ctx, evt); err != nil {
				oc.Logger.Error("failed to process ozone event", "event", evt)
			}
			since = createdAt
			oc.lastCursor.Store(since.String())
		}
		if !anyNewEvents {
			oc.Logger.Debug("... ozone poller sleeping", "period", period.String())
			time.Sleep(period)
		}
	}
}

func (oc *OzoneConsumer) HandleOzoneEvent(ctx context.Context, eventView *toolsozone.ModerationDefs_ModEventView) error {

	oc.Logger.Debug("received ozone event", "eventID", eventView.Id, "createdAt", eventView.CreatedAt)

	if err := oc.Engine.ProcessOzoneEvent(ctx, eventView); err != nil {
		oc.Logger.Error("engine failed to process ozone event", "err", err)
	}
	return nil
}

func (oc *OzoneConsumer) ReadLastCursor(ctx context.Context) (string, error) {
	// if redis isn't configured, just skip
	if oc.RedisClient == nil {
		oc.Logger.Info("redis not configured, skipping ozone cursor read")
		return "", nil
	}

	val, err := oc.RedisClient.Get(ctx, ozoneCursorKey).Result()
	if err == redis.Nil || val == "" {
		oc.Logger.Info("no pre-existing ozone cursor in redis")
		return "", nil
	} else if err != nil {
		return "", err
	}
	oc.Logger.Info("successfully found prior ozone offset timestamp in redis", "cursor", val)
	return val, nil
}

func (oc *OzoneConsumer) PersistCursor(ctx context.Context) error {
	// if redis isn't configured, just skip
	if oc.RedisClient == nil {
		return nil
	}
	lastCursor := oc.lastCursor.Load()
	if lastCursor == nil || lastCursor == "" {
		return nil
	}
	err := oc.RedisClient.Set(ctx, ozoneCursorKey, lastCursor, 14*24*time.Hour).Err()
	return err
}

// this method runs in a loop, persisting the current cursor state every 5 seconds
func (oc *OzoneConsumer) RunPersistCursor(ctx context.Context) error {

	// if redis isn't configured, just skip
	if oc.RedisClient == nil {
		return nil
	}
	ticker := time.NewTicker(5 * time.Second)
	for {
		select {
		case <-ctx.Done():
			lastCursor := oc.lastCursor.Load()
			if lastCursor != nil && lastCursor != "" {
				oc.Logger.Info("persisting final ozone cursor timestamp", "cursor", lastCursor)
				err := oc.PersistCursor(ctx)
				if err != nil {
					oc.Logger.Error("failed to persist ozone cursor", "err", err, "cursor", lastCursor)
				}
			}
			return nil
		case <-ticker.C:
			lastCursor := oc.lastCursor.Load()
			if lastCursor != nil && lastCursor != "" {
				err := oc.PersistCursor(ctx)
				if err != nil {
					oc.Logger.Error("failed to persist ozone cursor", "err", err, "cursor", lastCursor)
				}
			}
		}
	}
}