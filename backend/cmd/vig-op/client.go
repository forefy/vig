package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/forefy/vig/backend/internal/api"
)

// workspace is the sandbox's state: where the job, the token and the
// checkout live.
type workspace struct {
	Home string
	API  string
	http *http.Client
}

func openWorkspace() (*workspace, error) {
	home := os.Getenv("VIG_OP_HOME")
	if home == "" {
		home = "/work"
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, fmt.Errorf("workspace %s: %w", home, err)
	}
	base := strings.TrimRight(os.Getenv("VIG_API"), "/")
	if base == "" {
		base = "http://localhost:7112"
	}
	return &workspace{Home: home, API: base, http: &http.Client{Timeout: 60 * time.Second}}, nil
}

func (w *workspace) jobPath() string   { return filepath.Join(w.Home, "job.json") }
func (w *workspace) tokenPath() string { return filepath.Join(w.Home, "token") }
func (w *workspace) srcDir() string    { return filepath.Join(w.Home, "src") }

// job returns the claimed job. Every command after claim needs it.
func (w *workspace) job() (api.Job, error) {
	var j api.Job
	b, err := os.ReadFile(w.jobPath())
	if err != nil {
		return j, errors.New("no claimed job in " + w.Home + " (run `vig-op claim` first)")
	}
	if err := json.Unmarshal(b, &j); err != nil {
		return j, fmt.Errorf("job.json: %w", err)
	}
	return j, nil
}

func (w *workspace) saveJob(j api.Job) error {
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(w.jobPath(), b, 0o600); err != nil {
		return err
	}
	return os.WriteFile(w.tokenPath(), []byte(j.Token), 0o600)
}

// token is the bearer for every call: the bound token once a job is claimed,
// otherwise the unbound one from the environment.
func (w *workspace) token() (string, error) {
	if b, err := os.ReadFile(w.tokenPath()); err == nil && len(bytes.TrimSpace(b)) > 0 {
		return string(bytes.TrimSpace(b)), nil
	}
	if t := os.Getenv("VIG_OP_TOKEN"); t != "" {
		return t, nil
	}
	return "", errors.New("no operator token: set VIG_OP_TOKEN (an unbound token from the admin console)")
}

// problem is the RFC 9457 body the API returns on every error.
type problem struct {
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
	Errors []struct {
		Message  string `json:"message"`
		Location string `json:"location"`
	} `json:"errors"`
}

func (p problem) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d %s", p.Status, p.Title)
	if p.Detail != "" {
		b.WriteString(": " + p.Detail)
	}
	for _, e := range p.Errors {
		b.WriteString("\n  " + e.Location + " " + e.Message)
	}
	return b.String()
}

// call posts JSON and decodes the response into out. API errors come back as
// a problem so the operator sees exactly what the server refused.
func (w *workspace) call(method, path string, in, out any) error {
	tok, err := w.token()
	if err != nil {
		return err
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, w.API+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "vig-op")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := w.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var p problem
		if json.Unmarshal(raw, &p) == nil && p.Status != 0 {
			return p
		}
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(raw)))
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}
