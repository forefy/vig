package ethereum

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/forefy/vig/backend/internal/chain"
	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/evm"
)

// The watch keeps an EVM contract's surface live from the public
// endpoint's websocket: every new block is read in full for the
// transactions sent to the contract, and a logs subscription delivers
// every event it emitted, which is how calls that reached it through
// another contract are seen at all. Receipts and the transactions behind
// events are read within a budget. Every hundred blocks the code hash
// and the proxy slots are read again, so an upgrade is a row on the
// contract the moment it lands. Coverage is counted in blocks.

// wsConn is one websocket per chain, shared by every session on it.
type wsConn struct {
	mu    sync.Mutex
	conns map[string]*chain.WSConn
}

var sockets = &wsConn{conns: map[string]*chain.WSConn{}}

// Dial replaces chain.DialWS in tests.
var Dial = chain.DialWS

func (w *wsConn) acquire(ctx context.Context, c Chain, url string) (*chain.WSConn, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if conn := w.conns[c.ID]; conn != nil && conn.Alive() {
		return conn, nil
	}
	conn, err := Dial(context.WithoutCancel(ctx), []string{url})
	if err != nil {
		return nil, err
	}
	w.conns[c.ID] = conn
	return conn, nil
}

const (
	backfillBlocks = 50  // blocks read in full after a disconnect; the rest is a gap
	lifeCheck      = 100 // blocks between code and proxy reads
)

