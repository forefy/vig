package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/forefy/vig/backend/internal/auth"
	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/github"
	"github.com/forefy/vig/backend/internal/store"
)

// OperatorTokenPrefix starts every job-scoped operator token.
const OperatorTokenPrefix = "vop_"

type reassignIn struct {
	ID   string `path:"id"`
	Body struct {
		Reason string `json:"reason" minLength:"1"`
	}
}

type creditIn struct {
	Body struct {
		UserID string `json:"user_id"`
		Delta  int    `json:"delta" minimum:"1"`
		Reason string `json:"reason" minLength:"1"`
	}
}

type claimReviewIn struct {
	ID   string `path:"id"`
	Body struct {
		Reason string `json:"reason,omitempty"`
	}
}

type adminDelayIn struct {
	ID   string `path:"id"`
	Body struct {
		ETAAt  string `json:"eta_at" format:"date-time"`
		Reason string `json:"reason" minLength:"1"`
	}
}

type adminAuditsIn struct {
	State   string `query:"state" enum:"queued,claimed,cloning,programs_verified,analysis,human_review,delivered,delayed,failed,cancelled" required:"false"`
	UserID  string `query:"user_id" required:"false"`
	Adapter string `query:"adapter" enum:"solana,ethereum,webapp,repo" required:"false"`
	Hidden  string `query:"hidden" enum:"true,false" required:"false" doc:"Only hidden or only visible audits; both when absent"`
	Q       string `query:"q" required:"false" doc:"Repository name contains"`
	Limit   int    `query:"limit" minimum:"1" maximum:"500" required:"false"`
}

type adminReasonIn struct {
	ID   string `path:"id"`
	Body struct {
		Reason string `json:"reason" minLength:"1"`
	}
}

type adminCancelIn struct {
	ID   string `path:"id"`
	Body struct {
		Reason string `json:"reason" minLength:"1"`
		Hide   bool   `json:"hide" doc:"Also take the audit off its owner's list"`
	}
}

type adminHideIn struct {
	ID   string `path:"id"`
	Body struct {
		Hidden bool   `json:"hidden"`
		Reason string `json:"reason" minLength:"1"`
	}
}

type adminStateIn struct {
	ID   string `path:"id"`
	Body struct {
		State  core.AuditState `json:"state" enum:"queued,claimed,cloning,programs_verified,analysis,human_review,delivered,failed,cancelled" doc:"Where the audit goes. Pipeline states are set directly from any state; failed and cancelled keep their own rules; delayed has its own route"`
		Reason string          `json:"reason" minLength:"1"`
	}
}

type adminDeleteIn struct {
	ID   string `path:"id"`
	Body struct {
		Reason string `json:"reason,omitempty" maxLength:"140" doc:"Optional; kept on the action log"`
	}
}

type adminDeleteManyIn struct {
	Body struct {
		IDs    []string `json:"ids" minItems:"1" maxItems:"200" doc:"Audit ids to delete"`
		Reason string   `json:"reason,omitempty" maxLength:"140"`
	}
}

// DeletedMany is the receipt of a bulk delete: one receipt per audit that
// went, one line per id that did not.
type DeletedMany struct {
	Deleted []DeletedAudit `json:"deleted"`
	Failed  []struct {
		ID    string `json:"id"`
		Error string `json:"error"`
	} `json:"failed"`
}

// DeletedAudit is what remains of a deleted audit: the action log row and
// this receipt.
type DeletedAudit struct {
	ID       string          `json:"id"`
	Repo     string          `json:"repo"`
	UserID   string          `json:"user_id"`
	State    core.AuditState `json:"state" doc:"The state the audit was in"`
	Events   int             `json:"events"`
	Reports  int             `json:"reports"`
	Refunded bool            `json:"refunded" doc:"Whether the credit or slot came back on the way out"`
}

type adminReviseIn struct {
	ID     string `path:"id"`
	Reason string `query:"reason" minLength:"3" maxLength:"140" doc:"Why this version is made; shown on the report's revision trail" example:"re-tested after the fix landed"`
	Body   core.FindingsDocument
}

type impersonateIn struct {
	Body struct {
		UserID  string `json:"user_id"`
		AuditID string `json:"audit_id,omitempty" doc:"Audit the admin is coming from; the view returns there"`
		Minutes int    `json:"minutes" minimum:"1" maximum:"60" default:"15"`
	}
}

type createOpTokenIn struct {
	Body struct {
		AuditID string `json:"audit_id,omitempty" doc:"Queued: the token claims this audit. In progress: replaces the job's token. Empty: binds to whatever the claim takes"`
		Hours   int    `json:"hours" minimum:"1" maximum:"48" default:"48"`
	}
}

type opTokenOut struct {
	Body struct {
		Token     string `json:"token" doc:"Shown once"`
		ExpiresAt string `json:"expires_at" format:"date-time"`
	}
}

