package admin

import (
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/store"
)

func TestPolicyWriteRefusesEachInvalidValue(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"trip_after below one", `{"cooldown":{"trip_after":0}}`, "trip_after"},
		{"retry count of zero", `{"retry":{"max_attempts":0}}`, "max_attempts"},
		{"retry count past the cap", `{"retry":{"max_attempts":20}}`, "between 1 and 10"},
		{"total under connect plus first_byte", `{"timeout":{"total":"5s"}}`, "policy.timeout.total"},
		{"a duration that will not parse", `{"timeout":{"idle":"soon"}}`, "policy.timeout.idle"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := testServerFull(t)
			cookie, token := login(t, s)
			w := do(t, s, cookie, token, "PUT", "/api/policy", tc.body)
			if w.Code != 400 {
				t.Fatalf("PUT = %d, want 400: %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tc.want) {
				t.Errorf("the refusal does not say why: %s", w.Body.String())
			}
		})
	}
}

func TestInvalidPolicyIsNeverWritten(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	if w := do(t, s, cookie, token, "PUT", "/api/policy",
		`{"retry":{"max_attempts":20}}`); w.Code != 400 {
		t.Fatalf("PUT = %d, want 400: %s", w.Code, w.Body.String())
	}
	stored, err := store.StoredConfigKeys(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	if stored["policy.retry.max_attempts"] {
		t.Error("a refused write left a row behind")
	}
}

func TestPolicyWriteTakesEffectOnTheNextSnapshot(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	if w := do(t, s, cookie, token, "PUT", "/api/policy",
		`{"timeout":{"idle":"90s"}}`); w.Code != 200 {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	if got := s.deps.Config.Current().Policy.Timeout.Idle; got != 90*time.Second {
		t.Errorf("idle = %s, want 90s", got)
	}
}

// The seven policy keys are registry rows, so a restart-only one is accepted
// here for the same reason it is on PUT /api/config.
func TestPolicyWriteAcceptsARestartOnlyField(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "PUT", "/api/policy",
		`{"timeout":{"connect":"5s"}}`)
	if w.Code != 200 {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "policy.timeout.connect") {
		t.Errorf("the answer does not name the key that waits: %s", w.Body.String())
	}
}

// Emptying a box means "use the default", which is a deleted row rather than a
// write of whatever the default currently is.
func TestPolicyWriteWithAnEmptyDurationResetsTheKey(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	if w := do(t, s, cookie, token, "PUT", "/api/policy",
		`{"timeout":{"idle":"90s"}}`); w.Code != 200 {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	if w := do(t, s, cookie, token, "PUT", "/api/policy",
		`{"timeout":{"idle":""}}`); w.Code != 200 {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	stored, err := store.StoredConfigKeys(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	if stored["policy.timeout.idle"] {
		t.Error("the row survived an emptied field")
	}
	if got := s.deps.Config.Current().Policy.Timeout.Idle; got != 120*time.Second {
		t.Errorf("idle = %s, want the compiled default", got)
	}
}

// Both blocks or neither, on one transaction. A save that wrote its aliases
// and then failed on its policy would leave the screen half-applied.
func TestPutConfigWritesNothingWhenTheSettingIsInvalid(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "groq", "http://127.0.0.1:1")
	w := do(t, s, cookie, token, "PUT", "/api/config",
		`{"aliases":{"fast":["groq/llama"]},"set":{"policy.retry.max_attempts":"20"}}`)
	if w.Code != 400 {
		t.Fatalf("PUT = %d, want 400: %s", w.Code, w.Body.String())
	}
	aliases, err := db.Aliases(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 0 {
		t.Errorf("the alias half of a refused save landed: %v", aliases)
	}
}

func TestPutConfigWritesBothBlocksTogether(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "groq", "http://127.0.0.1:1")
	w := do(t, s, cookie, token, "PUT", "/api/config",
		`{"aliases":{"fast":["groq/llama"]},"set":{"policy.retry.max_attempts":"5"}}`)
	if w.Code != 200 {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	aliases, err := db.Aliases(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases["fast"]) != 1 {
		t.Errorf("aliases = %v", aliases)
	}
	if got := s.deps.Config.Current().Policy.Retry.MaxAttempts; got != 5 {
		t.Errorf("max_attempts = %d, want 5", got)
	}
}
