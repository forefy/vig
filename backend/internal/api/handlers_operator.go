package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/forefy/vig/backend/internal/auth"
	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/github"
	"github.com/forefy/vig/backend/internal/store"
)

type opEventIn struct {
	ID   string `path:"id"`
	Body struct {
		State   core.AuditState `json:"state" enum:"cloning,programs_verified,analysis,human_review,delivered"`
		Payload map[string]any  `json:"payload,omitempty"`
	}
}

type opProgramsIn struct {
	ID   string `path:"id"`
	Body struct {
		Programs []core.ProgramPin `json:"programs" minItems:"1"`
	}
}

type opReportIn struct {
	ID   string `path:"id"`
	Body core.FindingsDocument
}

type opDelayIn struct {
	ID   string `path:"id"`
	Body struct {
		ETAAt  string `json:"eta_at" format:"date-time"`
		Reason string `json:"reason" minLength:"1"`
	}
}

type opFailIn struct {
	ID   string `path:"id"`
	Body struct {
		Reason string `json:"reason" minLength:"1"`
	}
}

func (d *Deps) registerOperator(api huma.API) {
	huma.Register(api, withAuth(op("claimJob", http.MethodPost, "/v1/op/jobs/claim", "Claim the next queued audit", "operator"), operatorAuth), d.claimJob)
	huma.Register(api, withAuth(op("postJobEvent", http.MethodPost, "/v1/op/jobs/{id}/events", "Advance the job state", "operator"), operatorAuth), d.postJobEvent)
	huma.Register(api, withAuth(op("postJobPrograms", http.MethodPost, "/v1/op/jobs/{id}/programs", "Record verified bytecode hashes and match status", "operator"), operatorAuth), d.postJobPrograms)
	huma.Register(api, withAuth(op("postJobReport", http.MethodPost, "/v1/op/jobs/{id}/report", "Submit findings", "operator"), operatorAuth), d.postJobReport)
	huma.Register(api, withAuth(op("delayJob", http.MethodPost, "/v1/op/jobs/{id}/delay", "Delay with a new ETA", "operator"), operatorAuth), d.delayJob)
	huma.Register(api, withAuth(op("failJob", http.MethodPost, "/v1/op/jobs/{id}/fail", "Fail and refund", "operator"), operatorAuth), d.failJob)
}

// job loads the audit the operator token is bound to. A token that has not
// claimed, or that is bound to another audit, is refused.
func (d *Deps) job(ctx context.Context, id string) (store.Audit, string, error) {
	p := principal(ctx)
	if p.OperatorAuditID == "" || p.OperatorAuditID != id {
		return store.Audit{}, "", huma.Error403Forbidden("token is not bound to this job")
	}
	a, err := d.Store.GetAudit(ctx, id)
	if err != nil {
		return a, "", fail(err)
	}
	return a, p.OperatorTokenID, nil
}

// dropClone removes the cached checkout once a job is terminal, unless
// another live audit still reads the same repository at the same commit.
func (d *Deps) dropClone(ctx context.Context, a store.Audit) {
	if d.GitHub == nil {
		return
	}
	n, err := d.Store.CountActiveAuditsForClone(ctx, a.Repo, a.HeadSHA, a.ID)
	if err != nil || n > 0 {
		return
	}
	_ = d.GitHub.RemoveClone(a.Repo, a.HeadSHA)
}

func (d *Deps) claimJob(ctx context.Context, _ *struct{}) (*body[Job], error) {
	p := principal(ctx)
	a, err := d.Store.ClaimJob(ctx, p.OperatorTokenID)
	if err != nil {
		return nil, fail(err)
	}
	out, err := d.auditWithEvents(ctx, a)
	if err != nil {
		return nil, fail(err)
	}
	tok, err := d.Store.OperatorTokenByHash(ctx, auth.Hash(req(ctx).Bearer))
	if err != nil {
		return nil, fail(err)
	}
	t, err := d.Store.GetTarget(ctx, a.TargetID)
	if err != nil {
		return nil, fail(err)
	}
	target, err := d.toTarget(ctx, t)
	if err != nil {
		return nil, fail(err)
	}
	d.notify(ctx, a.UserID, "audit_state", map[string]any{"audit_id": a.ID, "state": string(a.State)})
	job := Job{Audit: out, Target: target, Token: req(ctx).Bearer, TokenExpiresAt: tok.ExpiresAt, CloneURL: "https://github.com/" + a.Repo + ".git"}
	// What people corrected on earlier reports of this code rides with the
	// job, so the operator (or agent) reads the lessons before the code.
	if lessons, err := d.Store.ListLessons(ctx, a.Repo, 100); err == nil {
		for _, l := range lessons {
			job.Lessons = append(job.Lessons, toLesson(l))
		}
	}
	if t.SourceKind == "installation" {
		// Private source: mint a one-hour token scoped to this repository for
		// the operator's clone. It is returned once and never stored.
		it, err := d.installationToken(ctx, t)
		if err != nil {
			return nil, err
		}
		job.CloneToken = it.Token
		job.CloneTokenExpires = &it.ExpiresAt
	}
	return &body[Job]{Body: job}, nil
}