// Watch implements core.Watcher: one session per audit.
func (a *Adapter) Watch(ctx context.Context, in core.WatchInput, sink core.WatchSink) error {
	if a.Clients == nil {
		return core.ErrNotWired
	}
	if in.Surface == nil {
		return errors.New("no surface to watch")
	}
	budget := in.Budget
	if budget.Details == 0 {
		budget = core.DefaultWatchBudget()
	}
	_ = sink.State(ctx, core.WatchConnecting, "")

	subjects := map[string]*watched{}
	byChain := map[string][]*watched{}
	for _, addr := range in.Subjects {
		ch := chainFor(in.Chains, addr)
		addr = strings.ToLower(addr)
		facts, ifc := a.interfaceOf(ctx, addr, ch, nil, in.Siblings)
		s := &watched{addr: addr, chain: ch, client: a.Clients(ch), acc: newRowAcc(addr, ifc, a.Lookup, true), facts: facts, cursor: in.Cursors[addr], seen: map[string]bool{}, tokens: budget.Details, refill: time.Now()}
		var explorer *evm.Explorer
		if a.Explorers != nil {
			explorer = a.Explorers(ch)
		}
		s.acc.symbol = a.symbolOf(s.client, explorer)
		subjects[addr] = s
		byChain[ch.ID] = append(byChain[ch.ID], s)
	}
	if len(in.Accounts) == 0 {
		var accounts []core.WatchedAccount
		for _, s := range subjects {
			accounts = append(accounts, core.WatchedAccount{Address: s.addr, NodeID: programNodeID(s.addr), Role: "program"})
		}
		_ = sink.Accounts(ctx, accounts)
	}

	type sub struct {
		s     *chain.Subscription
		chain string
		addr  string // empty for heads
		holds bool   // a Transfer filter on the subject: what it holds changed
	}
	var subs []sub
	defer func() {
		for _, sb := range subs {
			sb.s.Close()
		}
	}()
	dead := make(chan error, len(byChain))
	for id, list := range byChain {
		ch := list[0].chain
		url := ch.RPC
		if o := a.RPCOverride[ch.ID]; o != "" {
			url = o
		}
		conn, err := sockets.acquire(ctx, ch, url)
		if err != nil {
			return err
		}
		heads, err := conn.Subscribe(ctx, "eth_subscribe", "newHeads")
		if err != nil {
			return fmt.Errorf("newHeads: %w", err)
		}
		subs = append(subs, sub{s: heads, chain: id})
		for _, s := range list {
			lg, err := conn.Subscribe(ctx, "eth_subscribe", "logs", map[string]any{"address": s.addr})
			if err != nil {
				return fmt.Errorf("logs: %w", err)
			}
			subs = append(subs, sub{s: lg, chain: id, addr: s.addr})
			// Every ERC-20 Transfer into or out of the contract, from any
			// token: what the contract holds moves.
			topic := "0x" + hex.EncodeToString(transferTopic[:])
			padded := "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(s.addr, "0x")
			for _, topics := range [][]any{{topic, nil, padded}, {topic, padded}} {
				h, err := conn.Subscribe(ctx, "eth_subscribe", "logs", map[string]any{"topics": topics})
				if err != nil {
					slog.Warn("evm watch: holdings subscription", "contract", s.addr, "err", err)
					continue
				}
				subs = append(subs, sub{s: h, chain: id, addr: s.addr, holds: true})
			}
		}
		go func(id string, conn *chain.WSConn) {
			select {
			case <-conn.Done():
				err := conn.Err()
				if err == nil {
					err = errors.New("closed")
				}
				dead <- fmt.Errorf("chain %s: websocket: %w", id, err)
			case <-ctx.Done():
			}
		}(id, conn)
	}

	// Catch up from every cursor before going live.
	_ = sink.State(ctx, core.WatchBackfilling, "")
	for _, s := range subjects {
		if err := a.backfill(ctx, s.client, s.addr, s.acc, &s.cursor, sink, budget); err != nil {
			slog.Warn("evm watch: backfill", "contract", s.addr, "err", err)
		}
		s.dirty = true
	}
	_ = sink.State(ctx, core.WatchLive, "")

	events := make(chan struct {
		sub
		n chain.Notification
	}, 64)
	for _, sb := range subs {
		go func(sb sub) {
			for n := range sb.s.C {
				select {
				case events <- struct {
					sub
					n chain.Notification
				}{sb, n}:
				case <-ctx.Done():
					return
				}
			}
		}(sb)
	}
	flush := time.NewTicker(2 * time.Second)
	defer flush.Stop()
	flushAll := func(force bool) {
		now := time.Now().UTC()
		for _, s := range subjects {
			if len(s.acc.evidence) > 0 {
				rows := s.acc.evidence
				s.acc.evidence = nil
				if err := sink.Evidence(ctx, rows); err != nil {
					slog.Warn("evm watch: evidence", "contract", s.addr, "err", err)
				}
			}
			if len(s.acc.nodes) > 0 || len(s.acc.edges) > 0 {
				nodes, edges := s.acc.nodes, s.acc.edgeList()
				s.acc.nodes, s.acc.edges = nil, map[string]*core.SurfaceEdge{}
				if err := sink.Extend(ctx, nodes, edges); err != nil {
					slog.Warn("evm watch: extend", "contract", s.addr, "err", err)
				}
			}
			if !s.dirty && !force {
				continue
			}
			s.dirty = false
			_ = sink.Cursor(ctx, s.addr, s.cursor)
			if s.span != "" {
				_, _ = sink.Coverage(ctx, core.CoverageSpan{ID: s.span, Subject: s.addr, Kind: core.CoverageLive, ToSlot: s.spanTo, ToAt: &now})
			}
		}
	}
	for {
		select {
		case <-ctx.Done():
			flushAll(true)
			_ = sink.State(context.WithoutCancel(ctx), core.WatchEnded, "")
			return ctx.Err()
		case err := <-dead:
			flushAll(true)
			return err
		case <-flush.C:
			flushAll(false)
		case ev := <-events:
			if ev.addr == "" {
				h, err := evm.ParseHeader(ev.n.Result)
				if err != nil {
					continue
				}
				// One block read serves every contract watched on the chain.
				list := byChain[ev.chain]
				blk, err := list[0].client.BlockByNumber(ctx, h.Number)
				if err != nil {
					slog.Debug("evm watch: block", "n", h.Number, "err", err)
					continue
				}
				for _, s := range list {
					for _, tx := range blk.Txs {
						if tx.To != s.addr {
							continue
						}
						s.seen[tx.Hash] = true
						var rc *evm.Receipt
						if s.spend(budget) {
							if r, err := s.client.TransactionReceipt(ctx, tx.Hash); err == nil {
								rc = &r
							}
						}
						s.acc.add(ctx, tx, blk.Number, blk.Timestamp, rc)
					}
					now := blk.Timestamp
					s.cursor = core.Cursor{Ref: h.Hash, Slot: h.Number, At: &now}
					if s.span == "" {
						if id, err := sink.Coverage(ctx, core.CoverageSpan{Subject: s.addr, Kind: core.CoverageLive, FromSlot: h.Number, ToSlot: h.Number, FromAt: &now, ToAt: &now}); err == nil {
							s.span = id
						}
					}
					s.spanTo = h.Number
					s.dirty = true
					if h.Number-s.last >= lifeCheck {
						s.last = h.Number
						a.life(ctx, s.client, s.addr, s.chain, &s.facts, sink, h.Number, now)
					}
				}
				continue
			}
			s := subjects[ev.addr]
			if s == nil {
				continue
			}
			lg, err := evm.ParseLog(ev.n.Result)
			if err != nil {
				continue
			}
			at := time.Now().UTC()
			if ev.holds {
				a.holdingMoved(ctx, s, lg, at, sink)
				continue
			}
			s.acc.event(lg, at, s.acc.nextEvent(lg.TxHash))
			if !s.seen[lg.TxHash] && s.spend(budget) {
				// Reached through another contract: the transaction says who.
				s.seen[lg.TxHash] = true
				if tx, block, err := s.client.TransactionByHash(ctx, lg.TxHash); err == nil {
					var rc *evm.Receipt
					if r, err := s.client.TransactionReceipt(ctx, tx.Hash); err == nil {
						rc = &r
					}
					s.acc.add(ctx, tx, block, at, rc)
				}
			}
		}
	}
}

