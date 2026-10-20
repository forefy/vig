package evm

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Client is a JSON-RPC client over the public Ethereum endpoints, tried in
// order, with no key and no cache: the spike reads each block once.
type Client struct {
	URLs []string
	HTTP *http.Client
}

// PublicRPC is the keyless mainnet endpoint that answered when probed.
const PublicRPC = "https://ethereum-rpc.publicnode.com"

// New builds a client; an empty list means the public endpoint.
func New(urls []string) *Client {
	if len(urls) == 0 {
		urls = []string{PublicRPC}
	}
	return &Client{URLs: urls, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

type rpcResp struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	} `json:"error"`
}

// RevertError is an eth_call that executed and reverted: the answer the
// caller wanted, not an endpoint failure.
type RevertError struct {
	Msg  string
	Data []byte
}

func (e *RevertError) Error() string { return e.Msg }

// Call performs one method, falling through the endpoints on failure.
func (c *Client) Call(ctx context.Context, method string, params ...any) (json.RawMessage, error) {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	var last error
	for _, u := range c.URLs {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.HTTP.Do(req)
		if err != nil {
			last = err
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		if err != nil {
			last = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			last = fmt.Errorf("%s: %s", u, resp.Status)
			continue
		}
		var r rpcResp
		if err := json.Unmarshal(raw, &r); err != nil {
			last = err
			continue
		}
		if r.Error != nil {
			if r.Error.Code == 3 || strings.Contains(r.Error.Message, "revert") {
				var data string
				_ = json.Unmarshal(r.Error.Data, &data)
				return nil, &RevertError{Msg: r.Error.Message, Data: parseData(data)}
			}
			last = fmt.Errorf("rpc %s: %s (%d)", method, r.Error.Message, r.Error.Code)
			continue
		}
		return r.Result, nil
	}
	if last == nil {
		last = errors.New("no endpoints configured")
	}
	return nil, last
}

// Hex quantities and data as the RPC renders them.
func parseQuantity(s string) (uint64, error) {
	return strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 64)
}

func parseData(s string) []byte {
	b, _ := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	return b
}

// Tx is one transaction of a block, as much as the spike reads.
type Tx struct {
	Hash  string
	From  string
	To    string
	Input []byte
	Value string
}

// Block is one block with its transactions.
type Block struct {
	Number    uint64
	Timestamp time.Time
	Txs       []Tx
}

// BlockNumber is the chain head.
func (c *Client) BlockNumber(ctx context.Context) (uint64, error) {
	raw, err := c.Call(ctx, "eth_blockNumber")
	if err != nil {
		return 0, err
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, err
	}
	return parseQuantity(s)
}

// BlockByNumber reads a block with its full transactions.
func (c *Client) BlockByNumber(ctx context.Context, n uint64) (Block, error) {
	raw, err := c.Call(ctx, "eth_getBlockByNumber", "0x"+strconv.FormatUint(n, 16), true)
	if err != nil {
		return Block{}, err
	}
	var b struct {
		Number       string `json:"number"`
		Timestamp    string `json:"timestamp"`
		Transactions []struct {
			Hash  string `json:"hash"`
			From  string `json:"from"`
			To    string `json:"to"`
			Input string `json:"input"`
			Value string `json:"value"`
		} `json:"transactions"`
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return Block{}, err
	}
	if b.Number == "" {
		return Block{}, errors.New("block not found")
	}
	out := Block{}
	out.Number, _ = parseQuantity(b.Number)
	if ts, err := parseQuantity(b.Timestamp); err == nil {
		out.Timestamp = time.Unix(int64(ts), 0).UTC()
	}
	for _, t := range b.Transactions {
		out.Txs = append(out.Txs, Tx{Hash: t.Hash, From: strings.ToLower(t.From), To: strings.ToLower(t.To), Input: parseData(t.Input), Value: t.Value})
	}
	return out, nil
}

// Log is one event a contract emitted.
type Log struct {
	Address     string
	Topics      [][32]byte
	Data        []byte
	TxHash      string
	BlockNumber uint64
}

type rawLog struct {
	Address     string   `json:"address"`
	Topics      []string `json:"topics"`
	Data        string   `json:"data"`
	TxHash      string   `json:"transactionHash"`
	BlockNumber string   `json:"blockNumber"`
}

