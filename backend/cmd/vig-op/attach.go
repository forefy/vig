package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/forefy/vig/backend/internal/core"
)

// attachOut is what the upload returns: the reference the document carries.
type attachOut struct {
	ID     string      `json:"id"`
	SHA256 string      `json:"sha256"`
	Visual core.Visual `json:"visual"`
}

// cmdAttach uploads a capture, a recording or a file for a finding and
// writes the reference into findings.json: under the finding, or under one
// of its flow steps when the ordinal is written as finding.step. The place
// is checked before anything is uploaded, so a wrong step number stores
// nothing. Without a findings.json yet, it prints the reference to paste.
func cmdAttach(w *workspace, args []string) error {
	fs := flag.NewFlagSet("attach", flag.ContinueOnError)
	into := fs.String("into", filepath.Join(w.Home, "findings.json"), "the findings document to write the reference into")
	typ := fs.String("type", "", "override the detected content type, e.g. video/mp4")
	proof := fs.Bool("proof", false, "hold it as proof of the method in evidence (the tool's screen, its output): kept with the finding, never shown in the report")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 3 {
		return errors.New(`usage: vig-op attach [--into findings.json] [--type mime] [--proof] <ordinal>[.<step>] <file> "<caption>"`)
	}
	ordinal, step, err := parseRef(rest[0])
	if err != nil {
		return err
	}
	job, err := w.job()
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(rest[1])
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return fmt.Errorf("%s is empty", rest[1])
	}
	// Where the reference goes, settled before the upload.
	doc, err := readDoc(*into)
	haveDoc := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	at := -1
	if haveDoc {
		for i := range doc.Findings {
			if doc.Findings[i].Ordinal == ordinal {
				at = i
			}
		}
		if at >= 0 && step > len(doc.Findings[at].Sites) {
			return fmt.Errorf("finding %d has %d steps, not %d", ordinal, len(doc.Findings[at].Sites), step)
		}
		// A step shows one attachment; more belong on the finding, where
		// the impact item carries them all.
		if at >= 0 && step > 0 && len(doc.Findings[at].Sites[step-1].Visuals) > 0 {
			return fmt.Errorf("finding %d step %d already has an attachment; replace it in findings.json or attach to finding %d instead", ordinal, step, ordinal)
		}
	}
	name := filepath.Base(rest[1])
	mt := *typ
	if mt == "" {
		mt = detectType(name, raw)
	}
	// One recording per finding, wherever it rides.
	if at >= 0 && core.VisualKindOf(mt) == core.VisualVideo {
		for _, v := range doc.Findings[at].Visuals {
			if core.VisualKindOf(v.Mime) == core.VisualVideo {
				return fmt.Errorf("finding %d already has a recording; one per finding", ordinal)
			}
		}
		for _, st := range doc.Findings[at].Sites {
			for _, v := range st.Visuals {
				if core.VisualKindOf(v.Mime) == core.VisualVideo {
					return fmt.Errorf("finding %d already has a recording on a step; one per finding", ordinal)
				}
			}
		}
	}
	body := map[string]any{"ordinal": ordinal, "caption": rest[2], "mime": mt, "name": name, "data_base64": base64.StdEncoding.EncodeToString(raw)}
	var out attachOut
	if err := w.call(http.MethodPost, "/v1/op/jobs/"+job.Audit.ID+"/visuals", body, &out); err != nil {
		return err
	}
	v := out.Visual
	if *proof {
		v.Role = core.VisualProof
	}
	fmt.Printf("stored     %s  %s  %s  %d bytes  sha256 %s%s\n", v.ID, v.Kind, v.Mime, v.Size, short(out.SHA256), map[bool]string{true: "  proof", false: ""}[*proof])
	if at < 0 {
		ref, _ := json.MarshalIndent(v, "", "  ")
		where := "no " + *into + " yet"
		if haveDoc {
			where = *into + " has no finding " + strconv.Itoa(ordinal)
		}
		fmt.Printf("%s; put this under finding %d%s as a visuals entry:\n%s\n", where, ordinal, stepWords(step), ref)
		return nil
	}
	if step == 0 {
		doc.Findings[at].Visuals = append(doc.Findings[at].Visuals, v)
	} else {
		doc.Findings[at].Sites[step-1].Visuals = append(doc.Findings[at].Sites[step-1].Visuals, v)
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(*into, append(b, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("written    %s  finding %d%s\n", *into, ordinal, stepWords(step))
	return nil
}

// parseRef reads "3" as finding 3 and "3.2" as the second step of finding 3.
func parseRef(s string) (ordinal, step int, err error) {
	o, st, hasStep := strings.Cut(s, ".")
	ordinal, err = strconv.Atoi(o)
	if err != nil || ordinal < 1 {
		return 0, 0, fmt.Errorf("%q is not a finding ordinal", s)
	}
	if hasStep {
		step, err = strconv.Atoi(st)
		if err != nil || step < 1 {
			return 0, 0, fmt.Errorf("%q is not a step number", st)
		}
	}
	return ordinal, step, nil
}

func stepWords(step int) string {
	if step == 0 {
		return ""
	}
	return " step " + strconv.Itoa(step)
}

// knownTypes names what an operator attaches, by extension, so the type is
// the same in every sandbox whatever its mime table says. Anything else
// falls back to the host table and then to the bytes.
var knownTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp", ".gif": "image/gif",
	".mp4": "video/mp4", ".webm": "video/webm",
	".py": "text/x-python", ".rs": "text/x-rust", ".sh": "text/x-sh", ".sol": "text/x-solidity",
	".ts": "text/typescript", ".js": "text/javascript", ".go": "text/x-go",
	".json": "application/json", ".toml": "application/toml", ".yaml": "application/yaml", ".yml": "application/yaml",
	".txt": "text/plain", ".log": "text/plain", ".md": "text/markdown", ".csv": "text/csv", ".pdf": "application/pdf",
	".zip": "application/zip", ".tar": "application/x-tar", ".gz": "application/gzip", ".tgz": "application/gzip",
	".har": "application/json", ".pcap": "application/vnd.tcpdump.pcap",
}

// detectType names the content by extension first, which tells an mp4
// from a webm and a script from a log, and by the bytes when the extension
// says nothing.
func detectType(name string, raw []byte) string {
	ext := strings.ToLower(filepath.Ext(name))
	if t, ok := knownTypes[ext]; ok {
		return t
	}
	if t := mime.TypeByExtension(ext); t != "" {
		t, _, _ = strings.Cut(t, ";")
		return strings.TrimSpace(t)
	}
	t := http.DetectContentType(raw)
	t, _, _ = strings.Cut(t, ";")
	return strings.TrimSpace(t)
}

func readDoc(path string) (core.FindingsDocument, error) {
	var doc core.FindingsDocument
	b, err := os.ReadFile(path)
	if err != nil {
		return doc, err
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return doc, fmt.Errorf("%s: %w", path, err)
	}
	return doc, nil
}