func (d *Deps) registerAdmin(api huma.API) {
	huma.Register(api, withAuth(op("adminQueue", http.MethodGet, "/v1/admin/queue", "Queued and active audits", "admin"), adminAuth), d.adminQueue)
	huma.Register(api, withAuth(op("adminListAudits", http.MethodGet, "/v1/admin/audits", "Every audit across users, newest first, with filters", "admin"), adminAuth), d.adminListAudits)
	huma.Register(api, withAuth(op("adminGetAudit", http.MethodGet, "/v1/admin/audits/{id}", "Any audit with its timeline and report summary", "admin"), adminAuth), d.adminGetAudit)
	huma.Register(api, withAuth(op("adminFail", http.MethodPost, "/v1/admin/audits/{id}/fail", "Fail a running audit; the credit or slot comes back", "admin"), adminAuth), d.adminFail)
	huma.Register(api, withAuth(op("adminCancel", http.MethodPost, "/v1/admin/audits/{id}/cancel", "Cancel an audit from any state, delivered included; the credit or slot comes back, a public report goes private, and hide takes it off the owner's list", "admin"), adminAuth), d.adminCancel)
	huma.Register(api, withAuth(op("adminHide", http.MethodPost, "/v1/admin/audits/{id}/hide", "Take an audit off its owner's list, or put it back", "admin"), adminAuth), d.adminHide)
	huma.Register(api, withAuth(op("adminSetState", http.MethodPost, "/v1/admin/audits/{id}/state", "Put an audit in a state: any pipeline state from any state (a refunded audit is paid for again, a delivered one goes private), or failed and cancelled by their own rules", "admin"), adminAuth), d.adminSetState)
	huma.Register(api, withAuth(op("adminDeleteAudits", http.MethodPost, "/v1/admin/audits/delete", "Delete several audits at once; each goes the way a single delete does, and the receipt says which did not", "admin"), adminAuth), d.adminDeleteAudits)
	huma.Register(api, withAuth(op("adminDeleteAudit", http.MethodDelete, "/v1/admin/audits/{id}", "Delete the audit row and everything under it; a cost still held comes back and the ledger keeps its rows", "admin"), adminAuth), d.adminDeleteAudit)
	huma.Register(api, withAuth(op("adminGetReportJSON", http.MethodGet, "/v1/admin/audits/{id}/report.json", "The current report as the findings document, to edit and post back as a revision", "admin"), adminAuth), d.adminReportJSON)
	huma.Register(api, withAuth(op("adminReviseReport", http.MethodPost, "/v1/admin/audits/{id}/report", "Store a new version of a delivered report; the owner sees the change on the timeline", "admin"), adminAuth), d.adminReviseReport)
	huma.Register(api, withAuth(op("adminSetFindingStatus", http.MethodPatch, "/v1/admin/audits/{id}/report/findings/{fid}", "Move a finding's status as admin; archiving needs a reason and the move joins its history", "admin"), adminAuth), d.adminSetFindingStatus)
	huma.Register(api, withAuth(op("adminListLessons", http.MethodGet, "/v1/admin/lessons", "What owners and admins corrected on delivered findings: archived as not relevant, rated up or down, with the reasons; newest first, one repository or all", "admin"), adminAuth), d.adminListLessons)
	huma.Register(api, withAuth(op("adminSetFindingSeverity", http.MethodPatch, "/v1/admin/audits/{id}/report/findings/{fid}/severity", "Re-rate a finding up or down with a reason, as admin; the change joins its history", "admin"), adminAuth), d.adminSetFindingSeverity)
	huma.Register(api, withAuth(op("adminReassign", http.MethodPost, "/v1/admin/audits/{id}/reassign", "Release a claimed job back to the queue", "admin"), adminAuth), d.adminReassign)
	huma.Register(api, withAuth(op("adminDelay", http.MethodPost, "/v1/admin/audits/{id}/delay", "Delay an audit with a new ETA", "admin"), adminAuth), d.adminDelay)
	huma.Register(api, withAuth(op("adminListClaims", http.MethodGet, "/v1/admin/tweet-claims", "Pending claims", "admin"), adminAuth), d.adminListClaims)
	huma.Register(api, withAuth(op("adminApproveClaim", http.MethodPost, "/v1/admin/tweet-claims/{id}/approve", "Approve and grant a credit", "admin"), adminAuth), d.adminApproveClaim)
	huma.Register(api, withAuth(op("adminRejectClaim", http.MethodPost, "/v1/admin/tweet-claims/{id}/reject", "Reject", "admin"), adminAuth), d.adminRejectClaim)
	huma.Register(api, withAuth(op("adminRevokeClaim", http.MethodPost, "/v1/admin/tweet-claims/{id}/revoke", "Revoke a granted credit", "admin"), adminAuth), d.adminRevokeClaim)
	huma.Register(api, withAuth(op("adminGrantCredits", http.MethodPost, "/v1/admin/credits/grant", "Grant credits", "admin"), adminAuth), d.adminGrantCredits)
	huma.Register(api, withAuth(op("adminRevokeCredits", http.MethodPost, "/v1/admin/credits/revoke", "Revoke credits", "admin"), adminAuth), d.adminRevokeCredits)
	huma.Register(api, withAuth(op("adminListUsers", http.MethodGet, "/v1/admin/users", "Users", "admin"), adminAuth), d.adminListUsers)
	huma.Register(api, withAuth(op("adminCreateUser", http.MethodPost, "/v1/admin/users", "Create a user by hand: a concierge customer with no sign-in yet, or a second account to work a flow locally; optional starting credits and a pro plan", "admin"), adminAuth), d.adminCreateUser)
	huma.Register(api, withAuth(op("adminGetUser", http.MethodGet, "/v1/admin/users/{id}", "User with identities, plan, ledger and audits", "admin"), adminAuth), d.adminGetUser)
	huma.Register(api, withAuth(op("adminCreateOperatorToken", http.MethodPost, "/v1/admin/operator-tokens", "Mint a job-scoped operator token", "admin"), adminAuth), d.adminCreateOperatorToken)
	huma.Register(api, withAuth(op("adminListActions", http.MethodGet, "/v1/admin/actions", "Admin action log", "admin"), adminAuth), d.adminListActions)
	huma.Register(api, withAuth(op("adminImpersonate", http.MethodPost, "/v1/admin/impersonate", "View the app as a user, read-only, for a bounded time", "admin"), adminAuth), d.adminImpersonate)
}

