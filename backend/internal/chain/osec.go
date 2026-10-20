package chain

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// VerifiedBuild is the OtterSec registry answer for a program.
type VerifiedBuild struct {
	Verified       bool
	OnChainHash    string
	ExecutableHash string
	RepoURL        string
	Commit         string
	LastVerifiedAt string
}

// Osec queries the OtterSec verified-build API.
type Osec struct {
	BaseURL string
	HTTP    *http.Client
}

// NewOsec builds a client for the verify service.
func NewOsec(baseURL string) *Osec {
	return &Osec{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: 15 * time.Second}}
}

// Status looks a program up in the registry.
func (o *Osec) Status(ctx context.Context, programID string) (VerifiedBuild, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.BaseURL+"/status/"+programID, nil)
	if err != nil {
		return VerifiedBuild{}, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return VerifiedBuild{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return VerifiedBuild{}, fmt.Errorf("verify api: %s", resp.Status)
	}
	var r struct {
		IsVerified     bool    `json:"is_verified"`
		OnChainHash    string  `json:"on_chain_hash"`
		ExecutableHash string  `json:"executable_hash"`
		RepoURL        string  `json:"repo_url"`
		Commit         string  `json:"commit"`
		LastVerifiedAt *string `json:"last_verified_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return VerifiedBuild{}, err
	}
	v := VerifiedBuild{Verified: r.IsVerified, OnChainHash: r.OnChainHash, ExecutableHash: r.ExecutableHash, RepoURL: r.RepoURL, Commit: r.Commit}
	if r.LastVerifiedAt != nil {
		v.LastVerifiedAt = *r.LastVerifiedAt
	}
	return v, nil
}