func (l rawLog) log() Log {
	out := Log{Address: strings.ToLower(l.Address), Data: parseData(l.Data), TxHash: l.TxHash}
	out.BlockNumber, _ = parseQuantity(l.BlockNumber)
	for _, t := range l.Topics {
		var w [32]byte
		copy(w[:], parseData(t))
		out.Topics = append(out.Topics, w)
	}
	return out
}

// Logs reads every event one address emitted over a block range: the one
// call that sees calls reached through other contracts.
func (c *Client) Logs(ctx context.Context, address string, from, to uint64) ([]Log, error) {
	raw, err := c.Call(ctx, "eth_getLogs", map[string]any{"address": address, "fromBlock": "0x" + strconv.FormatUint(from, 16), "toBlock": "0x" + strconv.FormatUint(to, 16)})
	if err != nil {
		return nil, err
	}
	var list []rawLog
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	out := make([]Log, 0, len(list))
	for _, l := range list {
		out = append(out, l.log())
	}
	return out, nil
}

// ErrNotFound is a transaction or receipt the endpoint does not hold:
// past its horizon, or not yet mined.
var ErrNotFound = errors.New("not found")

// Receipt is the outcome of one transaction.
type Receipt struct {
	Status  bool
	GasUsed uint64
	Logs    []Log
}

