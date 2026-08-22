package router

import (
	"sort"
	"time"

	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/provider"
)

// orderCredentials returns creds least-recently-used first, ties broken by id.
//
// The tie-break is not cosmetic: without it the order of equally-aged
// credentials would depend on the input slice, and the candidate sequence would
// stop being reproducible from the snapshot alone.
//
// It copies rather than sorting in place, because the input belongs to the
// provider set and is shared by every concurrent request.
func orderCredentials(providerID string, creds []provider.Credential,
	lastUsed map[health.CredKey]time.Time) []provider.Credential {

	out := make([]provider.Credential, len(creds))
	copy(out, creds)

	at := func(id string) (time.Time, bool) {
		t, ok := lastUsed[health.CredKey{ProviderID: providerID, KeyID: id}]
		return t, ok
	}

	sort.SliceStable(out, func(i, j int) bool {
		ti, okI := at(out[i].ID)
		tj, okJ := at(out[j].ID)
		switch {
		case !okI && !okJ:
			// Both never dispatched to: fall through to the id tie-break.
		case !okI:
			return true // never used sorts before ever used
		case !okJ:
			return false
		case !ti.Equal(tj):
			return ti.Before(tj)
		}
		return out[i].ID < out[j].ID
	})
	return out
}
