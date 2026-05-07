// Adapter from internal/updatepoll's State (which carries the
// poller's wire-shape PendingUpdate) to the API package's
// UpdatePollSnapshot interface (which carries the API's wire-shape
// PendingSoftwareUpdate). The translation is field-for-field;
// keeping the two types separate avoids a cross-package dependency
// between updatepoll and api.

package core

import (
	"time"

	"github.com/bluenviron/mediamtx/internal/api"
	"github.com/bluenviron/mediamtx/internal/updatepoll"
)

type updatePollSnapshot struct {
	state *updatepoll.State
}

func (s updatePollSnapshot) Pending() *api.PendingSoftwareUpdate {
	if s.state == nil {
		return nil
	}
	p := s.state.Pending()
	if p == nil {
		return nil
	}
	return &api.PendingSoftwareUpdate{
		LifecycleID:    p.LifecycleID,
		State:          p.State,
		StateChangedAt: p.StateChangedAt,
		ManifestID:     p.ManifestID,
		Version:        p.Version,
		Channel:        p.Channel,
		ReleaseNotes:   p.ReleaseNotes,
	}
}

func (s updatePollSnapshot) LastPoll() time.Time {
	if s.state == nil {
		return time.Time{}
	}
	return s.state.LastPoll()
}
