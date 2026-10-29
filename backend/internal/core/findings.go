// Package core holds the contracts that never change between the concierge
// phase and the autonomous agent: findings, audit states, ledger rules and
// the target adapter interface. Nothing here imports a chain SDK.
package core

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Severity of a finding. Ordered from most to least severe.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
	SeverityInfo     Severity = "info"
)

var severities = []Severity{SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow, SeverityInfo}

// FindingStatus is set by the report owner as they act on a finding.
type FindingStatus string

const (
	FindingOpen         FindingStatus = "open"
	FindingFixed        FindingStatus = "fixed"
	FindingAcknowledged FindingStatus = "acknowledged"
	FindingDisputed     FindingStatus = "disputed"
	FindingArchived     FindingStatus = "archived" // judged not relevant, with a reason; kept visible, never counted
)

// ExploitableBy says who has to be to reach a finding: the privilege an
// attacker needs, not the method they would use. It is a precondition rather
// than a recipe, so unlike ExploitPath it is shown to every reader - what it
// tells them is how exposed they are, not how to do it.
type ExploitableBy string

const (
	ExploitableAnyone           ExploitableBy = "anyone"            // no privileges at all
	ExploitableAnyUser          ExploitableBy = "any_user"          // any authenticated user of the site, nothing more
	ExploitableInternal         ExploitableBy = "internal"          // requires insider standing: repo, cluster or telemetry access - never a customer
	ExploitableAnySigner        ExploitableBy = "any_signer"        // a funded account that can sign, nothing more
	ExploitableAccountOwner     ExploitableBy = "account_owner"     // must control a particular account or position
	ExploitableAdmin            ExploitableBy = "admin"             // a privileged role inside the program
	ExploitableUpgradeAuthority ExploitableBy = "upgrade_authority" // whoever holds the program's upgrade key
	ExploitableOtherProgram     ExploitableBy = "other_program"     // only reachable through a CPI from a particular caller, or through another service
	ExploitableInternalNetwork  ExploitableBy = "internal_network"  // web: only from inside the network, a VPN session or a pod
	ExploitableAPIKey           ExploitableBy = "api_key"           // web: whoever holds an API key or an integration's credentials
	ExploitableNone             ExploitableBy = "none"              // not reachable with the current instruction set
)

var exploitableBy = []ExploitableBy{ExploitableAnyone, ExploitableAnyUser, ExploitableInternal, ExploitableAnySigner, ExploitableAccountOwner, ExploitableAdmin, ExploitableUpgradeAuthority, ExploitableOtherProgram, ExploitableInternalNetwork, ExploitableAPIKey, ExploitableNone}

func validExploitableBy(e ExploitableBy) bool {
	for _, v := range exploitableBy {
		if v == e {
			return true
		}
	}
	return false
}

// MatchStatus says how the reviewed source relates to the deployed bytecode.
type MatchStatus string

const (
	MatchVerified   MatchStatus = "verified"   // registry-verified build matches
	MatchSelfBuilt  MatchStatus = "self_built" // our own build of the commit hashes to the deployed bytecode
	MatchMismatch   MatchStatus = "mismatch"   // build does not match what is deployed
	MatchUnverified MatchStatus = "unverified" // could not build or compare yet
)

// ProgramPin identifies exactly which deployed artifact a report is about.
type ProgramPin struct {
	ID             string      `json:"id" doc:"Program address" example:"TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"`
	Slot           uint64      `json:"slot" doc:"Slot of the deployment that was reviewed"`
	BytecodeSHA256 string      `json:"bytecode_sha256" doc:"sha256 of the deployed program data"`
	Match          MatchStatus `json:"match" enum:"verified,self_built,mismatch,unverified"`
}

// Pin is the report header. It is what makes a report defensible.
type Pin struct {
	Repo     string       `json:"repo" doc:"owner/name" example:"vig-demo/vault-programs"`
	Commit   string       `json:"commit" doc:"Full commit sha that was reviewed"`
	BaseSHA  string       `json:"base_sha,omitempty" doc:"For diff reviews, the commit findings are relative to"`
	Programs []ProgramPin `json:"programs"`
	Excluded []string     `json:"excluded,omitempty" doc:"Paths in the repository the review left out, as folders like audits/ or contracts/test/"`
}

// Summary counts findings by severity.
type Summary struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
	Info     int `json:"info"`
}

// Location points at code and, for on-chain targets, at the instruction.
// Webapp targets locate a finding at the live endpoint instead: url is set
// and the file fields become the optional source-snapshot reference.
type Location struct {
	File        string `json:"file" doc:"Path relative to the repo root"`
	LineStart   int    `json:"line_start" minimum:"1"`
	LineEnd     int    `json:"line_end,omitempty" minimum:"1"`
	ProgramID   string `json:"program_id,omitempty"`
	Package     string `json:"package,omitempty" doc:"The program or crate in the repository this location belongs to"`
	Instruction string `json:"instruction,omitempty"`
	URL         string `json:"url,omitempty" format:"uri" doc:"For webapp targets, the live endpoint the finding is about"`
}

// Snippet is the code a finding points at, captured from the pinned commit
// when the report is submitted. The clone does not outlive delivery, so the
// report carries its own evidence.
type Snippet struct {
	StartLine int    `json:"start_line" minimum:"1" doc:"Line number of the first line in code"`
	Code      string `json:"code" doc:"Lines around the location, newline separated"`
	Fix       string `json:"fix,omitempty" doc:"The recommended code at this location, shown against the located lines"`
	FixAt     int    `json:"fix_at,omitempty" minimum:"1" doc:"Line the recommendation is shown before. Defaults to just after the located lines, which is wrong when the fix is a guard that has to precede them"`
}

// FixKind says what closes a finding. A code fix is a change to the
// located lines and must carry a placed patch; an operational fix (rotate
// a secret, revoke a token, change a setting) has no lines to patch.
type FixKind string

const (
	FixCode        FixKind = "code"
	FixOperational FixKind = "operational"
)

