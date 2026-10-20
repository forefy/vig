package core

import "time"

// SKU is a purchasable item.
type SKU string

const (
	SKUMini SKU = "mini" // one credit
	SKUPro  SKU = "pro"  // 12 months, one run per rolling day
)

// USDC amounts are in base units (6 decimals).
const USDCDecimals = 1_000_000

// Plan describes a tier as the UI renders it. This is the pricing source of
// truth and is served by GET /v1/plans.
type Plan struct {
	Key            string `json:"key" enum:"free,mini,pro,team"`
	Name           string `json:"name"`
	SKU            SKU    `json:"sku,omitempty" enum:"mini,pro"`
	PriceUSDC      int64  `json:"price_usdc" doc:"Total charged once, USDC base units. 0 for free and talk-to-us tiers"`
	DisplayMonthly int64  `json:"display_monthly_usdc,omitempty" doc:"Per-month figure for the card. The pay button always shows price_usdc"`
	TermDays       int    `json:"term_days,omitempty"`
	RunsPerDay     int    `json:"runs_per_day,omitempty"`
	Credits        int    `json:"credits,omitempty"`
	MaxPrograms    int    `json:"max_programs"`
	MaxLines       int    `json:"max_lines"`
	Gate           string `json:"gate" enum:"tweet,usdc,contact" doc:"How the tier is obtained"`
}

// Plans as decided in SPEC §2 and §3. Caps are the assumed defaults.
var Plans = []Plan{
	{Key: "free", Name: "Free", PriceUSDC: 0, Credits: 1, MaxPrograms: 1, MaxLines: 10_000, Gate: "tweet"},
	{Key: "mini", Name: "Mini", SKU: SKUMini, PriceUSDC: 25 * USDCDecimals, Credits: 1, MaxPrograms: 1, MaxLines: 10_000, Gate: "usdc"},
	{Key: "pro", Name: "Pro", SKU: SKUPro, PriceUSDC: 540 * USDCDecimals, DisplayMonthly: 45 * USDCDecimals, TermDays: 365, RunsPerDay: 1, MaxPrograms: 3, MaxLines: 40_000, Gate: "usdc"},
	{Key: "team", Name: "Team", PriceUSDC: 0, MaxPrograms: 10, MaxLines: 200_000, Gate: "contact"},
}

// PlanBySKU finds the purchasable plan for a SKU.
func PlanBySKU(s SKU) (Plan, bool) {
	for _, p := range Plans {
		if p.SKU == s {
			return p, true
		}
	}
	return Plan{}, false
}

// SlotWindow is the rolling window for one Pro run.
const SlotWindow = 24 * time.Hour

// NextSlot returns when a Pro user may run again, given their last run.
// A zero lastRun means now.
func NextSlot(lastRun time.Time, now time.Time) time.Time {
	if lastRun.IsZero() {
		return now
	}
	next := lastRun.Add(SlotWindow)
	if next.Before(now) {
		return now
	}
	return next
}

// CreditTTL is how long purchased or granted credits stay valid.
const CreditTTL = 365 * 24 * time.Hour
