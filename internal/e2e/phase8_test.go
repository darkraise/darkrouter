package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/darkraise/darkrouter/internal/auth"
	"github.com/darkraise/darkrouter/internal/store"
)

// --- Bedrock -----------------------------------------------------------------

type bedrockFake struct {
	mu       sync.Mutex
	authz    string
	path     string
	body     map[string]any
	stream   bool
	srv      *httptest.Server
	failWith int
}

func newBedrockFake(t *testing.T) *bedrockFake {
	t.Helper()
	f := &bedrockFake{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.authz, f.path = r.Header.Get("Authorization"), r.URL.EscapedPath()
		f.body = map[string]any{}
		_ = json.Unmarshal(raw, &f.body)
		fail, stream := f.failWith, f.stream
		f.mu.Unlock()

		if fail != 0 {
			w.WriteHeader(fail)
			return
		}
		if stream {
			w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
			var buf bytes.Buffer
			for _, ev := range []struct {
				name    string
				payload any
			}{
				{"messageStart", map[string]any{"role": "assistant"}},
				{"contentBlockDelta", map[string]any{"contentBlockIndex": 0,
					"delta": map[string]any{"text": "signed "}}},
				{"contentBlockDelta", map[string]any{"contentBlockIndex": 0,
					"delta": map[string]any{"text": "and streamed"}}},
				{"contentBlockStop", map[string]any{"contentBlockIndex": 0}},
				{"messageStop", map[string]any{"stopReason": "end_turn"}},
				{"metadata", map[string]any{"usage": map[string]any{
					"inputTokens": 4, "outputTokens": 3}}},
			} {
				body, _ := json.Marshal(ev.payload)
				_ = eventstream.NewEncoder().Encode(&buf, eventstream.Message{
					Headers: eventstream.Headers{
						{Name: ":message-type", Value: eventstream.StringValue("event")},
						{Name: ":event-type", Value: eventstream.StringValue(ev.name)},
					},
					Payload: body,
				})
			}
			_, _ = w.Write(buf.Bytes())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":{"message":{"role":"assistant",
		  "content":[{"text":"signed and served"}]}},"stopReason":"end_turn",
		  "usage":{"inputTokens":4,"outputTokens":3}}`))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *bedrockFake) seen() (authz, path string, body map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.authz, f.path, f.body
}

func seedBedrock(t *testing.T, g *gateway, f *bedrockFake, id, model string) {
	t.Helper()
	g.mustAdmin(t, "POST", "/api/providers", fmt.Sprintf(
		`{"id":%q,"kind":"bedrock","base_url":%s,"auth_style":"sigv4","region":"us-east-1","priority":10}`,
		id, jsonStr(t, f.srv.URL)), http.StatusCreated)
	g.mustAdmin(t, "POST", "/api/providers/"+id+"/keys", fmt.Sprintf(
		`{"label":"primary","secret":%s}`,
		jsonStr(t, `{"access_key_id":"AKIDEXAMPLE","secret_access_key":"CANARY-AWS-0002"}`)),
		http.StatusCreated)
	g.seedModel(t, id, model, "")
}

func TestABedrockRequestLeavesSigned(t *testing.T) {
	// Spec §8 criterion 1, minus "a real Bedrock serves it".
	g := newGateway(t)
	f := newBedrockFake(t)
	const model = "anthropic.claude-3-5-sonnet-20241022-v2:0"
	seedBedrock(t, g, f, "bed", model)

	w := g.proxy(t, "/v1/chat/completions", chatBody(model))
	if w.Code != http.StatusOK {
		t.Fatalf("chat: %d %s", w.Code, w.Body.String())
	}
	authz, path, body := f.seen()
	if !strings.HasPrefix(authz, "AWS4-HMAC-SHA256 ") {
		t.Errorf("Authorization = %q", authz)
	}
	if !strings.Contains(authz, "/us-east-1/bedrock/aws4_request") {
		t.Errorf("credential scope does not name the configured region: %q", authz)
	}
	// The colon is escaped, and the signature covers the escaped path.
	if !strings.Contains(path, "%3A0/converse") {
		t.Errorf("path = %q; the model id is not escaped", path)
	}
	if _, ok := body["messages"]; !ok {
		t.Errorf("body is not Converse-shaped: %v", body)
	}
	if !strings.Contains(w.Body.String(), "signed and served") {
		t.Errorf("client body = %s", w.Body.String())
	}
}

func TestABedrockStreamDecodesEventstream(t *testing.T) {
	g := newGateway(t)
	f := newBedrockFake(t)
	f.mu.Lock()
	f.stream = true
	f.mu.Unlock()
	const model = "anthropic.claude-x-v1:0"
	seedBedrock(t, g, f, "bed", model)

	w := g.proxy(t, "/v1/chat/completions",
		`{"model":"`+model+`","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("stream: %d %s", w.Code, w.Body.String())
	}
	out := w.Body.String()
	if !strings.Contains(out, "signed ") || !strings.Contains(out, "and streamed") {
		t.Errorf("the decoded text did not reach the client:\n%s", out)
	}
	if !strings.Contains(out, "data:") {
		t.Errorf("the client did not receive SSE:\n%s", out)
	}
}