// StatusChange is one move of a finding's status, kept forever. Archiving
// always carries a reason: a finding judged not relevant is a lesson for
// the next report on the same code, not a deletion.
type StatusChange struct {
	From   FindingStatus `json:"from" enum:"open,fixed,acknowledged,disputed,archived"`
	To     FindingStatus `json:"to" enum:"open,fixed,acknowledged,disputed,archived"`
	Reason string        `json:"reason,omitempty" maxLength:"140" doc:"Why; required when archiving"`
	By     string        `json:"by" enum:"owner,admin,operator"`
	At     time.Time     `json:"at"`
}

// SeverityChange is one re-rating of a finding, kept forever: the owner
// disputes a rating down with a reason, an admin rates it up on review, a
// revision changes it. The current severity is the last change's To.
type SeverityChange struct {
	From   Severity  `json:"from" enum:"critical,high,medium,low,info"`
	To     Severity  `json:"to" enum:"critical,high,medium,low,info"`
	Reason string    `json:"reason" minLength:"3" maxLength:"140" doc:"Why, in a line"`
	By     string    `json:"by" enum:"owner,admin,operator" doc:"Who re-rated it"`
	At     time.Time `json:"at"`
}

// FixSite is the code a fix fence applies to, captured from the pinned
// commit when the report is submitted: a fence whose info line names a
// location (```rust programs/x/src/lib.rs:41-45) is shown as a patch against
// these lines, the located range as removed and the fence as added.
type FixSite struct {
	Location Location `json:"location"`
	Snippet  *Snippet `json:"snippet,omitempty"`
}

// SitesKind says how a finding's extra sites relate to each other.
type SitesKind string

const (
	SitesFlow        SitesKind = "flow"        // ordered steps: the input enters here, reaches here, triggers here
	SitesOccurrences SitesKind = "occurrences" // the same mistake in several places
)

// Site is one more place a finding points at, with its own code.
type Site struct {
	Location Location `json:"location"`
	Note     string   `json:"note,omitempty" maxLength:"80" doc:"A few words for the tab: what happens at this site"`
	Snippet  *Snippet `json:"snippet,omitempty"`
	Visuals  []Visual `json:"visuals,omitempty" maxItems:"1" doc:"At most one attachment for this step, shown under its code: a capture, a recording or a file, served from the stored bytes by id"`
}

// VisualKind says how the report shows an attachment.
type VisualKind string

const (
	VisualImage VisualKind = "image" // a picture, shown inline
	VisualVideo VisualKind = "video" // a recording, played inline
	VisualFile  VisualKind = "file"  // anything else: a PoC script, a transaction dump, a log; offered as a download
)

// VisualImageMimes and VisualVideoMimes are the formats the report renders
// inline. Any other well-formed type is stored and served as a download.
var (
	VisualImageMimes = []string{"image/png", "image/jpeg", "image/webp", "image/gif"}
	VisualVideoMimes = []string{"video/mp4", "video/webm"}
)

// Visual is an attachment that shows the finding happening or lets the
// reader reproduce it: a screenshot of the surface with the PoC mid-flight, a
// recording of the exploit running, the script that ran it. The bytes live
// in the store keyed by id; the findings document carries only the
// reference, so revisions keep them. Mime, name and size are what the
// upload returned; the server rebinds them from the stored bytes when the
// document is posted.
type Visual struct {
	ID      string     `json:"id" minLength:"36" maxLength:"36" format:"uuid" doc:"Stored attachment id, as returned by the upload endpoint"`
	Kind    VisualKind `json:"kind,omitempty" enum:"image,video,file" doc:"How the report shows it: image and video inline, file as a download. Filled from mime when absent"`
	Caption string     `json:"caption" minLength:"3" maxLength:"140" doc:"What the attachment shows"`
	Mime    string     `json:"mime" minLength:"3" maxLength:"100" doc:"image/png, image/jpeg, image/webp and image/gif render as pictures; video/mp4 and video/webm as a player; any other type is a download"`
	Name    string     `json:"name,omitempty" maxLength:"120" doc:"File name the reader saves a download as; required for files, ignored for images and videos"`
	Size    int64      `json:"size,omitempty" minimum:"0" doc:"Bytes, shown on a download"`
	Role    VisualRole `json:"role,omitempty" enum:"proof" doc:"proof: held as evidence that the method in evidence was run (the tool's screen, its output), kept with the finding and shown to operators and admins, never in the report"`
}

// VisualRole says what an attachment is for. Empty is the default: shown
// in the report where it sits.
type VisualRole string

// VisualProof is kept as evidence of the method, not shown to the reader.
const VisualProof VisualRole = "proof"

var mimeRE = regexp.MustCompile(`^[a-z0-9][a-z0-9!#$&^_.+-]*/[a-z0-9][a-z0-9!#$&^_.+-]*$`)

// ValidVisualMime says whether a type can be stored at all: well-formed
// type/subtype, lowercase, no parameters.
func ValidVisualMime(m string) bool {
	return len(m) <= 100 && mimeRE.MatchString(m)
}

// VisualKindOf says how a type renders: image and video inline, anything
// else as a file the reader downloads.
func VisualKindOf(mime string) VisualKind {
	mime = strings.ToLower(strings.TrimSpace(mime))
	for _, v := range VisualImageMimes {
		if v == mime {
			return VisualImage
		}
	}
	for _, v := range VisualVideoMimes {
		if v == mime {
			return VisualVideo
		}
	}
	return VisualFile
}

// Normalize lowercases the type, fills the kind from it and checks what
// each kind needs: a file carries a name, an inline attachment does not
// need one.
func (v *Visual) Normalize() error {
	v.Mime = strings.ToLower(strings.TrimSpace(v.Mime))
	v.Name = strings.TrimSpace(v.Name)
	v.Caption = strings.TrimSpace(v.Caption)
	if len(v.ID) != 36 {
		return errors.New("id must be a stored attachment id")
	}
	if !ValidVisualMime(v.Mime) {
		return fmt.Errorf("mime %q is not a type/subtype", v.Mime)
	}
	if len(v.Caption) < 3 {
		return errors.New("caption is required")
	}
	kind := VisualKindOf(v.Mime)
	if v.Kind != "" && v.Kind != kind {
		return fmt.Errorf("kind %q does not match mime %q", v.Kind, v.Mime)
	}
	if v.Role != "" && v.Role != VisualProof {
		return fmt.Errorf("role %q is not proof", v.Role)
	}
	v.Kind = kind
	if kind == VisualFile {
		if v.Name == "" {
			return errors.New("a file needs a name")
		}
		if strings.ContainsAny(v.Name, "/\\\x00") || v.Name == "." || v.Name == ".." {
			return fmt.Errorf("name %q must be a bare file name", v.Name)
		}
	}
	if v.Size < 0 {
		return errors.New("size must not be negative")
	}
	return nil
}

