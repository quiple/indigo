// Code generated by cmd/lexgen (see Makefile's lexgen); DO NOT EDIT.

package ozone

// schema: tools.ozone.team.defs

import (
	appbskytypes "github.com/bluesky-social/indigo/api/bsky"
)

// TeamDefs_Member is a "member" in the tools.ozone.team.defs schema.
type TeamDefs_Member struct {
	CreatedAt     *string                                     `json:"createdAt,omitempty" cborgen:"createdAt,omitempty"`
	Did           string                                      `json:"did" cborgen:"did"`
	Disabled      *bool                                       `json:"disabled,omitempty" cborgen:"disabled,omitempty"`
	LastUpdatedBy *string                                     `json:"lastUpdatedBy,omitempty" cborgen:"lastUpdatedBy,omitempty"`
	Profile       *appbskytypes.ActorDefs_ProfileViewDetailed `json:"profile,omitempty" cborgen:"profile,omitempty"`
	Role          string                                      `json:"role" cborgen:"role"`
	UpdatedAt     *string                                     `json:"updatedAt,omitempty" cborgen:"updatedAt,omitempty"`
}