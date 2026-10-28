package seed

import "github.com/forefy/vig/backend/internal/core"

// A seeded finding answers its exhaustion the way a careful writer would:
// most items done, one or two left open with the reason, so the demo bar
// is honest rather than full.
func exhaustSeed(doc *core.FindingsDocument) {
	for i := range doc.Findings {
		f := &doc.Findings[i]
		if len(f.Exhaustion) > 0 {
			continue
		}
		core.AnswerAll(f, true, "")
		switch f.Ordinal % 3 {
		case 0:
			core.Answer(f, "negative", false, "no fix deployed yet to re-test against")
		case 1:
			core.Answer(f, "below", false, "one role exists on this deployment; nothing below it to try")
		}
		if f.Severity == core.SeverityInfo || f.Severity == core.SeverityLow {
			core.Answer(f, "furthest", false, "stopped at the first effect; low impact does not pay for more")
		}
	}
}