// Finding is one issue in a report.
type Finding struct {
	ID                  string           `json:"id,omitempty" doc:"Server-assigned finding id, present on stored reports"`
	Ordinal             int              `json:"ordinal" minimum:"1" doc:"Required."`
	Severity            Severity         `json:"severity" enum:"critical,high,medium,low,info" doc:"Required."`
	Title               string           `json:"title" minLength:"3" maxLength:"140" doc:"Required."`
	Location            Location         `json:"location" doc:"Required."`
	Description         string           `json:"description" minLength:"1" doc:"Required. Plain text for a reader in a hurry: blank lines separate short paragraphs, lines starting with '- ' are bullets, backticks mark code, files, functions and errors, [[Title of another finding]] links to it. No paragraph over 600 characters; three or four blocks at most"`
	ExploitableBy       ExploitableBy    `json:"exploitable_by,omitempty" enum:"anyone,any_user,internal,any_signer,account_owner,admin,upgrade_authority,other_program,internal_network,api_key,none" doc:"Recommended. The privilege an attacker needs to reach this finding, from least to most: anyone (no account, no session), any_user (any signed-in account) or any_signer (any funded key), account_owner (a member of the affected account or position), api_key (an integration's credentials), internal_network (inside the network or a VPN), admin (a privileged role), upgrade_authority (the platform operator or upgrade key), other_program (only through another program or service), internal (insider standing on the code or the telemetry: repo, snapshot or store access, never a customer), none. Shown to every reader"`
	ExploitPath         string           `json:"exploit_path,omitempty" doc:"Optional. Shown only to verified owners"`
	ExploitPathWithheld bool             `json:"exploit_path_withheld,omitempty" doc:"An exploit path exists on this finding but is shown only to verified owners"`
	Fix                 string           `json:"fix" minLength:"1" doc:"Required. A '- ' bullet per change. A code fence (three backticks on their own line, closed the same way) right after a bullet carries the definite fix: the correct code, the exact header value, the configuration line. A fence whose info line names a place, like rust src/lib.rs:41-45, is shown as a patch replacing those lines (path:line alone inserts before it)"`
	FixKind             FixKind          `json:"fix_kind,omitempty" enum:"code,operational" doc:"Optional. operational says there are no lines to patch (rotate, revoke, configure), so the dry run stops advising a placed fence for a finding that names a file"`
	References          []string         `json:"references,omitempty" doc:"Optional."`
	Exhaustion          []Exhaustion     `json:"exhaustion,omitempty" doc:"What the reproduction ruled out: one answer per item vig lists for this finding (the dry run prints them), done or not, with a note when not. Required on a submitted finding"`
	ReferenceCode       []ReferenceCode  `json:"reference_code,omitempty" doc:"Set by the server: the lines each GitHub file link in references points at, read when the report was stored and pinned to their commit"`
	Snippet             *Snippet         `json:"snippet,omitempty" doc:"The enclosing item at the location"`
	SitesKind           SitesKind        `json:"sites_kind,omitempty" enum:"flow,occurrences" doc:"Recommended with sites. How the sites relate; required when sites are present"`
	Sites               []Site           `json:"sites,omitempty" doc:"Recommended for critical and high. Further places this finding points at, in order"`
	Status              FindingStatus    `json:"status" enum:"open,fixed,acknowledged,disputed,archived" doc:"Optional, defaults to open. Set by the owner afterwards; archived means judged not relevant, with a reason on status_changes, and is not counted"`
	CarriedFromID       string           `json:"carried_from_id,omitempty" doc:"Optional. Finding id in the previous report this continues"`
	Evidence            *Evidence        `json:"evidence,omitempty" doc:"Recommended. What established the finding beyond reading the code"`
	Visuals             []Visual         `json:"visuals,omitempty" doc:"Optional. Attachments for the finding as a whole, shown on the impact item: the script that ran it, a recording, a dump. Served from the stored bytes by id. A finding carries one recording in all, here or on a step"`
	Impact              string           `json:"impact,omitempty" doc:"Recommended. What the finding costs when it happens: who loses what, in plain terms. Same shape as description; the last item of the reproduction chain, carrying the finding-level attachments"`
	StatusChanges       []StatusChange   `json:"status_changes,omitempty" doc:"Server-kept history of status moves, oldest first, archiving with its reason. Ignored when posted"`
	SeverityChanges     []SeverityChange `json:"severity_changes,omitempty" doc:"Server-kept history of re-ratings, oldest first; severity is the current rating. Ignored when posted"`
	FixSites            []FixSite        `json:"fix_sites,omitempty" doc:"Server-captured code for every fix fence that names a location; the page renders such a fence as a patch against these lines"`
	NormalUse           string           `json:"normal_use,omitempty" doc:"Recommended. How this code is ordinarily used: who calls it, with what, and what comes back. Same shape as description; the first item of the reproduction chain"`
	Expected            string           `json:"expected,omitempty" doc:"Recommended. What the code is supposed to guarantee here, the groundwork the steps break. Same shape as description; follows normal use on the chain"`
}

// EvidenceKind says how a finding was established.
type EvidenceKind string

const (
	EvidenceStatic   EvidenceKind = "static"   // read and reasoned about the source
	EvidenceFuzz     EvidenceKind = "fuzz"     // reproduced by a fuzzing harness
	EvidenceOnChain  EvidenceKind = "onchain"  // observed in a transaction on the network
	EvidenceBytecode EvidenceKind = "bytecode" // established from the deployed image
	EvidenceLive     EvidenceKind = "live"     // observed against the running target, captures on file
)