// TransactionReceipt reads whether a transaction succeeded and what it emitted.
func (c *Client) TransactionReceipt(ctx context.Context, hash string) (Receipt, error) {
	raw, err := c.Call(ctx, "eth_getTransactionReceipt", hash)
	if err != nil {
		return Receipt{}, err
	}
	// A pruned endpoint answers null for a receipt past its horizon; that
	// is not a failed transaction.
	if string(raw) == "null" {
		return Receipt{}, ErrNotFound
	}
	var r struct {
		Status  string   `json:"status"`
		GasUsed string   `json:"gasUsed"`
		Logs    []rawLog `json:"logs"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return Receipt{}, err
	}
	out := Receipt{Status: r.Status == "0x1"}
	out.GasUsed, _ = parseQuantity(r.GasUsed)
	for _, l := range r.Logs {
		out.Logs = append(out.Logs, l.log())
	}
	return out, nil
}

// IsContract says whether an address holds code.
func (c *Client) IsContract(ctx context.Context, address string) (bool, error) {
	raw, err := c.Call(ctx, "eth_getCode", address, "latest")
	if err != nil {
		return false, err
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return false, err
	}
	return len(strings.TrimPrefix(s, "0x")) > 0, nil
}

// Header is a block without its transactions.
type Header struct {
	Number    uint64
	Hash      string
	Timestamp time.Time
}

// HeaderByNumber reads a block's header.
func (c *Client) HeaderByNumber(ctx context.Context, n uint64) (Header, error) {
	raw, err := c.Call(ctx, "eth_getBlockByNumber", "0x"+strconv.FormatUint(n, 16), false)
	if err != nil {
		return Header{}, err
	}
	return ParseHeader(raw)
}

// ParseHeader decodes a block header as the RPC or a newHeads
// subscription renders it.
func ParseHeader(raw json.RawMessage) (Header, error) {
	var h struct {
		Number    string `json:"number"`
		Hash      string `json:"hash"`
		Timestamp string `json:"timestamp"`
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		return Header{}, err
	}
	if h.Number == "" {
		return Header{}, errors.New("block not found")
	}
	out := Header{Hash: h.Hash}
	out.Number, _ = parseQuantity(h.Number)
	if ts, err := parseQuantity(h.Timestamp); err == nil {
		out.Timestamp = time.Unix(int64(ts), 0).UTC()
	}
	return out, nil
}

// TransactionByHash reads one transaction: who sent it, to what, with
// which calldata.
func (c *Client) TransactionByHash(ctx context.Context, hash string) (Tx, uint64, error) {
	raw, err := c.Call(ctx, "eth_getTransactionByHash", hash)
	if err != nil {
		return Tx{}, 0, err
	}
	var t struct {
		Hash        string `json:"hash"`
		From        string `json:"from"`
		To          string `json:"to"`
		Input       string `json:"input"`
		Value       string `json:"value"`
		BlockNumber string `json:"blockNumber"`
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return Tx{}, 0, err
	}
	if t.Hash == "" {
		return Tx{}, 0, ErrNotFound
	}
	n, _ := parseQuantity(t.BlockNumber)
	return Tx{Hash: t.Hash, From: strings.ToLower(t.From), To: strings.ToLower(t.To), Input: parseData(t.Input), Value: t.Value}, n, nil
}

// Code reads a contract's deployed bytes.
func (c *Client) Code(ctx context.Context, address string) ([]byte, error) {
	raw, err := c.Call(ctx, "eth_getCode", address, "latest")
	if err != nil {
		return nil, err
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	return parseData(s), nil
}

// StorageAt reads one storage word.
func (c *Client) StorageAt(ctx context.Context, address, slot string) ([]byte, error) {
	raw, err := c.Call(ctx, "eth_getStorageAt", address, slot, "latest")
	if err != nil {
		return nil, err
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	return parseData(s), nil
}

// EIP-1967 slots: where a proxy keeps its implementation and its admin.
const (
	SlotImplementation = "0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc"
	SlotAdmin          = "0xb53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d6103"
)

// ParseLog decodes one log as a subscription delivers it.
func ParseLog(raw json.RawMessage) (Log, error) {
	var l rawLog
	if err := json.Unmarshal(raw, &l); err != nil {
		return Log{}, err
	}
	return l.log(), nil
}

// AddressOf renders a storage word's low twenty bytes as a checksummed
// address, or empty when the word is zero.
func AddressOf(word []byte) string {
	if len(word) < 20 {
		return ""
	}
	tail := word[len(word)-20:]
	zero := true
	for _, b := range tail {
		if b != 0 {
			zero = false
			break
		}
	}
	if zero {
		return ""
	}
	return Checksum(tail)
}

// CallContract executes a call against the latest state without sending
// it: the return data, or a RevertError with the revert data.
func (c *Client) CallContract(ctx context.Context, from, to string, data []byte, value string) ([]byte, error) {
	call := map[string]any{"to": to, "data": "0x" + hex.EncodeToString(data)}
	if from != "" {
		call["from"] = from
	}
	if value != "" && value != "0x0" && value != "0x" {
		call["value"] = value
	}
	raw, err := c.Call(ctx, "eth_call", call, "latest")
	if err != nil {
		return nil, err
	}
	var out string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return parseData(out), nil
}

// EstimateGas is the gas the same call would take against the latest state.
func (c *Client) EstimateGas(ctx context.Context, from, to string, data []byte, value string) (uint64, error) {
	call := map[string]any{"to": to, "data": "0x" + hex.EncodeToString(data)}
	if from != "" {
		call["from"] = from
	}
	if value != "" && value != "0x0" && value != "0x" {
		call["value"] = value
	}
	raw, err := c.Call(ctx, "eth_estimateGas", call)
	if err != nil {
		return 0, err
	}
	var out string
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, err
	}
	return parseQuantity(out)
}

// Symbol reads an ERC-20 symbol: a string, or the bytes32 some older
// tokens return. Empty when the address answers with neither.
func (c *Client) Symbol(ctx context.Context, address string) string {
	out, err := c.CallContract(ctx, "", address, []byte{0x95, 0xd8, 0x9b, 0x41}, "")
	if err != nil || len(out) == 0 {
		return ""
	}
	if len(out) >= 64 {
		d := Decoded{}
		if err := decodeTuple(out, []Param{{Name: "s", Type: "string"}}, "", d); err == nil && d["s"] != "" {
			return clean(d["s"])
		}
	}
	if len(out) == 32 {
		return clean(string(bytes.TrimRight(out, "\x00")))
	}
	return ""
}

// clean keeps a symbol printable and short.
func clean(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r > 0x7e {
			continue
		}
		b.WriteRune(r)
		if b.Len() >= 12 {
			break
		}
	}
	return b.String()
}

// BalanceOf reads an ERC-20 balance.
func (c *Client) BalanceOf(ctx context.Context, token, holder string) (*big.Int, error) {
	data := append([]byte{0x70, 0xa0, 0x82, 0x31}, make([]byte, 32)...)
	copy(data[4+12:], parseData(holder))
	out, err := c.CallContract(ctx, "", token, data, "")
	if err != nil {
		return nil, err
	}
	if len(out) < 32 {
		return nil, errors.New("short balanceOf answer")
	}
	return new(big.Int).SetBytes(out[:32]), nil
}

// Decimals reads an ERC-20's decimals; 18 when it does not say.
func (c *Client) Decimals(ctx context.Context, token string) int {
	out, err := c.CallContract(ctx, "", token, []byte{0x31, 0x3c, 0xe5, 0x67}, "")
	if err != nil || len(out) < 32 {
		return 18
	}
	return int(new(big.Int).SetBytes(out[:32]).Int64())
}
