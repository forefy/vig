package core

import (
	"fmt"
	"time"
)

// AuditState is the pipeline position of one audit run.
type AuditState string

const (
	StateQueued           AuditState = "queued"
	StateClaimed          AuditState = "claimed"
	StateCloning          AuditState = "cloning"
	StateProgramsVerified AuditState = "programs_verified"
	StateAnalysis         AuditState = "analysis"
	StateHumanReview      AuditState = "human_review"
	StateDelivered        AuditState = "delivered"
	StateDelayed          AuditState = "delayed"
	StateFailed           AuditState = "failed"
	// StateCancelled is an admin's decision, allowed from any state including
	// delivered: what the audit cost comes back and the run is over.
	StateCancelled AuditState = "cancelled"
)

// Pipeline is the happy path in order. Delayed and Failed are side states.
var Pipeline = []AuditState{
	StateQueued, StateClaimed, StateCloning, StateProgramsVerified,
	StateAnalysis, StateHumanReview, StateDelivered,
}

// Terminal states accept no further transitions.
func (s AuditState) Terminal() bool {
	return s == StateDelivered || s == StateFailed || s == StateCancelled
}

// Refunded reports whether the audit's cost has already been given back.
func (s AuditState) Refunded() bool { return s == StateFailed || s == StateCancelled }

// Active states are ones where an operator holds the job.
func (s AuditState) Active() bool {
	switch s {
	case StateClaimed, StateCloning, StateProgramsVerified, StateAnalysis, StateHumanReview:
		return true
	}
	return false
}

// Next returns the next happy-path state, or "" at the end.
func (s AuditState) Next() AuditState {
	for i, st := range Pipeline {
		if st == s && i+1 < len(Pipeline) {
			return Pipeline[i+1]
		}
	}
	return ""
}

// Transition holds the rule for moving an audit between states. resume is the
// state a delayed audit returns to and must be provided when leaving Delayed.
type Transition struct {
	From, To AuditState
	Resume   AuditState
}

// CanTransition applies the state machine rules from SPEC §6.
//
//   - happy path moves one step forward at a time
//   - any non-terminal state may go to delayed or failed
//   - delayed returns only to the state it left (Resume)
func CanTransition(t Transition) error {
	if t.From.Terminal() {
		return fmt.Errorf("audit is %s and cannot change", t.From)
	}
	switch t.To {
	case StateFailed:
		return nil
	case StateDelayed:
		if t.From == StateDelayed {
			return fmt.Errorf("already delayed")
		}
		return nil
	}
	if t.From == StateDelayed {
		if t.Resume == "" {
			return fmt.Errorf("leaving delayed requires the resume state")
		}
		if t.To != t.Resume {
			return fmt.Errorf("delayed audit must resume at %s, not %s", t.Resume, t.To)
		}
		return nil
	}
	if t.From.Next() != t.To {
		return fmt.Errorf("cannot move from %s to %s", t.From, t.To)
	}
	return nil
}

// ClearsCost reports whether entering this state gives the credit or slot back.
func (s AuditState) ClearsCost() bool { return s.Refunded() }

// DeletesClone reports whether entering this state must remove the checkout.
func (s AuditState) DeletesClone() bool { return s.Terminal() }

// CloneRetentionDays is how long a source checkout outlives delivery: it is
// removed the moment the report is delivered. Reports stay while the account
// exists.
const CloneRetentionDays = 0

// OverdueGrace is how far past its ETA an active audit may run before the
// user is told it is overdue.
const OverdueGrace = 1 * time.Hour