// Evidence ties a finding to an artifact a reader can check: a crash input
// hash, a transaction signature, a bytecode offset.
type Evidence struct {
	Kind     EvidenceKind `json:"kind" enum:"static,fuzz,onchain,bytecode,live"`
	Tool     string       `json:"tool,omitempty" doc:"What produced the artifact, e.g. trident" maxLength:"60"`
	Artifact string       `json:"artifact,omitempty" doc:"Crash input hash, transaction signature, offset or harness name" maxLength:"200"`
}

// Ran names what the evidence claims was executed, or nothing for a
// static reading with no tool: "burp", "fuzzing with trident", "a live
// probe", "an on-chain read". What ran can be shown; what was read cannot.
func (e *Evidence) Ran() string {
	if e == nil {
		return ""
	}
	tool := strings.TrimSpace(e.Tool)
	switch e.Kind {
	case EvidenceFuzz:
		if tool != "" {
			return "fuzzing with " + tool
		}
		return "fuzzing"
	case EvidenceLive:
		if tool != "" {
			return "a live probe with " + tool
		}
		return "a live probe"
	case EvidenceOnChain:
		if tool != "" {
			return "an on-chain read with " + tool
		}
		return "an on-chain read"
	}
	if tool != "" {
		return tool
	}
	return ""
}

// Attached counts the finding's attachments, on itself and on its steps.
func (f Finding) Attached() int {
	n := len(f.Visuals)
	for _, st := range f.Sites {
		n += len(st.Visuals)
	}
	return n
}

