package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/forefy/vig/backend/internal/core"
)

// Report is a reports row with its findings.
type Report struct {
	ID            string
	AuditID       string
	Adapter       string
	Pin           core.Pin
	Summary       core.Summary
	Methods       *core.Methods
	PublishedSlug string
	PublishedAt   *time.Time
	CreatedAt     time.Time
	Revision      int    // 1 for the delivered report, +1 per revision
	Supersedes    string // the version this one replaced, when revised
	Known         []core.Known
	Findings      []Finding
}

// Finding is a findings row.
type Finding struct {
	ID            string
	ReportID      string
	CarriedFromID string
	core.Finding
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Document renders the report as the stable findings JSON.
func (r Report) Document() core.FindingsDocument {
	doc := core.FindingsDocument{Report: core.ReportHeader{AuditID: r.AuditID, Pin: r.Pin, Summary: r.Summary, Methods: r.Methods, Known: r.Known}, Findings: make([]core.Finding, 0, len(r.Findings))}
	for _, f := range r.Findings {
		ff := f.Finding
		ff.CarriedFromID = f.CarriedFromID
		doc.Findings = append(doc.Findings, ff)
	}
	return doc
}

// StoreReport validates and stores a findings document for an audit. For a
// diff audit, findings whose title and file match one in the user's previous
// report on the same repo carry that finding's status and id. A finding may
// also name its predecessor explicitly with carried_from_id; the id must be
// a finding on one of the user's earlier reports for the same repository.
func (s *Store) StoreReport(ctx context.Context, auditID string, doc core.FindingsDocument) (Report, error) {
	if err := doc.Validate(); err != nil {
		return Report{}, ErrBadTransition{Err: err}
	}
	reportID := NewID()
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		a, err := getAuditLocked(ctx, tx, auditID)
		if err != nil {
			return err
		}
		if a.ReportID != "" {
			return ErrConflict
		}
		prev := map[string]Finding{}
		byID := map[string]Finding{}
		if a.Coverage == "diff" || a.BaseSHA != "" {
			var prevReportID string
			err := tx.QueryRow(ctx, `select r.id from reports r join audits pa on pa.id=r.audit_id join targets pt on pt.id=pa.target_id
				where pa.user_id=$1 and pt.repo_full_name=$2 and pa.id<>$3 and (pa.head_sha=$4 or $4='') order by pa.created_at desc limit 1`,
				a.UserID, a.Repo, auditID, a.BaseSHA).Scan(&prevReportID)
			if err == nil {
				fs, err := s.listFindings(ctx, tx, prevReportID)
				if err != nil {
					return err
				}
				for _, f := range fs {
					prev[f.Title+"\x00"+f.Location.File] = f
				}
			} else if wrap(err) != ErrNotFound {
				return err
			}
		}
		for _, f := range doc.Findings {
			if f.CarriedFromID == "" || !ValidID(f.CarriedFromID) {
				continue
			}
			if _, ok := byID[f.CarriedFromID]; ok {
				continue
			}
			rows, err := s.listFindingsWhere(ctx, tx, `f.id=$1 and pa.user_id=$2 and pt.repo_full_name=$3 and pa.id<>$4`, f.CarriedFromID, a.UserID, a.Repo, auditID)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				return ErrBadTransition{Err: fmt.Errorf("carried_from_id %s is not a finding on an earlier report of %s", f.CarriedFromID, a.Repo)}
			}
			byID[f.CarriedFromID] = rows[0]
		}
		pin, _ := json.Marshal(doc.Report.Pin)
		summary, _ := json.Marshal(doc.Report.Summary)
		var methods, known []byte
		if doc.Report.Methods != nil {
			methods, _ = json.Marshal(doc.Report.Methods)
		}
		if len(doc.Report.Known) > 0 {
			known, _ = json.Marshal(doc.Report.Known)
		}
		if _, err := tx.Exec(ctx, `insert into reports(id, audit_id, pin, summary, methods, known) values($1,$2,$3,$4,$5,$6)`, reportID, auditID, pin, summary, methods, known); err != nil {
			return wrap(err)
		}
		for _, f := range doc.Findings {
			status := f.Status
			var carried *string
			if p, ok := byID[f.CarriedFromID]; ok {
				status = p.Status
				id := p.ID
				carried = &id
			} else if p, ok := prev[f.Title+"\x00"+f.Location.File]; ok {
				status = p.Status
				id := p.ID
				carried = &id
			}
			if _, err := insertFinding(ctx, tx, reportID, f, status, carried); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `update audits set report_id=$2, updated_at=now() where id=$1`, auditID, reportID); err != nil {
			return err
		}
		for _, p := range doc.Report.Pin.Programs {
			if err := s.SetProgramMatch(ctx, tx, a.TargetID, p); err != nil {
				return err
			}
		}
		return s.AddEvent(ctx, tx, AuditEvent{AuditID: auditID, Kind: "report_stored", Actor: "operator", State: a.State,
			Payload: map[string]any{"report_id": reportID, "findings": len(doc.Findings), "summary": doc.Report.Summary}})
	})
	if err != nil {
		return Report{}, err
	}
	return s.GetReport(ctx, reportID)
}

