package seed

import (
	"testing"

	"github.com/forefy/vig/backend/internal/core"
)

// Every seeded report must pass the same validation a posted one does,
// patches placed where the recommendations are and the rest operational.
func TestSeededReportsValidate(t *testing.T) {
	for name, doc := range map[string]core.FindingsDocument{
		"vault":      vaultReport("vig-demo/vault-programs", "aaaaaaaaaaaa", "P1"),
		"vault-diff": vaultDiffReport("vig-demo/vault-programs", "bbbbbbbbbbbb", "aaaaaaaaaaaa", "P1"),
		"stake-pool": stakepoolReport("vig-demo/stake-pool", "cccccccccccc", "P2"),
	} {
		d := doc
		d.Report.AuditID = "0199c0f0-0000-7000-8000-000000000000"
		// The seed stores a proof capture for every finding whose evidence
		// ran a tool before the report is stored; stand in for it here.
		for i := range d.Findings {
			if d.Findings[i].Evidence.Ran() != "" && d.Findings[i].Attached() == 0 {
				d.Findings[i].Visuals = append(d.Findings[i].Visuals, core.Visual{ID: "0199c0f0-0000-7000-8000-00000000ffff", Caption: "the tool's screen", Mime: "image/png", Role: core.VisualProof})
			}
		}
		if err := d.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