// WithoutProof drops the attachments held as proof of the method; the
// report shows the rest.
func (f Finding) WithoutProof() Finding {
	keep := func(vs []Visual) []Visual {
		if len(vs) == 0 {
			return vs
		}
		out := make([]Visual, 0, len(vs))
		for _, v := range vs {
			if v.Role != VisualProof {
				out = append(out, v)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	f.Visuals = keep(f.Visuals)
	if len(f.Sites) > 0 {
		sites := make([]Site, len(f.Sites))
		copy(sites, f.Sites)
		for i := range sites {
			sites[i].Visuals = keep(sites[i].Visuals)
		}
		f.Sites = sites
	}
	return f
}

// WithoutProof is the document as the report shows it: proof attachments
// stay with the stored finding and leave the page.
func (d FindingsDocument) WithoutProof() FindingsDocument {
	out := d
	out.Findings = make([]Finding, len(d.Findings))
	for i, f := range d.Findings {
		out.Findings[i] = f.WithoutProof()
	}
	return out
}

// Methods records how a report was produced. Every flag is a fact the
// report page shows as a chip; fuzzing carries its campaign numbers.
type Methods struct {
	Static   bool     `json:"static" doc:"Source was read and reasoned about"`
	Bytecode bool     `json:"bytecode" doc:"The deployed image was profiled or disassembled"`
	OnChain  bool     `json:"onchain" doc:"Live accounts or transactions were inspected"`
	Live     bool     `json:"live,omitempty" doc:"The running target was probed and observed, not just its source"`
	Fuzzing  *Fuzzing `json:"fuzzing,omitempty"`
}

// Fuzzing is one campaign's numbers.
type Fuzzing struct {
	Tool      string `json:"tool" doc:"Harness runner, e.g. trident, honggfuzz, cargo-fuzz" maxLength:"60"`
	Harnesses int    `json:"harnesses" minimum:"0"`
	Runs      int64  `json:"runs" minimum:"0" doc:"Executions across all harnesses"`
	Crashes   int    `json:"crashes" minimum:"0" doc:"Distinct crashing inputs"`
	DurationS int64  `json:"duration_s" minimum:"0"`
	Coverage  string `json:"coverage,omitempty" doc:"Coverage the tool reported, e.g. 72% edges, or what the invariants covered" maxLength:"120"`
}

// ReportHeader is the report-level part of the findings document.
type ReportHeader struct {
	AuditID string   `json:"audit_id"`
	Pin     Pin      `json:"pin"`
	Summary Summary  `json:"summary"`
	Methods *Methods `json:"methods,omitempty" doc:"How the report was produced; absent on older reports"`
	Known   []Known  `json:"known,omitempty" doc:"Findings earlier reports raised on this code, with their state at the pinned commit; not counted in the summary and not repeated in findings"`
}

// KnownStatus is where a prior report's finding stands at the pinned commit.
type KnownStatus string

const (
	KnownFixed        KnownStatus = "fixed"
	KnownOpen         KnownStatus = "open"
	KnownAcknowledged KnownStatus = "acknowledged"
)

// Known is one finding an earlier report raised on this code.
type Known struct {
	Title     string         `json:"title" minLength:"3" maxLength:"140"`
	Severity  Severity       `json:"severity" enum:"critical,high,medium,low,info"`
	Source    string         `json:"source" minLength:"1" maxLength:"60" doc:"Who raised it and when, e.g. Cyfrin 2026-03"`
	Status    KnownStatus    `json:"status" enum:"fixed,open,acknowledged" doc:"At the pinned commit"`
	Reference string         `json:"reference,omitempty" maxLength:"300" doc:"The report, as a path in the repository or a URL"`
	Code      *ReferenceCode `json:"code,omitempty" doc:"Set by the server: the lines a GitHub file link in reference points at, read when the report was stored"`
}

// FindingsDocument is the single exchange format: the operator CLI posts it,
// the agent emits it, the report page renders it, the JSON export is it.
type FindingsDocument struct {
	Report ReportHeader `json:"report"`
	// dropped is what the last Validate took out: links to findings the
	// document does not carry. Advise says them to the writer.
	dropped  []string
	Findings []Finding `json:"findings" doc:"In ordinal order. Each reads as a chain: normal_use, expected, the code steps (location, then sites in order), impact. fix is bullets, one per change, with a fenced patch under a bullet when the code is definite. GET /v1/findings/shape lists what is required, recommended and optional with a filled example; POST …/report/check validates and advises without storing"`
}

// Summarize recomputes the summary from the findings.
func Summarize(findings []Finding) Summary {
	var s Summary
	for _, f := range findings {
		if f.Status == FindingArchived {
			continue
		}
		switch f.Severity {
		case SeverityCritical:
			s.Critical++
		case SeverityHigh:
			s.High++
		case SeverityMedium:
			s.Medium++
		case SeverityLow:
			s.Low++
		case SeverityInfo:
			s.Info++
		}
	}
	return s
}

// Validate checks a findings document as the operator API receives it.
// asPlace reads a location whose file is a URL as the endpoint it is: a
// web finding written with the address in file, and a line number that
// means nothing, shows as the address and nothing else.
func (l *Location) asPlace() {
	f := strings.TrimSpace(l.File)
	lf := strings.ToLower(f)
	if l.URL == "" && (strings.HasPrefix(lf, "http://") || strings.HasPrefix(lf, "https://")) {
		l.URL = f
		l.File = ""
		l.LineStart = 0
		l.LineEnd = 0
	}
}

func (d *FindingsDocument) Validate() error {
	var errs []string
	for i := range d.Findings {
		d.Findings[i].Location.asPlace()
		for j := range d.Findings[i].Sites {
			d.Findings[i].Sites[j].Location.asPlace()
		}
	}
	d.dropped = d.resolveLinks()
	if d.Report.AuditID == "" {
		errs = append(errs, "report.audit_id is required")
	}
	if d.Report.Pin.Repo == "" || !strings.Contains(d.Report.Pin.Repo, "/") {
		errs = append(errs, "report.pin.repo must be owner/name")
	}
	if len(d.Report.Pin.Commit) < 7 {
		errs = append(errs, "report.pin.commit must be a commit sha")
	}
	// An empty program list is a source-only review: the code was audited
	// before or without a deployment, and the pin is the commit alone.
	for i, p := range d.Report.Pin.Programs {
		if p.ID == "" || p.BytecodeSHA256 == "" {
			errs = append(errs, fmt.Sprintf("report.pin.programs[%d] needs id and bytecode_sha256", i))
		}
		if !validMatch(p.Match) {
			errs = append(errs, fmt.Sprintf("report.pin.programs[%d].match is invalid", i))
		}
	}
	for i, p := range d.Report.Pin.Excluded {
		if strings.TrimSpace(p) == "" || strings.Contains(p, "..") || strings.HasPrefix(p, "/") {
			errs = append(errs, fmt.Sprintf("report.pin.excluded[%d] must be a relative path", i))
		}
	}
	for i, k := range d.Report.Known {
		if strings.TrimSpace(k.Title) == "" || strings.TrimSpace(k.Source) == "" {
			errs = append(errs, fmt.Sprintf("report.known[%d] needs title and source", i))
		}
		if !validSeverity(k.Severity) {
			errs = append(errs, fmt.Sprintf("report.known[%d].severity is invalid", i))
		}
		switch k.Status {
		case KnownFixed, KnownOpen, KnownAcknowledged:
		default:
			errs = append(errs, fmt.Sprintf("report.known[%d].status must be fixed, open or acknowledged", i))
		}
	}
	seen := map[int]bool{}
	for i, f := range d.Findings {
		if f.Ordinal < 1 || seen[f.Ordinal] {
			errs = append(errs, fmt.Sprintf("findings[%d].ordinal must be unique and >= 1", i))
		}
		seen[f.Ordinal] = true
		if !validSeverity(f.Severity) {
			errs = append(errs, fmt.Sprintf("findings[%d].severity is invalid", i))
		}
		if err := proseShape(f.Description); err != nil {
			return fmt.Errorf("finding %d description: %w", f.Ordinal, err)
		}
		if err := fixShape(f.Fix); err != nil {
			return fmt.Errorf("finding %d fix: %w", f.Ordinal, err)
		}
		if f.FixKind != "" && f.FixKind != FixCode && f.FixKind != FixOperational {
			errs = append(errs, fmt.Sprintf("findings[%d].fix_kind must be code or operational", i))
		}
		if err := proseShape(f.Impact); err != nil {
			return fmt.Errorf("finding %d impact: %w", f.Ordinal, err)
		}
		if err := proseShape(f.Expected); err != nil {
			return fmt.Errorf("finding %d expected: %w", f.Ordinal, err)
		}
		if err := proseShape(f.NormalUse); err != nil {
			return fmt.Errorf("finding %d normal_use: %w", f.Ordinal, err)
		}
		if f.Title == "" || f.Description == "" || f.Fix == "" {
			errs = append(errs, fmt.Sprintf("findings[%d] needs title, description and fix", i))
		}
		if f.Location.URL == "" && (f.Location.File == "" || f.Location.LineStart < 1) {
			errs = append(errs, fmt.Sprintf("findings[%d].location needs file and line_start, or a url", i))
		}
		if f.Location.LineEnd != 0 && f.Location.LineEnd < f.Location.LineStart {
			errs = append(errs, fmt.Sprintf("findings[%d].location.line_end before line_start", i))
		}
		if f.Status == "" {
			d.Findings[i].Status = FindingOpen
		} else if !validFindingStatus(f.Status) {
			errs = append(errs, fmt.Sprintf("findings[%d].status is invalid", i))
		}
		if f.ExploitableBy != "" && !validExploitableBy(f.ExploitableBy) {
			errs = append(errs, fmt.Sprintf("findings[%d].exploitable_by %q is not one of %v", i, f.ExploitableBy, exploitableBy))
		}
	}
	for i, f := range d.Findings {
		if f.Evidence != nil {
			switch f.Evidence.Kind {
			case EvidenceStatic, EvidenceFuzz, EvidenceOnChain, EvidenceBytecode, EvidenceLive:
			default:
				errs = append(errs, fmt.Sprintf("findings[%d].evidence.kind %q is not static, fuzz, onchain, bytecode or live", i, f.Evidence.Kind))
			}
		}
		for j := range f.Visuals {
			if err := f.Visuals[j].Normalize(); err != nil {
				errs = append(errs, fmt.Sprintf("findings[%d].visuals[%d]: %v", i, j, err))
			}
		}
		// One recording per finding, on a step or on itself: a second one
		// is a second finding or a capture. Counted by mime so it holds
		// whether or not kinds were filled.
		videos := 0
		for _, v := range f.Visuals {
			if VisualKindOf(v.Mime) == VisualVideo {
				videos++
			}
		}
		for _, st := range f.Sites {
			for _, v := range st.Visuals {
				if VisualKindOf(v.Mime) == VisualVideo {
					videos++
				}
			}
		}
		if videos > 1 {
			errs = append(errs, fmt.Sprintf("findings[%d] carries %d recordings; one per finding, across its steps and its own attachments", i, videos))
		}
		// A method that ran leaves something behind: the tool's screen, its
		// output, a recording. Evidence that names a tool, or says the
		// target was probed, fuzzed or read on chain, has to come with it;
		// a static reading needs nothing.
		errs = append(errs, d.Findings[i].exhaustionErrors(i)...)
		if claim := f.Evidence.Ran(); claim != "" && f.Attached() == 0 {
			errs = append(errs, fmt.Sprintf("findings[%d]: evidence says %s but nothing is attached; attach the capture that proves it (role proof keeps it out of the report)", i, claim))
		}
		for j := range f.Sites {
			if len(f.Sites[j].Visuals) > 1 {
				errs = append(errs, fmt.Sprintf("findings[%d].sites[%d] carries %d attachments; a step shows one, the rest go on the finding", i, j, len(f.Sites[j].Visuals)))
			}
			for k := range f.Sites[j].Visuals {
				if err := f.Sites[j].Visuals[k].Normalize(); err != nil {
					errs = append(errs, fmt.Sprintf("findings[%d].sites[%d].visuals[%d]: %v", i, j, k, err))
				}
			}
		}
	}
	if m := d.Report.Methods; m != nil && m.Fuzzing != nil {
		fz := m.Fuzzing
		if fz.Tool == "" {
			errs = append(errs, "report.methods.fuzzing.tool is required")
		}
		if fz.Runs < 0 || fz.Crashes < 0 || fz.Harnesses < 0 || fz.DurationS < 0 {
			errs = append(errs, "report.methods.fuzzing counts must not be negative")
		}
	}
	for i, f := range d.Findings {
		if len(f.Sites) > 0 && f.SitesKind != SitesFlow && f.SitesKind != SitesOccurrences {
			errs = append(errs, fmt.Sprintf("findings[%d].sites_kind must be flow or occurrences", i))
		}
		for j, st := range f.Sites {
			if st.Location.URL == "" && (st.Location.File == "" || st.Location.LineStart < 1) {
				errs = append(errs, fmt.Sprintf("findings[%d].sites[%d].location needs a file and line_start, or a url", i, j))
			}
			if st.Location.LineEnd != 0 && st.Location.LineEnd < st.Location.LineStart {
				errs = append(errs, fmt.Sprintf("findings[%d].sites[%d]: line_end before line_start", i, j))
			}
		}
	}
	// The summary is recomputed even when validation fails, so a rejected
	// document never carries a stale summary.
	if s := Summarize(d.Findings); s != d.Report.Summary {
		d.Report.Summary = s
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// ForViewer returns a copy with owner-only fields removed when the viewer is
// not a verified owner of every program in the pin.
func (d FindingsDocument) ForViewer(isOwner bool) FindingsDocument {
	if isOwner {
		return d
	}
	out := d
	out.Findings = make([]Finding, len(d.Findings))
	for i, f := range d.Findings {
		out.Findings[i] = f.ForViewer(isOwner)
	}
	return out
}

// ForViewer strips what a reader who does not own the programs must not see.
// It keeps the fact that something was stripped: a finding that never had an
// exploit path and one whose exploit path is withheld are different, and a
// report that cannot tell them apart has to claim the withholding over all of
// its findings at once.
func (f Finding) ForViewer(isOwner bool) Finding {
	if isOwner {
		return f
	}
	f.ExploitPathWithheld = f.ExploitPath != ""
	f.ExploitPath = ""
	return f
}

func validSeverity(s Severity) bool {
	for _, v := range severities {
		if v == s {
			return true
		}
	}
	return false
}

func validFindingStatus(s FindingStatus) bool {
	switch s {
	case FindingOpen, FindingFixed, FindingAcknowledged, FindingDisputed, FindingArchived:
		return true
	}
	return false
}

func validMatch(m MatchStatus) bool {
	switch m {
	case MatchVerified, MatchSelfBuilt, MatchMismatch, MatchUnverified:
		return true
	}
	return false
}

// proseParagraphMax is the longest paragraph a finding may carry. Longer
// text is a wall the reader skips; the writer breaks it with a blank line
// or a bullet list. proseCodeMax bounds a fenced code block: it is the
// correct code or the exact value, not a file.
const (
	proseParagraphMax = 600
	proseCodeMax      = 2000
)

// proseBlock is one unit of finding prose: a paragraph, a bullet list, or a
// fenced code block.
type proseBlock struct {
	code bool
	text string
	info string // the fence's info line, e.g. "rust" or "rust src/lib.rs:41-45"
}

// proseBlocks splits prose into blocks. Blank lines separate paragraphs; a
// line that is exactly ``` or ```lang opens a code block that runs to the
// next ``` line and may hold blank lines. An unclosed fence is an error.
func proseBlocks(text string) ([]proseBlock, error) {
	var out []proseBlock
	var para []string
	flush := func() {
		if t := strings.TrimSpace(strings.Join(para, "\n")); t != "" {
			out = append(out, proseBlock{text: t})
		}
		para = nil
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			flush()
			info := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "```"))
			var code []string
			closed := false
			for i++; i < len(lines); i++ {
				if strings.TrimSpace(lines[i]) == "```" {
					closed = true
					break
				}
				code = append(code, lines[i])
			}
			if !closed {
				return nil, errors.New("a ``` code fence is never closed")
			}
			out = append(out, proseBlock{code: true, text: strings.Join(code, "\n"), info: info})
			continue
		}
		if strings.TrimSpace(l) == "" {
			flush()
			continue
		}
		para = append(para, l)
	}
	flush()
	return out, nil
}

// proseShape checks the light structure finding prose is written in:
// blank lines between paragraphs, "- " bullets, backticks for identifiers,
// ``` fences around code. A bullet list may run long; a paragraph may not;
// a code block is bounded on its own.
func proseShape(text string) error {
	blocks, err := proseBlocks(text)
	if err != nil {
		return err
	}
	for _, b := range blocks {
		if b.code {
			if len(b.text) > proseCodeMax {
				return fmt.Errorf("a code block is %d characters; keep each under %d", len(b.text), proseCodeMax)
			}
			continue
		}
		if strings.HasPrefix(b.text, "- ") {
			continue
		}
		if len(b.text) > proseParagraphMax {
			return fmt.Errorf("a paragraph is %d characters; keep each under %d, split with a blank line or use '- ' bullets", len(b.text), proseParagraphMax)
		}
	}
	return nil
}

// fixShape is the stricter shape of a fix: every line of prose is a "- "
// bullet, one per change, and a ``` fence right after a bullet carries the
// correct code, the exact header value, the configuration line - whatever
// closes the finding outright. A bullet with no definite code stands alone.
func fixShape(text string) error {
	if strings.TrimSpace(text) == "" {
		return nil // reported as a missing field, not as a shape
	}
	if err := proseShape(text); err != nil {
		return err
	}
	blocks, _ := proseBlocks(text)
	bullets := 0
	for _, b := range blocks {
		if b.code {
			if _, _, err := fenceLocation(b.info); err != nil {
				return err
			}
			continue
		}
		for _, l := range strings.Split(b.text, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(l), "- ") {
				return fmt.Errorf("fix lines start with '- ' (one bullet per change); %q does not. Put the correct code in a ``` fence after its bullet", trunc(l, 40))
			}
			bullets++
		}
	}
	if bullets == 0 {
		return errors.New("fix needs at least one '- ' bullet")
	}
	return nil
}

var fenceLocRE = regexp.MustCompile(`^([^\s:]+):(\d+)(?:-(\d+))?$`)

// fenceLocation reads the location a fix fence names: the second word of
// its info line as path:start or path:start-end. The first word is the
// language. ok is false when the fence names none.
func fenceLocation(info string) (loc Location, ok bool, err error) {
	words := strings.Fields(info)
	if len(words) < 2 {
		return loc, false, nil
	}
	m := fenceLocRE.FindStringSubmatch(words[1])
	if m == nil || strings.Contains(m[1], "..") || strings.HasPrefix(m[1], "/") {
		return loc, false, fmt.Errorf("fence %q: the place a fix applies is written path:line or path:start-end", info)
	}
	loc.File = m[1]
	loc.LineStart, _ = strconv.Atoi(m[2])
	if m[3] != "" {
		loc.LineEnd, _ = strconv.Atoi(m[3])
	}
	if loc.LineStart < 1 || (loc.LineEnd != 0 && loc.LineEnd < loc.LineStart) {
		return loc, false, fmt.Errorf("fence %q: lines must be 1 or more and end after start", info)
	}
	return loc, true, nil
}

// FixLocations lists the places a fix's fences apply to, in order, without
// repeats. The checkout capture fills a FixSite for each.
func FixLocations(fix string) []Location {
	blocks, err := proseBlocks(fix)
	if err != nil {
		return nil
	}
	var out []Location
	seen := map[Location]bool{}
	for _, b := range blocks {
		if !b.code {
			continue
		}
		if loc, ok, err := fenceLocation(b.info); ok && err == nil && !seen[loc] {
			seen[loc] = true
			out = append(out, loc)
		}
	}
	return out
}

func trunc(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// Shape is what an agent needs before it writes a document: which finding
// fields must be there, which the report reads better with, which are
// free, and a filled example in the shape the page renders.
type Shape struct {
	Chain       []string         `json:"chain" doc:"The order a finding reads in on the page"`
	Required    []string         `json:"required"`
	Recommended []string         `json:"recommended"`
	Optional    []string         `json:"optional"`
	Rules       []string         `json:"rules" doc:"What validation refuses, in one line each"`
	Example     FindingsDocument `json:"example"`
}

// DescribeShape returns the shape with an example built around a pin.
func DescribeShape(pin Pin) Shape {
	return Shape{
		Chain:       []string{"normal_use", "expected", "location + sites (flow: ordered steps)", "impact"},
		Required:    []string{"ordinal", "severity", "title", "location.file", "location.line_start", "description", "fix", "exhaustion (one answer per item the dry run lists)"},
		Recommended: []string{"normal_use", "expected", "impact", "exploitable_by", "evidence", "sites_kind + sites (critical and high)", "sites[].note", "a fenced patch in fix with its place (```rust path:start-end)", "visuals (one capture or file per step, the script and one recording on the finding; a role proof capture of every tool evidence names)"},
		Optional:    []string{"[[Title of another finding]] in prose, when it has to be read first", "exploit_path (owners only)", "references", "status", "carried_from_id", "report.pin.excluded", "report.known", "report.methods"},
		Rules: []string{
			"prose: paragraphs under 600 characters, blank lines between them, '- ' bullets, backticks for identifiers, ``` fences for code (under 2000 characters)",
			"fix: every line a '- ' bullet; a fence right after a bullet is its definite code; a fence place is path:line or path:start-end",
			"links: [[Title of another finding]] in prose is set as a link to it; one to a finding not in the report, or to the finding itself, is dropped and its words kept",
			"sites: sites_kind is flow or occurrences; each site has file and line_start; at most one attachment per site",
			"visuals: ids come from the upload endpoint for this audit; mime, name and size are rebound from the stored bytes; one recording per finding",
			"evidence that names a tool, or says live, fuzz or onchain, needs an attachment on the finding or a step: the tool's screen or output; role proof keeps it out of the report (vig-op attach --proof)",
			"exhaustion: vig lists five to ten things the reproduction had to rule out (the dry run prints them for each finding); answer every one with done true or false, a note when not done; the report shows them as a row of dots",
			"report.pin must match the audited repo and commit; the summary is recomputed",
		},
		Example: ExampleDocument(pin),
	}
}

// ExampleDocument is a filled findings document: the CLI template and the
// shape endpoint print it, with the pin of the job when there is one.
func ExampleDocument(pin Pin) FindingsDocument {
	if pin.Programs == nil {
		pin.Programs = []ProgramPin{}
	}
	if pin.Excluded == nil {
		pin.Excluded = []string{}
	}
	return FindingsDocument{
		Report: ReportHeader{
			Pin:     pin,
			Known:   []Known{},
			Methods: &Methods{Static: true, Bytecode: false, OnChain: false, Fuzzing: &Fuzzing{Tool: "", Harnesses: 0, Runs: 0, Crashes: 0, DurationS: 0}},
		},
		Findings: []Finding{exampleExhausted()},
	}
}

// ExampleFinding is one finding with every field in the shape the page
// renders, prose placeholders saying what goes where.
func ExampleFinding() Finding {
	return Finding{
		Ordinal: 1, Severity: SeverityHigh, Title: "",
		Location:      Location{File: "programs/<name>/src/lib.rs", LineStart: 1, Instruction: ""},
		Description:   "One short paragraph on what is wrong.\n\n- a bullet per consequence\n- `backticks` for files, functions and errors\n\nKeep every paragraph under 600 characters; the report rejects longer ones.",
		NormalUse:     "How this code is ordinarily used: who calls it, with what, and what comes back. Shown first on the chain.",
		Expected:      "What the code is supposed to guarantee here: the invariant the steps break. Shown after normal use.",
		Impact:        "Who loses what when this happens. Shown last on the chain, with the finding's attachments.",
		ExploitableBy: ExploitableAnyone, // anyone | any_user | any_signer | account_owner | api_key | internal_network | admin | upgrade_authority | other_program | none
		ExploitPath:   "",
		Fix:           "- one bullet per change; name where its code goes and it renders as a patch\n```rust programs/<name>/src/lib.rs:1-1\n// the correct code for those lines\n```\n\n- a bullet with no definite code stands alone",
		References:    []string{},
		Status:        FindingOpen,
		Evidence:      &Evidence{Kind: EvidenceStatic, Artifact: ""},
		// More places this finding points at. sites_kind is "flow" (ordered
		// steps, each note is the tab caption) or "occurrences" (the same
		// mistake elsewhere). Snippets are attached from the checkout. Each
		// step shows at most one attachment; the finding's own ride on impact.
		SitesKind: SitesFlow,
		Sites:     []Site{{Location: Location{File: "programs/<name>/src/lib.rs", LineStart: 1}, Note: "input enters", Visuals: []Visual{}}},
		Visuals:   []Visual{},
	}
}

// Advise lists what a valid document still leaves out: the recommended
// fields the report reads better with. Nothing here blocks a post; the
// check endpoint and the CLI print it so the writer can add what is
// missing before submitting.
func (d *FindingsDocument) Advise() []string {
	var out []string
	say := func(f Finding, msg string) { out = append(out, fmt.Sprintf("finding %d: %s", f.Ordinal, msg)) }
	out = append(out, d.dropped...)
	for _, f := range d.Findings {
		if linked := d.Linked(f); len(linked) > 0 {
			var to []string
			for _, n := range linked {
				to = append(to, fmt.Sprintf("%d", n))
			}
			say(f, fmt.Sprintf("links to finding %s; a link is for a finding the reader has to read first, not a see-also; keep it only if this one cannot be understood without it", strings.Join(to, ", ")))
		}
		if f.NormalUse == "" {
			say(f, "no normal_use; the chain opens on the code instead of on how it is ordinarily used")
		}
		if f.Expected == "" {
			say(f, "no expected; say what the code must guarantee at the location")
		}
		if f.Impact == "" {
			say(f, "no impact; the chain ends on the last step instead of on who loses what")
		}
		if (f.Severity == SeverityCritical || f.Severity == SeverityHigh) && len(f.Sites) == 0 {
			say(f, "no sites; a critical or high finding reads best as a flow of steps (sites_kind flow, one note per step)")
		}
		if f.SitesKind == SitesFlow {
			for j, st := range f.Sites {
				if st.Note == "" {
					say(f, fmt.Sprintf("sites[%d] has no note; the tab shows file:line instead of what happens there", j))
				}
			}
		}
		if f.ExploitableBy == "" {
			say(f, "no exploitable_by; the reader cannot tell who can reach it")
		}
		if f.Evidence == nil {
			say(f, "no evidence; say how it was established (static, fuzz, onchain, bytecode, live)")
		}
		if f.FixKind == FixOperational {
			// Declared operational: nothing to patch, nothing to advise.
		} else if f.Location.File != "" && len(FixLocations(f.Fix)) == 0 {
			say(f, "names "+f.Location.File+" but the fix carries no patch; add a ``` fence with path:start-end for the definite code, or fix_kind operational when there are no lines to change")
		} else if !strings.Contains(f.Fix, "```") {
			say(f, "fix has no fenced code; add the definite code with its place (```rust path:start-end) when one exists")
		} else if len(FixLocations(f.Fix)) == 0 {
			say(f, "fix fences name no place; put path:start-end on the fence line so it renders as a patch against those lines")
		}
		attached := f.Attached()
		if attached == 0 && (f.Severity == SeverityCritical || f.Severity == SeverityHigh) {
			say(f, "no attachments; a capture or recording of the PoC and the script that ran it make it reproducible")
		}
		if done, total := f.ExhaustionDone(); total == 0 {
			say(f, "no exhaustion; answer the items the dry run lists, done or not, a note on each not done")
		} else if done < total {
			var open []string
			for _, a := range f.Exhaustion {
				if !a.Done {
					open = append(open, a.Key)
				}
			}
			say(f, fmt.Sprintf("exhaustion %d of %d; still open: %s", done, total, strings.Join(open, ", ")))
		}
		for _, raw := range f.References {
			if link, ok := ParseBlobLink(raw); ok && !link.IsSHA() {
				say(f, fmt.Sprintf("references a file on %s; its lines are read and the link pinned to that commit when the report is stored", link.Rev))
			}
		}
	}
	if d.Report.Methods == nil {
		out = append(out, "report.methods missing; say how the report was produced")
	}
	return out
}

// exampleExhausted is the example finding with its exhaustion answered the
// way a report should: every item, one left open with the reason.
func exampleExhausted() Finding {
	f := ExampleFinding()
	AnswerAll(&f, true, "")
	Answer(&f, "negative", false, "no fix deployed yet to re-test against")
	return f
}
