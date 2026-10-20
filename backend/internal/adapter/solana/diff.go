package solana

import (
	"strconv"

	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/rustsrc"
)

// diffFacts compares the handlers of one program between the head and the
// base checkout: "added" when the base has no such handler, "changed" when
// anything the handler reaches differs, "same" otherwise. Handlers the
// base had and the head lacks are not on the map and are not reported.
func diffFacts(head, base *rustsrc.Model, pid string) map[string]string {
	hc, ok := head.CrateFor(pid)
	if !ok {
		return nil
	}
	var before map[string]string
	if bc, ok := base.CrateFor(pid); ok {
		before = map[string]string{}
		for _, ix := range bc.Instructions {
			before[ix.Name] = bc.Fingerprint(ix, base)
		}
	}
	out := map[string]string{}
	for _, ix := range hc.Instructions {
		prev, had := before[ix.Name]
		switch {
		case !had:
			out[ix.Name] = "added"
		case prev != hc.Fingerprint(ix, head):
			out[ix.Name] = "changed"
		default:
			out[ix.Name] = "same"
		}
	}
	return out
}

// applyDiff writes the diff of one program onto its instruction nodes and
// the counts onto the program node.
func applyDiff(s *core.Surface, pid string, head, base *rustsrc.Model) {
	d := diffFacts(head, base, pid)
	if d == nil {
		return
	}
	changed, added := 0, 0
	for name, state := range d {
		setFacts(s, ixNodeID(pid, name), map[string]string{"diff": state})
		switch state {
		case "changed":
			changed++
		case "added":
			added++
		}
	}
	setFacts(s, programNodeID(pid), map[string]string{"diff_changed": strconv.Itoa(changed), "diff_added": strconv.Itoa(added)})
}