func (d *Deps) postJobEvent(ctx context.Context, in *opEventIn) (*body[Audit], error) {
	a, tokenID, err := d.job(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	a, err = d.Store.Advance(ctx, a.ID, in.Body.State, "operator", tokenID, in.Body.Payload)
	if err != nil {
		return nil, fail(err)
	}
	if a.State == core.StateDelivered {
		d.dropClone(ctx, a)
		d.notify(ctx, a.UserID, "audit_delivered", map[string]any{"audit_id": a.ID, "report_id": a.ReportID})
	} else {
		d.notify(ctx, a.UserID, "audit_state", map[string]any{"audit_id": a.ID, "state": string(a.State)})
	}
	out, err := d.auditWithEvents(ctx, a)
	if err != nil {
		return nil, fail(err)
	}
	return &body[Audit]{Body: out}, nil
}

func (d *Deps) postJobPrograms(ctx context.Context, in *opProgramsIn) (*body[Audit], error) {
	a, tokenID, err := d.job(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if a.State.Terminal() {
		return nil, huma.Error409Conflict("audit is " + string(a.State))
	}
	inAudit := map[string]bool{}
	for _, id := range a.ProgramIDs {
		inAudit[id] = true
	}
	recorded := make([]map[string]any, 0, len(in.Body.Programs))
	for _, pin := range in.Body.Programs {
		if !inAudit[pin.ID] {
			return nil, huma.Error400BadRequest("program " + pin.ID + " is not in this audit")
		}
		if pin.Match == "" || pin.BytecodeSHA256 == "" {
			return nil, huma.Error400BadRequest("each program needs bytecode_sha256 and match")
		}
		if err := d.Store.SetProgramMatch(ctx, d.Store.Pool, a.TargetID, pin); err != nil {
			return nil, fail(err)
		}
		recorded = append(recorded, map[string]any{"id": pin.ID, "slot": pin.Slot, "bytecode_sha256": pin.BytecodeSHA256, "match": string(pin.Match)})
	}
	if err := d.Store.AddEvent(ctx, d.Store.Pool, store.AuditEvent{AuditID: a.ID, Kind: "programs_recorded", Actor: "operator", ActorID: tokenID, State: a.State, Payload: map[string]any{"programs": recorded}}); err != nil {
		return nil, fail(err)
	}
	out, err := d.auditWithEvents(ctx, a)
	if err != nil {
		return nil, fail(err)
	}
	return &body[Audit]{Body: out}, nil
}

func (d *Deps) postJobReport(ctx context.Context, in *opReportIn) (*body[Report], error) {
	a, _, err := d.job(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if a.State.Terminal() {
		return nil, huma.Error409Conflict("audit is " + string(a.State))
	}
	doc := in.Body
	if doc.Report.AuditID == "" {
		doc.Report.AuditID = a.ID
	}
	if doc.Report.AuditID != a.ID {
		return nil, huma.Error400BadRequest("report.audit_id does not match the job")
	}
	if err := d.bindVisuals(ctx, a.ID, &doc); err != nil {
		return nil, err
	}
	if err := doc.Validate(); err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	if open := doc.Unexhausted(false); len(open) > 0 {
		return nil, huma.Error422UnprocessableEntity(strings.Join(open, "; "))
	}
	if doc.Report.Pin.Repo != a.Repo || doc.Report.Pin.Commit != a.HeadSHA {
		return nil, huma.Error422UnprocessableEntity("report.pin must match the audited repo and commit")
	}
	// Capture the code each finding points at while the checkout still exists.
	if d.GitHub != nil {
		if root, ok := d.GitHub.Cached(a.Repo, a.HeadSHA); ok {
			github.AttachSnippets(root, doc.Findings)
		}
		d.GitHub.CaptureReferences(ctx, &doc)
	}
	r, err := d.Store.StoreReport(ctx, a.ID, doc)
	if err != nil {
		return nil, fail(err)
	}
	d.extendWebSurface(ctx, a, doc.Findings)
	return &body[Report]{Body: toReport(r, true)}, nil
}

func (d *Deps) delayJob(ctx context.Context, in *opDelayIn) (*body[Audit], error) {
	a, tokenID, err := d.job(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	eta, err := time.Parse(time.RFC3339, in.Body.ETAAt)
	if err != nil {
		return nil, huma.Error400BadRequest("eta_at must be RFC 3339")
	}
	if !eta.After(d.now()) {
		return nil, huma.Error400BadRequest("eta_at must be in the future")
	}
	a, err = d.Store.DelayAudit(ctx, a.ID, eta, in.Body.Reason, "operator", tokenID)
	if err != nil {
		return nil, fail(err)
	}
	d.notify(ctx, a.UserID, "audit_delayed", map[string]any{"audit_id": a.ID, "eta_at": eta.UTC().Format(time.RFC3339), "reason": in.Body.Reason})
	out, err := d.auditWithEvents(ctx, a)
	if err != nil {
		return nil, fail(err)
	}
	return &body[Audit]{Body: out}, nil
}

func (d *Deps) failJob(ctx context.Context, in *opFailIn) (*body[Audit], error) {
	a, tokenID, err := d.job(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	a, err = d.Store.FailAudit(ctx, a.ID, in.Body.Reason, "operator", tokenID)
	if err != nil {
		return nil, fail(err)
	}
	d.dropClone(ctx, a)
	d.notify(ctx, a.UserID, "audit_failed", map[string]any{"audit_id": a.ID, "reason": in.Body.Reason})
	out, err := d.auditWithEvents(ctx, a)
	if err != nil {
		return nil, fail(err)
	}
	return &body[Audit]{Body: out}, nil
}
