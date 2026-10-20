package rustsrc

import (
	"path"
	"sort"
	"strings"
)

func key(f *Function) string { return f.File + ":" + f.Name }

// index groups functions by name for resolution.
func index(fns []*Function) map[string][]*Function {
	out := map[string][]*Function{}
	for _, f := range fns {
		out[f.Name] = append(out[f.Name], f)
	}
	return out
}

// resolve picks the function a call means. One candidate by name is a
// match. Several are narrowed by the call's qualifier: an impl type
// (Self::, Type::) or a module name that the candidate's file path
// carries. What is still ambiguous is reported as unresolved; a name no
// function has is external and ignored.
func resolve(c Call, from *Function, byName map[string][]*Function, ctxType string) (*Function, bool, bool) {
	cands := byName[c.Name]
	// ctx.accounts.into() builds a CpiContext through a From impl.
	if c.Name == "into" && c.Method && strings.HasSuffix(c.Path, "accounts") {
		cands = nil
		for _, f := range byName["from"] {
			if f.Crate == from.Crate {
				cands = append(cands, f)
			}
		}
	}
	if len(cands) == 0 {
		return nil, false, false
	}
	var narrowed []*Function
	q := c.Path
	if i := strings.LastIndex(q, "::"); i >= 0 {
		q = q[i+2:]
	}
	switch {
	case q == "Self" && from.Impl != "", c.Method && (c.Path == "self" || strings.HasPrefix(c.Path, "self.")) && from.Impl != "":
		// Self::f() and self.f() inside an impl mean that impl's f.
		for _, f := range cands {
			if f.Impl == from.Impl {
				narrowed = append(narrowed, f)
			}
		}
	case c.Method && ctxType != "" && (c.Path == "ctx.accounts" || strings.HasSuffix(c.Path, ".accounts")):
		// ctx.accounts.f() in a handler means the context struct's f.
		for _, f := range cands {
			if f.Impl == ctxType {
				narrowed = append(narrowed, f)
			}
		}
	case q != "" && q[0] >= 'A' && q[0] <= 'Z':
		for _, f := range cands {
			if f.Impl == q {
				narrowed = append(narrowed, f)
			}
		}
	case q != "" && !c.Method:
		for _, f := range cands {
			base := strings.TrimSuffix(path.Base(f.File), ".rs")
			if base == q || strings.Contains(f.File, "/"+q+"/") {
				narrowed = append(narrowed, f)
			}
		}
		// token::transfer with no local module named token is another
		// crate's function, not the one defined here.
		if len(narrowed) == 0 && q != "crate" && q != "super" && q != "self" {
			return nil, false, false
		}
	}
	if len(narrowed) == 0 && len(cands) == 1 {
		return cands[0], true, false
	}
	if len(narrowed) == 0 {
		// Prefer the same crate, then the same file.
		for _, f := range cands {
			if f.Crate == from.Crate {
				narrowed = append(narrowed, f)
			}
		}
		if len(narrowed) > 1 {
			var same []*Function
			for _, f := range narrowed {
				if f.File == from.File {
					same = append(same, f)
				}
			}
			if len(same) == 1 {
				narrowed = same
			}
		}
	}
	if len(narrowed) == 1 {
		return narrowed[0], true, false
	}
	return nil, false, true
}

// reach walks resolved calls from the seeds, bounded, and returns the
// functions in visit order plus how many calls stayed ambiguous.
func reach(seeds []*Function, byName map[string][]*Function, limit int, ctxType string) ([]*Function, int) {
	seen := map[string]bool{}
	var out []*Function
	queue := append([]*Function(nil), seeds...)
	unresolved := 0
	for len(queue) > 0 && len(out) < limit {
		f := queue[0]
		queue = queue[1:]
		if seen[key(f)] {
			continue
		}
		seen[key(f)] = true
		out = append(out, f)
		for _, c := range f.Calls {
			t, ok, ambiguous := resolve(c, f, byName, ctxType)
			if ambiguous {
				unresolved++
			}
			if ok && !seen[key(t)] {
				queue = append(queue, t)
			}
		}
	}
	return out, unresolved
}

func sortedKeys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
