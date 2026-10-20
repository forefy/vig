package solana

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/forefy/vig/backend/internal/chain"
	"github.com/forefy/vig/backend/internal/core"
)

// Watcher keeps a mapped surface live from the public RPC's websocket. A
// logs subscription per subject delivers every confirmed transaction that
// mentions it, with the call tree in its logs; an account subscription per
// shared-state address delivers every write with the new data; the
// program-data account is watched the same way, so an upgrade is a row on
// the program node the moment it lands. A bounded number of transactions
// are also read in full, for the rows that need signers, keys and
// arguments. After a disconnect the signature cursor is walked back to
// where the last session stopped, and whatever the budget could not read
// is recorded as a gap, never as silence.
type Watcher struct {
	*SurfaceBuilder
	// URLs are the HTTP endpoints; their websocket form is derived.
	URLs []string
	// Dial replaces chain.DialWS in tests.
	Dial func(ctx context.Context, urls []string) (*chain.WSConn, error)

	// One connection per watched program, shared by every audit of it:
	// identical subscriptions share one stream, so ten audits of one
	// program cost the endpoint one stream, and a program whose traffic
	// overwhelms its socket takes down only its own sessions.
	mu    sync.Mutex
	conns map[string]*chain.WSConn
	next  map[string]int // endpoint to try first per program, rotated after a death
}

// acquire returns the connection of a program, dialling when there is
// none or the last one died. Connections outlive any one session.
func (w *Watcher) acquire(ctx context.Context, key string, dial func(context.Context, []string) (*chain.WSConn, error)) (*chain.WSConn, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.conns == nil {
		w.conns = map[string]*chain.WSConn{}
	}
	if c := w.conns[key]; c != nil && c.Alive() {
		return c, nil
	}
	if w.next == nil {
		w.next = map[string]int{}
	}
	// A connection the endpoint closed is retried on the next endpoint
	// first, so a stream one public node refuses may run on another.
	urls := w.URLs
	if n := len(urls); n > 1 {
		if w.conns[key] != nil {
			w.next[key] = (w.next[key] + 1) % n
		}
		i := w.next[key] % n
		urls = append(append([]string{}, urls[i:]...), urls[:i]...)
	}
	conn, err := dial(context.WithoutCancel(ctx), urls)
	if err != nil {
		return nil, err
	}
	w.conns[key] = conn
	return conn, nil
}

// subject is one audited program with what the map knows of it.
type subject struct {
	pid     string
	specs   []instructionSpec
	idl     *idlSpec
	byDisc  map[string]instructionSpec
	byName  map[string]string // folded name -> node name, from specs and the map
	seen    map[string]int    // node id -> rows this session, for the "first few in full" rule
	span    string            // open live coverage span id
	spanTo  uint64
	cursor  core.Cursor
	dirty   bool // cursor or span changed since the last flush
	flushed time.Time
	// Past the row budget, calls are only counted: per node until the
	// next flush, and the program says its rows are sampled.
	minute  time.Time
	rows    int
	tallies map[string]*tally
	sampled bool
	// Rows wait for the flush so the stream is never behind a write.
	pending []core.SurfaceEvidence
	// The logs stream and how many of its notifications this session
	// missed; a miss closes the live span and records a gap.
	logs    *chain.Subscription
	dropped int
}

// pendingFlush is how many rows may wait before a flush is forced.
const pendingFlush = 500

// rowWindow is the slice of a minute the row budget is spread over.
const rowWindow = 5 * time.Second

// tally is calls on one node not stored as rows.
type tally struct {
	calls, failed int
	units         int64
	at            time.Time
}

// backfillReads bounds the transactions read in full while catching up
// after a disconnect; the rest of the listed range is a gap.
const backfillReads = 40

// firstInFull is how many calls of each instruction per session are read
// in full regardless of priority, so every node carries keys and signers.
const firstInFull = 3