// watched is one contract in a session.
type watched struct {
	addr   string
	chain  Chain
	client *evm.Client
	acc    *rowAcc
	facts  Facts
	cursor core.Cursor
	span   string
	spanTo uint64
	dirty  bool
	seen   map[string]bool // transaction hashes this session placed
	tokens int
	refill time.Time
	last   uint64 // block of the last life check
}

// spend takes one detail read from the budget, refilled by the minute.
func (s *watched) spend(b core.WatchBudget) bool {
	if time.Since(s.refill) >= time.Minute {
		s.tokens, s.refill = b.Details, time.Now()
	}
	if s.tokens <= 0 {
		return false
	}
	s.tokens--
	return true
}

// nextEvent numbers a transaction's events in arrival order, so refs stay
// distinct without a log index.
func (acc *rowAcc) nextEvent(txHash string) int {
	if acc.eventN == nil {
		acc.eventN = map[string]int{}
	}
	n := acc.eventN[txHash]
	acc.eventN[txHash] = n + 1
	return n
}

// backfill reads the blocks since the cursor, up to the block budget, and
// records the rest as a gap.
func (a *Adapter) backfill(ctx context.Context, c *evm.Client, addr string, acc *rowAcc, cursor *core.Cursor, sink core.WatchSink, budget core.WatchBudget) error {
	head, err := c.BlockNumber(ctx)
	if err != nil {
		return err
	}
	if cursor.Slot == 0 || cursor.Slot >= head {
		return nil
	}
	from := cursor.Slot + 1
	if head-from+1 > backfillBlocks {
		gapTo := head - backfillBlocks
		_, _ = sink.Coverage(ctx, core.CoverageSpan{Subject: addr, Kind: core.CoverageGap, FromSlot: from, ToSlot: gapTo})
		from = gapTo + 1
	}
	var first, last time.Time
	for n := from; n <= head && ctx.Err() == nil; n++ {
		blk, err := c.BlockByNumber(ctx, n)
		if err != nil {
			return err
		}
		if first.IsZero() {
			first = blk.Timestamp
		}
		last = blk.Timestamp
		for _, tx := range blk.Txs {
			if tx.To == addr {
				acc.add(ctx, tx, blk.Number, blk.Timestamp, nil)
			}
		}
		*cursor = core.Cursor{Slot: n, At: &last}
		if budget.Pace > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(budget.Pace):
			}
		}
	}
	if logs, err := c.Logs(ctx, addr, from, head); err == nil {
		for _, lg := range logs {
			acc.event(lg, last, acc.nextEvent(lg.TxHash))
		}
	}
	if head >= from {
		_, _ = sink.Coverage(ctx, core.CoverageSpan{Subject: addr, Kind: core.CoverageBackfill, FromSlot: from, ToSlot: head, FromAt: &first, ToAt: &last})
	}
	return sink.Cursor(ctx, addr, *cursor)
}

