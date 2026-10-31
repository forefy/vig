package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/forefy/vig/backend/internal/api"
	"github.com/forefy/vig/backend/internal/core"
)

// indexPartFile is one Parquet part zz wrote, found under the target's
// directory of a table.
type indexPartFile struct {
	Table  string
	File   string
	Path   string
	Bytes  int64
	SHA256 string
}

// cmdIndex pushes what zz built for the job: every Parquet part under
// <root>/index/<table>/target=<slug>/ is staged, then the manifest seals
// them as the job's index, replacing any earlier one.
func cmdIndex(w *workspace, args []string) error {
	const use = "usage: vig-op index push [--root DIR] [--target SLUG] [--tool-version V]"
	if len(args) < 1 || args[0] != "push" {
		return errors.New(use)
	}
	fs := flag.NewFlagSet("index push", flag.ContinueOnError)
	root := fs.String("root", defaultZZRoot(), "a zz checkout or data dir holding index/<table>/target=<slug>/*.parquet")
	target := fs.String("target", "", "the zz target slug the operator used (default: the job's audit id)")
	toolVersion := fs.String("tool-version", "", "zz version to record (default: `zz --version`)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New(use)
	}
	job, err := w.job()
	if err != nil {
		return err
	}
	slug := *target
	if slug == "" {
		slug = job.Audit.ID
	}
	parts, err := indexParts(*root, slug)
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		return fmt.Errorf("no parts under %s: expected %s/index/<table>/target=%s/*.parquet (zz index <target> first, or pass --root and --target)", *root, *root, slug)
	}
	version := *toolVersion
	if version == "" {
		version = zzVersion()
	}
	base := "/v1/op/jobs/" + job.Audit.ID + "/index"
	// Anything left from an earlier push goes first, so the seal names
	// exactly what this run staged.
	if err := w.call(http.MethodDelete, base+"/staging", nil, nil); err != nil {
		return err
	}
	var total int64
	for i, p := range parts {
		raw, err := os.ReadFile(p.Path)
		if err != nil {
			return err
		}
		body := map[string]any{"table": p.Table, "file": p.File, "sha256": p.SHA256, "data_base64": base64.StdEncoding.EncodeToString(raw)}
		var out struct {
			Staged int `json:"staged"`
		}
		if err := w.call(http.MethodPut, base+"/parts", body, &out); err != nil {
			return fmt.Errorf("%s/%s: %w", p.Table, p.File, err)
		}
		total += p.Bytes
		fmt.Printf("staged  %3d/%d  %s/%s  %d bytes  sha256 %s\n", i+1, len(parts), p.Table, p.File, p.Bytes, short(p.SHA256))
	}
	manifest := indexManifest(parts, "zz", version)
	var build api.IndexBuild
	if err := w.call(http.MethodPost, base, manifest, &build); err != nil {
		return err
	}
	n := 0
	for _, t := range build.Tables {
		n += len(t.Parts)
	}
	fmt.Printf("sealed  %s %s  %d tables  %d parts  %d bytes  built %s\n", build.Tool, build.ToolVersion, len(build.Tables), n, build.Bytes, build.BuiltAt.UTC().Format("2006-01-02 15:04:05"))
	for _, t := range build.Tables {
		fmt.Printf("  %-24s %3d parts  %12d bytes\n", t.Name, len(t.Parts), t.Bytes)
	}
	return nil
}

// indexParts walks <root>/index/<table>/target=<slug>/ for every table
// directory and returns the Parquet parts found, sorted by table and file,
// with their sizes and digests. A table or file name the server would
// refuse is skipped with a note rather than failing the whole push.
func indexParts(root, slug string) ([]indexPartFile, error) {
	tables, err := os.ReadDir(filepath.Join(root, "index"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []indexPartFile
	for _, t := range tables {
		if !t.IsDir() {
			continue
		}
		if !core.ValidIndexTable(t.Name()) {
			fmt.Fprintf(os.Stderr, "vig-op: skipping table %q: lowercase letters, digits and underscores\n", t.Name())
			continue
		}
		dir := filepath.Join(root, "index", t.Name(), "target="+slug)
		files, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".parquet") {
				continue
			}
			if !core.ValidIndexFile(f.Name()) {
				fmt.Fprintf(os.Stderr, "vig-op: skipping %s/%s: a plain .parquet file name\n", t.Name(), f.Name())
				continue
			}
			path := filepath.Join(dir, f.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			if len(raw) == 0 {
				fmt.Fprintf(os.Stderr, "vig-op: skipping %s/%s: empty\n", t.Name(), f.Name())
				continue
			}
			sum := sha256.Sum256(raw)
			out = append(out, indexPartFile{Table: t.Name(), File: f.Name(), Path: path, Bytes: int64(len(raw)), SHA256: hex.EncodeToString(sum[:])})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Table != out[j].Table {
			return out[i].Table < out[j].Table
		}
		return out[i].File < out[j].File
	})
	return out, nil
}

// indexManifest groups the parts by table, in order. Row counts stay 0:
// zz does not report them and the server sums bytes itself.
func indexManifest(parts []indexPartFile, tool, version string) core.IndexManifest {
	m := core.IndexManifest{Tool: tool, ToolVersion: version}
	at := map[string]int{}
	for _, p := range parts {
		i, ok := at[p.Table]
		if !ok {
			i = len(m.Tables)
			at[p.Table] = i
			m.Tables = append(m.Tables, core.IndexTable{Name: p.Table})
		}
		m.Tables[i].Parts = append(m.Tables[i].Parts, core.IndexPart{File: p.File, Bytes: p.Bytes, SHA256: p.SHA256})
		m.Tables[i].Bytes += p.Bytes
	}
	return m
}

// defaultZZRoot is $ZZ_ROOT, else ~/zz.
func defaultZZRoot() string {
	if r := os.Getenv("ZZ_ROOT"); r != "" {
		return r
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "zz"
	}
	return filepath.Join(home, "zz")
}

// zzVersion is the last word of `zz --version`, "unknown" without a zz.
func zzVersion() string {
	out, err := exec.Command("zz", "--version").Output()
	if err != nil {
		return "unknown"
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "unknown"
	}
	return fields[len(fields)-1]
}