// Watch implements core.Watcher.
func (w *Watcher) Watch(ctx context.Context, in core.WatchInput, sink core.WatchSink) error {
	if w.SurfaceBuilder == nil || w.Chain == nil {
		return errors.New("chain not configured")
	}
	if in.Surface == nil {
		return errors.New("no surface to watch")
	}
	budget := in.Budget
	if budget.Details == 0 {
		budget = core.DefaultWatchBudget()
	}
	dial := w.Dial
	if dial == nil {
		dial = chain.DialWS
	}
	_ = sink.State(ctx, core.WatchConnecting, "")

	subjects := map[string]*subject{}
	for _, pid := range in.Subjects {
		specs, idl, _ := w.instructions(ctx, pid)
		s := &subject{pid: pid, specs: specs, idl: idl, byDisc: map[string]instructionSpec{}, byName: map[string]string{}, seen: map[string]int{}, cursor: in.Cursors[pid]}
		for _, ins := range specs {
			s.byDisc[sigKey(pid, ins.Discriminator)] = ins
			s.byName[foldName(ins.Name)] = ins.Name
		}
		for _, n := range in.Surface.Nodes {
			if n.Kind == "instruction" && n.Parent == programNodeID(pid) {
				if _, ok := s.byName[foldName(n.Name)]; !ok {
					s.byName[foldName(n.Name)] = n.Name
				}
			}
		}
		// A first session starts where the sample ended, so the watch
		// covers everything since intake, read or declared a gap. A
		// stored cursor with no ref is one the endpoint could not walk
		// to: the gap is already recorded, and the stream starts fresh.
		if _, stored := in.Cursors[pid]; !stored {
			s.cursor = newestSampled(in.Sample, pid)
		}
		subjects[pid] = s
	}

	accounts := in.Accounts
	if len(accounts) == 0 {
		accounts = w.pickAccounts(ctx, in, budget.Accounts)
		if err := sink.Accounts(ctx, accounts); err != nil {
			return err
		}
	}
	byAddress := map[string]core.WatchedAccount{}
	for _, a := range accounts {
		byAddress[a.Address] = a
	}

	type sub struct {
		s       *chain.Subscription
		subject string
		account string
	}
	var subs []sub
	defer func() {
		for _, sb := range subs {
			sb.s.Close()
		}
	}()
	conns := map[string]*chain.WSConn{}
	for pid, s := range subjects {
		conn, err := w.acquire(ctx, pid, dial)
		if err != nil {
			return err
		}
		conns[pid] = conn
		st, err := conn.LogsFor(ctx, pid)
		if err != nil {
			return fmt.Errorf("logs subscription: %w", err)
		}
		s.logs = st
		subs = append(subs, sub{s: st, subject: pid})
	}
	for _, a := range accounts {
		conn := conns[programOf(a.NodeID)]
		if conn == nil {
			continue
		}
		st, err := conn.AccountFor(ctx, a.Address)
		if err != nil {
			slog.Warn("watch: account subscription", "address", a.Address, "err", err)
			continue
		}
		subs = append(subs, sub{s: st, account: a.Address})
	}
	// Any connection dying ends the session; the next one backfills.
	dead := make(chan error, len(conns))
	for pid, conn := range conns {
		go func(pid string, conn *chain.WSConn) {
			select {
			case <-conn.Done():
				err := conn.Err()
				if err == nil {
					err = errors.New("closed")
				}
				dead <- fmt.Errorf("%s: websocket: %w", shortID(pid), err)
			case <-ctx.Done():
			}
		}(pid, conn)
	}

	priority := map[string]bool{}
	for _, id := range in.Priority {
		priority[id] = true
	}
	// Detail reads run beside the stream, paced, up to the per-minute
	// budget; the queue is small on purpose so a burst never piles up
	// reads that would arrive long after their row.
	details := make(chan detailReq, 64)
	go w.detailLoop(ctx, details, subjects, sink, budget)

	// Catch up from every cursor before going live, so the live span
	// starts where the backfill ends and the two meet without a hole.
	_ = sink.State(ctx, core.WatchBackfilling, "")
	for _, s := range subjects {
		if err := w.backfill(ctx, s, sink, budget); err != nil {
			slog.Warn("watch: backfill", "program", s.pid, "err", err)
		}
	}
	_ = sink.State(ctx, core.WatchLive, "")

	// Fan every subscription into one loop.
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
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	edges := &edgeAcc{edges: map[string]*core.SurfaceEdge{}}
	for {
		select {
		case <-ctx.Done():
			w.flushAll(context.WithoutCancel(ctx), subjects, edges, sink, true)
			_ = sink.State(context.WithoutCancel(ctx), core.WatchEnded, "")
			return ctx.Err()
		case err := <-dead:
			w.flushAll(ctx, subjects, edges, sink, true)
			return err
		case <-ping.C:
			for _, conn := range conns {
				if err := conn.Ping(ctx); err != nil {
					w.flushAll(ctx, subjects, edges, sink, true)
					return fmt.Errorf("ping: %w", err)
				}
			}
		case <-flush.C:
			// A session that fell behind its stream missed a range it can
			// name: the live span closes there and a gap is recorded, and
			// the stream goes on. Nothing reconnects.
			for _, s := range subjects {
				if s.logs == nil {
					continue
				}
				if d := s.logs.Dropped(); d > s.dropped {
					slog.Warn("watch: notifications dropped", "program", s.pid, "dropped", d-s.dropped)
					s.dropped = d
					if s.span != "" && s.cursor.Slot > s.spanTo {
						at := time.Now().UTC()
						_, _ = sink.Coverage(ctx, core.CoverageSpan{ID: s.span, Subject: s.pid, Kind: core.CoverageLive, ToSlot: s.spanTo, ToAt: &at})
						_, _ = sink.Coverage(ctx, core.CoverageSpan{Subject: s.pid, Kind: core.CoverageGap, FromSlot: s.spanTo + 1, ToSlot: s.cursor.Slot, FromAt: &at, ToAt: &at})
					}
					s.span, s.spanTo = "", 0
				}
			}
			w.flushAll(ctx, subjects, edges, sink, false)
		case ev := <-events:
			switch {
			case ev.subject != "":
				w.onLogs(ctx, subjects[ev.subject], ev.n, sink, details, priority, edges, budget)
			case ev.account != "":
				w.onAccount(ctx, byAddress[ev.account], subjects, ev.n, sink)
			}
		}
	}
}