// life reads the code hash and the proxy slots again; a change is a
// write on the contract and new facts on its node.
func (a *Adapter) life(ctx context.Context, c *evm.Client, addr string, ch Chain, prev *Facts, sink core.WatchSink, block uint64, at time.Time) {
	f, err := a.Inspect(ctx, addr, ch, "")
	if err != nil || !f.Exists {
		return
	}
	changed := f.CodeKeccak != prev.CodeKeccak || f.Implementation != prev.Implementation || f.Admin != prev.Admin
	*prev = f
	if !changed {
		return
	}
	facts := map[string]string{"bytecode_now": f.CodeKeccak, "last_upgrade": at.Format(time.RFC3339)}
	if f.Implementation != "" {
		facts["implementation"] = f.Implementation
		facts["loader"] = "evm proxy"
		facts["immutable"] = "false"
	}
	if f.Admin != "" {
		facts["authority"] = f.Admin
	}
	_ = sink.Facts(ctx, programNodeID(addr), facts)
	summary := map[string]any{"slot": block, "live": true, "kind": "program_data", "bytecode_now": f.CodeKeccak}
	if f.Implementation != "" {
		summary["implementation"] = f.Implementation
	}
	if f.Admin != "" {
		summary["authority_now"] = f.Admin
	}
	_ = sink.Evidence(ctx, []core.SurfaceEvidence{{NodeID: programNodeID(addr), Kind: "write", Ref: "upgrade@" + fmt.Sprint(block), Actor: "system", At: &at, Summary: summary}})
}

// transferTopic is the ERC-20 Transfer(address,address,uint256) topic.
var transferTopic = evm.Keccak([]byte("Transfer(address,address,uint256)"))

// holdingMoved re-reads one token's balance after a Transfer touched the
// contract, rewrites the holdings fact, and records the move as a write
// on the contract's node.
func (a *Adapter) holdingMoved(ctx context.Context, s *watched, lg evm.Log, at time.Time, sink core.WatchSink) {
	token := strings.ToLower(lg.Address)
	if token == s.addr || len(lg.Topics) < 3 {
		return
	}
	bal, err := s.client.BalanceOf(ctx, token, s.addr)
	if err != nil {
		return
	}
	var h *evm.Holding
	for i := range s.facts.Holdings {
		if s.facts.Holdings[i].Token == token {
			h = &s.facts.Holdings[i]
		}
	}
	if h == nil {
		sym := ""
		if s.acc.symbol != nil {
			sym = s.acc.symbol(ctx, token)
		}
		s.facts.Holdings = append(s.facts.Holdings, evm.Holding{Token: token, Symbol: sym, Decimals: s.client.Decimals(ctx, token), Value: new(big.Int)})
		h = &s.facts.Holdings[len(s.facts.Holdings)-1]
	}
	before := new(big.Int).Set(h.Value)
	h.Value = bal
	kept := s.facts.Holdings[:0]
	for _, x := range s.facts.Holdings {
		if x.Value.Sign() > 0 {
			kept = append(kept, x)
		}
	}
	s.facts.Holdings = kept
	_ = sink.Facts(ctx, programNodeID(s.addr), map[string]string{"holdings": evm.FormatHoldings(s.facts.Holdings), "holdings_count": strconv.Itoa(len(s.facts.Holdings))})
	delta := new(big.Int).Sub(bal, before)
	name := h.Symbol
	if name == "" {
		name = shortAddr(token)
	}
	summary := map[string]any{"slot": lg.BlockNumber, "live": true, "kind": "holding", "token": token, "symbol": name, "balance": evm.Units(bal, h.Decimals), "delta": evm.Units(delta, h.Decimals)}
	if delta.Sign() < 0 {
		summary["delta"] = "-" + evm.Units(new(big.Int).Neg(delta), h.Decimals)
	}
	_ = sink.Evidence(ctx, []core.SurfaceEvidence{{NodeID: programNodeID(s.addr), Kind: "write", Ref: "holding@" + lg.TxHash + ":" + token, Actor: "system", At: &at, Summary: summary}})
}
