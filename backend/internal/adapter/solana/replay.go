package solana

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/forefy/vig/backend/internal/chain"
	"github.com/forefy/vig/backend/internal/core"
)

// Replay re-executes a finalized transaction against current state and
// records what a reviewer needs: outcome, compute units, the decoded error,
// the log tree, the CPI calls, and every account of the audited call with
// its state before and after, decoded through the IDL when it has one.
// Nothing here needs a key or a validator; a surfnet works the same way.
type Replay struct {
	Signature   string            `json:"replay"`
	Instruction string            `json:"instruction,omitempty"`
	Args        map[string]string `json:"args,omitempty"`
	Outcome     string            `json:"outcome"`
	Error       *ReplayError      `json:"error,omitempty"`
	Units       uint64            `json:"units"`
	Logs        []string          `json:"logs,omitempty"`
	Calls       []ReplayCall      `json:"calls,omitempty"`
	Accounts    []ReplayAccount   `json:"accounts,omitempty"`
	Moves       []TokenMove       `json:"moves,omitempty"`
	Via         string            `json:"via,omitempty"`
	Tag         string            `json:"tag,omitempty"`
	// What a what-if changed before the run, so the row can be read against
	// the unmodified replay.
	Patch      *ArgPatch `json:"patch,omitempty"`
	Overrides  []string  `json:"overrides,omitempty" doc:"Accounts rewritten on the surfnet"`
	TimeTravel string    `json:"time_travel,omitempty" doc:"Clock offset as given, e.g. 36h"`
}

// ArgPatch is one instruction argument rewritten before replay.
type ArgPatch struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// TokenMove is the net balance change of one mint across the watched token
// accounts, in base units, signed. Decimals is 0 when no watched account
// is the mint itself.
type TokenMove struct {
	Mint     string `json:"mint"`
	Decimals int    `json:"decimals"`
	Delta    string `json:"delta"`
}

type ReplayError struct {
	Code int    `json:"code,omitempty"`
	Name string `json:"name,omitempty"`
	Msg  string `json:"msg,omitempty"`
	Raw  string `json:"raw"`
}

type ReplayCall struct {
	Program string `json:"program"`
	Name    string `json:"name"`
	Depth   int    `json:"depth"`
}

type ReplayAccount struct {
	Name     string       `json:"name,omitempty"`
	Pubkey   string       `json:"pubkey"`
	Signer   bool         `json:"signer,omitempty"`
	Writable bool         `json:"writable,omitempty"`
	Type     string       `json:"type,omitempty" doc:"IDL account name, token, program, system"`
	Before   *ReplayState `json:"before,omitempty"`
	After    *ReplayState `json:"after,omitempty"`
	Changed  []string     `json:"changed,omitempty" doc:"Field names whose value differs"`
	PDA      string       `json:"pda,omitempty" doc:"ok or mismatch against the IDL's seeds; empty when not checkable"`
}

type ReplayState struct {
	Lamports uint64            `json:"lamports"`
	Owner    string            `json:"owner,omitempty"`
	Size     int               `json:"size"`
	Fields   map[string]string `json:"fields,omitempty"`
}

const maxReplayLogs = 60

// maxWatched is what the public simulateTransaction accepts in accounts.addresses.
const maxWatched = 27

// watched picks the account indices to fetch state for: writable first, in
// call order, then the rest, up to limit. Repeated pubkeys are fetched once.
func watched(accounts []chain.CallAccount, limit int) []int {
	var out []int
	seen := map[string]bool{}
	pick := func(writable bool) {
		for i, a := range accounts {
			if a.Writable != writable || seen[a.Pubkey] || len(out) >= limit {
				continue
			}
			seen[a.Pubkey] = true
			out = append(out, i)
		}
	}
	pick(true)
	pick(false)
	return out
}

// Profile replays one signature for a program as it was sent.
func Profile(ctx context.Context, c *chain.Client, pid string, idl *idlSpec, sig, instruction string) (*Replay, error) {
	return profilePatched(ctx, c, pid, idl, sig, profileOpts{instruction: instruction})
}