// adminImpersonate switches the admin's session to the target for a bounded
// time. The target is never an admin, and the view is read-only.
func (d *Deps) adminImpersonate(ctx context.Context, in *impersonateIn) (*body[ImpersonationState], error) {
	p := principal(ctx)
	target, err := d.Store.GetUser(ctx, in.Body.UserID)
	if err != nil {
		return nil, fail(err)
	}
	if target.ID == p.UserID {
		return nil, huma.Error400BadRequest("that is you")
	}
	if target.Role == "admin" {
		return nil, huma.Error403Forbidden("admins cannot be viewed as")
	}
	if in.Body.AuditID != "" {
		a, err := d.Store.GetAudit(ctx, in.Body.AuditID)
		if err != nil {
			return nil, fail(err)
		}
		if a.UserID != target.ID {
			return nil, huma.Error400BadRequest("audit belongs to another user")
		}
	}
	minutes := in.Body.Minutes
	if minutes == 0 {
		minutes = 15
	}
	imp, err := d.Store.StartImpersonation(ctx, p.UserID, p.SessionID, target.ID, in.Body.AuditID, time.Duration(minutes)*time.Minute)
	if err != nil {
		return nil, fail(err)
	}
	if _, err := d.logAdmin(ctx, "impersonate.begin", "user", target.ID, map[string]any{"impersonation_id": imp.ID, "audit_id": in.Body.AuditID, "expires_at": imp.ExpiresAt.UTC().Format(time.RFC3339)}); err != nil {
		return nil, fail(err)
	}
	return &body[ImpersonationState]{Body: toImpersonation(imp)}, nil
}

func toImpersonation(i store.Impersonation) ImpersonationState {
	return ImpersonationState{ID: i.ID, AdminID: i.AdminID, UserID: i.TargetUserID, AuditID: i.AuditID, ExpiresAt: i.ExpiresAt}
}

// logAdmin writes the admin action row every mutation must leave behind.
func (d *Deps) logAdmin(ctx context.Context, kind, subjectKind, subjectID string, payload map[string]any) (store.AdminAction, error) {
	return d.Store.LogAdminAction(ctx, store.AdminAction{AdminID: principal(ctx).UserID, Kind: kind, SubjectKind: subjectKind, SubjectID: subjectID, Payload: payload})
}

func (d *Deps) adminQueue(ctx context.Context, _ *struct{}) (*body[list[Audit]], error) {
	rows, err := d.Store.ListQueue(ctx)
	if err != nil {
		return nil, fail(err)
	}
	out := make([]Audit, 0, len(rows))
	for _, a := range rows {
		out = append(out, toAudit(a))
	}
	return items(out), nil
}

func (d *Deps) adminListAudits(ctx context.Context, in *adminAuditsIn) (*body[list[AdminAudit]], error) {
	f := store.AuditFilter{State: in.State, UserID: in.UserID, Adapter: in.Adapter, Query: in.Q, Limit: in.Limit}
	if in.Hidden != "" {
		h := in.Hidden == "true"
		f.Hidden = &h
	}
	rows, err := d.Store.ListAllAudits(ctx, f)
	if err != nil {
		return nil, fail(err)
	}
	names := map[string]string{}
	out := make([]AdminAudit, 0, len(rows))
	for _, a := range rows {
		name, ok := names[a.UserID]
		if !ok {
			if u, err := d.Store.GetUser(ctx, a.UserID); err == nil {
				name = u.DisplayName
			}
			names[a.UserID] = name
		}
		out = append(out, AdminAudit{Audit: toAudit(a), UserID: a.UserID, UserName: name})
	}
	return items(out), nil
}

func (d *Deps) adminFail(ctx context.Context, in *adminReasonIn) (*body[Audit], error) {
	a, err := d.Store.FailAudit(ctx, in.ID, in.Body.Reason, "system", principal(ctx).UserID)
	if err != nil {
		return nil, fail(err)
	}
	if _, err := d.logAdmin(ctx, "audit.fail", "audit", a.ID, map[string]any{"reason": in.Body.Reason}); err != nil {
		return nil, fail(err)
	}
	d.notify(ctx, a.UserID, "audit_failed", map[string]any{"audit_id": a.ID, "reason": in.Body.Reason})
	out, err := d.auditWithEvents(ctx, a)
	if err != nil {
		return nil, fail(err)
	}
	return &body[Audit]{Body: out}, nil
}