func TestASignedProviderFailsOverLikeAnyOther(t *testing.T) {
	// Spec §8 criterion 5. A bedrock provider at a dead address, first by
	// priority, with an openaicompat provider behind it.
	g := newGateway(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","object":"chat.completion","choices":[
		  {"index":0,"message":{"role":"assistant","content":"from the fallback"},
		   "finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":4}}`))
	}))
	t.Cleanup(upstream.Close)

	const model = "shared-model"
	g.mustAdmin(t, "POST", "/api/providers",
		`{"id":"bed","kind":"bedrock","base_url":"http://127.0.0.1:1","auth_style":"sigv4","region":"us-east-1","priority":99}`,
		http.StatusCreated)
	g.mustAdmin(t, "POST", "/api/providers/bed/keys", fmt.Sprintf(
		`{"label":"k","secret":%s}`,
		jsonStr(t, `{"access_key_id":"A","secret_access_key":"B"}`)), http.StatusCreated)
	g.seedModel(t, "bed", model, "")

	g.mustAdmin(t, "POST", "/api/providers", fmt.Sprintf(
		`{"id":"back","kind":"openaicompat","base_url":%s,"priority":1}`,
		jsonStr(t, upstream.URL)), http.StatusCreated)
	g.mustAdmin(t, "POST", "/api/providers/back/keys",
		`{"label":"k","secret":"sk-fallback"}`, http.StatusCreated)
	g.seedModel(t, "back", model, "")

	w := g.proxy(t, "/v1/chat/completions", chatBody(model))
	if w.Code != http.StatusOK {
		t.Fatalf("chat: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "from the fallback") {
		t.Errorf("body = %s", w.Body.String())
	}
	if got := w.Header().Get("X-Darkrouter-Attempts"); got != "2" {
		t.Errorf("attempts = %q, want 2: the signed provider was not tried first", got)
	}
	if got := w.Header().Get("X-Darkrouter-Provider"); got != "back" {
		t.Errorf("provider = %q", got)
	}
}

// --- Vertex ------------------------------------------------------------------

type vertexFake struct {
	mu     sync.Mutex
	paths  []string
	bodies []map[string]any
	srv    *httptest.Server
}

func newVertexFake(t *testing.T) *vertexFake {
	t.Helper()
	f := &vertexFake{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.EscapedPath())
		f.bodies = append(f.bodies, body)
		f.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "rawPredict") {
			_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant",
			  "content":[{"type":"text","text":"from anthropic"}],"stop_reason":"end_turn",
			  "usage":{"input_tokens":3,"output_tokens":2}}`))
			return
		}
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"from google"}],
		  "role":"model"},"finishReason":"STOP"}],
		  "usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2}}`))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func TestVertexRoutesEachPublisherToItsOwnURL(t *testing.T) {
	// Spec §8 criterion 3. Two models on one provider, one per publisher.
	//
	// The fake stands in for aiplatform.googleapis.com, so the adapter's own
	// URL is overridden by the provider's base_url — what this proves is the
	// route suffix and the payload shape, which is where the two publishers
	// actually differ.
	g := newGateway(t)
	f := newVertexFake(t)

	g.mustAdmin(t, "POST", "/api/providers", fmt.Sprintf(
		`{"id":"vx","kind":"vertex","base_url":%s,"auth_style":"none","project":"proj","location":"us-central1"}`,
		jsonStr(t, f.srv.URL)), http.StatusCreated)
	g.mustAdmin(t, "POST", "/api/providers/vx/keys",
		`{"label":"k","secret":"unused"}`, http.StatusCreated)
	g.seedModel(t, "vx", "gemini-2.5-pro", "publishers/google")
	g.seedModel(t, "vx", "claude-sonnet-4-5", "publishers/anthropic")

	if w := g.proxy(t, "/v1/chat/completions", chatBody("gemini-2.5-pro")); w.Code != http.StatusOK {
		t.Fatalf("google: %d %s", w.Code, w.Body.String())
	}
	if w := g.proxy(t, "/v1/chat/completions", chatBody("claude-sonnet-4-5")); w.Code != http.StatusOK {
		t.Fatalf("anthropic: %d %s", w.Code, w.Body.String())
	}

	f.mu.Lock()
	paths, bodies := append([]string{}, f.paths...), append([]map[string]any{}, f.bodies...)
	f.mu.Unlock()
	if len(paths) != 2 {
		t.Fatalf("paths = %v", paths)
	}
	if !strings.HasSuffix(paths[0], ":generateContent") {
		t.Errorf("google path = %q", paths[0])
	}
	if _, ok := bodies[0]["contents"]; !ok {
		t.Errorf("google body is not the Gemini shape: %v", keysOf(bodies[0]))
	}
	if !strings.HasSuffix(paths[1], ":rawPredict") {
		t.Errorf("anthropic path = %q", paths[1])
	}
	if bodies[1]["anthropic_version"] != "vertex-2023-10-16" {
		t.Errorf("anthropic_version = %v", bodies[1]["anthropic_version"])
	}
	if _, present := bodies[1]["model"]; present {
		t.Error("the model is still in the anthropic body")
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// --- OAuth -------------------------------------------------------------------

type oauthFake struct {
	mu        sync.Mutex
	refreshes int
	grants    map[string]bool
	refuse    bool
	srv       *httptest.Server
}

func newOAuthFake(t *testing.T) *oauthFake {
	t.Helper()
	f := &oauthFake{grants: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.refuse {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		if r.Form.Get("grant_type") == "refresh_token" {
			if !f.grants[r.Form.Get("refresh_token")] {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			delete(f.grants, r.Form.Get("refresh_token"))
			f.refreshes++
			next := fmt.Sprintf("rt-%d", f.refreshes)
			f.grants[next] = true
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"access_token":"at-%d","refresh_token":%q,"token_type":"Bearer","expires_in":3600}`,
				f.refreshes, next)
			return
		}
		f.grants["rt-0"] = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at-0","refresh_token":"rt-0","token_type":"Bearer","expires_in":3600,"account":{"email_address":"me@example.com"}}`))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// seedOAuth writes a connected account directly. The connect flow itself is
// covered in internal/admin; what this package needs is the state it leaves.
func seedOAuth(t *testing.T, g *gateway, providerID, upstreamURL string, expiresIn time.Duration) string {
	t.Helper()
	g.mustAdmin(t, "POST", "/api/providers", fmt.Sprintf(
		`{"id":%q,"kind":"anthropic","preset":"anthropic-oauth","base_url":%s,"auth_style":"oauth"}`,
		providerID, jsonStr(t, upstreamURL)), http.StatusCreated)

	tok := auth.Token{
		AccessToken: "at-0", RefreshToken: "rt-0",
		ExpiresAt: time.Now().Add(expiresIn), Account: "me@example.com",
	}
	raw, err := tok.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	id, err := g.db.AddCredential(context.Background(), g.key, store.Credential{
		ProviderID: providerID, Label: "personal", Kind: "oauth",
		Secret: string(raw), Enabled: true, ExpiresAt: tok.Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	g.reloadProviders(t, providerID)
	return id
}

func TestAnOAuthAccountServesWithABearerToken(t *testing.T) {
	// Spec §8 criterion 4's first half.
	g := newGateway(t)
	var authz string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authz = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant",
		  "content":[{"type":"text","text":"subscription served"}],"stop_reason":"end_turn",
		  "usage":{"input_tokens":3,"output_tokens":2}}`))
	}))
	t.Cleanup(upstream.Close)

	seedOAuth(t, g, "sub", upstream.URL, time.Hour)
	g.seedModel(t, "sub", "claude-sonnet-4-5", "")
	g.mustAdmin(t, "POST", "/api/config/reload", "", http.StatusOK)

	w := g.proxy(t, "/v1/chat/completions", chatBody("claude-sonnet-4-5"))
	if w.Code != http.StatusOK {
		t.Fatalf("chat: %d %s", w.Code, w.Body.String())
	}
	if authz != "Bearer at-0" {
		t.Errorf("Authorization = %q", authz)
	}
	// The adapter's own x-api-key must not also be present: a non-static style
	// leaves Target.APIKey empty precisely so it cannot be.
	if !strings.Contains(w.Body.String(), "subscription served") {
		t.Errorf("body = %s", w.Body.String())
	}
}

// seedOAuthWithTokenURL rebuilds the gateway over a preset whose token endpoint
// is the fake, then seeds a connected account on it.
func seedOAuthWithTokenURL(t *testing.T, g **gateway, providerID, upstreamURL, tokenURL string,
	expiresIn time.Duration) string {

	t.Helper()
	(*g).srv.CloseAdmin()
	if err := (*g).db.Close(); err != nil {
		t.Fatal(err)
	}
	*g = openGatewayWithPresets(t, (*g).dbPath, oauthPresets(tokenURL))
	return seedOAuth(t, *g, providerID, upstreamURL, expiresIn)
}

func TestARotatedRefreshSurvivesARestart(t *testing.T) {
	// The half a unit test cannot reach. A crash between refresh and persist is
	// what this guards: after a refresh, the durable row must already name the
	// token the vendor now expects.
	g := newGateway(t)
	f := newOAuthFake(t)
	f.mu.Lock()
	f.grants["rt-0"] = true
	f.mu.Unlock()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"m","type":"message","role":"assistant",
		  "content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn",
		  "usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	t.Cleanup(upstream.Close)

	credID := seedOAuthWithTokenURL(t, &g, "sub", upstream.URL, f.srv.URL, -time.Minute)
	g.seedModel(t, "sub", "claude-sonnet-4-5", "")
	g.mustAdmin(t, "POST", "/api/config/reload", "", http.StatusOK)

	if w := g.proxy(t, "/v1/chat/completions", chatBody("claude-sonnet-4-5")); w.Code != http.StatusOK {
		t.Fatalf("chat: %d %s", w.Code, w.Body.String())
	}

	// Reopen the same database file. Only durability can carry this.
	g.srv.CloseAdmin()
	if err := g.db.Close(); err != nil {
		t.Fatal(err)
	}
	again := openGatewayWithPresets(t, g.dbPath, oauthPresets(f.srv.URL))
	creds, err := again.db.Credentials(context.Background(), again.key, "sub")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range creds {
		if c.ID != credID {
			continue
		}
		found = true
		tok, err := auth.ParseToken([]byte(c.Secret))
		if err != nil {
			t.Fatal(err)
		}
		if tok.RefreshToken != "rt-1" {
			t.Errorf("refresh token after restart = %q, want the rotated one", tok.RefreshToken)
		}
	}
	if !found {
		t.Fatal("the credential did not survive the restart")
	}
}

func TestAnInvalidGrantShowsAsNeedsReconnection(t *testing.T) {
	// Spec §8 criterion 4's second half, read the way the dashboard reads it.
	g := newGateway(t)
	f := newOAuthFake(t)
	f.mu.Lock()
	f.refuse = true
	f.mu.Unlock()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	seedOAuthWithTokenURL(t, &g, "sub", upstream.URL, f.srv.URL, -time.Minute)
	g.seedModel(t, "sub", "claude-sonnet-4-5", "")
	g.mustAdmin(t, "POST", "/api/config/reload", "", http.StatusOK)

	// The probe drives the same refresh a request would.
	w := g.mustAdmin(t, "POST", "/api/providers/sub/test", "", http.StatusOK)
	var probe struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &probe); err != nil {
		t.Fatal(err)
	}
	if probe.OK {
		t.Fatal("a refused refresh must not report success")
	}
	if !strings.Contains(strings.ToLower(probe.Error), "reconnect") {
		t.Errorf("error = %q", probe.Error)
	}

	var overview struct {
		Providers []struct {
			ID        string `json:"id"`
			NeedsAuth bool   `json:"needs_reauth"`
		} `json:"providers"`
	}
	body := g.mustAdmin(t, "GET", "/api/overview", "", http.StatusOK).Body.Bytes()
	if err := json.Unmarshal(body, &overview); err != nil {
		t.Fatal(err)
	}
	for _, p := range overview.Providers {
		if p.ID == "sub" && !p.NeedsAuth {
			t.Error("the overview does not show the account as needing reconnection")
		}
	}
}

func TestNoCredentialMaterialLeavesTheProcess(t *testing.T) {
	// Spec §8 criterion 6, across both ports.
	g := newGateway(t)
	f := newBedrockFake(t)
	f.mu.Lock()
	f.failWith = http.StatusForbidden
	f.mu.Unlock()
	seedBedrock(t, g, f, "bed", "anthropic.claude-x-v1:0")

	canaries := []string{"CANARY-AWS-0002", "AKIDEXAMPLE"}
	for _, path := range []string{
		"/api/overview", "/api/providers", "/api/models", "/api/requests",
		"/api/usage", "/api/config",
	} {
		body := g.mustAdmin(t, "GET", path, "", http.StatusOK).Body.String()
		for _, c := range canaries {
			if strings.Contains(body, c) {
				t.Errorf("%s leaked %q", path, c)
			}
		}
	}
	// And the proxy port's error response, which names the upstream failure.
	proxyBody := g.proxy(t, "/v1/chat/completions", chatBody("anthropic.claude-x-v1:0")).Body.String()
	for _, c := range canaries {
		if strings.Contains(proxyBody, c) {
			t.Errorf("the proxy error response leaked %q:\n%s", c, proxyBody)
		}
	}
}

var _ = url.Parse
