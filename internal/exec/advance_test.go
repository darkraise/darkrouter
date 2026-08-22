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

		// Everything else retryable means the provider is down; its remaining
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

// A provider with three credentials must be skipped in one step, not three.
func TestAdvanceSkipsEveryRemainingCredentialOfTheProvider(t *testing.T) {
	cands := []router.Candidate{
		{ProviderID: "groq", KeyID: "g1"},
		{ProviderID: "groq", KeyID: "g2"},
		{ProviderID: "groq", KeyID: "g3"},
		{ProviderID: "cerebras", KeyID: "c1"},
	}
	got, act := nextIndex(cands, 0, adapter.OutcomeRetryableProvider, 500)
	if got != 3 || act != actionNext {
		t.Errorf("nextIndex = %d/%v, want 3/next", got, act)
	}
}
