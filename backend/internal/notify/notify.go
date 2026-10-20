// Package notify writes in-app notifications (the source of truth) and fans
// them out to external channels asynchronously. Notify only writes the row;
// the Dispatcher delivers to Discord and email with retries, so no request
// handler ever waits on a third-party call.
package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/forefy/vig/backend/internal/store"
)

// ErrNotConfigured is returned by a sender that has no credentials.
var ErrNotConfigured = errors.New("channel not configured")

// Sender delivers one message to one external channel.
type Sender interface {
	// Name is the channel key stored on the notification.
	Name() string
	// Configured reports whether Send can work at all.
	Configured() bool
	// Send delivers a message to the recipient address on this channel (a
	// Discord user id, an email address).
	Send(ctx context.Context, to, subject, text string) error
}

// Notifier stores notifications and decides which channels each one gets.
type Notifier struct {
	Store   *store.Store
	Senders []Sender
}

// New builds a notifier with the given channels.
func New(s *store.Store, senders ...Sender) *Notifier {
	return &Notifier{Store: s, Senders: senders}
}

// Retry schedule: attempt n that fails waits Backoff[n-1] before the next.
var Backoff = []time.Duration{1 * time.Minute, 5 * time.Minute, 30 * time.Minute}

// MaxAttempts bounds external delivery tries per notification.
const MaxAttempts = 3

// recipients resolves the address for each configured channel the user can
// receive on. Email needs a verified address on an identity and the user's
// preference on.
func (n *Notifier) recipients(ctx context.Context, userID string) (map[string]string, error) {
	out := map[string]string{}
	idents, err := n.Store.ListIdentities(ctx, userID)
	if err != nil {
		return nil, err
	}
	u, err := n.Store.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, s := range n.Senders {
		if !s.Configured() {
			continue
		}
		switch s.Name() {
		case "email":
			if !u.NotifyEmail || u.DeletedAt != nil {
				continue
			}
			for _, id := range idents {
				if id.Email != "" {
					out["email"] = id.Email
					break
				}
			}
		default:
			for _, id := range idents {
				if id.Provider == s.Name() {
					out[s.Name()] = id.ProviderID
					break
				}
			}
		}
	}
	return out, nil
}

// Notify writes the notification with its channels: in-app is delivered on
// the spot, every other channel is queued for the Dispatcher. It never
// blocks on the network and never returns an error to the caller.
func (n *Notifier) Notify(ctx context.Context, userID, kind string, payload map[string]any) {
	if n == nil || n.Store == nil {
		return
	}
	channels := []string{"inapp"}
	if rcpts, err := n.recipients(ctx, userID); err == nil {
		for _, s := range n.Senders {
			if _, ok := rcpts[s.Name()]; ok {
				channels = append(channels, s.Name())
			}
		}
	} else {
		slog.Warn("notify: resolve recipients", "user", userID, "err", err)
	}
	if _, err := n.Store.CreateNotification(ctx, store.Notification{UserID: userID, Kind: kind, Payload: payload, Channels: channels}); err != nil {
		slog.Error("notify: write", "user", userID, "kind", kind, "err", err)
	}
}

// Dispatcher delivers queued external channels.
type Dispatcher struct {
	Notifier *Notifier
	Every    time.Duration
	Batch    int
}

