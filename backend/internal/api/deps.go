package api

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/forefy/vig/backend/internal/adapter/solana"
	"github.com/forefy/vig/backend/internal/auth"
	"github.com/forefy/vig/backend/internal/chain"
	"github.com/forefy/vig/backend/internal/config"
	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/github"
	"github.com/forefy/vig/backend/internal/jobs"
	"github.com/forefy/vig/backend/internal/notify"
	"github.com/forefy/vig/backend/internal/store"
)

// Commit is set at build time next to Version.
var Commit = "unknown"

// Deps is everything the handlers need. Only Adapters is required; with a
// nil Store every data operation answers 503 so the contract can still be
// rendered and tested without Postgres.
type Deps struct {
	specs     specCache
	Config    config.Config
	Store     *store.Store
	Signer    *auth.Signer
	Providers map[string]auth.Provider
	Chain     *chain.Client
	Inspector *solana.Inspector
	GitHub    *github.Client
	App       *github.App
	Adapters  map[string]core.Adapter
	// AdapterOrder is how the target cards line up; keys left out follow
	// alphabetically.
	AdapterOrder []string
	Notifier     *notify.Notifier
	Discord      *notify.Discord
	Scans        *jobs.ScanRunner
	HTTP         *http.Client
	// Now is the clock; tests override it.
	Now func() time.Time
	// BaseCtx is the process context fire-and-forget work runs under, so a
	// shutdown cancels it. main sets it; nil means context.Background.
	BaseCtx context.Context

	api       huma.API
	limiter   *limiter
	semOnce   sync.Once
	liveCache sync.Map // program id -> livenessEntry
	sem       chan struct{}
}

// BackgroundSlots bounds the work handlers hand off to goroutines (scan
// runs, tarball warm-ups) so a burst of requests cannot fork without limit.
const BackgroundSlots = 4

// background runs fn on a goroutine under BaseCtx when a slot is free. When
// every slot is busy the work is dropped and false returned; every caller
// has a slower path (the scan worker loop, a lazy checkout) that covers it.
func (d *Deps) background(fn func(ctx context.Context)) bool {
	d.semOnce.Do(func() { d.sem = make(chan struct{}, BackgroundSlots) })
	select {
	case d.sem <- struct{}{}:
	default:
		return false
	}
	ctx := d.BaseCtx
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		defer func() { <-d.sem }()
		fn(ctx)
	}()
	return true
}

// httpClient returns the outbound client, defaulting when unset.
func (d *Deps) httpClient() *http.Client {
	if d.HTTP == nil {
		return &http.Client{Timeout: 30 * time.Second}
	}
	return d.HTTP
}

// Contract returns deps for spec generation and contract tests.
func Contract(adapters map[string]core.Adapter) *Deps {
	return &Deps{Adapters: adapters, Signer: auth.NewSigner("contract", false), Now: time.Now, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func (d *Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *Deps) ready() error {
	if d.Store == nil {
		return huma.Error503ServiceUnavailable("database not configured")
	}
	return nil
}

// notify writes a notification when a notifier is wired.
func (d *Deps) notify(ctx context.Context, userID, kind string, payload map[string]any) {
	if d.Notifier != nil {
		d.Notifier.Notify(context.WithoutCancel(ctx), userID, kind, payload)
	}
}

// fail maps store and domain errors to problem responses.
func fail(err error) error {
	var se huma.StatusError
	if errors.As(err, &se) {
		return err
	}
	var bad store.ErrBadTransition
	var noSlot store.ErrNoSlot
	var reuse store.ErrRefreshReuse
	switch {
	case errors.Is(err, store.ErrNotFound):
		return huma.Error404NotFound("not found")
	case errors.Is(err, store.ErrConflict):
		return huma.Error409Conflict("conflict")
	case errors.Is(err, store.ErrRateLimited):
		return huma.Error429TooManyRequests(err.Error())
	case errors.As(err, &reuse):
		return huma.Error401Unauthorized("refresh token reuse detected; the session was revoked")
	case errors.Is(err, github.ErrAppNotConfigured):
		return huma.Error503ServiceUnavailable("GitHub App not configured")
	case errors.As(err, &bad):
		return huma.Error409Conflict(bad.Error())
	case errors.As(err, &noSlot):
		return huma.Error402PaymentRequired(noSlot.Error())
	case errors.Is(err, core.ErrInsufficientCredits):
		return huma.Error402PaymentRequired("no credits and no active plan")
	case errors.Is(err, github.ErrNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, github.ErrRateLimited):
		return huma.Error429TooManyRequests(err.Error())
	case errors.Is(err, auth.ErrNotConfigured):
		return huma.Error503ServiceUnavailable(err.Error())
	}
	return huma.Error500InternalServerError("internal error", err)
}