// newestSampled finds where the sample ended for a subject.
func newestSampled(sample []core.SurfaceEvidence, pid string) core.Cursor {
	var c core.Cursor
	prefix := "ix:" + pid + ":"
	for _, ev := range sample {
		if ev.Kind != "tx" || ev.Ref == "" || !strings.HasPrefix(ev.NodeID, prefix) {
			continue
		}
		slot := summarySlot(ev.Summary)
		if slot > c.Slot {
			c = core.Cursor{Ref: ev.Ref, Slot: slot, At: ev.At}
		}
	}
	return c
}

func summarySlot(summary map[string]any) uint64 {
	switch v := summary["slot"].(type) {
	case float64:
		return uint64(v)
	case uint64:
		return v
	case int:
		return uint64(v)
	case int64:
		return uint64(v)
	}
	return 0
}

// pickAccounts chooses the addresses to subscribe to: for every audited
// program its program-data and IDL accounts, then the addresses the sample
// passed again and again for a writable, unsigned declared account, which
// is what shared state looks like from outside: a config, a vault, a pool.
func (w *Watcher) pickAccounts(ctx context.Context, in core.WatchInput, cap int) []core.WatchedAccount {
	var out []core.WatchedAccount
	for _, pid := range in.Subjects {
		if pd, err := w.Chain.ProgramDataAddress(ctx, pid); err == nil {
			out = append(out, core.WatchedAccount{Address: pd, NodeID: programNodeID(pid), Role: "program"})
		}
		if idl, err := chain.AnchorIDLAddress(pid); err == nil {
			out = append(out, core.WatchedAccount{Address: idl, NodeID: programNodeID(pid), Role: "idl"})
		}
	}
	// Declared accounts per instruction, in declaration order, which is
	// the order the keys were recorded in.
	decl := map[string][]core.SurfaceNode{}
	for _, n := range in.Surface.Nodes {
		if n.Kind == "account" && n.Parent != "" {
			decl[n.Parent] = append(decl[n.Parent], n)
		}
	}
	subject := map[string]bool{}
	for _, pid := range in.Subjects {
		subject[pid] = true
	}
	type key struct{ node, address string }
	counts := map[key]int{}
	rows := map[string]int{}
	for _, ev := range in.Sample {
		if ev.Kind != "tx" {
			continue
		}
		keys, _ := ev.Summary["keys"].([]any)
		accts := decl[ev.NodeID]
		if len(keys) == 0 || len(accts) == 0 {
			continue
		}
		rows[ev.NodeID]++
		for i, k := range keys {
			addr, _ := k.(string)
			if i >= len(accts) || addr == "" || subject[addr] || knownPrograms[addr] != "" {
				continue
			}
			a := accts[i]
			if a.Facts["writable"] != "true" || a.Facts["signer"] == "true" {
				continue
			}
			counts[key{a.ID, addr}]++
		}
	}
	type pick struct {
		key
		n int
	}
	var picks []pick
	for k, n := range counts {
		// Seen on most calls, or on several: a per-user account never is.
		if n >= 3 || (n >= 2 && n == rows[parentOf(k.node)]) {
			picks = append(picks, pick{k, n})
		}
	}
	sort.Slice(picks, func(i, j int) bool {
		if picks[i].n != picks[j].n {
			return picks[i].n > picks[j].n
		}
		return picks[i].address < picks[j].address
	})
	seen := map[string]bool{}
	for _, a := range out {
		seen[a.Address] = true
	}
	for _, p := range picks {
		if cap > 0 && len(out) >= cap {
			break
		}
		if seen[p.address] {
			continue
		}
		seen[p.address] = true
		out = append(out, core.WatchedAccount{Address: p.address, NodeID: p.node, Role: "state"})
	}
	return out
}

