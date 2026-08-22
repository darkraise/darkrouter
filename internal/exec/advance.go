package exec

import (
	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/router"
)

type advanceAction int

const (
	// actionFinish: the attempt succeeded; serve it.
	actionFinish advanceAction = iota
	// actionReturn: stop the chain and return this outcome to the client.
	actionReturn
	// actionNext: continue at the returned index, which may be past the end.
	actionNext
)

// nextIndex applies master design §8.1's advance behavior.
//
// The returned index may be len(cands), which means the chain is exhausted.
func nextIndex(cands []router.Candidate, i int, o adapter.Outcome, statusCode int) (int, advanceAction) {
	switch o {
	case adapter.OutcomeSuccess:
		return i, actionFinish

	case adapter.OutcomeFatal:
		// One malformed client request must not become a fleet-wide burst of
		// identical failures.
		return i, actionReturn

	case adapter.OutcomeClientCancelled:
		return i, actionReturn

	case adapter.OutcomeRetryableCredential, adapter.OutcomeRetryableModel:
		// A bad credential or a missing model says nothing about the provider's
		// other credentials, so step one at a time.
		return i + 1, actionNext

	case adapter.OutcomeRetryableProvider:
		if statusCode == 429 {
			// Rate limits are per credential: the next key is worth trying.
			return i + 1, actionNext
		}
		// The upstream is down. Every remaining credential on this provider
		// will hit the same wall, so skip them all in one step.
		return skipProvider(cands, i), actionNext

	default:
		return i + 1, actionNext
	}
}

// skipProvider returns the index of the first candidate belonging to a
// different provider than cands[i].
func skipProvider(cands []router.Candidate, i int) int {
	if i >= len(cands) {
		return i
	}
	id := cands[i].ProviderID
	j := i + 1
	for j < len(cands) && cands[j].ProviderID == id {
		j++
	}
	return j
}
