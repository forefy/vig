package core

import "testing"

func TestHappyPath(t *testing.T) {
	for i := 0; i+1 < len(Pipeline); i++ {
		if err := CanTransition(Transition{From: Pipeline[i], To: Pipeline[i+1]}); err != nil {
			t.Fatalf("%s -> %s: %v", Pipeline[i], Pipeline[i+1], err)
		}
	}
}

func TestNoSkipping(t *testing.T) {
	if err := CanTransition(Transition{From: StateQueued, To: StateAnalysis}); err == nil {
		t.Fatal("queued -> analysis should be rejected")
	}
	if err := CanTransition(Transition{From: StateAnalysis, To: StateCloning}); err == nil {
		t.Fatal("moving backwards should be rejected")
	}
}

func TestDelayedRoundTrip(t *testing.T) {
	if err := CanTransition(Transition{From: StateAnalysis, To: StateDelayed}); err != nil {
		t.Fatal(err)
	}
	if err := CanTransition(Transition{From: StateDelayed, To: StateAnalysis, Resume: StateAnalysis}); err != nil {
		t.Fatal(err)
	}
	if err := CanTransition(Transition{From: StateDelayed, To: StateHumanReview, Resume: StateAnalysis}); err == nil {
		t.Fatal("delayed must resume where it left")
	}
	if err := CanTransition(Transition{From: StateDelayed, To: StateAnalysis}); err == nil {
		t.Fatal("resume state is required")
	}
	if err := CanTransition(Transition{From: StateDelayed, To: StateDelayed}); err == nil {
		t.Fatal("cannot delay a delayed audit")
	}
}

func TestTerminal(t *testing.T) {
	for _, s := range []AuditState{StateDelivered, StateFailed} {
		if err := CanTransition(Transition{From: s, To: StateQueued}); err == nil {
			t.Fatalf("%s should be terminal", s)
		}
		if !s.DeletesClone() {
			t.Fatalf("%s must delete the clone", s)
		}
	}
	if !StateFailed.ClearsCost() || StateDelivered.ClearsCost() {
		t.Fatal("only failed refunds the cost")
	}
}

func TestFailFromAnywhere(t *testing.T) {
	for _, s := range Pipeline[:len(Pipeline)-1] {
		if err := CanTransition(Transition{From: s, To: StateFailed}); err != nil {
			t.Fatalf("%s -> failed: %v", s, err)
		}
	}
	if err := CanTransition(Transition{From: StateDelayed, To: StateFailed}); err != nil {
		t.Fatal(err)
	}
}