func (d *Deps) adminCancel(ctx context.Context, in *adminCancelIn) (*body[Audit], error) {
	a, err := d.Store.CancelAudit(ctx, in.ID, in.Body.Reason, principal(ctx).UserID, in.Body.Hide)
	if err != nil {
		return nil, fail(err)
	}
	if _, err := d.logAdmin(ctx, "audit.cancel", "audit", a.ID, map[string]any{"reason": in.Body.Reason, "hide": in.Body.Hide}); err != nil {
		return nil, fail(err)
	}
	if !in.Body.Hide {
		d.notify(ctx, a.UserID, "audit_cancelled", map[string]any{"audit_id": a.ID, "reason": in.Body.Reason})
	}
	out, err := d.auditWithEvents(ctx, a)
	if err != nil {
		return nil, fail(err)
	}
	return &body[Audit]{Body: out}, nil
}

// adminSetState dispatches on the target: failed and cancelled keep the
// rules and the refunds of their own routes, every other state is the
// admin's override of the pipeline position.
func (d *Deps) adminSetState(ctx context.Context, in *adminStateIn) (*body[Audit], error) {
	var a store.Audit
	var err error
	adminID := principal(ctx).UserID
	switch in.Body.State {
	case core.StateFailed:
		a, err = d.Store.FailAudit(ctx, in.ID, in.Body.Reason, "system", adminID)
	case core.StateCancelled:
		a, err = d.Store.CancelAudit(ctx, in.ID, in.Body.Reason, adminID, false)
	default:
		a, err = d.Store.SetAuditState(ctx, in.ID, in.Body.State, in.Body.Reason, adminID)
	}
	if err != nil {
		return nil, fail(err)
	}
	if _, err := d.logAdmin(ctx, "audit.state", "audit", a.ID, map[string]any{"state": string(a.State), "reason": in.Body.Reason}); err != nil {
		return nil, fail(err)
	}
	switch a.State {
	case core.StateDelivered:
		d.dropClone(ctx, a)
		d.notify(ctx, a.UserID, "audit_delivered", map[string]any{"audit_id": a.ID, "report_id": a.ReportID})
	case core.StateFailed:
		d.notify(ctx, a.UserID, "audit_failed", map[string]any{"audit_id": a.ID, "reason": in.Body.Reason})
	case core.StateCancelled:
		d.notify(ctx, a.UserID, "audit_cancelled", map[string]any{"audit_id": a.ID, "reason": in.Body.Reason})
	default:
		d.notify(ctx, a.UserID, "audit_state", map[string]any{"audit_id": a.ID, "state": string(a.State)})
	}
	out, err := d.auditWithEvents(ctx, a)
	if err != nil {
		return nil, fail(err)
	}
	return &body[Audit]{Body: out}, nil
}

// adminDeleteAudit is the one row delete in the product. The action log
// keeps what the row said, so the deletion itself stays on record.
func (d *Deps) adminDeleteAudit(ctx context.Context, in *adminDeleteIn) (*body[DeletedAudit], error) {
	out, err := d.deleteAudit(ctx, in.ID, in.Body.Reason)
	if err != nil {
		return nil, err
	}
	return &body[DeletedAudit]{Body: out}, nil
}

// deleteAudit is the one deletion path: the row and everything under it
// go, a held cost comes back, the checkout is dropped, the action log
// keeps the receipt. A reason is welcome, never required.
func (d *Deps) deleteAudit(ctx context.Context, id, reason string) (DeletedAudit, error) {
	del, err := d.Store.DeleteAudit(ctx, id, principal(ctx).UserID)
	if err != nil {
		return DeletedAudit{}, fail(err)
	}
	a := del.Audit
	d.dropClone(ctx, a)
	if _, err := d.logAdmin(ctx, "audit.delete", "audit", a.ID, map[string]any{"reason": reason, "repo": a.Repo, "user_id": a.UserID, "state": string(a.State),
		"cost_kind": a.CostKind, "head_sha": a.HeadSHA, "events": del.Events, "reports": del.Reports, "refunded": del.Refunded, "created_at": a.CreatedAt.UTC().Format(time.RFC3339)}); err != nil {
		return DeletedAudit{}, fail(err)
	}
	return DeletedAudit{ID: a.ID, Repo: a.Repo, UserID: a.UserID, State: a.State, Events: del.Events, Reports: del.Reports, Refunded: del.Refunded}, nil
}