// insertFinding writes one finding row of a report.
func insertFinding(ctx context.Context, tx pgx.Tx, reportID string, f core.Finding, status core.FindingStatus, carried *string) (string, error) {
	loc, _ := json.Marshal(f.Location)
	refs, _ := json.Marshal(f.References)
	if f.References == nil {
		refs = []byte("[]")
	}
	var evidence, snippet, sites, visuals, fixSites []byte
	if f.Evidence != nil {
		evidence, _ = json.Marshal(f.Evidence)
	}
	if f.Snippet != nil {
		snippet, _ = json.Marshal(f.Snippet)
	}
	if len(f.Sites) > 0 {
		sites, _ = json.Marshal(f.Sites)
	}
	if len(f.Visuals) > 0 {
		visuals, _ = json.Marshal(f.Visuals)
	}
	if len(f.FixSites) > 0 {
		fixSites, _ = json.Marshal(f.FixSites)
	}
	var refCode []byte
	if len(f.ReferenceCode) > 0 {
		refCode, _ = json.Marshal(f.ReferenceCode)
	}
	id := NewID()
	_, err := tx.Exec(ctx, `insert into findings(id, report_id, ordinal, severity, title, location, description, exploit_path, fix, refs, status, carried_from_finding_id, evidence, snippet, sites_kind, sites, exploitable_by, visuals, impact, expected, normal_use, fix_sites, fix_kind, reference_code)
		values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24)`,
		id, reportID, f.Ordinal, string(f.Severity), f.Title, loc, f.Description, nullStr(f.ExploitPath), f.Fix, refs, string(status), carried, evidence, snippet, nullStr(string(f.SitesKind)), sites, nullStr(string(f.ExploitableBy)), visuals, nullStr(f.Impact), nullStr(f.Expected), nullStr(f.NormalUse), fixSites, nullStr(string(f.FixKind)), refCode)
	return id, wrap(err)
}

// Revision is what a report revision changed against the version before.
type Revision struct {
	Report  Report
	Added   int
	Removed int
	Kept    int
}

