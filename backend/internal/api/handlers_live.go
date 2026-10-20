package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/store"
)

// WatchStatus is where an audit's live surface stands.
type WatchStatus struct {
	State        string                `json:"state" enum:"off,connecting,backfilling,live,error,ended" doc:"off: no session has run yet"`
	Error        string                `json:"error,omitempty"`
	Since        *time.Time            `json:"since,omitempty" doc:"When the watch began"`
	LastEventAt  *time.Time            `json:"last_event_at,omitempty"`
	LastSlot     uint64                `json:"last_slot,omitempty"`
	QuietSeconds int                   `json:"quiet_seconds,omitempty" doc:"Seconds since the newest observation while live: traffic lag on a busy program, silence on a quiet one"`
	EndsAt       *time.Time            `json:"ends_at,omitempty"`
	Watched      []core.WatchedAccount `json:"watched" doc:"Addresses the watch subscribes to"`
}

// LiveView is the feed of a live surface: rows after a cursor, the spans
// that say how complete they are, and the totals since the watch began.
type LiveView struct {
	Watch    WatchStatus            `json:"watch"`
	Evidence []core.SurfaceEvidence `json:"evidence" doc:"Live rows after the cursor, oldest first; the newest when no cursor"`
	Cursor   string                 `json:"cursor,omitempty" doc:"Pass back as after to read what follows"`
	Coverage []core.CoverageSpan    `json:"coverage" doc:"Slot ranges per subject: live, backfill, gap"`
	Totals   []core.NodeTotal       `json:"totals" doc:"Per node since the watch began"`
	Minutes  []core.NodeMinute      `json:"minutes" doc:"Per node per minute, the last hour"`
	Activity []core.ActivityBucket  `json:"activity" doc:"Calls over the whole watch, all nodes, in buckets sized so the series stays short"`
	Bucket   int                    `json:"bucket" doc:"Activity bucket width in seconds"`
}

type liveIn struct {
	ID     string    `path:"id"`
	After  string    `query:"after" doc:"Evidence id the last read ended at"`
	Before time.Time `query:"before" doc:"Read the newest rows at or before this time instead of following the cursor"`
	Since  time.Time `query:"since" doc:"With before: read rows after this time and up to before, oldest first"`
	Full   bool      `query:"full" doc:"Only rows read in full (keys, signers, arguments), the newest; the cursor is not moved"`
	Limit  int       `query:"limit" minimum:"1" maximum:"1000" doc:"Rows per read; default 200"`
}

const (
	liveRowsDefault = 200
	coverageSpans   = 500
	activityPoints  = 1500
)

func (d *Deps) registerLive(api huma.API) {
	huma.Register(api, withAuth(op("getAuditLive", http.MethodGet, "/v1/audits/{id}/surface/live", "Live surface feed", "audits"), userAuth), d.getAuditLive)
	huma.Register(api, withAuth(op("adminGetAuditLive", http.MethodGet, "/v1/admin/audits/{id}/surface/live", "Any audit's live surface feed", "admin"), adminAuth), d.adminGetAuditLive)
	huma.Register(api, withAuth(op("getJobLive", http.MethodGet, "/v1/op/jobs/{id}/surface/live", "The job's live surface feed", "operator"), operatorAuth), d.getJobLive)
}

// liveView assembles the feed.
func (d *Deps) liveView(ctx context.Context, a store.Audit, after string, since, before time.Time, full bool, limit int) (LiveView, error) {
	if limit <= 0 {
		limit = liveRowsDefault
	}
	view := LiveView{Watch: WatchStatus{State: "off", Watched: []core.WatchedAccount{}}, Evidence: []core.SurfaceEvidence{}, Coverage: []core.CoverageSpan{}, Totals: []core.NodeTotal{}, Minutes: []core.NodeMinute{}, Activity: []core.ActivityBucket{}, Bucket: 60}
	wt, err := d.Store.GetWatch(ctx, a.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return view, nil
	case err != nil:
		return view, err
	}
	now := d.now()
	view.Watch = WatchStatus{State: string(wt.State), Error: wt.Error, Since: &wt.StartedAt, LastEventAt: wt.LastEventAt, LastSlot: wt.LastSlot, EndsAt: wt.EndsAt, Watched: wt.Accounts}
	if view.Watch.Watched == nil {
		view.Watch.Watched = []core.WatchedAccount{}
	}
	if wt.State == core.WatchLive && wt.LastEventAt != nil {
		view.Watch.QuietSeconds = int(now.Sub(*wt.LastEventAt).Seconds())
	}
	var rows []core.SurfaceEvidence
	if full {
		rows, err = d.Store.ListLiveEvidenceFull(ctx, a.ID, limit)
		view.Cursor = after
	} else if !before.IsZero() {
		// A read into the past: the cursor is not moved, so the follower
		// picks up where it was. A range plays forward; a point looks back.
		if !since.IsZero() {
			rows, err = d.Store.ListLiveEvidenceBetween(ctx, a.ID, since, before, limit)
		} else {
			rows, err = d.Store.ListLiveEvidenceBefore(ctx, a.ID, before, limit)
		}
		view.Cursor = after
	} else {
		rows, err = d.Store.ListLiveEvidence(ctx, a.ID, after, limit)
		if len(rows) > 0 {
			view.Cursor = rows[len(rows)-1].ID
		} else {
			view.Cursor = after
		}
	}
	if err != nil {
		return view, err
	}
	view.Evidence = rows
	if view.Coverage, err = d.Store.ListCoverage(ctx, a.ID, coverageSpans); err != nil {
		return view, err
	}
	if view.Totals, err = d.Store.RollupTotals(ctx, a.ID); err != nil {
		return view, err
	}
	if view.Minutes, err = d.Store.RollupMinutes(ctx, a.ID, now.Add(-time.Hour).Truncate(time.Minute)); err != nil {
		return view, err
	}
	// The whole watch in at most activityPoints buckets.
	bucket := time.Minute
	for age := now.Sub(wt.StartedAt); age/bucket > activityPoints; {
		bucket *= 2
	}
	view.Bucket = int(bucket / time.Second)
	if view.Activity, err = d.Store.RollupBuckets(ctx, a.ID, bucket); err != nil {
		return view, err
	}
	return view, nil
}

func (d *Deps) getAuditLive(ctx context.Context, in *liveIn) (*body[LiveView], error) {
	a, _, err := d.ownedAudit(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	v, err := d.liveView(ctx, a, in.After, in.Since, in.Before, in.Full, in.Limit)
	if err != nil {
		return nil, fail(err)
	}
	return &body[LiveView]{Body: v}, nil
}

func (d *Deps) adminGetAuditLive(ctx context.Context, in *liveIn) (*body[LiveView], error) {
	a, err := d.Store.GetAudit(ctx, in.ID)
	if err != nil {
		return nil, fail(err)
	}
	v, err := d.liveView(ctx, a, in.After, in.Since, in.Before, in.Full, in.Limit)
	if err != nil {
		return nil, fail(err)
	}
	return &body[LiveView]{Body: v}, nil
}

func (d *Deps) getJobLive(ctx context.Context, in *liveIn) (*body[LiveView], error) {
	a, _, err := d.job(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	v, err := d.liveView(ctx, a, in.After, in.Since, in.Before, in.Full, in.Limit)
	if err != nil {
		return nil, fail(err)
	}
	return &body[LiveView]{Body: v}, nil
}