// adminDeleteAudits deletes each id in turn; one that fails does not stop
// the rest, and the receipt names it.
func (d *Deps) adminDeleteAudits(ctx context.Context, in *adminDeleteManyIn) (*body[DeletedMany], error) {
	out := DeletedMany{Deleted: []DeletedAudit{}}
	seen := map[string]bool{}
	for _, id := range in.Body.IDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		del, err := d.deleteAudit(ctx, id, in.Body.Reason)
		if err != nil {
			msg := err.Error()
			if se, ok := err.(huma.StatusError); ok {
				msg = se.Error()
			}
			out.Failed = append(out.Failed, struct {
				ID    string `json:"id"`
				Error string `json:"error"`
			}{ID: id, Error: msg})
			continue
		}
		out.Deleted = append(out.Deleted, del)
	}
	if out.Failed == nil {
		out.Failed = []struct {
			ID    string `json:"id"`
			Error string `json:"error"`
		}{}
	}
	return &body[DeletedMany]{Body: out}, nil
}

func (d *Deps) adminHide(ctx context.Context, in *adminHideIn) (*body[Audit], error) {
	a, err := d.Store.SetAuditHidden(ctx, in.ID, in.Body.Hidden, in.Body.Reason, principal(ctx).UserID)
	if err != nil {
		return nil, fail(err)
	}
	kind := "audit.hide"
	if !in.Body.Hidden {
		kind = "audit.unhide"
	}
	if _, err := d.logAdmin(ctx, kind, "audit", a.ID, map[string]any{"reason": in.Body.Reason}); err != nil {
		return nil, fail(err)
	}
	out, err := d.auditWithEvents(ctx, a)
	if err != nil {
		return nil, fail(err)
	}
	return &body[Audit]{Body: out}, nil
}

func (d *Deps) adminReportJSON(ctx context.Context, in *idPath) (*body[core.FindingsDocument], error) {
	a, err := d.Store.GetAudit(ctx, in.ID)
	if err != nil {
		return nil, fail(err)
	}
	if a.ReportID == "" {
		return nil, huma.Error404NotFound("no report")
	}
	r, err := d.Store.GetReport(ctx, a.ReportID)
	if err != nil {
		return nil, fail(err)
	}
	doc := r.Document()
	for i := range doc.Findings {
		doc.Findings[i].ID = r.Findings[i].ID
	}
	return &body[core.FindingsDocument]{Body: doc}, nil
}

func (d *Deps) adminReviseReport(ctx context.Context, in *adminReviseIn) (*body[ReportRevision], error) {
	a, err := d.Store.GetAudit(ctx, in.ID)
	if err != nil {
		return nil, fail(err)
	}
	doc := in.Body
	if doc.Report.AuditID == "" {
		doc.Report.AuditID = a.ID
	}
	if doc.Report.AuditID != a.ID {
		return nil, huma.Error400BadRequest("report.audit_id does not match the audit")
	}
	if err := d.bindVisuals(ctx, a.ID, &doc); err != nil {
		return nil, err
	}
	if err := doc.Validate(); err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	carryPostedIDs(doc.Findings)
	// A finding new to this revision answers its exhaustion like a
	// submitted one; a carried finding keeps what it had.
	if open := doc.Unexhausted(true); len(open) > 0 {
		return nil, huma.Error422UnprocessableEntity(strings.Join(open, "; "))
	}
	// A revision names new places in its fixes; capture their lines while
	// the checkout is still around, as a submission does.
	if d.GitHub != nil {
		if root, ok := d.GitHub.Cached(a.Repo, a.HeadSHA); ok {
			github.AttachSnippets(root, doc.Findings)
		}
		d.GitHub.CaptureReferences(ctx, &doc)
	}
	reason := strings.TrimSpace(in.Reason)
	if len(reason) < 3 {
		return nil, huma.Error422UnprocessableEntity("a revision needs a reason")
	}
	rev, err := d.Store.ReviseReport(ctx, a.ID, doc, principal(ctx).UserID, reason)
	if err != nil {
		return nil, fail(err)
	}
	d.extendWebSurface(ctx, a, doc.Findings)
	if _, err := d.logAdmin(ctx, "report.revise", "audit", a.ID, map[string]any{"report_id": rev.Report.ID, "revision": rev.Report.Revision, "reason": reason, "added": rev.Added, "removed": rev.Removed, "kept": rev.Kept}); err != nil {
		return nil, fail(err)
	}
	d.notify(ctx, a.UserID, "report_revised", map[string]any{"audit_id": a.ID, "revision": rev.Report.Revision, "added": rev.Added, "removed": rev.Removed})
	return &body[ReportRevision]{Body: ReportRevision{Report: toReport(rev.Report, true), Added: rev.Added, Removed: rev.Removed, Kept: rev.Kept}}, nil
}

type lessonsIn struct {
	Repo  string `query:"repo" doc:"owner/name; empty for every repository"`
	Limit int    `query:"limit" minimum:"1" maximum:"500" default:"200"`
}

func (d *Deps) adminListLessons(ctx context.Context, in *lessonsIn) (*body[list[Lesson]], error) {
	rows, err := d.Store.ListLessons(ctx, strings.TrimSpace(in.Repo), in.Limit)
	if err != nil {
		return nil, fail(err)
	}
	out := make([]Lesson, 0, len(rows))
	for _, l := range rows {
		out = append(out, toLesson(l))
	}
	return &body[list[Lesson]]{Body: list[Lesson]{Items: out}}, nil
}