// ReviseReport stores a new version of a delivered audit's report. The
// earlier version stays; the audit points at the new one, which takes over
// the public slug. A finding that names its predecessor (carried_from_id)
// or shares its title and file keeps that finding's status and, when it
// brings none, its snippet, since the checkout is gone by now.
func (s *Store) ReviseReport(ctx context.Context, auditID string, doc core.FindingsDocument, adminID string) (Revision, error) {
	if err := doc.Validate(); err != nil {
		return Revision{}, ErrBadTransition{Err: err}
	}
	reportID := NewID()
	var out Revision
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		a, err := getAuditLocked(ctx, tx, auditID)
		if err != nil {
			return err
		}
		// A report can be revised once it exists: in review (work in
		// progress on a stored report) and after delivery (corrections).
		if (a.State != core.StateDelivered && a.State != core.StateHumanReview) || a.ReportID == "" {
			return ErrBadTransition{Err: fmt.Errorf("audit is %s and has no report to revise", a.State)}
		}
		if doc.Report.Pin.Repo != a.Repo || doc.Report.Pin.Commit != a.HeadSHA {
			return ErrBadTransition{Err: errors.New("report.pin must match the audited repo and commit")}
		}
		var prevRevision int
		var slug *string
		var publishedAt *time.Time
		if err := tx.QueryRow(ctx, `select revision, published_slug, published_at from reports where id=$1 for update`, a.ReportID).Scan(&prevRevision, &slug, &publishedAt); err != nil {
			return wrap(err)
		}
		prevFindings, err := s.listFindings(ctx, tx, a.ReportID)
		if err != nil {
			return err
		}
		byID := map[string]Finding{}
		byKey := map[string]Finding{}
		for _, f := range prevFindings {
			byID[f.ID] = f
			byKey[f.Title+"\x00"+f.Location.File] = f
		}
		pin, _ := json.Marshal(doc.Report.Pin)
		summary, _ := json.Marshal(doc.Report.Summary)
		var methods, known []byte
		if doc.Report.Methods != nil {
			methods, _ = json.Marshal(doc.Report.Methods)
		}
		if len(doc.Report.Known) > 0 {
			known, _ = json.Marshal(doc.Report.Known)
		}
		// The slug is unique: the old version lets go of it first.
		if _, err := tx.Exec(ctx, `update reports set published_slug=null, published_at=null where id=$1`, a.ReportID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into reports(id, audit_id, pin, summary, methods, revision, supersedes, published_slug, published_at, known) values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			reportID, auditID, pin, summary, methods, prevRevision+1, a.ReportID, slug, publishedAt, known); err != nil {
			return wrap(err)
		}
		matched := map[string]bool{}
		// A kept finding is one that carries its id, or keeps its title and
		// file. A retitled, relocated finding posted without its id is a
		// new finding to the system, and its history stays on the version
		// it was made on; the dry run says so before the post.
		for _, f := range doc.Findings {
			status := f.Status
			p, ok := byID[f.CarriedFromID]
			if !ok {
				p, ok = byKey[f.Title+"\x00"+f.Location.File]
			}
			// A revision continues this same report, so the kept finding
			// gets the previous version's status and snippet but NOT a
			// carried pointer: carried marks a finding that continues a
			// finding from a PREVIOUS report (StoreReport's cross-audit
			// match), and stamping it here made every surviving finding
			// wear the badge on its second revision.
			var carried *string
			if ok {
				status = p.Status
				matched[p.ID] = true
				if f.Snippet == nil {
					f.Snippet = p.Snippet
				}
				out.Kept++
			} else {
				out.Added++
			}
			newID, err := insertFinding(ctx, tx, reportID, f, status, carried)
			if err != nil {
				return err
			}
			// The re-rating history follows the finding into its next
			// version, and a revision that rates it differently is itself
			// one more entry, so the trail never has a silent step.
			if ok {
				if _, err := tx.Exec(ctx, `insert into finding_severity_changes(id, finding_id, from_severity, to_severity, reason, by_role, actor_user_id, created_at)
					select gen_random_uuid(), $2, from_severity, to_severity, reason, by_role, actor_user_id, created_at from finding_severity_changes where finding_id=$1`, p.ID, newID); err != nil {
					return wrap(err)
				}
				if _, err := tx.Exec(ctx, `insert into finding_status_changes(id, finding_id, from_status, to_status, reason, by_role, actor_user_id, created_at)
					select gen_random_uuid(), $2, from_status, to_status, reason, by_role, actor_user_id, created_at from finding_status_changes where finding_id=$1`, p.ID, newID); err != nil {
					return wrap(err)
				}
				if p.Severity != f.Severity {
					if _, err := tx.Exec(ctx, `insert into finding_severity_changes(id, finding_id, from_severity, to_severity, reason, by_role, actor_user_id)
						values($1,$2,$3,$4,$5,$6,$7)`, NewID(), newID, string(p.Severity), string(f.Severity), fmt.Sprintf("revision %d", prevRevision+1), "admin", nullStr(adminID)); err != nil {
						return wrap(err)
					}
				}
			}
		}
		out.Removed = len(prevFindings) - len(matched)
		if _, err := tx.Exec(ctx, `update audits set report_id=$2, updated_at=now() where id=$1`, auditID, reportID); err != nil {
			return err
		}
		return s.AddEvent(ctx, tx, AuditEvent{AuditID: auditID, Kind: "report_revised", Actor: "system", ActorID: adminID, State: a.State,
			Payload: map[string]any{"report_id": reportID, "revision": prevRevision + 1, "findings": len(doc.Findings), "added": out.Added, "removed": out.Removed, "kept": out.Kept, "summary": doc.Report.Summary}})
	})
	if err != nil {
		return Revision{}, err
	}
	r, err := s.GetReport(ctx, reportID)
	if err != nil {
		return Revision{}, err
	}
	out.Report = r
	return out, nil
}