// profilePatched replays a signature, optionally with one scalar argument
// of the audited call rewritten in the wire bytes before simulation. When
// the transaction calls the program more than once, instruction picks
// which call; the first one otherwise.
func profilePatched(ctx context.Context, c *chain.Client, pid string, idl *idlSpec, sig string, o profileOpts) (*Replay, error) {
	argName, argValue, instruction, known := o.argName, o.argValue, o.instruction, o.known
	parsedRaw, err := c.GetTransaction(ctx, sig)
	if err != nil {
		return nil, err
	}
	tx, err := chain.ParseCalls(parsedRaw)
	if err != nil {
		return nil, err
	}
	var call *chain.Call
	via := ""
	// A call matches by the name the map gave it: the IDL's, a native
	// program's, the discriminator an Anchor handler name implies (a
	// program with no IDL, mapped from source or bytecode), or the hashed
	// name traffic alone produces.
	wanted := func(c *chain.Call) bool {
		if instruction == "" || callName(pid, c, idl) == instruction {
			return true
		}
		if len(c.Data) >= 8 {
			var d [8]byte
			copy(d[:], c.Data[:8])
			return d == anchorDiscriminator("global:"+instruction) || discriminatorName(c.Data[:8]) == instruction
		}
		return false
	}
	for i := range tx.Calls {
		if tx.Calls[i].ProgramID == pid && wanted(&tx.Calls[i]) {
			call = &tx.Calls[i]
			break
		}
		for j := range tx.Calls[i].InnerCalls {
			if tx.Calls[i].InnerCalls[j].ProgramID == pid && wanted(&tx.Calls[i].InnerCalls[j]) {
				call = &tx.Calls[i].InnerCalls[j]
				via = tx.Calls[i].ProgramID
				break
			}
		}
		if call != nil {
			break
		}
	}
	if call == nil {
		return nil, fmt.Errorf("transaction does not call %s %s", pid, instruction)
	}
	wire, err := c.TransactionBase64(ctx, sig)
	if err != nil {
		return nil, err
	}
	if argName != "" {
		if idl == nil {
			return nil, fmt.Errorf("argument patch needs the program's IDL")
		}
		wire, err = patchArg(wire, call.Data, idl, argName, argValue)
		if err != nil {
			return nil, err
		}
	}
	r := &Replay{Signature: sig, Via: via, Outcome: "ok", Instruction: instruction, Overrides: o.overrides, TimeTravel: o.timeTravel}
	if argName != "" {
		r.Patch = &ArgPatch{Name: argName, Value: argValue}
	}
	var spec *instructionSpec
	if idl != nil && len(call.Data) >= 8 {
		var d [8]byte
		copy(d[:], call.Data[:8])
		for i := range idl.Instructions {
			if idl.Instructions[i].Discriminator == d {
				spec = &idl.Instructions[i]
				break
			}
		}
	}
	if spec != nil {
		r.Instruction = spec.Name
		r.Args = decodeArgs(call.Data[8:], spec.Args, idl)
	} else if native, ok := nativeSpec(pid); ok {
		if ns, ok := native.resolveCall(*call); ok {
			spec = &ns
			r.Instruction = ns.Name
			// A pre-parsed call carries named accounts, not an ordered list.
			if len(call.Accounts) == 0 {
				for _, a := range ns.Accounts {
					if pk := call.ParsedAccount(a.Name); pk != "" {
						call.Accounts = append(call.Accounts, chain.CallAccount{Pubkey: pk, Signer: a.Signer, Writable: a.Writable})
					}
				}
			}
			r.Args = parsedArgs(call.ParsedInfo)
		}
	}
	if r.Instruction == "" && len(call.Data) >= 8 {
		r.Instruction = knownName(call.Data[:8], known)
	}
	// The public endpoint returns state for at most maxWatched accounts per
	// simulation, so the writable ones are watched first: they are the
	// ones that change.
	watch := watched(call.Accounts, maxWatched)
	keys := make([]string, len(watch))
	pos := map[int]int{}
	for i, idx := range watch {
		keys[i] = call.Accounts[idx].Pubkey
		pos[idx] = i
	}
	var before []chain.RawAccount
	if len(keys) > 0 {
		if before, err = c.GetMultipleAccounts(ctx, keys); err != nil {
			return nil, err
		}
	}
	sim, err := c.Simulate(ctx, wire, keys)
	if err != nil {
		return nil, err
	}
	r.Units = sim.UnitsConsumed
	if sim.Err != nil {
		r.Error = decodeError(sim.Err, idl)
		r.Outcome = r.Error.Raw
	}
	if len(sim.Logs) > maxReplayLogs {
		sim.Logs = append(sim.Logs[:maxReplayLogs/2], sim.Logs[len(sim.Logs)-maxReplayLogs/2:]...)
	}
	r.Logs = sim.Logs
	r.Calls = callsFromLogs(sim.Logs)
	for i, a := range call.Accounts {
		acc := ReplayAccount{Pubkey: a.Pubkey, Signer: a.Signer, Writable: a.Writable}
		if spec != nil && i < len(spec.Accounts) {
			acc.Name = spec.Accounts[i].Name
		}
		if p, ok := pos[i]; ok {
			if p < len(before) {
				acc.Before, acc.Type = decodeState(before[p], idl)
			}
			// A failed replay commits nothing; the post-state it reports is
			// empty, not a change.
			if sim.Err == nil && p < len(sim.Accounts) {
				acc.After, _ = decodeState(sim.Accounts[p], idl)
				acc.Changed = changed(acc.Before, acc.After)
			}
		}
		if spec != nil && i < len(spec.Accounts) {
			acc.PDA = pdaCheck(spec, i, *call, pid)
		}
		r.Accounts = append(r.Accounts, acc)
	}
	r.Moves = tokenMoves(r.Accounts)
	return r, nil
}