func (d *Deps) adminSetFindingStatus(ctx context.Context, in *findingStatusIn) (*body[core.Finding], error) {
	a, err := d.Store.GetAudit(ctx, in.ID)
	if err != nil {
		return nil, fail(err)
	}
	r, err := d.Store.GetReportByAudit(ctx, a.ID)
	if err != nil {
		return nil, fail(err)
	}
	reason := strings.TrimSpace(in.Body.Reason)
	if in.Body.Status == core.FindingArchived && len(reason) < 3 {
		return nil, huma.Error422UnprocessableEntity("archiving a finding needs a reason")
	}
	f, err := d.Store.SetFindingStatus(ctx, r.ID, in.FID, in.Body.Status, reason, "admin", principal(ctx).UserID)
	if err != nil {
		return nil, fail(err)
	}
	if _, err := d.logAdmin(ctx, "finding.status", "finding", f.ID, map[string]any{"audit_id": a.ID, "to": in.Body.Status, "reason": reason}); err != nil {
		return nil, fail(err)
	}
	out := f.Finding
	out.ID = f.ID
	out.CarriedFromID = f.CarriedFromID
	return &body[core.Finding]{Body: out}, nil
}

func (d *Deps) adminSetFindingSeverity(ctx context.Context, in *findingSeverityIn) (*body[core.Finding], error) {
	a, err := d.Store.GetAudit(ctx, in.ID)
	if err != nil {
		return nil, fail(err)
	}
	r, err := d.Store.GetReportByAudit(ctx, a.ID)
	if err != nil {
		return nil, fail(err)
	}
	f, err := d.Store.SetFindingSeverity(ctx, r.ID, in.FID, in.Body.Severity, strings.TrimSpace(in.Body.Reason), "admin", principal(ctx).UserID)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, huma.Error409Conflict("finding is already " + string(in.Body.Severity))
		}
		return nil, fail(err)
	}
	if _, err := d.logAdmin(ctx, "finding.severity", "finding", f.ID, map[string]any{"audit_id": a.ID, "to": in.Body.Severity, "reason": in.Body.Reason}); err != nil {
		return nil, fail(err)
	}
	out := f.Finding
	out.ID = f.ID
	out.CarriedFromID = f.CarriedFromID
	return &body[core.Finding]{Body: out}, nil
}

func (d *Deps) adminGetAudit(ctx context.Context, in *idPath) (*body[AdminAudit], error) {
	a, err := d.Store.GetAudit(ctx, in.ID)
	if err != nil {
		return nil, fail(err)
	}
	if _, err := d.logAdmin(ctx, "audit.view", "audit", a.ID, nil); err != nil {
		return nil, fail(err)
	}
	out, err := d.auditWithEvents(ctx, a)
	if err != nil {
		return nil, fail(err)
	}
	res := AdminAudit{Audit: out, UserID: a.UserID}
	if u, err := d.Store.GetUser(ctx, a.UserID); err == nil {
		res.UserName = u.DisplayName
	}
	if a.ReportID != "" {
		if r, err := d.Store.GetReport(ctx, a.ReportID); err == nil {
			s := r.Summary
			res.Summary = &s
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, fail(err)
		}
	}
	return &body[AdminAudit]{Body: res}, nil
}

func (d *Deps) adminDelay(ctx context.Context, in *adminDelayIn) (*body[Audit], error) {
	eta, err := time.Parse(time.RFC3339, in.Body.ETAAt)
	if err != nil {
		return nil, huma.Error400BadRequest("eta_at must be RFC 3339")
	}
	if !eta.After(d.now()) {
		return nil, huma.Error400BadRequest("eta_at must be in the future")
	}
	a, err := d.Store.DelayAudit(ctx, in.ID, eta, in.Body.Reason, "system", principal(ctx).UserID)
	if err != nil {
		return nil, fail(err)
	}
	if _, err := d.logAdmin(ctx, "audit.delay", "audit", a.ID, map[string]any{"eta_at": eta.UTC().Format(time.RFC3339), "reason": in.Body.Reason}); err != nil {
		return nil, fail(err)
	}
	d.notify(ctx, a.UserID, "audit_delayed", map[string]any{"audit_id": a.ID, "eta_at": eta.UTC().Format(time.RFC3339), "reason": in.Body.Reason})
	out, err := d.auditWithEvents(ctx, a)
	if err != nil {
		return nil, fail(err)
	}
	return &body[Audit]{Body: out}, nil
}

func (d *Deps) adminReassign(ctx context.Context, in *reassignIn) (*body[Audit], error) {
	a, err := d.Store.Reassign(ctx, in.ID, principal(ctx).UserID, in.Body.Reason)
	if err != nil {
		return nil, fail(err)
	}
	if _, err := d.logAdmin(ctx, "audit.reassign", "audit", a.ID, map[string]any{"reason": in.Body.Reason}); err != nil {
		return nil, fail(err)
	}
	d.notify(ctx, a.UserID, "audit_state", map[string]any{"audit_id": a.ID, "state": string(a.State)})
	out, err := d.auditWithEvents(ctx, a)
	if err != nil {
		return nil, fail(err)
	}
	return &body[Audit]{Body: out}, nil
}

