package exec

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/router"
)

// groq has two credentials, cerebras one.
func chain() []router.Candidate {
	return []router.Candidate{
		{ProviderID: "groq", KeyID: "g1", Model: "m"},
		{ProviderID: "groq", KeyID: "g2", Model: "m"},
		{ProviderID: "cerebras", KeyID: "c1", Model: "m"},
	}
}

func TestAdvanceTable(t *testing.T) {
	cases := []struct {
		name       string
		from       int
		outcome    adapter.Outcome
		statusCode int
		wantIndex  int
		wantAction advanceAction
	}{
		{"success finishes", 0, adapter.OutcomeSuccess, 200, 0, actionFinish},
		{"fatal returns immediately", 0, adapter.OutcomeFatal, 422, 0, actionReturn},
		{"client cancellation stops", 0, adapter.OutcomeClientCancelled, 0, 0, actionReturn},

		// 429 is per credential, so the next key on the same provider is worth trying.
		{"429 tries the next credential", 0, adapter.OutcomeRetryableProvider, 429, 1, actionNext},
		{"429 on the last credential advances the provider", 1, adapter.OutcomeRetryableProvider, 429, 2, actionNext},

		// Everything else retryable means the target is down; its remaining
		// credentials will hit the same wall.
		{"503 skips the provider's remaining credentials", 0, adapter.OutcomeRetryableProvider, 503, 2, actionNext},
		{"timeout skips the provider's remaining credentials", 0, adapter.OutcomeRetryableProvider, 0, 2, actionNext},

		// A bad credential is worth rotating past.
		{"401 tries the next credential", 0, adapter.OutcomeRetryableCredential, 401, 1, actionNext},
		{"402 on the last credential advances the provider", 1, adapter.OutcomeRetryableCredential, 402, 2, actionNext},

		// An unknown model says nothing about the credential.
		{"404 advances one step", 0, adapter.OutcomeRetryableModel, 404, 1, actionNext},

		// Running off the end is exhaustion, not a wrap-around.
		{"503 on the last provider exhausts", 2, adapter.OutcomeRetryableProvider, 503, 3, actionNext},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotIdx, gotAct := nextIndex(chain(), tc.from, tc.outcome, tc.statusCode)
			if gotIdx != tc.wantIndex || gotAct != tc.wantAction {
				t.Errorf("nextIndex = %d/%v, want %d/%v", gotIdx, gotAct, tc.wantIndex, tc.wantAction)
			}
		})
	}
}

// A model with three credentials on one provider must be skipped in one step,
// not three, and the trace must name the two that were never attempted.
func TestAdvanceSkipsEveryRemainingCredentialOfTheTarget(t *testing.T) {
	cands := []router.Candidate{
		{ProviderID: "groq", KeyID: "g1", Model: "m"},
		{ProviderID: "groq", KeyID: "g2", Model: "m"},
		{ProviderID: "groq", KeyID: "g3", Model: "m"},
		{ProviderID: "cerebras", KeyID: "c1", Model: "m"},
	}
	got, act := nextIndex(cands, 0, adapter.OutcomeRetryableProvider, 500)
	if got != 3 || act != actionNext {
		t.Errorf("nextIndex = %d/%v, want 3/next", got, act)
	}
	skips := advanceSkips(cands, 0, got)
	want := []string{"groq/g2/m:target_failed", "groq/g3/m:target_failed"}
	if len(skips) != len(want) || skips[0] != want[0] || skips[1] != want[1] {
		t.Errorf("advanceSkips = %v, want %v", skips, want)
	}
}

// An alias `lmstudio/a → lmstudio/b` promises b as the fallback for a. A 500
// from a says nothing about b — breakers are per model — so b must be tried.
func TestAdvanceTriesAnotherModelOnTheSameProvider(t *testing.T) {
	cands := []router.Candidate{
		{ProviderID: "lmstudio", Model: "mock-error"},
		{ProviderID: "lmstudio", Model: "mock-fast"},
	}
	for _, status := range []int{500, 503, 0} {
		got, act := nextIndex(cands, 0, adapter.OutcomeRetryableProvider, status)
		if got != 1 || act != actionNext {
			t.Errorf("status %d: nextIndex = %d/%v, want 1/next", status, got, act)
		}
		if skips := advanceSkips(cands, 0, got); len(skips) != 0 {
			t.Errorf("status %d: advanceSkips = %v, want none", status, skips)
		}
	}
}

// Only the run of the failed model is skipped: the provider's next model still
// gets its first credential, and its own second credential is not skipped
// along with the failed one's.
func TestAdvanceSkipStopsAtTheNextModelOnTheSameProvider(t *testing.T) {
	cands := []router.Candidate{
		{ProviderID: "openrouter", KeyID: "k1", Model: "a"},
		{ProviderID: "openrouter", KeyID: "k2", Model: "a"},
		{ProviderID: "openrouter", KeyID: "k1", Model: "b"},
		{ProviderID: "openrouter", KeyID: "k2", Model: "b"},
	}
	got, act := nextIndex(cands, 0, adapter.OutcomeRetryableProvider, 502)
	if got != 2 || act != actionNext {
		t.Errorf("nextIndex = %d/%v, want 2/next", got, act)
	}
	if skips := advanceSkips(cands, 0, got); len(skips) != 1 || skips[0] != "openrouter/k2/a:target_failed" {
		t.Errorf("advanceSkips = %v, want [openrouter/k2/a:target_failed]", skips)
	}
}

// 429 is per credential, so it still steps one at a time even when the next
// candidate is the same model on the same provider, and nothing is skipped.
func TestAdvance429StepsOneCredentialAtATime(t *testing.T) {
	cands := []router.Candidate{
		{ProviderID: "groq", KeyID: "g1", Model: "m"},
		{ProviderID: "groq", KeyID: "g2", Model: "m"},
		{ProviderID: "groq", KeyID: "g3", Model: "m"},
	}
	for from := 0; from < len(cands); from++ {
		got, act := nextIndex(cands, from, adapter.OutcomeRetryableProvider, 429)
		if got != from+1 || act != actionNext {
			t.Errorf("from %d: nextIndex = %d/%v, want %d/next", from, got, act, from+1)
		}
		if skips := advanceSkips(cands, from, got); len(skips) != 0 {
			t.Errorf("from %d: advanceSkips = %v, want none", from, skips)
		}
	}
}

// Running off the end must not index past the candidate list.
func TestAdvanceSkipsClampAtTheEnd(t *testing.T) {
	if skips := advanceSkips(chain(), 2, 5); len(skips) != 0 {
		t.Errorf("advanceSkips = %v, want none", skips)
	}
}