// Loop runs until ctx ends.
func (d *Dispatcher) Loop(ctx context.Context) {
	every := d.Every
	if every == 0 {
		every = 5 * time.Second
	}
	for {
		if err := d.Once(ctx); err != nil && ctx.Err() == nil {
			slog.Error("notify dispatcher", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// Once claims due notifications and attempts every pending channel. Results
// land in delivered; a failed channel is retried on the backoff schedule
// until MaxAttempts, after which its last error stays recorded.
func (d *Dispatcher) Once(ctx context.Context) error {
	n := d.Notifier
	if n == nil || n.Store == nil {
		return nil
	}
	batch := d.Batch
	if batch == 0 {
		batch = 50
	}
	rows, err := n.Store.ClaimDueNotifications(ctx, batch, 2*time.Minute)
	if err != nil {
		return err
	}
	for _, row := range rows {
		d.deliver(ctx, row)
	}
	return nil
}

func (d *Dispatcher) deliver(ctx context.Context, row store.Notification) {
	n := d.Notifier
	rcpts, err := n.recipients(ctx, row.UserID)
	if err != nil {
		slog.Warn("notify: recipients", "notification", row.ID, "err", err)
		rcpts = map[string]string{}
	}
	senders := map[string]Sender{}
	for _, s := range n.Senders {
		senders[s.Name()] = s
	}
	subject, text := Render(row.Kind, row.Payload)
	results := map[string]string{}
	failed := false
	for _, ch := range row.Pending() {
		s, ok := senders[ch]
		if !ok || !s.Configured() {
			results[ch] = "skipped: channel not configured"
			continue
		}
		to, ok := rcpts[ch]
		if !ok {
			results[ch] = "skipped: no recipient"
			continue
		}
		sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := s.Send(sctx, to, subject, text)
		cancel()
		if err == nil {
			results[ch] = "ok"
			continue
		}
		failed = true
		if row.Attempts >= MaxAttempts {
			results[ch] = "failed: " + err.Error()
		} else {
			slog.Warn("notify: send", "notification", row.ID, "channel", ch, "attempt", row.Attempts, "err", err)
		}
	}
	var next *time.Time
	if failed && row.Attempts < MaxAttempts {
		wait := Backoff[len(Backoff)-1]
		if row.Attempts-1 < len(Backoff) {
			wait = Backoff[row.Attempts-1]
		}
		t := time.Now().Add(wait)
		next = &t
	}
	if err := n.Store.RecordDelivery(context.WithoutCancel(ctx), row.ID, results, next); err != nil {
		slog.Error("notify: record delivery", "notification", row.ID, "err", err)
	}
}

// Render returns a subject and a short body for external channels.
func Render(kind string, payload map[string]any) (subject, text string) {
	text = Text(kind, payload)
	subject = strings.TrimPrefix(text, "vig: ")
	if i := strings.IndexAny(subject, ",.\n"); i > 0 {
		subject = subject[:i]
	}
	return subject, text
}

// Text renders a short line for external channels.
func Text(kind string, payload map[string]any) string {
	get := func(k string) string {
		v, _ := payload[k].(string)
		return v
	}
	switch kind {
	case "audit_state":
		return fmt.Sprintf("vig: audit %s is now %s", get("audit_id"), get("state"))
	case "audit_delivered":
		return fmt.Sprintf("vig: report ready for audit %s", get("audit_id"))
	case "audit_delayed":
		return fmt.Sprintf("vig: audit %s delayed, new ETA %s", get("audit_id"), get("eta_at"))
	case "audit_overdue":
		return fmt.Sprintf("vig: audit %s is past its ETA of %s", get("audit_id"), get("eta_at"))
	case "audit_cancelled":
		return fmt.Sprintf("vig: audit %s was cancelled, credit restored", get("audit_id"))
	case "report_revised":
		return fmt.Sprintf("vig: report for audit %s revised (rev %v)", get("audit_id"), payload["revision"])
	case "audit_failed":
		return fmt.Sprintf("vig: audit %s failed, credit restored", get("audit_id"))
	case "claim_approved":
		return "vig: your free audit credit was granted"
	case "claim_rejected":
		return "vig: your post link was not accepted"
	case "claim_revoked":
		return "vig: your free audit credit was revoked"
	case "credits_granted":
		return fmt.Sprintf("vig: %v credit(s) added", payload["delta"])
	case "credits_revoked":
		return fmt.Sprintf("vig: %v credit(s) removed", payload["delta"])
	case "surface_drift":
		what := "was upgraded"
		if get("kind") == "idl" {
			what = "changed its interface"
		}
		noun := "program"
		if get("adapter") == "ethereum" {
			noun = "contract"
		}
		return fmt.Sprintf("vig: %s %s in audit %s %s", noun, get("program"), get("audit_id"), what)
	case "purchase_confirmed":
		return fmt.Sprintf("vig: purchase %s confirmed", get("sku"))
	case "purchase_expired":
		return fmt.Sprintf("vig: purchase %s expired before a payment arrived", get("sku"))
	}
	return "vig: " + kind
}