func (d *Deps) adminListClaims(ctx context.Context, _ *struct{}) (*body[list[TweetClaim]], error) {
	rows, err := d.Store.ListAllTweetClaims(ctx)
	if err != nil {
		return nil, fail(err)
	}
	out := make([]TweetClaim, 0, len(rows))
	for _, c := range rows {
		out = append(out, toClaim(c))
	}
	return items(out), nil
}

func (d *Deps) reviewClaim(ctx context.Context, in *claimReviewIn, action string, fn func(ctx context.Context, id, adminID, reason string) (store.TweetClaim, error)) (*body[TweetClaim], error) {
	if !store.ValidID(in.ID) {
		return nil, huma.Error404NotFound("not found")
	}
	c, err := fn(ctx, in.ID, principal(ctx).UserID, in.Body.Reason)
	if err != nil {
		return nil, fail(err)
	}
	if _, err := d.logAdmin(ctx, "tweet_claim."+action, "tweet_claim", c.ID, map[string]any{"reason": in.Body.Reason, "user_id": c.UserID, "url": c.URL}); err != nil {
		return nil, fail(err)
	}
	d.notify(ctx, c.UserID, "claim_"+c.State, map[string]any{"claim_id": c.ID, "reason": in.Body.Reason})
	return &body[TweetClaim]{Body: toClaim(c)}, nil
}

func (d *Deps) adminApproveClaim(ctx context.Context, in *claimReviewIn) (*body[TweetClaim], error) {
	return d.reviewClaim(ctx, in, "approve", d.Store.ApproveClaim)
}

func (d *Deps) adminRejectClaim(ctx context.Context, in *claimReviewIn) (*body[TweetClaim], error) {
	return d.reviewClaim(ctx, in, "reject", d.Store.RejectClaim)
}

func (d *Deps) adminRevokeClaim(ctx context.Context, in *claimReviewIn) (*body[TweetClaim], error) {
	return d.reviewClaim(ctx, in, "revoke", d.Store.RevokeClaim)
}

func (d *Deps) adminCredits(ctx context.Context, in *creditIn, kind core.LedgerKind) (*body[LedgerRow], error) {
	target, err := d.Store.GetUser(ctx, in.Body.UserID)
	if err != nil {
		return nil, huma.Error404NotFound("user not found")
	}
	delta := in.Body.Delta
	if kind == core.LedgerRevoke {
		delta = -delta
	}
	// One transaction: the ledger append first, the admin_actions row only
	// once it succeeded, so a refused revoke leaves no action behind.
	row, _, err := d.Store.AdminCredit(ctx, principal(ctx).UserID, target.ID, kind, delta, in.Body.Reason)
	if err != nil {
		return nil, fail(err)
	}
	d.notify(ctx, target.ID, "credits_"+map[core.LedgerKind]string{core.LedgerGrant: "granted", core.LedgerRevoke: "revoked"}[kind], map[string]any{"delta": in.Body.Delta, "reason": in.Body.Reason})
	return &body[LedgerRow]{Body: toLedgerRow(row)}, nil
}

func (d *Deps) adminGrantCredits(ctx context.Context, in *creditIn) (*body[LedgerRow], error) {
	return d.adminCredits(ctx, in, core.LedgerGrant)
}

func (d *Deps) adminRevokeCredits(ctx context.Context, in *creditIn) (*body[LedgerRow], error) {
	return d.adminCredits(ctx, in, core.LedgerRevoke)
}

func (d *Deps) adminUser(ctx context.Context, u store.User) (AdminUser, error) {
	idents, err := d.Store.ListIdentities(ctx, u.ID)
	if err != nil {
		return AdminUser{}, err
	}
	credits, err := d.Store.Balance(ctx, d.Store.Pool, u.ID)
	if err != nil {
		return AdminUser{}, err
	}
	n, err := d.Store.CountAudits(ctx, u.ID)
	if err != nil {
		return AdminUser{}, err
	}
	out := AdminUser{User: toUser(u), Identities: toIdentities(idents), Credits: credits, Audits: n}
	if plan, err := d.Store.GetPlan(ctx, u.ID); err == nil {
		out.Plan = toPlanState(plan, d.now())
	}
	return out, nil
}

type adminCreateUserIn struct {
	Body struct {
		DisplayName string `json:"display_name" minLength:"2" maxLength:"60"`
		Credits     int    `json:"credits,omitempty" minimum:"0" maximum:"100" doc:"Starting credits, granted on the ledger with the admin's name"`
		Plan        string `json:"plan,omitempty" enum:"pro" doc:"pro starts a year of one run a day"`
	}
}