// tokenMoves nets the watched token accounts' balance changes per mint.
// Decimals come from the mint when it is among the watched accounts.
func tokenMoves(accounts []ReplayAccount) []TokenMove {
	decimals := map[string]int{}
	for _, a := range accounts {
		if a.Type == "mint" && a.Before != nil {
			if d, err := strconv.Atoi(a.Before.Fields["decimals"]); err == nil {
				decimals[a.Pubkey] = d
			}
		}
	}
	sums := map[string]*big.Int{}
	seen := map[string]bool{}
	for _, a := range accounts {
		if a.Type != "token" || seen[a.Pubkey] || a.Before == nil || a.After == nil {
			continue
		}
		seen[a.Pubkey] = true
		mint := a.Before.Fields["mint"]
		if mint == "" {
			mint = a.After.Fields["mint"]
		}
		before, ok1 := new(big.Int).SetString(a.Before.Fields["amount"], 10)
		after, ok2 := new(big.Int).SetString(a.After.Fields["amount"], 10)
		if mint == "" || !ok1 || !ok2 {
			continue
		}
		delta := new(big.Int).Sub(after, before)
		if delta.Sign() == 0 {
			continue
		}
		if sums[mint] == nil {
			sums[mint] = new(big.Int)
		}
		sums[mint].Add(sums[mint], delta)
	}
	var out []TokenMove
	for mint, d := range sums {
		if d.Sign() == 0 {
			continue
		}
		out = append(out, TokenMove{Mint: mint, Decimals: decimals[mint], Delta: d.String()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mint < out[j].Mint })
	return out
}

// pdaCheck derives the address the IDL's seeds imply for the call's i-th
// account and compares it with what was passed. Account seeds resolve by
// name from the call's accounts, argument seeds from the call's raw bytes.
// An unresolvable seed means no verdict rather than a wrong one.
func pdaCheck(spec *instructionSpec, i int, call chain.Call, pid string) string {
	acc := spec.Accounts[i]
	if len(acc.PDASeeds) == 0 || i >= len(call.Accounts) {
		return ""
	}
	var seeds [][]byte
	for _, sd := range acc.PDASeeds {
		b, ok := resolveSeed(sd, spec, call)
		if !ok {
			return ""
		}
		seeds = append(seeds, b)
	}
	prog, err := chain.Decode(pid)
	if acc.PDAProgram != nil {
		var ok bool
		if prog, ok = resolveSeed(*acc.PDAProgram, spec, call); !ok || len(prog) != 32 {
			return ""
		}
	} else if err != nil {
		return ""
	}
	addr, _, err := chain.FindProgramAddress(seeds, prog)
	if err != nil {
		return ""
	}
	if chain.Encode(addr) == call.Accounts[i].Pubkey {
		return "ok"
	}
	return "mismatch"
}

// resolveSeed turns one seed into bytes: the literal, the named account's
// key, or the named argument's wire bytes. A dotted account path also
// matches by its last segment, which is how nested groups are named.
func resolveSeed(sd seedSpec, spec *instructionSpec, call chain.Call) ([]byte, bool) {
	switch sd.Kind {
	case "const":
		return sd.Value, len(sd.Value) > 0
	case "account":
		want := snake(sd.Path)
		for _, cand := range []string{want, want[strings.LastIndex(want, ".")+1:]} {
			for j, a := range spec.Accounts {
				if a.Name == cand && j < len(call.Accounts) {
					b, err := chain.Decode(call.Accounts[j].Pubkey)
					return b, err == nil
				}
			}
		}
		return nil, false
	case "arg":
		if len(call.Data) < 8 {
			return nil, false
		}
		return argSeedBytes(call.Data[8:], spec.Args, snake(sd.Path))
	}
	return nil, false
}

// argSeedBytes locates one argument in the call's bytes by walking the
// fixed-size fields before it; strings and byte vectors are sized by their
// length prefix and contribute their content. Anything else stops the walk.
func argSeedBytes(data []byte, args []fieldSpec, name string) ([]byte, bool) {
	off := 0
	for _, f := range args {
		var width, skip int
		if w, ok := scalarWidth(f.Type); ok {
			width = w
		} else if isBytesType(f.Type) {
			if len(data) < off+4 {
				return nil, false
			}
			skip = 4
			width = int(binary.LittleEndian.Uint32(data[off:]))
		} else {
			return nil, false
		}
		if len(data) < off+skip+width {
			return nil, false
		}
		if snake(f.Name) == name {
			return data[off+skip : off+skip+width], true
		}
		off += skip + width
	}
	return nil, false
}

// isBytesType reports a string or vec<u8>/bytes argument.
func isBytesType(typ json.RawMessage) bool {
	var s string
	if json.Unmarshal(typ, &s) == nil {
		return s == "string" || s == "bytes"
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(typ, &m) != nil {
		return false
	}
	var inner string
	return json.Unmarshal(m["vec"], &inner) == nil && inner == "u8"
}

// Evidence wraps a replay as a surface evidence row on its instruction.
func (r *Replay) Evidence(pid string, actor string, at *time.Time) core.SurfaceEvidence {
	b, _ := json.Marshal(r)
	var summary map[string]any
	_ = json.Unmarshal(b, &summary)
	name := r.Instruction
	if name == "" {
		name = "ix_unknown"
	}
	// The ref keys the row: a tagged replay by its tag, an untagged one by
	// when it ran, so the same signature replayed again later is a second
	// row (the drift) rather than a duplicate.
	ref := "replay:" + r.Signature + ":" + r.Tag
	if r.Tag == "" && at != nil {
		ref += at.UTC().Format(time.RFC3339)
	}
	return core.SurfaceEvidence{NodeID: ixNodeID(pid, name), Kind: "replay", Ref: ref, Actor: actor, At: at, Summary: summary}
}

// decodeError turns a simulation error into the IDL's name and message
// when it is a custom program error.
func decodeError(e any, idl *idlSpec) *ReplayError {
	raw, _ := json.Marshal(e)
	out := &ReplayError{Raw: string(raw)}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return out
	}
	ie, ok := m["InstructionError"].([]any)
	if !ok || len(ie) != 2 {
		return out
	}
	inner, ok := ie[1].(map[string]any)
	if !ok {
		if s, ok := ie[1].(string); ok {
			out.Raw = s
		}
		return out
	}
	if code, ok := inner["Custom"].(float64); ok {
		out.Code = int(code)
		out.Raw = "custom " + strconv.Itoa(int(code))
		if idl != nil {
			if def, ok := idl.Errors[int(code)]; ok {
				out.Name = def.Name
				out.Msg = def.Msg
				out.Raw = def.Name
			}
		}
		if out.Name == "" {
			if name, ok := anchorErrors[int(code)]; ok {
				out.Name = name
				out.Raw = name
			}
		}
	}
	return out
}

// callsFromLogs rebuilds the call tree from "Program X invoke [n]" lines,
// naming Anchor instructions from the "Instruction: Name" log that follows.
func callsFromLogs(logs []string) []ReplayCall {
	var out []ReplayCall
	for i, l := range logs {
		if !strings.HasPrefix(l, "Program ") || !strings.Contains(l, " invoke [") {
			continue
		}
		parts := strings.Fields(l)
		if len(parts) < 4 {
			continue
		}
		depth, _ := strconv.Atoi(strings.Trim(parts[3], "[]"))
		call := ReplayCall{Program: parts[1], Depth: depth}
		for j := i + 1; j < len(logs) && j < i+4; j++ {
			if k := strings.Index(logs[j], "Instruction: "); k >= 0 {
				call.Name = snake(strings.TrimSpace(logs[j][k+len("Instruction: "):]))
				break
			}
		}
		out = append(out, call)
	}
	return out
}

// decodeState decodes an account through the IDL when its discriminator is
// known, as a token account when the Token program owns it, else as size
// and lamports only.
func decodeState(a chain.RawAccount, idl *idlSpec) (*ReplayState, string) {
	if !a.Exists {
		return nil, ""
	}
	st := &ReplayState{Lamports: a.Lamports, Owner: a.Owner, Size: len(a.Data)}
	typ := ""
	switch {
	case a.Owner == chain.TokenProgram || a.Owner == chain.Token2022Program:
		switch {
		case len(a.Data) >= 165:
			typ = "token"
			st.Fields = map[string]string{
				"mint":   chain.Encode(a.Data[0:32]),
				"owner":  chain.Encode(a.Data[32:64]),
				"amount": strconv.FormatUint(binary.LittleEndian.Uint64(a.Data[64:72]), 10),
			}
		case len(a.Data) >= 82:
			typ = "mint"
			st.Fields = map[string]string{
				"supply":   strconv.FormatUint(binary.LittleEndian.Uint64(a.Data[36:44]), 10),
				"decimals": strconv.Itoa(int(a.Data[44])),
			}
			if binary.LittleEndian.Uint32(a.Data[0:4]) == 1 {
				st.Fields["mint_authority"] = chain.Encode(a.Data[4:36])
			}
		}
	case a.Owner == "11111111111111111111111111111111":
		typ = "system"
	default:
		if idl != nil && len(a.Data) >= 8 {
			var d [8]byte
			copy(d[:], a.Data[:8])
			if layout, ok := idl.Accounts[d]; ok {
				typ = layout.Name
				st.Fields = decodeFields(a.Data[8:], layout.Fields, idl, "")
			}
		}
	}
	return st, typ
}

// decodeArgs decodes instruction arguments the same way.
func decodeArgs(data []byte, args []fieldSpec, idl *idlSpec) map[string]string {
	return decodeFields(data, args, idl, "")
}

// decodeFields reads scalar fields in order and stops at the first field
// it cannot size, so what it returns is exact and what it skips is
// absent rather than wrong.
func decodeFields(data []byte, fields []fieldSpec, idl *idlSpec, prefix string) map[string]string {
	out := map[string]string{}
	off := 0
	for _, f := range fields {
		v, n, ok := decodeScalar(data[off:], f.Type, idl, prefix+snake(f.Name), out)
		if !ok {
			break
		}
		if v != "" {
			out[prefix+snake(f.Name)] = v
		}
		off += n
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func decodeScalar(b []byte, typ json.RawMessage, idl *idlSpec, name string, into map[string]string) (string, int, bool) {
	var s string
	if json.Unmarshal(typ, &s) == nil {
		switch s {
		case "bool":
			if len(b) < 1 {
				return "", 0, false
			}
			return strconv.FormatBool(b[0] != 0), 1, true
		case "u8", "i8":
			if len(b) < 1 {
				return "", 0, false
			}
			if s == "i8" {
				return strconv.Itoa(int(int8(b[0]))), 1, true
			}
			return strconv.Itoa(int(b[0])), 1, true
		case "u16", "i16":
			if len(b) < 2 {
				return "", 0, false
			}
			v := binary.LittleEndian.Uint16(b)
			if s == "i16" {
				return strconv.Itoa(int(int16(v))), 2, true
			}
			return strconv.Itoa(int(v)), 2, true
		case "u32", "i32":
			if len(b) < 4 {
				return "", 0, false
			}
			v := binary.LittleEndian.Uint32(b)
			if s == "i32" {
				return strconv.Itoa(int(int32(v))), 4, true
			}
			return strconv.FormatUint(uint64(v), 10), 4, true
		case "u64", "i64":
			if len(b) < 8 {
				return "", 0, false
			}
			v := binary.LittleEndian.Uint64(b)
			if s == "i64" {
				return strconv.FormatInt(int64(v), 10), 8, true
			}
			return strconv.FormatUint(v, 10), 8, true
		case "u128", "i128":
			if len(b) < 16 {
				return "", 0, false
			}
			le := make([]byte, 16)
			for i := 0; i < 16; i++ {
				le[i] = b[15-i]
			}
			v := new(big.Int).SetBytes(le)
			if s == "i128" && b[15]&0x80 != 0 {
				v.Sub(v, new(big.Int).Lsh(big.NewInt(1), 128))
			}
			return v.String(), 16, true
		case "pubkey", "publicKey":
			if len(b) < 32 {
				return "", 0, false
			}
			return chain.Encode(b[:32]), 32, true
		case "string", "bytes":
			if len(b) < 4 {
				return "", 0, false
			}
			n := int(binary.LittleEndian.Uint32(b))
			if n < 0 || len(b) < 4+n {
				return "", 0, false
			}
			v := b[4 : 4+n]
			if s == "string" {
				return clip(string(v), 48), 4 + n, true
			}
			return clip("0x"+hexString(v), 48), 4 + n, true
		}
		return "", 0, false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(typ, &m) != nil {
		return "", 0, false
	}
	if inner, ok := m["option"]; ok {
		if len(b) < 1 {
			return "", 0, false
		}
		if b[0] == 0 {
			return "none", 1, true
		}
		v, n, ok := decodeScalar(b[1:], inner, idl, name, into)
		if !ok {
			return "", 0, false
		}
		return v, 1 + n, true
	}
	if inner, ok := m["vec"]; ok {
		if len(b) < 4 {
			return "", 0, false
		}
		count := int(binary.LittleEndian.Uint32(b))
		return decodeSeq(b[4:], inner, count, 4, idl, name, into)
	}
	if arr, ok := m["array"]; ok {
		var parts []json.RawMessage
		var count int
		if json.Unmarshal(arr, &parts) != nil || len(parts) != 2 || json.Unmarshal(parts[1], &count) != nil {
			return "", 0, false
		}
		return decodeSeq(b, parts[0], count, 0, idl, name, into)
	}
	if d, ok := m["defined"]; ok && idl != nil {
		var dn string
		if json.Unmarshal(d, &dn) != nil {
			var obj struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(d, &obj) == nil {
				dn = obj.Name
			}
		}
		if vs := enumVariants(idl.types[dn]); vs != nil {
			if len(b) < 1 || int(b[0]) >= len(vs) {
				return "", 0, false
			}
			v := vs[b[0]]
			off := 1
			for _, f := range v.Fields {
				fv, n, ok := decodeScalar(b[off:], f.Type, idl, name+"."+snake(f.Name), into)
				if !ok {
					return "", 0, false
				}
				if fv != "" {
					into[name+"."+snake(f.Name)] = fv
				}
				off += n
			}
			return v.Name, off, true
		}
		fields := structFields(idl.types[dn])
		if fields == nil {
			return "", 0, false
		}
		off := 0
		for _, f := range fields {
			v, n, ok := decodeScalar(b[off:], f.Type, idl, name+"."+snake(f.Name), into)
			if !ok {
				return "", 0, false
			}
			if v != "" {
				into[name+"."+snake(f.Name)] = v
			}
			off += n
		}
		return "", off, true
	}
	return "", 0, false
}

// decodeSeq reads count elements of one type. Scalars join into one
// bracketed value; structured elements land as name[i].field, the first
// few only.
func decodeSeq(b []byte, elem json.RawMessage, count, prefix int, idl *idlSpec, name string, into map[string]string) (string, int, bool) {
	if count < 0 || count > 1<<16 {
		return "", 0, false
	}
	off := 0
	var vals []string
	for i := 0; i < count; i++ {
		sub := map[string]string{}
		v, n, ok := decodeScalar(b[off:], elem, idl, name+"["+strconv.Itoa(i)+"]", sub)
		if !ok {
			return "", 0, false
		}
		off += n
		if v != "" {
			vals = append(vals, v)
		} else if i < 4 {
			for k, x := range sub {
				into[k] = x
			}
		}
	}
	if count == 0 {
		return "[]", prefix + off, true
	}
	if len(vals) == 0 {
		return "", prefix + off, true
	}
	if len(vals) > 6 {
		vals = append(vals[:6], "…"+strconv.Itoa(len(vals)-6)+" more")
	}
	return "[" + strings.Join(vals, ", ") + "]", prefix + off, true
}

type idlVariant struct {
	Name   string      `json:"name"`
	Fields []fieldSpec `json:"fields"`
}

// enumVariants returns a type's variants, or nil when it is not an enum.
func enumVariants(typ json.RawMessage) []idlVariant {
	var t struct {
		Kind     string       `json:"kind"`
		Variants []idlVariant `json:"variants"`
	}
	if json.Unmarshal(typ, &t) != nil || t.Kind != "enum" {
		return nil
	}
	return t.Variants
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// changed lists the fields, lamports or size that differ.
func changed(before, after *ReplayState) []string {
	// A simulation reports an untouched, empty address as an account with
	// nothing in it; that is the same absence the read reported.
	empty := func(s *ReplayState) bool { return s == nil || (s.Lamports == 0 && s.Size == 0) }
	if empty(before) || empty(after) {
		if empty(before) && empty(after) {
			return nil
		}
		return []string{"exists"}
	}
	var out []string
	if before.Lamports != after.Lamports {
		out = append(out, "lamports")
	}
	if before.Size != after.Size {
		out = append(out, "size")
	}
	for k, v := range before.Fields {
		if after.Fields[k] != v {
			out = append(out, k)
		}
	}
	return out
}

// ProfileOption changes how a replay runs.
type ProfileOption func(*profileOpts)

type profileOpts struct {
	argName, argValue string
	instruction       string
	known             map[string]string
	overrides         []string
	timeTravel        string
}

// WithKnown names a call the way the map already does when the program
// has no IDL: instruction name to hex discriminator, or to "" for a name
// read from source, whose discriminator is the Anchor global namespace.
func WithKnown(names map[string]string) ProfileOption {
	return func(o *profileOpts) { o.known = names }
}

// knownName resolves a discriminator against the map's names, and falls
// back to the hashed name traffic alone produces, so a replay always lands
// on the node the map would have given the same call.
func knownName(d []byte, known map[string]string) string {
	var disc [8]byte
	copy(disc[:], d[:8])
	hexd := hex.EncodeToString(disc[:])
	for name, want := range known {
		if want == hexd || (want == "" && anchorDiscriminator("global:"+name) == disc) {
			return name
		}
	}
	return discriminatorName(d[:8])
}

// WithInstruction picks which of the program's calls in the transaction to
// replay, by instruction name; the first one otherwise.
func WithInstruction(name string) ProfileOption {
	return func(o *profileOpts) { o.instruction = name }
}

// WithArg patches one scalar argument of the audited call before replay.
func WithArg(name, value string) ProfileOption {
	return func(o *profileOpts) { o.argName, o.argValue = name, value }
}

// WithOverrides records the accounts a what-if rewrote on the surfnet
// before this replay; the rewrite itself happens in the CLI.
func WithOverrides(keys []string) ProfileOption {
	return func(o *profileOpts) { o.overrides = keys }
}

// WithTimeTravel records the clock offset a what-if applied, as given.
func WithTimeTravel(d string) ProfileOption {
	return func(o *profileOpts) { o.timeTravel = d }
}

// ProfileWith fetches the program's IDL itself and replays with options.
func ProfileWith(ctx context.Context, c *chain.Client, pid, sig string, opts ...ProfileOption) (*Replay, error) {
	var o profileOpts
	for _, opt := range opts {
		opt(&o)
	}
	var idl *idlSpec
	if raw, err := c.IDL(ctx, pid); err == nil {
		idl, _ = parseIDL(raw)
	}
	return profilePatched(ctx, c, pid, idl, sig, o)
}

// patchArg rewrites one scalar argument inside the transaction bytes. The
// call's data is located verbatim in the wire message, the argument's
// offset comes from the IDL's fixed-size fields before it, and the value is
// encoded in the argument's own width. Anything variable-size before the
// argument is refused rather than guessed.
func patchArg(wireB64 string, data []byte, idl *idlSpec, name, value string) (string, error) {
	wire, err := base64.StdEncoding.DecodeString(wireB64)
	if err != nil {
		return "", err
	}
	if len(data) < 8 {
		return "", fmt.Errorf("call has no discriminator")
	}
	var d [8]byte
	copy(d[:], data[:8])
	var spec *instructionSpec
	for i := range idl.Instructions {
		if idl.Instructions[i].Discriminator == d {
			spec = &idl.Instructions[i]
		}
	}
	if spec == nil {
		return "", fmt.Errorf("instruction not in the IDL")
	}
	off := 8
	for _, f := range spec.Args {
		width, ok := scalarWidth(f.Type)
		if !ok {
			return "", fmt.Errorf("argument %s sits after a variable-size field", name)
		}
		if snake(f.Name) == snake(name) {
			enc, err := encodeScalar(f.Type, value)
			if err != nil {
				return "", err
			}
			at := bytes.Index(wire, data)
			if at < 0 {
				return "", fmt.Errorf("instruction bytes not found in the transaction")
			}
			copy(wire[at+off:], enc)
			return base64.StdEncoding.EncodeToString(wire), nil
		}
		off += width
	}
	return "", fmt.Errorf("argument %s not in %s", name, spec.Name)
}

func scalarWidth(typ json.RawMessage) (int, bool) {
	var s string
	if json.Unmarshal(typ, &s) != nil {
		return 0, false
	}
	switch s {
	case "bool", "u8", "i8":
		return 1, true
	case "u16", "i16":
		return 2, true
	case "u32", "i32", "f32":
		return 4, true
	case "u64", "i64", "f64":
		return 8, true
	case "u128", "i128":
		return 16, true
	case "pubkey", "publicKey":
		return 32, true
	}
	return 0, false
}

func encodeScalar(typ json.RawMessage, value string) ([]byte, error) {
	var s string
	_ = json.Unmarshal(typ, &s)
	switch s {
	case "bool":
		if value == "true" {
			return []byte{1}, nil
		}
		return []byte{0}, nil
	case "pubkey", "publicKey":
		return chain.Decode(value)
	case "u8", "u16", "u32", "u64", "i8", "i16", "i32", "i64":
		n, ok := new(big.Int).SetString(value, 10)
		if !ok {
			return nil, fmt.Errorf("%s is not an integer", value)
		}
		width, _ := scalarWidth(typ)
		if n.Sign() < 0 {
			n.Add(n, new(big.Int).Lsh(big.NewInt(1), uint(width*8)))
		}
		out := make([]byte, width)
		b := n.Bytes()
		if len(b) > width {
			return nil, fmt.Errorf("%s does not fit %s", value, s)
		}
		for i := range b {
			out[i] = b[len(b)-1-i]
		}
		return out, nil
	}
	return nil, fmt.Errorf("cannot encode %s", s)
}

// callName names a call the way the map does: IDL discriminator, native
// table, else the discriminator itself.
func callName(pid string, c *chain.Call, idl *idlSpec) string {
	if native, ok := nativeSpec(pid); ok {
		if spec, ok := native.resolveCall(*c); ok {
			return spec.Name
		}
	}
	if idl != nil && len(c.Data) >= 8 {
		var d [8]byte
		copy(d[:], c.Data[:8])
		for i := range idl.Instructions {
			if idl.Instructions[i].Discriminator == d {
				return idl.Instructions[i].Name
			}
		}
		return discriminatorName(c.Data[:8])
	}
	return ""
}

// parsedArgs reads a pre-parsed call's arguments: everything in its info
// that is not an address. {amount, decimals, uiAmountString} objects
// reduce to the raw amount.
func parsedArgs(info map[string]any) map[string]string {
	out := map[string]string{}
	for k, v := range info {
		var text string
		switch x := v.(type) {
		case string:
			if chain.ValidPubkey(x) {
				continue
			}
			text = x
		case float64:
			text = strconv.FormatFloat(x, 'f', -1, 64)
		case bool:
			text = strconv.FormatBool(x)
		case map[string]any:
			amt, ok := x["amount"].(string)
			if !ok {
				continue
			}
			text = amt
		default:
			continue
		}
		out[snake(k)] = text
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// callArgs decodes a sampled call's arguments the same way a replay does,
// from the IDL for Anchor programs and from the parsed form for native ones.
func callArgs(pid string, c chain.Call, idl *idlSpec) map[string]string {
	if c.ParsedInfo != nil {
		return parsedArgs(c.ParsedInfo)
	}
	if idl == nil || len(c.Data) < 8 {
		return nil
	}
	var d [8]byte
	copy(d[:], c.Data[:8])
	for i := range idl.Instructions {
		if idl.Instructions[i].Discriminator == d {
			return decodeArgs(c.Data[8:], idl.Instructions[i].Args, idl)
		}
	}
	return nil
}
