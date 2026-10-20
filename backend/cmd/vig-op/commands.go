package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/forefy/vig/backend/internal/api"
	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/github"
)

func cmdClaim(w *workspace, args []string) error {
	fs := flag.NewFlagSet("claim", flag.ContinueOnError)
	noClone := fs.Bool("no-clone", false, "claim only, do not check out the source")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, err := os.Stat(w.jobPath()); err == nil {
		return errors.New("a job is already claimed in " + w.Home + "; finish it (event delivered / fail) or use a fresh sandbox")
	}
	var job api.Job
	if err := w.call(http.MethodPost, "/v1/op/jobs/claim", nil, &job); err != nil {
		return err
	}
	if err := w.saveJob(job); err != nil {
		return err
	}
	printJob(job)
	if *noClone {
		return nil
	}
	if err := checkout(w, job); err != nil {
		return err
	}
	fmt.Printf("source     %s (%s)\n", w.srcDir(), short(job.Audit.HeadSHA))
	return nil
}

// checkout clones the repo and pins it at head_sha. Private repos carry a
// one-hour installation token in the job; it is used once and never written
// to disk.
func checkout(w *workspace, job api.Job) error {
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("git is not installed in this sandbox")
	}
	url := job.CloneURL
	if job.CloneToken != "" {
		url = strings.Replace(url, "https://", "https://x-access-token:"+job.CloneToken+"@", 1)
	}
	_ = os.RemoveAll(w.srcDir())
	if out, err := exec.Command("git", "clone", "--quiet", "--no-checkout", url, w.srcDir()).CombinedOutput(); err != nil {
		return fmt.Errorf("git clone: %s", strings.TrimSpace(strings.ReplaceAll(string(out), job.CloneToken, "***")))
	}
	if out, err := run(w.srcDir(), "git", "checkout", "--quiet", "--detach", job.Audit.HeadSHA); err != nil {
		return fmt.Errorf("git checkout %s: %s", short(job.Audit.HeadSHA), out)
	}
	if job.CloneToken != "" {
		// Drop the credential from the remote so a later `git fetch` cannot leak it.
		_, _ = run(w.srcDir(), "git", "remote", "set-url", "origin", job.CloneURL)
	}
	return nil
}

func cmdStatus(w *workspace) error {
	job, err := w.job()
	if err != nil {
		return err
	}
	var a api.Audit
	// The audit endpoint is user-scoped; the operator reads its job through
	// an idempotent no-op event on the current state is not allowed, so the
	// saved job plus the server's answer to a programs list is the view.
	a = job.Audit
	printJob(job)
	fmt.Println("timeline")
	for _, e := range a.Events {
		fmt.Printf("  %s  %-18s %s\n", e.CreatedAt.UTC().Format("01-02 15:04"), e.Kind, e.Actor)
	}
	return nil
}

func cmdVerify(w *workspace, args []string) error {
	job, err := w.job()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	var sos, matches multiFlag
	fs.Var(&sos, "so", "<program id>=<path to built .so>; repeatable")
	fs.Var(&matches, "match", "<program id>=verified|self_built|mismatch|unverified to record a result you established another way; repeatable")
	if err := fs.Parse(args); err != nil {
		return err
	}
	soFor := kv(sos)
	matchFor := kv(matches)
	pins := make([]core.ProgramPin, 0, len(job.Target.Programs))
	for _, p := range job.Target.Programs {
		pin := core.ProgramPin{ID: p.ID, Slot: p.LastDeploySlot, BytecodeSHA256: p.BytecodeSHA256, Match: core.MatchUnverified}
		switch {
		case matchFor[p.ID] != "":
			pin.Match = core.MatchStatus(matchFor[p.ID])
		case soFor[p.ID] != "":
			h, err := hashELF(soFor[p.ID])
			if err != nil {
				return fmt.Errorf("%s: %w", p.ID, err)
			}
			if h == p.BytecodeSHA256 {
				pin.Match = core.MatchSelfBuilt
			} else {
				pin.Match = core.MatchMismatch
			}
			fmt.Printf("%s  built %s  deployed %s  %s\n", short(p.ID), short(h), short(p.BytecodeSHA256), pin.Match)
		case p.VerifiedBuild == core.MatchVerified && p.VerifiedCommit != "" && strings.HasPrefix(p.VerifiedCommit, job.Audit.HeadSHA[:7]):
			// The registry already ties this bytecode to the audited commit.
			pin.Match = core.MatchVerified
		default:
			if path, ok := findBuiltProgram(w.srcDir(), p.ID); ok {
				h, err := hashELF(path)
				if err == nil {
					if h == p.BytecodeSHA256 {
						pin.Match = core.MatchSelfBuilt
					} else {
						pin.Match = core.MatchMismatch
					}
					fmt.Printf("%s  %s -> %s\n", short(p.ID), filepath.Base(path), pin.Match)
				}
			}
		}
		if pin.BytecodeSHA256 == "" {
			return fmt.Errorf("%s has no deployed bytecode hash; the program was not found on chain at intake", p.ID)
		}
		pins = append(pins, pin)
	}
	if len(pins) == 0 {
		return errors.New("the job has no programs to verify")
	}
	var out api.Audit
	body := struct {
		Programs []core.ProgramPin `json:"programs"`
	}{pins}
	if err := w.call(http.MethodPost, "/v1/op/jobs/"+job.Audit.ID+"/programs", body, &out); err != nil {
		return err
	}
	for _, p := range pins {
		fmt.Printf("recorded   %s %s\n", short(p.ID), p.Match)
	}
	return nil
}

