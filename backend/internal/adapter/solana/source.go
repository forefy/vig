package solana

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/rustsrc"
)

// staticProgramIDs maps a CPI target name the source model can tell to the
// program it means, so a static edge lands on the same node traffic does.
var staticProgramIDs = map[string]string{
	"System":           "11111111111111111111111111111111",
	"Token":            "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA",
	"Token-2022":       "TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb",
	"Associated Token": "ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL",
	"Token Metadata":   "metaqbxxUerdq28cj1RbAWkYQm3ybzjb6a8bt518x1s",
	"Memo":             "MemoSq4gqABAXKb96qnH8TysNcWxMyWCqXgDLGmfcHr",
	"Stake":            "Stake11111111111111111111111111111111111111",
	"BPF Loader":       "BPFLoaderUpgradeab1e11111111111111111111111",
}

// staticNodes lays out what the source declares for one program: its
// instructions with their handler location and what the reachable code
// does, their accounts with every constraint, and one CPI edge per target.
// Node ids follow the IDL scheme so the two sources meet on the same node.
// peers maps the normalised names of the audited crates to their program
// ids, so a CPI the source makes into a sibling program lands on that
// program, and on the instruction when the source names it, instead of on
// an anonymous external node.
type peers map[string]string

func norm(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// peersOf names every audited program the model has a crate for.
func peersOf(model *rustsrc.Model, subjects []string) peers {
	out := peers{}
	if model == nil {
		return out
	}
	for _, pid := range subjects {
		c, ok := model.CrateFor(pid)
		if !ok {
			continue
		}
		for _, name := range []string{c.Name, c.Module} {
			if n := norm(name); len(n) >= 3 {
				out[n] = pid
			}
		}
	}
	return out
}

// resolve finds the audited program a target name means: equal after
// normalising, or one containing the other (LoomFiber, loom_fiber
// and fiber all meet). Well-known programs never resolve here.
func (p peers) resolve(target, self string) (string, bool) {
	if _, known := staticProgramIDs[target]; known || target == "" {
		return "", false
	}
	n := norm(target)
	best := ""
	for k, pid := range p {
		if pid == self && n != k {
			continue
		}
		if n == k {
			return pid, true
		}
		if len(k) >= 4 && len(n) >= 4 && (strings.Contains(n, k) || strings.Contains(k, n)) && pid != self {
			best = pid
		}
	}
	return best, best != ""
}

func staticNodes(pid string, c *rustsrc.Crate, ps peers) ([]core.SurfaceNode, []core.SurfaceEdge) {
	var nodes []core.SurfaceNode
	var edges []core.SurfaceEdge
	for _, ix := range c.Instructions {
		facts := map[string]string{"program": pid, "file": ix.File, "line": strconv.Itoa(ix.Line)}
		// The directory of the instruction's own module, when it has one.
		if ix.ContextFile != "" && ix.ContextFile != ix.File {
			if dir := path.Base(path.Dir(ix.ContextFile)); dir != "src" && dir != "." {
				facts["module"] = dir
			}
			facts["context_file"] = ix.ContextFile
		}
		if len(ix.Args) > 0 {
			var args []string
			for _, a := range ix.Args {
				args = append(args, a.Name+":"+a.Type)
			}
			facts["args"] = strings.Join(args, ", ")
		}
		if len(ix.Accounts) > 0 {
			facts["accounts"] = strconv.Itoa(len(ix.Accounts))
		}
		count := func(k string, n int) {
			if n > 0 {
				facts[k] = strconv.Itoa(n)
			}
		}
		count("reach", ix.Counts.Functions)
		count("raw", ix.Counts.Raw)
		count("cpi", ix.Counts.CPI)
		count("signed_cpi", ix.Counts.SignedCPI)
		count("pda", ix.Counts.PDA)
		count("token", ix.Counts.Token)
		count("lamports", ix.Counts.Lamports)
		count("unsafe", ix.Counts.Unsafe)
		count("realloc", ix.Counts.Realloc)
		count("serde", ix.Counts.Serde)
		count("unresolved", ix.Unresolved)
		if ix.Remaining {
			facts["remaining"] = "true"
		}
		id := ixNodeID(pid, ix.Name)
		nodes = append(nodes, core.SurfaceNode{ID: id, Parent: programNodeID(pid), Kind: "instruction", Name: ix.Name, Source: "static", Facts: facts})
		for _, a := range ix.Accounts {
			f := map[string]string{}
			set := func(k string, on bool) {
				if on {
					f[k] = "true"
				}
			}
			set("signer", a.Signer)
			set("writable", a.Writable)
			set("raw", a.Raw)
			set("optional", a.Optional)
			set("init", a.Init)
			set("close", a.Close)
			set("realloc", a.Realloc)
			set("zero_copy", a.ZeroCopy)
			set("bump", a.Bump)
			if a.Seeds != "" {
				f["seeds"] = a.Seeds
			}
			if len(a.HasOne) > 0 {
				f["has_one"] = strings.Join(a.HasOne, ", ")
			}
			if a.Owner != "" {
				f["owner"] = a.Owner
			}
			if a.Address != "" {
				f["address"] = a.Address
			}
			if a.Constraints > 0 {
				f["constraints"] = strconv.Itoa(a.Constraints)
			}
			if a.Wrapper != "" {
				t := a.Wrapper
				if a.Inner != "" {
					t += "<" + a.Inner + ">"
				}
				f["type"] = t
			}
			if a.Line > 0 {
				f["line"] = strconv.Itoa(a.Line)
			}
			nodes = append(nodes, core.SurfaceNode{ID: acctNodeID(pid, ix.Name, a.Name), Parent: id, Kind: "account", Name: a.Name, Source: "static", Facts: f})
		}
		// One edge per target, carrying what the code does through it.
		type edgeAcc struct {
			edge core.SurfaceEdge
			ops  []string
			n    int
		}
		byTarget := map[string]*edgeAcc{}
		var order []string
		// Sibling programs the source calls by instruction name.
		named := map[string]bool{}
		for _, st := range ix.Sites {
			if st.Op == "anchor_cpi" {
				if rp, ok := ps.resolve(st.Target, pid); ok {
					named[rp] = true
				}
			}
		}
		signedInto := map[string]bool{}
		for _, st := range ix.Sites {
			if st.Kind != "cpi" && st.Kind != "token" {
				continue
			}
			target := st.Target
			if target == "" {
				target = "dynamic"
			}
			key, to := target, ""
			var ext *core.SurfaceNode
			rp, sibling := ps.resolve(st.Target, pid)
			switch {
			case st.Op == "anchor_cpi":
				if !sibling {
					continue // its CpiContext already carries the call
				}
				key, to = "ix:"+rp+":"+st.Ix, ixNodeID(rp, st.Ix)
			case sibling && named[rp]:
				// The context of a call the source names by instruction: its
				// signing belongs on that instruction's edge.
				if st.Signed {
					signedInto[rp] = true
				}
				continue
			case sibling:
				key, to = "p:"+rp, programNodeID(rp)
			default:
				if known, ok := staticProgramIDs[target]; ok {
					to = programNodeID(known)
					ext = &core.SurfaceNode{ID: to, Kind: "program", Name: KnownProgram(known), Source: "static", External: true, Facts: map[string]string{"program": known}}
				} else {
					to = "p:static:" + strings.ToLower(strings.ReplaceAll(target, " ", "_"))
					ext = &core.SurfaceNode{ID: to, Kind: "program", Name: target, Source: "static", External: true}
				}
			}
			acc, ok := byTarget[key]
			if !ok {
				if ext != nil {
					nodes = append(nodes, *ext)
				}
				acc = &edgeAcc{edge: core.SurfaceEdge{From: id, To: to, Kind: "cpi", Source: "static", Facts: map[string]string{"file": st.File, "line": strconv.Itoa(st.Line)}}}
				byTarget[key] = acc
				order = append(order, key)
			}
			acc.n++
			if st.Signed {
				acc.edge.Facts["signed"] = "true"
			}
			if st.Kind == "token" {
				acc.ops = append(acc.ops, st.Op)
			}
		}
		for rp := range signedInto {
			for key, acc := range byTarget {
				if strings.HasPrefix(key, "ix:"+rp+":") {
					acc.edge.Facts["signed"] = "true"
				}
			}
		}
		for _, t := range order {
			acc := byTarget[t]
			acc.edge.Facts["sites"] = strconv.Itoa(acc.n)
			if len(acc.ops) > 0 {
				acc.edge.Facts["ops"] = strings.Join(uniq(acc.ops), ", ")
			}
			edges = append(edges, acc.edge)
		}
	}
	return nodes, edges
}

// summary maps the model's totals onto the chain-neutral shape.
func summary(m *rustsrc.Model) *core.SourceSummary {
	s := m.Summarize()
	return &core.SourceSummary{Programs: s.Programs, Instructions: s.Instructions, Accounts: s.Accounts, Raw: s.Raw, CPI: s.CPI, SignedCPI: s.SignedCPI, PDA: s.PDA, Token: s.Token, Unsafe: s.Unsafe, Code: s.Code}
}

// sourceIDLRow compares the instruction names the on-chain IDL declares
// with the handlers the source declares for the same program.
func sourceIDLRow(f Facts) core.PanelRow {
	switch {
	case f.SourceIx == nil:
		return core.PanelRow{Value: "pending", State: core.BadgePending}
	case !f.SourceFound:
		return core.PanelRow{Value: "not found", State: core.BadgeWarn}
	case !f.IDLPresent:
		return core.PanelRow{Value: "no IDL", Detail: fmt.Sprintf("%d in source", len(f.SourceIx)), State: core.BadgeNeutral}
	case !f.IDLRead:
		return core.PanelRow{Value: "pending", Detail: fmt.Sprintf("%d in source", len(f.SourceIx)), State: core.BadgePending}
	}
	inIDL, inSrc := map[string]bool{}, map[string]bool{}
	for _, n := range f.IDLIx {
		inIDL[n] = true
	}
	for _, n := range f.SourceIx {
		inSrc[n] = true
	}
	var onlyIDL, onlySrc []string
	for n := range inIDL {
		if !inSrc[n] {
			onlyIDL = append(onlyIDL, n)
		}
	}
	for n := range inSrc {
		if !inIDL[n] {
			onlySrc = append(onlySrc, n)
		}
	}
	if len(onlyIDL) == 0 && len(onlySrc) == 0 {
		return core.PanelRow{Value: "match", Detail: fmt.Sprintf("%d instructions", len(f.SourceIx)), State: core.BadgeOK}
	}
	sort.Strings(onlyIDL)
	sort.Strings(onlySrc)
	var parts []string
	if len(onlyIDL) > 0 {
		parts = append(parts, fmt.Sprintf("+%d idl", len(onlyIDL)))
	}
	if len(onlySrc) > 0 {
		parts = append(parts, fmt.Sprintf("+%d source", len(onlySrc)))
	}
	return core.PanelRow{Value: "mismatch", Detail: strings.Join(parts, " "), State: core.BadgeBad}
}

// instructionNames lists a crate's handler names, sorted.
func instructionNames(c *rustsrc.Crate) []string {
	out := make([]string, 0, len(c.Instructions))
	for _, ix := range c.Instructions {
		out = append(out, ix.Name)
	}
	sort.Strings(out)
	return out
}
