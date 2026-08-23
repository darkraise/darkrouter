package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestVerifiersAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		v, err := NewVerifier()
		if err != nil {
			t.Fatal(err)
		}
		if seen[v] {
			t.Fatal("a verifier repeated")
		}
		seen[v] = true
		// RFC 7636 requires 43 to 128 characters from the unreserved set.
		if len(v) < 43 || len(v) > 128 {
			t.Errorf("verifier length %d is outside RFC 7636's range", len(v))
		}
		if strings.ContainsAny(v, "+/=") {
			t.Errorf("verifier %q is not URL-safe base64", v)
		}
	}
}

func TestChallengeIsS256(t *testing.T) {
	// The server recomputes this. A plain challenge would let anyone who
	// intercepts the redirect exchange the code themselves.
	v := "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHI"
	sum := sha256.Sum256([]byte(v))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if got := Challenge(v); got != want {
		t.Errorf("challenge = %q, want %q", got, want)
	}
}

func flowStore(t *testing.T) *FlowStore {
	t.Helper()
	return NewFlowStore(10 * time.Minute)
}

func TestStateIsSingleUse(t *testing.T) {
	// Replaying a callback must not bind a second account, and must not let an
	// attacker who saw one redirect reuse it.
	s := flowStore(t)
	state, err := s.Begin(Flow{ProviderID: "p", SessionID: "sess", Verifier: "v"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(state, "sess"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(state, "sess"); err == nil {
		t.Fatal("a state must not be claimable twice")
	}
}

func TestStateIsBoundToTheSession(t *testing.T) {
	// The forced-binding attack: the victim's browser follows an attacker's
	// redirect carrying the attacker's code. Without this check the victim's
	// gateway ends up serving traffic on the attacker's account.
	s := flowStore(t)
	state, err := s.Begin(Flow{ProviderID: "p", SessionID: "victim", Verifier: "v"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(state, "attacker"); !errors.Is(err, ErrWrongSession) {
		t.Fatalf("a state from another session must be refused, got %v", err)
	}
	// And a refused claim must not consume it: the legitimate operator's own
	// callback is still on its way, and letting a blocked attack invalidate it
	// turns the block into a denial of service.
	if _, err := s.Claim(state, "victim"); err != nil {
		t.Fatalf("the real session lost its flow: %v", err)
	}
}

func TestStateExpires(t *testing.T) {
	s := NewFlowStore(time.Millisecond)
	state, err := s.Begin(Flow{ProviderID: "p", SessionID: "sess", Verifier: "v"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := s.Claim(state, "sess"); !errors.Is(err, ErrStateExpired) {
		t.Fatalf("an expired state must be refused, got %v", err)
	}
}

func TestSweepDropsExpiredFlows(t *testing.T) {
	s := NewFlowStore(time.Millisecond)
	if _, err := s.Begin(Flow{ProviderID: "p", SessionID: "sess"}); err != nil {
		t.Fatal(err)
	}
	s.Sweep(time.Now().Add(time.Minute))
	s.mu.Lock()
	n := len(s.flows)
	s.mu.Unlock()
	if n != 0 {
		t.Errorf("%d flows survived the sweep", n)
	}
}

func TestAnUnknownStateIsRefused(t *testing.T) {
	if _, err := flowStore(t).Claim("never-issued", "sess"); !errors.Is(err, ErrUnknownState) {
		t.Fatalf("an unknown state must be refused, got %v", err)
	}
}

func TestStatesAreUnguessable(t *testing.T) {
	s := flowStore(t)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		state, err := s.Begin(Flow{ProviderID: "p", SessionID: "sess", Verifier: "v"})
		if err != nil {
			t.Fatal(err)
		}
		if seen[state] {
			t.Fatal("a state repeated")
		}
		seen[state] = true
		// 32 bytes of entropy. Anything a person could brute-force is the
		// whole vulnerability, since state is the only defense here.
		if len(state) < 43 {
			t.Errorf("state %q is too short to be unguessable", state)
		}
	}
}

func TestFlowStoreIsRaceFree(t *testing.T) {
	s := flowStore(t)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			state, err := s.Begin(Flow{ProviderID: "p", SessionID: "sess"})
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := s.Claim(state, "sess"); err != nil {
				t.Error(err)
			}
		}()
	}
	go s.Sweep(time.Now())
	wg.Wait()
}