const reportCols = `r.id, r.audit_id, r.pin, r.summary, r.methods, coalesce(r.published_slug,''), r.published_at, r.created_at, t.adapter, r.revision, coalesce(r.supersedes::text,''), r.known`

const reportFrom = ` from reports r join audits a on a.id=r.audit_id join targets t on t.id=a.target_id `

func (s *Store) scanReport(ctx context.Context, row pgx.Row) (Report, error) {
	var r Report
	var pin, summary, methods, known []byte
	if err := row.Scan(&r.ID, &r.AuditID, &pin, &summary, &methods, &r.PublishedSlug, &r.PublishedAt, &r.CreatedAt, &r.Adapter, &r.Revision, &r.Supersedes, &known); err != nil {
		return r, wrap(err)
	}
	_ = json.Unmarshal(pin, &r.Pin)
	_ = json.Unmarshal(summary, &r.Summary)
	if len(known) > 0 {
		_ = json.Unmarshal(known, &r.Known)
	}
	if len(methods) > 0 {
		r.Methods = &core.Methods{}
		_ = json.Unmarshal(methods, r.Methods)
	}
	fs, err := s.listFindings(ctx, s.Pool, r.ID)
	if err != nil {
		return r, err
	}
	r.Findings = fs
	return r, nil
}

// GetReport fetches a report with findings.
func (s *Store) GetReport(ctx context.Context, id string) (Report, error) {
	if !ValidID(id) {
		return Report{}, ErrNotFound
	}
	return s.scanReport(ctx, s.Pool.QueryRow(ctx, `select `+reportCols+reportFrom+`where r.id=$1`, id))
}

// GetReportByAudit fetches the report of an audit.
func (s *Store) GetReportByAudit(ctx context.Context, auditID string) (Report, error) {
	if !ValidID(auditID) {
		return Report{}, ErrNotFound
	}
	return s.scanReport(ctx, s.Pool.QueryRow(ctx, `select `+reportCols+reportFrom+`where r.audit_id=$1 order by r.revision desc limit 1`, auditID))
}

// GetReportBySlug fetches a published report.
func (s *Store) GetReportBySlug(ctx context.Context, slug string) (Report, error) {
	return s.scanReport(ctx, s.Pool.QueryRow(ctx, `select `+reportCols+reportFrom+`where r.published_slug=$1 and r.published_at is not null`, slug))
}

func (s *Store) listFindings(ctx context.Context, db DB, reportID string) ([]Finding, error) {
	return s.listFindingsWhere(ctx, db, `f.report_id=$1`, reportID)
}

