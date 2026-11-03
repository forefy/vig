package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/forefy/vig/backend/internal/api"
	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/index"
)

// indexPartFile is one Parquet part a build wrote, as core finds it.
type indexPartFile = core.IndexPartFile

// cmdIndex builds the job's index from its checkout and captures, or
// pushes parts already built: every Parquet part under
// <root>/index/<table>/target=<slug>/ is staged, then the manifest seals
// them as the job's index, replacing any earlier one.
func cmdIndex(w *workspace, args []string) error {
	const use = "usage: vig-op index build [--repo owner/name=DIR]... [--har FILE]... [--names FILE] [--resources FILE] [--out DIR] [--target SLUG] [--push]\n       vig-op index push [--root DIR] [--target SLUG]"
	if len(args) < 1 {
		return errors.New(use)
	}
	switch args[0] {
	case "build":
		return cmdIndexBuild(w, args[1:])
	case "push":
		return cmdIndexPush(w, args[1:])
	}
	return errors.New(use)
}

// repeated collects a flag given more than once.
type repeated []string

func (r *repeated) String() string     { return strings.Join(*r, ",") }
func (r *repeated) Set(v string) error { *r = append(*r, v); return nil }

// cmdIndexBuild extracts the index. Without --repo it reads the job's
// checkout at the pinned commit; with --target it builds off the job, for
// a demo or a dry run.
func cmdIndexBuild(w *workspace, args []string) error {
	fs := flag.NewFlagSet("index build", flag.ContinueOnError)
	var repos, hars repeated
	fs.Var(&repos, "repo", "owner/name=DIR: a checkout to read (default: the job's)")
	fs.Var(&hars, "har", "a capture to read; may repeat")
	names := fs.String("names", "", "a DNS listing: one name per line with what it resolves to")
	resources := fs.String("resources", "", "a cloud listing: a JSON array of resources")
	out := fs.String("out", "", "where the parts go (default: <workspace>/index-build)")
	target := fs.String("target", "", "the target slug (default: the job's audit id)")
	push := fs.Bool("push", false, "push the parts as the job's index when the build is done")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: vig-op index build [--repo owner/name=DIR]... [--har FILE]... [--names FILE] [--resources FILE] [--out DIR] [--target SLUG] [--push]")
	}
	if !index.CanExtract {
		return errors.New("this build of vig-op carries no parsers: build it with cgo")
	}
	slug := *target
	var inputs []index.RepoInput
	// Off a job the build needs a target and something to read: a
	// repository, a capture, a names listing or a cloud listing.
	given := len(repos) > 0 || len(hars) > 0 || *names != "" || *resources != ""
	if len(repos) == 0 || slug == "" || *push {
		job, err := w.job()
		if err != nil {
			if slug == "" || !given {
				return fmt.Errorf("no job here: pass --target and an input (--repo, --har, --names or --resources) to build off a job (%w)", err)
			}
		} else {
			if slug == "" {
				slug = job.Audit.ID
			}
			if len(repos) == 0 {
				if _, err := os.Stat(w.srcDir()); err != nil {
					return errors.New("no source here: vig-op claim first, or pass --repo")
				}
				inputs = append(inputs, index.RepoInput{Name: job.Audit.Repo, Dir: w.srcDir(), Commit: job.Audit.HeadSHA, Branch: gitBranch(w.srcDir())})
			}
		}
	}
	for _, r := range repos {
		name, dir, ok := strings.Cut(r, "=")
		if !ok || name == "" || dir == "" {
			return fmt.Errorf("--repo %q: owner/name=DIR", r)
		}
		inputs = append(inputs, index.RepoInput{Name: name, Dir: dir, Commit: gitHead(dir), Branch: gitBranch(dir)})
	}
	root := *out
	if root == "" {
		root = filepath.Join(w.Home, "index-build")
	}
	sum, err := index.Build(context.Background(), index.Options{Root: root, Slug: slug, Repos: inputs, HARs: hars, Names: *names, Resources: *resources,
		Log: func(s string) { fmt.Fprintln(os.Stderr, "vig-op: "+s) }})
	if err != nil {
		return err
	}
	fmt.Printf("built    %s  %d repos  %d files  %d lines  %d functions  %d calls  %d flows  %d routes\n", slug, len(inputs), sum.Files, sum.Lines, sum.Functions, sum.Calls, sum.Flows, sum.Routes)
	if sum.Requests > 0 || sum.Subdomains > 0 || sum.Resources > 0 {
		fmt.Printf("         %d requests  %d bodies  %d hosts  %d names  %d resources\n", sum.Requests, sum.Bodies, sum.Hosts, sum.Subdomains, sum.Resources)
	}
	fmt.Printf("         %d edges  %d parts under %s\n", sum.Edges, len(sum.Parts), filepath.Join(root, "index"))
	if *push {
		return pushParts(w, sum.Parts)
	}
	return nil
}

// cmdIndexPush stages parts already built and seals them.
func cmdIndexPush(w *workspace, args []string) error {
	fs := flag.NewFlagSet("index push", flag.ContinueOnError)
	root := fs.String("root", "", "the build's root holding index/<table>/target=<slug>/*.parquet (default: <workspace>/index-build)")
	target := fs.String("target", "", "the target slug the parts were built for (default: the job's audit id)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: vig-op index push [--root DIR] [--target SLUG]")
	}
	job, err := w.job()
	if err != nil {
		return err
	}
	slug := *target
	if slug == "" {
		slug = job.Audit.ID
	}
	if *root == "" {
		*root = filepath.Join(w.Home, "index-build")
	}
	parts, err := indexParts(*root, slug)
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		return fmt.Errorf("no parts under %s: expected %s/index/<table>/target=%s/*.parquet (vig-op index build first, or pass --root and --target)", *root, *root, slug)
	}
	return pushParts(w, parts)
}

// pushParts stages every part under the job's token and seals the index.
func pushParts(w *workspace, parts []indexPartFile) error {
	job, err := w.job()
	if err != nil {
		return err
	}
	base := "/v1/op/jobs/" + job.Audit.ID + "/index"
	// Anything left from an earlier push goes first, so the seal names
	// exactly what this run staged.
	if err := w.call(http.MethodDelete, base+"/staging", nil, nil); err != nil {
		return err
	}
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
		fmt.Printf("staged  %3d/%d  %s/%s  %d bytes  sha256 %s\n", i+1, len(parts), p.Table, p.File, p.Bytes, short(p.SHA256))
	}
	manifest := indexManifest(parts, index.Tool, api.Version)
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

// indexParts is core's walk with the notes on stderr.
func indexParts(root, slug string) ([]indexPartFile, error) {
	return core.IndexParts(root, slug, func(note string) { fmt.Fprintln(os.Stderr, "vig-op: "+note) })
}

// indexManifest is core's manifest of the parts.
func indexManifest(parts []indexPartFile, tool, version string) core.IndexManifest {
	return core.ManifestOf(parts, tool, version)
}

func gitHead(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gitBranch(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	b := strings.TrimSpace(string(out))
	if b == "HEAD" {
		return ""
	}
	return b
}