func cmdEvent(w *workspace, args []string) error {
	if len(args) < 1 {
		return errors.New("usage: vig-op event <cloning|programs_verified|analysis|human_review|delivered>")
	}
	job, err := w.job()
	if err != nil {
		return err
	}
	state := core.AuditState(args[0])
	var payload map[string]any
	if len(args) > 1 {
		payload = map[string]any{}
		for _, kvs := range args[1:] {
			k, v, ok := strings.Cut(kvs, "=")
			if !ok {
				return fmt.Errorf("payload %q must be key=value", kvs)
			}
			payload[k] = v
		}
	}
	body := struct {
		State   core.AuditState `json:"state"`
		Payload map[string]any  `json:"payload,omitempty"`
	}{state, payload}
	var out api.Audit
	if err := w.call(http.MethodPost, "/v1/op/jobs/"+job.Audit.ID+"/events", body, &out); err != nil {
		return err
	}
	job.Audit = out
	if err := w.saveJob(job); err != nil {
		return err
	}
	fmt.Printf("state      %s\n", out.State)
	if out.State.Terminal() {
		fmt.Println("job closed; the token is now revoked and this sandbox can be destroyed")
		_ = os.RemoveAll(w.srcDir())
	}
	return nil
}

func cmdReport(w *workspace, args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	check := fs.Bool("check", false, "validate and advise through the API without submitting")
	if err := fs.Parse(args); err != nil {
		return err
	}
	args = fs.Args()
	if len(args) != 1 {
		return errors.New("usage: vig-op report [--check] <findings.json>")
	}
	job, err := w.job()
	if err != nil {
		return err
	}
	b, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	var doc core.FindingsDocument
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("%s: %w", args[0], err)
	}
	if doc.Report.AuditID == "" {
		doc.Report.AuditID = job.Audit.ID
	}
	if doc.Report.Pin.Repo == "" {
		doc.Report.Pin.Repo = job.Audit.Repo
	}
	if doc.Report.Pin.Commit == "" {
		doc.Report.Pin.Commit = job.Audit.HeadSHA
	}
	if doc.Report.Pin.BaseSHA == "" && job.Audit.Coverage == "diff" {
		doc.Report.Pin.BaseSHA = job.Audit.BaseSHA
	}
	if len(doc.Report.Pin.Programs) == 0 {
		for _, p := range job.Target.Programs {
			doc.Report.Pin.Programs = append(doc.Report.Pin.Programs, core.ProgramPin{ID: p.ID, Slot: p.LastDeploySlot, BytecodeSHA256: p.BytecodeSHA256, Match: p.VerifiedBuild})
		}
	}
	if err := doc.Validate(); err != nil {
		return fmt.Errorf("findings.json is not a valid report: %w", err)
	}
	// Locations must exist in the checkout; a typo here would ship a report
	// pointing at nothing.
	if _, err := os.Stat(w.srcDir()); err == nil {
		for _, f := range doc.Findings {
			if f.Location.File == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(w.srcDir(), filepath.FromSlash(f.Location.File))); err != nil {
				return fmt.Errorf("finding %d: %s does not exist in the checkout", f.Ordinal, f.Location.File)
			}
		}
	}
	// Attach the lines each finding points at from the checkout.
	if _, err := os.Stat(w.srcDir()); err == nil {
		github.AttachSnippets(w.srcDir(), doc.Findings)
	}
	// What the report reads better with, before it goes out; --check
	// stops here, with the server's own verdict as well.
	for _, a := range doc.Advise() {
		fmt.Printf("advice     %s\n", a)
	}
	if *check {
		var res api.ReportCheck
		if err := w.call(http.MethodPost, "/v1/op/jobs/"+job.Audit.ID+"/report/check", doc, &res); err != nil {
			return err
		}
		for _, e := range res.Errors {
			fmt.Printf("refused    %s\n", e)
		}
		if res.Valid {
			fmt.Println("valid      the server would accept this document")
		}
		return nil
	}
	var out api.Report
	if err := w.call(http.MethodPost, "/v1/op/jobs/"+job.Audit.ID+"/report", doc, &out); err != nil {
		return err
	}
	s := out.Document.Report.Summary
	fmt.Printf("report     %s  critical %d  high %d  medium %d  low %d  info %d\n", out.ID, s.Critical, s.High, s.Medium, s.Low, s.Info)
	fmt.Println("next       vig-op event human_review, then vig-op event delivered")
	return nil
}