func (s *Store) listFindingsWhere(ctx context.Context, db DB, where string, args ...any) ([]Finding, error) {
	rows, err := db.Query(ctx, `select f.id, f.report_id, f.ordinal, f.severity, f.title, f.location, f.description, coalesce(f.exploit_path,''), f.fix, f.refs, f.status, coalesce(f.carried_from_finding_id::text,''), f.evidence, f.snippet, coalesce(f.sites_kind,''), f.sites, coalesce(f.exploitable_by,''), f.visuals, coalesce(f.impact,''), coalesce(f.expected,''), coalesce(f.normal_use,''), f.fix_sites, coalesce(f.fix_kind,''), f.reference_code, f.created_at, f.updated_at
		from findings f join reports r on r.id=f.report_id join audits pa on pa.id=r.audit_id join targets pt on pt.id=pa.target_id where `+where+` order by f.ordinal`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Finding{}
	for rows.Next() {
		var f Finding
		var loc, refs, evidence, snippet, sites, visuals, impact, expected, normalUse, fixSites, refCode []byte
		var fixKind string
		var sitesKind string
		if err := rows.Scan(&f.ID, &f.ReportID, &f.Ordinal, &f.Severity, &f.Title, &loc, &f.Description, &f.ExploitPath, &f.Fix, &refs, &f.Status, &f.CarriedFromID, &evidence, &snippet, &sitesKind, &sites, &f.ExploitableBy, &visuals, &impact, &expected, &normalUse, &fixSites, &fixKind, &refCode, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(loc, &f.Location)
		_ = json.Unmarshal(refs, &f.References)
		if len(evidence) > 0 {
			f.Evidence = &core.Evidence{}
			_ = json.Unmarshal(evidence, f.Evidence)
		}
		if len(snippet) > 0 {
			f.Snippet = &core.Snippet{}
			_ = json.Unmarshal(snippet, f.Snippet)
		}
		f.SitesKind = core.SitesKind(sitesKind)
		if len(sites) > 0 {
			_ = json.Unmarshal(sites, &f.Sites)
		}
		if len(visuals) > 0 {
			_ = json.Unmarshal(visuals, &f.Visuals)
		}
		f.Impact = string(impact)
		f.Expected = string(expected)
		f.NormalUse = string(normalUse)
		f.FixKind = core.FixKind(fixKind)
		if len(fixSites) > 0 {
			_ = json.Unmarshal(fixSites, &f.FixSites)
		}
		if len(refCode) > 0 {
			_ = json.Unmarshal(refCode, &f.ReferenceCode)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if err := s.attachSeverityChanges(ctx, db, out); err != nil {
		return nil, err
	}
	return out, s.attachStatusChanges(ctx, db, out)
}

// attachStatusChanges loads every status move of the given findings,
// oldest first, onto the finding it belongs to.
func (s *Store) attachStatusChanges(ctx context.Context, db DB, fs []Finding) error {
	if len(fs) == 0 {
		return nil
	}
	ids := make([]string, len(fs))
	at := map[string]int{}
	for i, f := range fs {
		ids[i] = f.ID
		at[f.ID] = i
	}
	rows, err := db.Query(ctx, `select finding_id, from_status, to_status, reason, by_role, created_at
		from finding_status_changes where finding_id = any($1::uuid[]) order by created_at, id`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var fid, from, to string
		var c core.StatusChange
		if err := rows.Scan(&fid, &from, &to, &c.Reason, &c.By, &c.At); err != nil {
			return err
		}
		c.From, c.To = core.FindingStatus(from), core.FindingStatus(to)
		if i, ok := at[fid]; ok {
			fs[i].StatusChanges = append(fs[i].StatusChanges, c)
		}
	}
	return rows.Err()
}

// Lesson is one correction a person made to a delivered finding: archived
// as not relevant, or rated up or down, always with the reason. Listed
// across reports so the next audit of the same code starts from them.
type Lesson struct {
	AuditID   string
	Repo      string
	FindingID string
	Ordinal   int
	Title     string
	Severity  core.Severity
	Kind      string // severity or status
	From      string
	To        string
	Reason    string
	By        string
	At        time.Time
}

// ListLessons returns corrections newest first, for one repository when
// repo is set, otherwise across every audit. Only delivered audits count:
// moves made while a report is still in review are the operator and the
// admin iterating, not the owner judging. A revision's own "revision N"
// rows are bookkeeping, not lessons. Only the current report of each audit
// is read; earlier versions carried their history forward.
func (s *Store) ListLessons(ctx context.Context, repo string, limit int) ([]Lesson, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.Pool.Query(ctx, `
		select audit_id, repo, finding_id, ordinal, title, severity, kind, from_v, to_v, reason, by_role, at from (
			select r.audit_id, pt.repo_full_name, f.id, f.ordinal, f.title, f.severity, 'severity', c.from_severity, c.to_severity, c.reason, c.by_role, c.created_at, a.state
			from finding_severity_changes c join findings f on f.id=c.finding_id join reports r on r.id=f.report_id
			join audits a on a.id=r.audit_id and a.report_id=r.id join targets pt on pt.id=a.target_id
			union all
			select r.audit_id, pt.repo_full_name, f.id, f.ordinal, f.title, f.severity, 'status', c.from_status, c.to_status, c.reason, c.by_role, c.created_at, a.state
			from finding_status_changes c join findings f on f.id=c.finding_id join reports r on r.id=f.report_id
			join audits a on a.id=r.audit_id and a.report_id=r.id join targets pt on pt.id=a.target_id
		) l(audit_id, repo, finding_id, ordinal, title, severity, kind, from_v, to_v, reason, by_role, at, state)
		where ($1 = '' or repo = $1) and state = 'delivered' and reason not like 'revision %'
		order by at desc limit $2`, repo, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Lesson{}
	for rows.Next() {
		var l Lesson
		var sev string
		if err := rows.Scan(&l.AuditID, &l.Repo, &l.FindingID, &l.Ordinal, &l.Title, &sev, &l.Kind, &l.From, &l.To, &l.Reason, &l.By, &l.At); err != nil {
			return nil, err
		}
		l.Severity = core.Severity(sev)
		out = append(out, l)
	}
	return out, rows.Err()
}

// attachSeverityChanges loads every re-rating of the given findings, oldest
// first, onto the finding it belongs to.
func (s *Store) attachSeverityChanges(ctx context.Context, db DB, fs []Finding) error {
	if len(fs) == 0 {
		return nil
	}
	ids := make([]string, len(fs))
	at := map[string]int{}
	for i, f := range fs {
		ids[i] = f.ID
		at[f.ID] = i
	}
	rows, err := db.Query(ctx, `select finding_id, from_severity, to_severity, reason, by_role, created_at
		from finding_severity_changes where finding_id = any($1::uuid[]) order by created_at, id`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var fid, from, to string
		var c core.SeverityChange
		if err := rows.Scan(&fid, &from, &to, &c.Reason, &c.By, &c.At); err != nil {
			return err
		}
		c.From, c.To = core.Severity(from), core.Severity(to)
		if i, ok := at[fid]; ok {
			fs[i].SeverityChanges = append(fs[i].SeverityChanges, c)
		}
	}
	return rows.Err()
}

// SetFindingSeverity re-rates one finding with a reason, appends the change
// to its history and recomputes the report's summary. Re-rating to the
// current severity is a conflict: there is nothing to record.
func (s *Store) SetFindingSeverity(ctx context.Context, reportID, findingID string, to core.Severity, reason, role, actorUserID string) (Finding, error) {
	if !ValidID(findingID) {
		return Finding{}, ErrNotFound
	}
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		var from string
		if err := tx.QueryRow(ctx, `select severity from findings where id=$1 and report_id=$2 for update`, findingID, reportID).Scan(&from); err != nil {
			return wrap(err)
		}
		if core.Severity(from) == to {
			return ErrConflict
		}
		if _, err := tx.Exec(ctx, `insert into finding_severity_changes(id, finding_id, from_severity, to_severity, reason, by_role, actor_user_id)
			values($1,$2,$3,$4,$5,$6,$7)`, NewID(), findingID, from, string(to), reason, role, nullStr(actorUserID)); err != nil {
			return wrap(err)
		}
		if _, err := tx.Exec(ctx, `update findings set severity=$3, updated_at=now() where id=$1 and report_id=$2`, findingID, reportID, string(to)); err != nil {
			return wrap(err)
		}
		fs, err := s.listFindings(ctx, tx, reportID)
		if err != nil {
			return err
		}
		docs := make([]core.Finding, len(fs))
		for i, f := range fs {
			docs[i] = f.Finding
		}
		summary, _ := json.Marshal(core.Summarize(docs))
		_, err = tx.Exec(ctx, `update reports set summary=$2 where id=$1`, reportID, summary)
		return wrap(err)
	})
	if err != nil {
		return Finding{}, err
	}
	fs, err := s.listFindings(ctx, s.Pool, reportID)
	if err != nil {
		return Finding{}, err
	}
	for _, f := range fs {
		if f.ID == findingID {
			return f, nil
		}
	}
	return Finding{}, ErrNotFound
}

// SetFindingStatus moves one finding's status, keeps the move with its
// reason, and recomputes the report's summary (archived findings are not
// counted). Setting the status it already has records nothing.
func (s *Store) SetFindingStatus(ctx context.Context, reportID, findingID string, status core.FindingStatus, reason, role, actorUserID string) (Finding, error) {
	if !ValidID(findingID) {
		return Finding{}, ErrNotFound
	}
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		var from string
		if err := tx.QueryRow(ctx, `select status from findings where id=$1 and report_id=$2 for update`, findingID, reportID).Scan(&from); err != nil {
			return wrap(err)
		}
		if core.FindingStatus(from) == status {
			return nil
		}
		if _, err := tx.Exec(ctx, `insert into finding_status_changes(id, finding_id, from_status, to_status, reason, by_role, actor_user_id)
			values($1,$2,$3,$4,$5,$6,$7)`, NewID(), findingID, from, string(status), reason, role, nullStr(actorUserID)); err != nil {
			return wrap(err)
		}
		if _, err := tx.Exec(ctx, `update findings set status=$3, updated_at=now() where id=$1 and report_id=$2`, findingID, reportID, string(status)); err != nil {
			return wrap(err)
		}
		fs, err := s.listFindings(ctx, tx, reportID)
		if err != nil {
			return err
		}
		docs := make([]core.Finding, len(fs))
		for i, f := range fs {
			docs[i] = f.Finding
		}
		summary, _ := json.Marshal(core.Summarize(docs))
		_, err = tx.Exec(ctx, `update reports set summary=$2 where id=$1`, reportID, summary)
		return wrap(err)
	})
	if err != nil {
		return Finding{}, err
	}
	fs, err := s.listFindings(ctx, s.Pool, reportID)
	if err != nil {
		return Finding{}, err
	}
	for _, f := range fs {
		if f.ID == findingID {
			return f, nil
		}
	}
	return Finding{}, ErrNotFound
}

// Publish sets the public slug.
func (s *Store) Publish(ctx context.Context, reportID, slug string) error {
	_, err := s.Pool.Exec(ctx, `update reports set published_slug=$2, published_at=now() where id=$1`, reportID, slug)
	return wrap(err)
}

// Unpublish clears the public slug.
// Unpublish takes the report off the public page but keeps its slug, so
// publishing it again restores the same address. Readers are gated on
// published_at, not on the slug, so an unpublished report stays private.
func (s *Store) Unpublish(ctx context.Context, reportID string) error {
	_, err := s.Pool.Exec(ctx, `update reports set published_at=null where id=$1`, reportID)
	return err
}
