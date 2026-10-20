package evm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Explorer is a Blockscout instance: a public, keyless index of every
// transaction an address received, which a public RPC endpoint cannot
// give past its archive horizon.
type Explorer struct {
	HTTP *http.Client
	Base string // https://eth.blockscout.com
}

// NewExplorer builds a client on one Blockscout instance.
func NewExplorer(base string) *Explorer {
	return &Explorer{HTTP: &http.Client{Timeout: 20 * time.Second}, Base: strings.TrimRight(base, "/")}
}

// ExplorerTx is one transaction the explorer lists.
type ExplorerTx struct {
	Hash  string
	Block uint64
	At    time.Time
}

// Transactions lists the newest transactions sent to an address, up to
// limit, newest first. Pages are followed until the limit or the end of
// the address's history; the second result says whether the list is the
// whole history.
func (e *Explorer) Transactions(ctx context.Context, address string, limit int) ([]ExplorerTx, bool, error) {
	var out []ExplorerTx
	next := url.Values{}
	for page := 0; page < 8 && len(out) < limit; page++ {
		q := url.Values{"filter": {"to"}}
		for k, v := range next {
			q[k] = v
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.Base+"/api/v2/addresses/"+address+"/transactions?"+q.Encode(), nil)
		if err != nil {
			return nil, false, err
		}
		resp, err := e.HTTP.Do(req)
		if err != nil {
			return nil, false, err
		}
		var body struct {
			Items []struct {
				Hash      string    `json:"hash"`
				Block     uint64    `json:"block_number"`
				Timestamp time.Time `json:"timestamp"`
			} `json:"items"`
			Next map[string]any `json:"next_page_params"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, false, fmt.Errorf("explorer: %s", resp.Status)
		}
		if err != nil {
			return nil, false, err
		}
		for _, it := range body.Items {
			out = append(out, ExplorerTx{Hash: it.Hash, Block: it.Block, At: it.Timestamp.UTC()})
		}
		if body.Next == nil || len(body.Items) == 0 {
			return out, true, nil
		}
		next = url.Values{}
		for k, v := range body.Next {
			next.Set(k, fmt.Sprint(v))
		}
	}
	return out, false, nil
}

// Receipt reads a transaction's outcome from the explorer: what a pruned
// endpoint no longer holds. Status, gas and the logs it emitted.
func (e *Explorer) Receipt(ctx context.Context, hash string) (Receipt, error) {
	var tx struct {
		Status  string `json:"status"` // ok, error
		GasUsed string `json:"gas_used"`
	}
	if err := e.get(ctx, "/api/v2/transactions/"+hash, &tx); err != nil {
		return Receipt{}, err
	}
	if tx.Status == "" {
		return Receipt{}, ErrNotFound
	}
	out := Receipt{Status: tx.Status == "ok"}
	out.GasUsed, _ = strconv.ParseUint(tx.GasUsed, 10, 64)
	var logs struct {
		Items []struct {
			Address struct {
				Hash string `json:"hash"`
			} `json:"address"`
			Topics []*string `json:"topics"`
			Data   string    `json:"data"`
		} `json:"items"`
	}
	if err := e.get(ctx, "/api/v2/transactions/"+hash+"/logs", &logs); err != nil {
		return out, nil
	}
	for _, it := range logs.Items {
		l := rawLog{Address: it.Address.Hash, Data: it.Data, TxHash: hash}
		for _, t := range it.Topics {
			if t != nil {
				l.Topics = append(l.Topics, *t)
			}
		}
		out.Logs = append(out.Logs, l.log())
	}
	return out, nil
}

// Name gives the name a verified contract carries on the explorer, or
// "" when it has none: what names a reached contract that is no token.
func (e *Explorer) Name(ctx context.Context, address string) string {
	var a struct {
		Name string `json:"name"`
	}
	if err := e.get(ctx, "/api/v2/addresses/"+address, &a); err != nil {
		return ""
	}
	return clean(a.Name)
}

// get reads one JSON resource. Blockscout refuses requests without a
// user agent.
func (e *Explorer) get(ctx context.Context, path string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.Base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "vig")
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("explorer: %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(into)
}

// Holding is one token balance an address holds.
type Holding struct {
	Token    string
	Symbol   string
	Decimals int
	Value    *big.Int
}

// TokenBalances lists the ERC-20 balances an address holds, as the
// explorer indexes them.
func (e *Explorer) TokenBalances(ctx context.Context, address string) ([]Holding, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.Base+"/api/v2/addresses/"+address+"/token-balances", nil)
	if err != nil {
		return nil, err
	}
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("explorer: %s", resp.Status)
	}
	var rows []struct {
		Token struct {
			Address  string `json:"address_hash"`
			Addr2    string `json:"address"`
			Symbol   string `json:"symbol"`
			Decimals string `json:"decimals"`
			Type     string `json:"type"`
		} `json:"token"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rows); err != nil {
		return nil, err
	}
	var out []Holding
	for _, r := range rows {
		if r.Token.Type != "" && r.Token.Type != "ERC-20" {
			continue
		}
		addr := r.Token.Address
		if addr == "" {
			addr = r.Token.Addr2
		}
		v, ok := new(big.Int).SetString(r.Value, 10)
		if !ok || v.Sign() == 0 {
			continue
		}
		d, _ := strconv.Atoi(r.Token.Decimals)
		out = append(out, Holding{Token: strings.ToLower(addr), Symbol: r.Token.Symbol, Decimals: d, Value: v})
	}
	return out, nil
}

// FormatHoldings renders holdings for a fact: "WETH 5.13 · USDC 100",
// largest units first as listed, at most eight, the rest counted.
func FormatHoldings(hs []Holding) string {
	parts := make([]string, 0, len(hs))
	for i, h := range hs {
		if i == 8 {
			parts = append(parts, fmt.Sprintf("+%d", len(hs)-8))
			break
		}
		name := h.Symbol
		if name == "" {
			name = h.Token[:6] + "…" + h.Token[len(h.Token)-4:]
		}
		parts = append(parts, name+" "+Units(h.Value, h.Decimals))
	}
	return strings.Join(parts, " · ")
}

// Units renders base units as a decimal with up to four fraction digits.
func Units(v *big.Int, decimals int) string {
	if v == nil {
		return "0"
	}
	if decimals <= 0 {
		return v.String()
	}
	s := v.String()
	if len(s) <= decimals {
		s = strings.Repeat("0", decimals-len(s)+1) + s
	}
	whole, frac := s[:len(s)-decimals], s[len(s)-decimals:]
	frac = strings.TrimRight(frac, "0")
	if len(frac) > 4 {
		frac = frac[:4]
	}
	if frac == "" {
		return whole
	}
	if whole == "0" && strings.Trim(frac, "0") == "" {
		// Nonzero, but below what four fraction digits can show.
		return "<0.0001"
	}
	return whole + "." + frac
}
