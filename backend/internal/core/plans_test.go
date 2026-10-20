package core

import (
	"testing"
	"time"
)

func TestPlansMatchSpec(t *testing.T) {
	mini, ok := PlanBySKU(SKUMini)
	if !ok || mini.PriceUSDC != 25*USDCDecimals || mini.Credits != 1 {
		t.Fatalf("mini plan wrong: %+v", mini)
	}
	pro, ok := PlanBySKU(SKUPro)
	if !ok || pro.PriceUSDC != 540*USDCDecimals || pro.DisplayMonthly != 45*USDCDecimals || pro.TermDays != 365 || pro.RunsPerDay != 1 {
		t.Fatalf("pro plan wrong: %+v", pro)
	}
	if _, ok := PlanBySKU("team"); ok {
		t.Fatal("team is not purchasable")
	}
}

func TestNextSlot(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	if !NextSlot(time.Time{}, now).Equal(now) {
		t.Fatal("never ran: now")
	}
	if got := NextSlot(now.Add(-2*time.Hour), now); !got.Equal(now.Add(22 * time.Hour)) {
		t.Fatalf("ran 2h ago: got %v", got)
	}
	if !NextSlot(now.Add(-30*time.Hour), now).Equal(now) {
		t.Fatal("ran 30h ago: now")
	}
}
