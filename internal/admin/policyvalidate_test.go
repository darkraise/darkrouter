package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestPolicyWriteRefusesEachInvalidValue(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	for name, body := range map[string]string{
		"max_attempts zero":     `{"retry":{"max_attempts":0}}`,
		"max_attempts eleven":   `{"retry":{"max_attempts":11}}`,
		"trip_after zero":       `{"cooldown":{"trip_after":0}}`,
		"cooldown.max zero":     `{"cooldown":{"max":"0s"}}`,
		"cooldown.max negative": `{"cooldown":{"max":"-1m"}}`,
		"timeout.total zero":    `{"timeout":{"total":"0s"}}`,
		"timeout.idle zero":     `{"timeout":{"idle":"0s"}}`,
		"total under the floor": `{"timeout":{"total":"30s"}}`,
		"unparseable duration":  `{"timeout":{"idle":"soon"}}`,
		"unknown field":         `{"retry":{"max_attempt":3}}`,
	} {
		w := do(t, s, cookie, token, "PUT", "/api/policy", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: PUT /api/policy = %d, want 400: %s", name, w.Code, w.Body.String())
		}
	}
	// The boundaries are accepted.
	for name, body := range map[string]string{
		"max_attempts ten":   `{"retry":{"max_attempts":10}}`,
		"max_attempts one":   `{"retry":{"max_attempts":1}}`,
		"trip_after one":     `{"cooldown":{"trip_after":1}}`,
		"total at the floor": `{"timeout":{"total":"70s"}}`,
	} {
		w := do(t, s, cookie, token, "PUT", "/api/policy", body)
		if w.Code != http.StatusOK {
			t.Errorf("%s: PUT /api/policy = %d, want 200: %s", name, w.Code, w.Body.String())
		}
	}
}

func TestInvalidPolicyIsNeverWritten(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "PUT", "/api/policy", `{"retry":{"max_attempts":50}}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
	if v, _, _ := db.GetSetting(t.Context(), "policy.retry.max_attempts"); v == "50" {
		t.Error("a refused policy value reached the database")
	}
	if got := s.deps.Config.Current().Policy.Retry.MaxAttempts; got == 50 {
		t.Error("a refused policy value reached the running config")
	}
}

func TestPutConfigWritesNothingWhenThePolicyIsInvalid(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	_ = do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"p1","name":"P","kind":"openaicompat","base_url":"https://x/v1"}`)
	w := do(t, s, cookie, token, "PUT", "/api/config",
		`{"aliases":{"fast":["p1/m"]},"policy":{"retry":{"max_attempts":0}}}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	aliases, err := db.Aliases(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 0 {
		t.Errorf("aliases %v were written beside a refused policy", aliases)
	}
}

func TestPutConfigWritesBothBlocksTogether(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	_ = do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"p1","name":"P","kind":"openaicompat","base_url":"https://x/v1"}`)
	w := do(t, s, cookie, token, "PUT", "/api/config",
		`{"aliases":{"fast":["p1/m"]},"policy":{"retry":{"max_attempts":4}}}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"valid":true`) {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	cfg := s.deps.Config.Current()
	if cfg.Policy.Retry.MaxAttempts != 4 || len(cfg.Aliases["fast"]) != 1 {
		t.Errorf("running config: attempts %d aliases %v", cfg.Policy.Retry.MaxAttempts, cfg.Aliases)
	}
	var got map[string]any
	_ = json.Unmarshal(do(t, s, cookie, token, "GET", "/api/policy", "").Body.Bytes(), &got)
	if got["retry"].(map[string]any)["max_attempts"].(float64) != 4 {
		t.Errorf("GET /api/policy = %v", got)
	}
}
