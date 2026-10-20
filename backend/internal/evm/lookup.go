package evm

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Sourcify serves verified ABIs with no key; the 4byte directory and the
// openchain database name selectors nobody verified, each knowing some
// the other does not. Together they are the IDL of an EVM contract.
const (
	SourcifyURL  = "https://sourcify.dev/server/v2/contract/"
	FourByteURL  = "https://www.4byte.directory/api/v1/signatures/?hex_signature=0x"
	OpenchainURL = "https://api.openchain.xyz/signature-database/v1/lookup?filter=true&function=0x"
)

// Lookup fetches interfaces and remembers what it learned.
type Lookup struct {
	HTTP      *http.Client
	Sourcify  string
	FourByte  string
	Openchain string

	mu    sync.Mutex
	names map[[4]byte]*Function
}

// NewLookup builds a lookup on the public services.
func NewLookup() *Lookup {
	return &Lookup{HTTP: &http.Client{Timeout: 20 * time.Second}, Sourcify: SourcifyURL, FourByte: FourByteURL, Openchain: OpenchainURL, names: map[[4]byte]*Function{}}
}

// Verified is what Sourcify has on file for a contract: its ABI and how
// it was built, and how well the build matches the chain.
type Verified struct {
	ABI        *ABI
	Compiler   string // 0.8.33
	ViaIR      bool
	EVMVersion string
	Match      string // exact or partial
}

// ABIFor reads a contract's verified ABI from Sourcify on one chain;
// ErrUnverified when it has none.
func (l *Lookup) ABIFor(ctx context.Context, chainID, address string) (*ABI, error) {
	v, err := l.Verified(ctx, chainID, address)
	if err != nil {
		return nil, err
	}
	return v.ABI, nil
}

// Verified reads a contract's verified record from Sourcify on one chain;
// ErrUnverified when it has none.
func (l *Lookup) Verified(ctx context.Context, chainID, address string) (*Verified, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.Sourcify+chainID+"/"+address+"?fields=abi,compilation", nil)
	if err != nil {
		return nil, err
	}
	resp, err := l.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrUnverified
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sourcify: %s", resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var body struct {
		ABI          json.RawMessage `json:"abi"`
		Match        string          `json:"match"`
		RuntimeMatch string          `json:"runtimeMatch"`
		Compilation  struct {
			CompilerVersion string `json:"compilerVersion"`
			Settings        struct {
				ViaIR      bool   `json:"viaIR"`
				EVMVersion string `json:"evmVersion"`
			} `json:"compilerSettings"`
		} `json:"compilation"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || len(body.ABI) == 0 {
		return nil, ErrUnverified
	}
	abi, err := ParseABI(body.ABI)
	if err != nil {
		return nil, err
	}
	v := &Verified{ABI: abi, ViaIR: body.Compilation.Settings.ViaIR, EVMVersion: body.Compilation.Settings.EVMVersion}
	if c := body.Compilation.CompilerVersion; c != "" {
		v.Compiler = strings.TrimPrefix(strings.SplitN(c, "+", 2)[0], "v")
	}
	switch m := body.Match; {
	case m == "" && body.RuntimeMatch != "":
		m = body.RuntimeMatch
		fallthrough
	default:
		if strings.HasPrefix(m, "exact") {
			v.Match = "exact"
		} else if m != "" {
			v.Match = "partial"
		}
	}
	return v, nil
}

// ErrUnverified says no verified source is on file for an address.
var ErrUnverified = errors.New("no verified source")

// Name resolves a selector through the 4byte directory, the oldest entry
// first since it is the one most likely to be real, then through openchain
// when 4byte has nothing. Nil when neither knows it.
func (l *Lookup) Name(ctx context.Context, sel [4]byte) *Function {
	l.mu.Lock()
	if f, ok := l.names[sel]; ok {
		l.mu.Unlock()
		return f
	}
	l.mu.Unlock()
	found := l.fourByte(ctx, sel)
	if found == nil && l.Openchain != "" {
		found = l.openchain(ctx, sel)
	}
	l.mu.Lock()
	l.names[sel] = found
	l.mu.Unlock()
	return found
}

func (l *Lookup) openchain(ctx context.Context, sel [4]byte) *Function {
	hexSel := "0x" + hex.EncodeToString(sel[:])
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.Openchain+hex.EncodeToString(sel[:]), nil)
	if err != nil {
		return nil
	}
	resp, err := l.HTTP.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var body struct {
		Result struct {
			Function map[string][]struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"result"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body) != nil {
		return nil
	}
	for _, r := range body.Result.Function[hexSel] {
		if f, err := ParseSignature(strings.TrimSpace(r.Name)); err == nil && f.Selector == sel {
			return &f
		}
	}
	return nil
}

func (l *Lookup) fourByte(ctx context.Context, sel [4]byte) *Function {
	if l.FourByte == "" {
		return nil
	}
	var found *Function
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.FourByte+hex.EncodeToString(sel[:]), nil)
	if err == nil {
		if resp, err := l.HTTP.Do(req); err == nil {
			defer resp.Body.Close()
			var body struct {
				Results []struct {
					ID  int    `json:"id"`
					Sig string `json:"text_signature"`
				} `json:"results"`
			}
			if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body) == nil && len(body.Results) > 0 {
				sort.Slice(body.Results, func(i, j int) bool { return body.Results[i].ID < body.Results[j].ID })
				for _, r := range body.Results {
					if f, err := ParseSignature(strings.TrimSpace(r.Sig)); err == nil && f.Selector == sel {
						found = &f
						break
					}
				}
			}
		}
	}
	return found
}