func cmdDelay(w *workspace, args []string) error {
	if len(args) < 2 {
		return errors.New("usage: vig-op delay <duration> <reason>")
	}
	job, err := w.job()
	if err != nil {
		return err
	}
	d, err := time.ParseDuration(args[0])
	if err != nil {
		return fmt.Errorf("duration %q: %w", args[0], err)
	}
	eta := time.Now().Add(d).UTC()
	body := struct {
		ETAAt  string `json:"eta_at"`
		Reason string `json:"reason"`
	}{eta.Format(time.RFC3339), strings.Join(args[1:], " ")}
	var out api.Audit
	if err := w.call(http.MethodPost, "/v1/op/jobs/"+job.Audit.ID+"/delay", body, &out); err != nil {
		return err
	}
	job.Audit = out
	_ = w.saveJob(job)
	fmt.Printf("state      %s  eta %s\n", out.State, eta.Format(time.RFC3339))
	return nil
}

func cmdFail(w *workspace, args []string) error {
	if len(args) < 1 {
		return errors.New("usage: vig-op fail <reason>")
	}
	job, err := w.job()
	if err != nil {
		return err
	}
	body := struct {
		Reason string `json:"reason"`
	}{strings.Join(args, " ")}
	var out api.Audit
	if err := w.call(http.MethodPost, "/v1/op/jobs/"+job.Audit.ID+"/fail", body, &out); err != nil {
		return err
	}
	job.Audit = out
	_ = w.saveJob(job)
	_ = os.RemoveAll(w.srcDir())
	fmt.Printf("state      %s  (%s); credit or slot restored\n", out.State, out.FailedReason)
	return nil
}

func cmdTemplate(w *workspace) error {
	job, err := w.job()
	if err != nil {
		return err
	}
	pin := core.Pin{Repo: job.Audit.Repo, Commit: job.Audit.HeadSHA, BaseSHA: job.Audit.BaseSHA}
	for _, p := range job.Target.Programs {
		pin.Programs = append(pin.Programs, core.ProgramPin{ID: p.ID, Slot: p.LastDeploySlot, BytecodeSHA256: p.BytecodeSHA256, Match: p.VerifiedBuild})
	}
	// The same example the shape endpoint serves, with this job's pin and
	// its first program on the finding.
	doc := core.ExampleDocument(pin)
	doc.Report.AuditID = job.Audit.ID
	doc.Findings[0].Location.ProgramID = firstProgram(job)
	b, _ := json.MarshalIndent(doc, "", "  ")
	fmt.Println(string(b))
	return nil
}

// ---- helpers ----

func printJob(job api.Job) {
	a := job.Audit
	fmt.Printf("job        %s\n", a.ID)
	fmt.Printf("repo       %s @ %s (%s)\n", a.Repo, short(a.HeadSHA), a.Coverage)
	if a.BaseSHA != "" {
		fmt.Printf("base       %s\n", short(a.BaseSHA))
	}
	for _, p := range job.Target.Programs {
		fmt.Printf("program    %s  slot %d  %s  %s\n", p.ID, p.LastDeploySlot, short(p.BytecodeSHA256), p.VerifiedBuild)
	}
	fmt.Printf("scope      %d files, %d lines\n", len(a.Scope.Files), a.Scope.Lines)
	fmt.Printf("state      %s", a.State)
	if a.ETAAt != nil {
		fmt.Printf("  eta %s", a.ETAAt.UTC().Format(time.RFC3339))
	}
	fmt.Println()
	fmt.Printf("token      expires %s\n", job.TokenExpiresAt.UTC().Format(time.RFC3339))
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func firstProgram(job api.Job) string {
	if len(job.Audit.ProgramIDs) > 0 {
		return job.Audit.ProgramIDs[0]
	}
	return ""
}

func run(dir string, name string, args ...string) (string, error) {
	c := exec.Command(name, args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// hashELF hashes a built program the way the chain client hashes deployed
// bytecode: sha256 over the bytes with trailing zero padding stripped, which
// is also what solana-verify compares.
func hashELF(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(b) < 4 || string(b[:4]) != "\x7fELF" {
		return "", fmt.Errorf("%s is not an ELF file", path)
	}
	b = bytes.TrimRight(b, "\x00")
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// findBuiltProgram looks for a .so under target/deploy whose source declares
// the program id. It covers the common case where the operator ran
// `solana-verify build` or `anchor build` before verify.
func findBuiltProgram(src, programID string) (string, bool) {
	sos, _ := filepath.Glob(filepath.Join(src, "target", "deploy", "*.so"))
	if len(sos) == 0 {
		return "", false
	}
	if len(sos) == 1 {
		return sos[0], true
	}
	// Several programs: match the .so whose bytes contain the program id, which
	// declare_id! embeds as the 32-byte key. Cheap and good enough here.
	for _, so := range sos {
		b, err := os.ReadFile(so)
		if err == nil && bytes.Contains(b, []byte(programID)) {
			return so, true
		}
	}
	return "", false
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(s string) error { *m = append(*m, s); return nil }

func kv(items []string) map[string]string {
	out := map[string]string{}
	for _, it := range items {
		k, v, ok := strings.Cut(it, "=")
		if ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}