// programOf reads the program id out of any node id on the map.
func programOf(nodeID string) string {
	parts := strings.SplitN(nodeID, ":", 3)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

// parentOf reads the instruction node id out of an account node id.
func parentOf(acctID string) string {
	parts := strings.SplitN(acctID, ":", 4)
	if len(parts) < 4 {
		return ""
	}
	return "ix:" + parts[1] + ":" + parts[2]
}

// backfill walks the signature list from the cursor to now. What the
// budget reads becomes rows; what it lists but cannot read is a gap.
func (w *Watcher) backfill(ctx context.Context, s *subject, sink core.WatchSink, budget core.WatchBudget) error {
	if s.cursor.Ref == "" {
		// Nothing to catch up from: the live span starts at the chain's
		// head, which the first notification names.
		return nil
	}
	var listed []chain.Signature
	before := ""
	reached := false
	for len(listed) < budget.Backfill {
		limit := min(1000, budget.Backfill-len(listed))
		page, err := w.Chain.SignaturesPage(ctx, s.pid, before, s.cursor.Ref, limit)
		if err != nil {
			// A cursor the endpoint no longer knows (beyond its history, or
			// never on this chain) cannot be walked to: everything from it
			// to the head is a gap, and the stream starts fresh.
			var rpcErr *chain.RPCError
			if errors.As(err, &rpcErr) && rpcErr.NotFound() && before == "" {
				head, herr := w.Chain.GetSlot(ctx)
				if herr == nil && head > s.cursor.Slot {
					_, _ = sink.Coverage(ctx, core.CoverageSpan{Subject: s.pid, Kind: core.CoverageGap, FromSlot: s.cursor.Slot + 1, ToSlot: head, FromAt: s.cursor.At})
				}
				slog.Warn("watch: cursor unknown to the endpoint, gap recorded", "program", s.pid, "cursor", s.cursor.Ref, "err", err)
				s.cursor = core.Cursor{Slot: max(head, s.cursor.Slot)}
				return sink.Cursor(ctx, s.pid, s.cursor)
			}
			return err
		}
		listed = append(listed, page...)
		if len(page) < limit {
			reached = true
			break
		}
		before = page[len(page)-1].Signature
		if !pace(ctx, core.SurfaceBudget{Pace: budget.Pace}) {
			return ctx.Err()
		}
	}
	if len(listed) == 0 {
		return nil
	}
	// Oldest first, so rows land in order and the cursor ends newest.
	sort.SliceStable(listed, func(i, j int) bool { return listed[i].Slot < listed[j].Slot })
	if !reached {
		// The list filled before meeting the cursor: the range between is
		// unknown, and stays so.
		gap := core.CoverageSpan{Subject: s.pid, Kind: core.CoverageGap, FromSlot: s.cursor.Slot + 1, ToSlot: listed[0].Slot - 1}
		if gap.ToSlot >= gap.FromSlot {
			_, _ = sink.Coverage(ctx, gap)
		}
	}
	read := 0
	var unread []chain.Signature
	for _, sig := range listed {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if read >= backfillReads {
			unread = append(unread, sig)
			continue
		}
		if read > 0 && !pace(ctx, core.SurfaceBudget{Pace: budget.Pace}) {
			return ctx.Err()
		}
		read++
		if err := w.readInFull(ctx, s, sig.Signature, sink, true); err != nil {
			slog.Debug("watch: backfill read", "sig", sig.Signature, "err", err)
		}
		at := time.Now().UTC()
		if sig.BlockTime != nil {
			at = time.Unix(*sig.BlockTime, 0).UTC()
		}
		s.cursor = core.Cursor{Ref: sig.Signature, Slot: sig.Slot, At: &at}
	}
	if read > 0 {
		span := core.CoverageSpan{Subject: s.pid, Kind: core.CoverageBackfill, FromSlot: listed[0].Slot, ToSlot: listed[read-1].Slot}
		span.FromAt, span.ToAt = sigTime(listed[0]), sigTime(listed[read-1])
		_, _ = sink.Coverage(ctx, span)
	}
	if len(unread) > 0 {
		span := core.CoverageSpan{Subject: s.pid, Kind: core.CoverageGap, FromSlot: unread[0].Slot, ToSlot: unread[len(unread)-1].Slot}
		span.FromAt, span.ToAt = sigTime(unread[0]), sigTime(unread[len(unread)-1])
		_, _ = sink.Coverage(ctx, span)
		last := unread[len(unread)-1]
		s.cursor = core.Cursor{Ref: last.Signature, Slot: last.Slot, At: sigTime(last)}
	}
	s.dirty = true
	return sink.Cursor(ctx, s.pid, s.cursor)
}

func sigTime(sig chain.Signature) *time.Time {
	if sig.BlockTime == nil {
		return nil
	}
	t := time.Unix(*sig.BlockTime, 0).UTC()
	return &t
}

// onLogs turns one confirmed transaction's logs into rows on the
// instructions it called, and queues a full read where the logs are not
// enough or the node matters.
func (w *Watcher) onLogs(ctx context.Context, s *subject, n chain.Notification, sink core.WatchSink, details chan<- detailReq, priority map[string]bool, edges *edgeAcc, budget core.WatchBudget) {
	ln, err := chain.ParseLogsNotification(n.Result)
	if err != nil || ln.Signature == "" {
		return
	}
	trace := chain.ParseLogs(ln.Logs)
	now := time.Now().UTC()
	// The row budget is per minute, spent in five-second windows so a busy
	// program's rows arrive steadily rather than in the minute's first
	// seconds; past it this transaction is counted and not stored, which
	// keeps the count complete on any traffic.
	if m := now.Truncate(rowWindow); !m.Equal(s.minute) {
		s.minute, s.rows = m, 0
	}
	capped := budget.Rows > 0 && s.rows >= max(1, budget.Rows/int(time.Minute/rowWindow))
	if capped && !s.sampled {
		s.sampled = true
		_ = sink.Facts(ctx, programNodeID(s.pid), map[string]string{"live_rows": "sampled"})
	}
	var rows []core.SurfaceEvidence
	queued := false
	for _, f := range trace.All {
		if f.Program != s.pid {
			continue
		}
		name := ""
		if f.Instruction != "" {
			name = s.byName[foldName(f.Instruction)]
			if name == "" {
				name = f.Instruction
			}
		}
		if name == "" {
			// No Anchor log names the call: only the bytes do. One read
			// resolves it; when the queue is full the row lands on the
			// program itself, so the call still counts.
			if !queued && w.queue(details, detailReq{subject: s, sig: ln.Signature, live: true}) {
				queued = true
				continue
			}
			if capped {
				s.count(programNodeID(s.pid), f, now)
				continue
			}
			rows = append(rows, core.SurfaceEvidence{NodeID: programNodeID(s.pid), Kind: "tx", Ref: ln.Signature, Actor: "system", At: &now,
				Summary: map[string]any{"slot": ln.Slot, "live": true, "source": "logs", "unresolved": true, "seq": f.Seq, "failed": f.Failed}})
			continue
		}
		id := ixNodeID(s.pid, name)
		summary := map[string]any{"slot": ln.Slot, "live": true, "source": "logs", "seq": f.Seq}
		if f.Depth > 1 {
			summary["depth"] = f.Depth
		}
		if f.Units > 0 {
			summary["units"] = f.Units
		}
		if f.Failed || ln.Err != nil {
			summary["failed"] = true
			if e := failureOf(f, s.idl); e != "" {
				summary["error"] = e
			}
			if p := failedIn(f); p != "" && p != s.pid {
				summary["error_program"] = p
			}
		}
		if f.Parent != nil {
			summary["via"] = f.Parent.Program
			if root := f.Root(); root.Program != f.Parent.Program {
				summary["root"] = root.Program
			}
			edges.add(programNodeID(f.Parent.Program), id, f.Parent.Program)
		}
		if callees := f.Callees(); len(callees) > 0 {
			var targets []string
			for _, c := range callees {
				if c == s.pid {
					continue
				}
				targets = append(targets, c)
				edges.add(id, programNodeID(c), c)
			}
			if len(targets) > 0 {
				summary["cpi"] = targets
			}
		}
		if _, known := s.byName[foldName(name)]; !known {
			edges.node(core.SurfaceNode{ID: id, Parent: programNodeID(s.pid), Kind: "instruction", Name: name, Source: "traffic", Facts: map[string]string{"program": s.pid}})
			s.byName[foldName(name)] = name
		}
		s.seen[id]++
		if capped {
			s.count(id, f, now)
			continue
		}
		rows = append(rows, core.SurfaceEvidence{NodeID: id, Kind: "tx", Ref: ln.Signature, Actor: "system", At: &now, Summary: summary})
		if !queued && (f.Failed || priority[id] || s.seen[id] <= firstInFull) {
			queued = w.queue(details, detailReq{subject: s, sig: ln.Signature, live: true})
		}
	}
	if len(rows) > 0 {
		s.rows++
		s.pending = append(s.pending, rows...)
		if len(s.pending) >= pendingFlush {
			s.flushRows(ctx, sink)
		}
	}
	s.cursor = core.Cursor{Ref: ln.Signature, Slot: ln.Slot, At: &now}
	if s.span == "" {
		id, err := sink.Coverage(ctx, core.CoverageSpan{Subject: s.pid, Kind: core.CoverageLive, FromSlot: ln.Slot, ToSlot: ln.Slot, FromAt: &now, ToAt: &now})
		if err == nil {
			s.span = id
		}
	}
	if ln.Slot > s.spanTo {
		s.spanTo = ln.Slot
	}
	s.dirty = true
}

// flushRows writes the rows waiting on a subject in one call.
func (s *subject) flushRows(ctx context.Context, sink core.WatchSink) {
	if len(s.pending) == 0 {
		return
	}
	rows := s.pending
	s.pending = nil
	if err := sink.Evidence(ctx, rows); err != nil {
		slog.Warn("watch: evidence", "program", s.pid, "rows", len(rows), "err", err)
	}
}

// count records a call past the row budget until the next flush.
func (s *subject) count(nodeID string, f *chain.LogFrame, at time.Time) {
	if s.tallies == nil {
		s.tallies = map[string]*tally{}
	}
	t := s.tallies[nodeID]
	if t == nil {
		t = &tally{at: at}
		s.tallies[nodeID] = t
	}
	t.calls++
	if f.Failed {
		t.failed++
	}
	t.units += int64(f.Units)
}

// queue offers a detail read without waiting.
func (w *Watcher) queue(details chan<- detailReq, r detailReq) bool {
	select {
	case details <- r:
		return true
	default:
		return false
	}
}

// failureOf names the failure the logs report for a frame: the Anchor
// error code when logged, the IDL's name for a custom code, else the raw
// reason.
func failureOf(f *chain.LogFrame, idl *idlSpec) string {
	if f.ErrorCode > 0 && idl != nil {
		if def, ok := idl.Errors[f.ErrorCode]; ok {
			return def.Name
		}
	}
	if f.Error != "" {
		return f.Error
	}
	for _, c := range f.Children {
		if e := failureOf(c, nil); e != "" {
			return e
		}
	}
	return ""
}

// failedIn finds the innermost program that reported failure under f.
func failedIn(f *chain.LogFrame) string {
	for _, c := range f.Children {
		if c.Failed {
			if p := failedIn(c); p != "" {
				return p
			}
			return c.Program
		}
	}
	return ""
}

// onAccount records a write to a watched address: decoded state for a
// shared account, the new hash and authority for the program-data
// account, a new interface for the IDL account.
func (w *Watcher) onAccount(ctx context.Context, a core.WatchedAccount, subjects map[string]*subject, n chain.Notification, sink core.WatchSink) {
	an, err := chain.ParseAccountNotification(n.Result)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	sum := sha256.Sum256(an.Account.Data)
	summary := map[string]any{"slot": an.Slot, "live": true, "address": a.Address, "role": a.Role, "lamports": an.Account.Lamports, "owner": an.Account.Owner, "size": len(an.Account.Data), "sha256": hex.EncodeToString(sum[:])}
	ref := a.Address + "@" + strconv.FormatUint(an.Slot, 10)
	switch a.Role {
	case "program":
		pid := strings.TrimPrefix(a.NodeID, "p:")
		facts := map[string]string{}
		if d := an.Account.Data; len(d) >= 45 && binary.LittleEndian.Uint32(d[:4]) == 3 {
			deploySlot := binary.LittleEndian.Uint64(d[4:12])
			summary["deploy_slot"] = deploySlot
			facts["last_deploy_slot"] = strconv.FormatUint(deploySlot, 10)
			if d[12] == 1 {
				auth := chain.Encode(d[13:45])
				summary["authority_now"] = auth
				facts["authority"] = auth
				facts["immutable"] = "false"
			} else {
				facts["authority"] = ""
				facts["immutable"] = "true"
			}
			h := chain.HashBytecode(d[45:])
			summary["bytecode_now"] = h
			facts["bytecode_now"] = h
			facts["last_upgrade"] = now.Format(time.RFC3339)
		}
		if len(facts) > 0 {
			if err := sink.Facts(ctx, a.NodeID, facts); err != nil {
				slog.Warn("watch: program facts", "program", pid, "err", err)
			}
		}
		summary["kind"] = "program_data"
		ref = "upgrade@" + strconv.FormatUint(an.Slot, 10)
	case "idl":
		summary["kind"] = "idl"
		ref = "idl@" + strconv.FormatUint(an.Slot, 10)
	default:
		pid := ""
		if parts := strings.SplitN(a.NodeID, ":", 4); len(parts) == 4 {
			pid = parts[1]
		}
		if s := subjects[pid]; s != nil && s.idl != nil && an.Account.Exists {
			if st, typ := decodeState(chain.RawAccount{Exists: true, Executable: an.Account.Executable, Lamports: an.Account.Lamports, Owner: an.Account.Owner, Data: an.Account.Data}, s.idl); st != nil {
				if typ != "" {
					summary["type"] = typ
				}
				if len(st.Fields) > 0 {
					summary["fields"] = st.Fields
				}
			}
		}
	}
	if err := sink.Evidence(ctx, []core.SurfaceEvidence{{NodeID: a.NodeID, Kind: "write", Ref: ref, Actor: "system", At: &now, Summary: summary}}); err != nil {
		slog.Warn("watch: write", "address", a.Address, "err", err)
	}
}

// detailReq asks for one transaction in full.
type detailReq struct {
	subject *subject
	sig     string
	live    bool
}

// detailLoop reads queued transactions in full, paced, within the
// per-minute budget. A read that lands replaces the log-derived row.
func (w *Watcher) detailLoop(ctx context.Context, reqs <-chan detailReq, subjects map[string]*subject, sink core.WatchSink, budget core.WatchBudget) {
	tokens := budget.Details
	refill := time.NewTicker(time.Minute)
	defer refill.Stop()
	for {
		// Out of budget, the loop waits for the refill rather than draining
		// the queue: a full queue is how the stream knows to keep a call
		// on the program instead of waiting for a read that will not come.
		if tokens <= 0 {
			select {
			case <-ctx.Done():
				return
			case <-refill.C:
				tokens = budget.Details
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-refill.C:
			tokens = budget.Details
		case r := <-reqs:
			tokens--
			if err := w.readInFull(ctx, r.subject, r.sig, sink, r.live); err != nil {
				slog.Debug("watch: detail", "sig", r.sig, "err", err)
			}
			if !pace(ctx, core.SurfaceBudget{Pace: budget.Pace}) {
				return
			}
		}
	}
}

// readInFull reads one transaction and records what the sampler would
// have: rows with keys, signers, arguments and moves, plus the nodes and
// edges it reveals.
func (w *Watcher) readInFull(ctx context.Context, s *subject, sig string, sink core.WatchSink, live bool) error {
	raw, err := w.Chain.GetTransaction(ctx, sig)
	if err != nil {
		return err
	}
	tx, err := chain.ParseCalls(raw)
	if err != nil {
		return err
	}
	acc := &rowAcc{edges: map[string]*core.SurfaceEdge{}}
	acc.add(s.pid, s.specs, s.byDisc, s.idl, tx)
	for i := range acc.evidence {
		acc.evidence[i].Summary["live"] = true
		acc.evidence[i].Summary["source"] = "tx"
		if acc.evidence[i].At == nil {
			now := time.Now().UTC()
			acc.evidence[i].At = &now
		}
	}
	for _, n := range acc.nodes {
		if n.Kind == "instruction" {
			s.byName[foldName(n.Name)] = n.Name
		}
	}
	// Edges a live row already counted from the logs are not counted
	// twice; a backfilled read is the only sighting of its transaction.
	var edges []core.SurfaceEdge
	if !live {
		edges = edgeList(acc.edges)
	}
	if len(acc.nodes) > 0 || len(edges) > 0 {
		if err := sink.Extend(ctx, acc.nodes, edges); err != nil {
			return err
		}
	}
	if len(acc.evidence) == 0 {
		return nil
	}
	return sink.Evidence(ctx, acc.evidence)
}

// flushAll writes the cursors and live spans that changed, and the edges
// the logs added, on the ticker or at exit.
func (w *Watcher) flushAll(ctx context.Context, subjects map[string]*subject, edges *edgeAcc, sink core.WatchSink, force bool) {
	now := time.Now().UTC()
	for _, s := range subjects {
		s.flushRows(ctx, sink)
		for id, t := range s.tallies {
			if err := sink.Tally(ctx, id, t.at, t.calls, t.failed, t.units); err != nil {
				slog.Warn("watch: tally", "program", s.pid, "err", err)
			}
			delete(s.tallies, id)
		}
		if !s.dirty || (!force && now.Sub(s.flushed) < 2*time.Second) {
			continue
		}
		s.dirty = false
		s.flushed = now
		if err := sink.Cursor(ctx, s.pid, s.cursor); err != nil {
			slog.Warn("watch: cursor", "program", s.pid, "err", err)
		}
		if s.span != "" {
			at := now
			if s.cursor.At != nil {
				at = *s.cursor.At
			}
			if _, err := sink.Coverage(ctx, core.CoverageSpan{ID: s.span, Subject: s.pid, Kind: core.CoverageLive, ToSlot: s.spanTo, ToAt: &at}); err != nil {
				slog.Warn("watch: coverage", "program", s.pid, "err", err)
			}
		}
	}
	if nodes, list := edges.take(); len(nodes) > 0 || len(list) > 0 {
		if err := sink.Extend(ctx, nodes, list); err != nil {
			slog.Warn("watch: extend", "err", err)
		}
	}
}

// edgeAcc batches the edges and nodes the logs reveal between flushes, so
// the map document is rewritten every few seconds rather than per call.
type edgeAcc struct {
	nodes []core.SurfaceNode
	have  map[string]bool
	edges map[string]*core.SurfaceEdge
}

func (e *edgeAcc) add(from, to, program string) {
	key := from + ">" + to
	if x, ok := e.edges[key]; ok {
		x.Count++
		return
	}
	e.edges[key] = &core.SurfaceEdge{From: from, To: to, Kind: "cpi", Count: 1, Source: "traffic"}
	if strings.HasPrefix(from, "p:") {
		e.node(core.SurfaceNode{ID: from, Kind: "program", Name: KnownProgram(program), Source: "traffic", External: true, Facts: map[string]string{"program": program}})
	}
	if strings.HasPrefix(to, "p:") {
		e.node(core.SurfaceNode{ID: to, Kind: "program", Name: KnownProgram(program), Source: "traffic", External: true, Facts: map[string]string{"program": program}})
	}
}

func (e *edgeAcc) node(n core.SurfaceNode) {
	if e.have == nil {
		e.have = map[string]bool{}
	}
	if e.have[n.ID] {
		return
	}
	e.have[n.ID] = true
	e.nodes = append(e.nodes, n)
}

func (e *edgeAcc) take() ([]core.SurfaceNode, []core.SurfaceEdge) {
	nodes, list := e.nodes, edgeList(e.edges)
	e.nodes, e.have, e.edges = nil, nil, map[string]*core.SurfaceEdge{}
	return nodes, list
}

// foldName compares instruction names across snake and camel spellings.
func foldName(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "_", ""))
}