// adminCreateUser makes a user with no identity. It signs in through an
// identity linked later, or, in dev, through the dev login by its id.
func (d *Deps) adminCreateUser(ctx context.Context, in *adminCreateUserIn) (*body[AdminUser], error) {
	u, err := d.Store.CreateUser(ctx, d.Store.Pool, strings.TrimSpace(in.Body.DisplayName), "")
	if err != nil {
		return nil, fail(err)
	}
	if in.Body.Credits > 0 {
		if _, _, err := d.Store.AdminCredit(ctx, principal(ctx).UserID, u.ID, core.LedgerGrant, in.Body.Credits, "starting credits"); err != nil {
			return nil, fail(err)
		}
	}
	if in.Body.Plan == "pro" {
		now := d.now()
		if _, err := d.Store.CreatePlan(ctx, d.Store.Pool, store.Plan{UserID: u.ID, Kind: "pro", StartsAt: now.Add(-time.Minute), EndsAt: now.Add(365 * 24 * time.Hour), RunCap: 1}); err != nil {
			return nil, fail(err)
		}
	}
	if _, err := d.logAdmin(ctx, "user.create", "user", u.ID, map[string]any{"display_name": u.DisplayName, "credits": in.Body.Credits, "plan": in.Body.Plan}); err != nil {
		return nil, fail(err)
	}
	out, err := d.adminUser(ctx, u)
	if err != nil {
		return nil, fail(err)
	}
	return &body[AdminUser]{Body: out}, nil
}

func (d *Deps) adminListUsers(ctx context.Context, _ *struct{}) (*body[list[AdminUser]], error) {
	rows, err := d.Store.ListUserSummaries(ctx, 500)
	if err != nil {
		return nil, fail(err)
	}
	out := make([]AdminUser, 0, len(rows))
	for _, r := range rows {
		au := AdminUser{User: toUser(r.User), Identities: toIdentities(r.Identities), Credits: r.Credits, Audits: r.Audits}
		if r.Plan != nil {
			au.Plan = toPlanState(*r.Plan, d.now())
		}
		out = append(out, au)
	}
	return items(out), nil
}

func (d *Deps) adminGetUser(ctx context.Context, in *idPath) (*body[AdminUserDetail], error) {
	u, err := d.Store.GetUser(ctx, in.ID)
	if err != nil {
		return nil, fail(err)
	}
	au, err := d.adminUser(ctx, u)
	if err != nil {
		return nil, fail(err)
	}
	ledger, err := d.Store.ListLedger(ctx, u.ID)
	if err != nil {
		return nil, fail(err)
	}
	audits, err := d.Store.ListAudits(ctx, u.ID)
	if err != nil {
		return nil, fail(err)
	}
	out := AdminUserDetail{AdminUser: au, Ledger: make([]LedgerRow, 0, len(ledger)), AuditList: make([]Audit, 0, len(audits))}
	for _, r := range ledger {
		out.Ledger = append(out.Ledger, toLedgerRow(r))
	}
	for _, a := range audits {
		out.AuditList = append(out.AuditList, toAudit(a))
	}
	return &body[AdminUserDetail]{Body: out}, nil
}

// adminCreateOperatorToken mints a token for a job. For a queued audit the
// token claims it; for a job already in progress the token replaces the one
// that claimed it, so a lost or expired token never forces a re-queue.
func (d *Deps) adminCreateOperatorToken(ctx context.Context, in *createOpTokenIn) (*opTokenOut, error) {
	renew := false
	if in.Body.AuditID != "" {
		a, err := d.Store.GetAudit(ctx, in.Body.AuditID)
		if err != nil {
			return nil, huma.Error404NotFound("audit not found")
		}
		if a.State.Terminal() {
			return nil, huma.Error409Conflict("audit is " + string(a.State))
		}
		renew = a.State != core.StateQueued
	}
	hours := in.Body.Hours
	if hours <= 0 {
		hours = 48
	}
	token := OperatorTokenPrefix + auth.RandomToken(24)
	expires := d.now().Add(time.Duration(hours) * time.Hour)
	var t store.OperatorToken
	var err error
	kind := "operator_token.create"
	if renew {
		kind = "operator_token.renew"
		t, err = d.Store.RenewOperatorToken(ctx, in.Body.AuditID, auth.Hash(token), expires, principal(ctx).UserID)
	} else {
		t, err = d.Store.CreateOperatorToken(ctx, in.Body.AuditID, auth.Hash(token), expires, principal(ctx).UserID)
	}
	if err != nil {
		return nil, fail(err)
	}
	if _, err := d.logAdmin(ctx, kind, "operator_token", t.ID, map[string]any{"audit_id": in.Body.AuditID, "hours": hours}); err != nil {
		return nil, fail(err)
	}
	out := &opTokenOut{}
	out.Body.Token = token
	out.Body.ExpiresAt = expires.UTC().Format(time.RFC3339)
	return out, nil
}

func (d *Deps) adminListActions(ctx context.Context, _ *struct{}) (*body[list[AdminAction]], error) {
	rows, err := d.Store.ListAdminActions(ctx, 500)
	if err != nil {
		return nil, fail(err)
	}
	out := make([]AdminAction, 0, len(rows))
	for _, a := range rows {
		out = append(out, toAdminAction(a))
	}
	return items(out), nil
}

// carryPostedIDs turns each finding's posted id into the version it revises.
// The id wins over carried_from_id: a document fetched from GET carries the
// previous revision's links, which no longer name any current finding, and
// matching on them would count a retitled finding as removed and added.
func carryPostedIDs(findings []core.Finding) {
	for i := range findings {
		if findings[i].ID != "" {
			findings[i].CarriedFromID = findings[i].ID
		}
		findings[i].ID = ""
	}
}
