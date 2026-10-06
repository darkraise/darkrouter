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

// skipTargetFailed is the trace reason for a candidate passed over because
// another credential for the same (provider, model) had just failed with a
// non-429 provider error.
const skipTargetFailed = "target_failed"

// nextIndex applies master design §8.1's advance behavior.
//
// The returned index may be len(cands), which means the chain is exhausted.
// Every candidate strictly between i and the returned index was passed over
// without an attempt; advanceSkips names them for the trace.
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
		// The target is down. Its other credentials will hit the same wall, so
		// skip them in one step — but only for this model. Breakers are scoped
		// per (provider, key, model), and a 500 from one model on a
		// self-hosted runtime or an aggregator says nothing about the next
		// model on the same provider, which is exactly the fallback an alias
		// like `lmstudio/a → lmstudio/b` promises.
		return skipTarget(cands, i), actionNext

	default:
		return i + 1, actionNext
	}
}

// skipTarget returns the index of the first candidate after i that is not
// another credential for the same (provider, model) as cands[i].
func skipTarget(cands []router.Candidate, i int) int {
	if i >= len(cands) {
		return i
	}
	c := cands[i]
	j := i + 1
	for j < len(cands) && cands[j].ProviderID == c.ProviderID && cands[j].Model == c.Model {
		j++
	}
	return j
}

// advanceSkips renders the candidates nextIndex jumped over, between the
// attempted index i and next, as trace skips. Without them the trace lists a
// candidate that was neither attempted nor skipped, and cannot say why.
func advanceSkips(cands []router.Candidate, i, next int) []string {
	if next > len(cands) {
		next = len(cands)
	}
	var out []string
	for j := i + 1; j < next; j++ {
		out = append(out, traceSkipOf(cands[j], skipTargetFailed))
	}
	return out
}
