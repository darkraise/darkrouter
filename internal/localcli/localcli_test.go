package localcli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeCLI answers without spawning anything, so the transport's own behaviour
// is under test rather than a program's.
type fakeCLI struct {
	models []string
	chunks []string
	err    error
	sawModel,
	sawPrompt string
}

func (f *fakeCLI) Scheme() string { return "fake" }

func (f *fakeCLI) Models(context.Context) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.models, nil
}

func (f *fakeCLI) Run(_ context.Context, model, prompt string, out io.Writer) error {
	f.sawModel, f.sawPrompt = model, prompt
	for _, c := range f.chunks {
		if _, err := io.WriteString(out, c); err != nil {
			return err
		}
	}
	return f.err
}

func do(t *testing.T, cli CLI, method, url, body string) *http.Response {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := NewTransport(cli).RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	return resp
}

func TestAListingLooksLikeAnOpenAIOne(t *testing.T) {
	// The discovery sweep parses this with the same code it uses for a remote
	// provider; a shape of its own would need a second parser.
	f := &fakeCLI{models: []string{"sonnet4.6", "gpt5.4"}}
	resp := do(t, f, "GET", "fake://cli/v1/models", "")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Object != "list" || len(got.Data) != 2 ||
		got.Data[0].ID != "sonnet4.6" || got.Data[1].ID != "gpt5.4" {
		t.Fatalf("listing = %+v", got)
	}
}

func TestAFailedListingIsAProviderAnswerNotATransportError(t *testing.T) {
	// A transport error is a connection fact and the executor retries it. A
	// CLI that is missing or logged out is neither, and the operator needs the
	// message rather than "unsupported protocol scheme".
	f := &fakeCLI{err: errors.New("auggie exited 1: not logged in")}
	resp := do(t, f, "GET", "fake://cli/v1/models", "")
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "not logged in") {
		t.Errorf("the reason did not reach the caller: %s", b)
	}
}

func TestANonStreamingChatIsOneCompletion(t *testing.T) {
	f := &fakeCLI{chunks: []string{"Hi ", "there."}}
	resp := do(t, f, "POST", "fake://cli/v1/chat/completions",
		`{"model":"sonnet4.6","messages":[{"role":"user","content":"hello"}]}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Object != "chat.completion" || len(got.Choices) != 1 {
		t.Fatalf("completion = %+v", got)
	}
	if got.Choices[0].Message.Content != "Hi there." {
		t.Errorf("content = %q, want the whole reply", got.Choices[0].Message.Content)
	}
	if got.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason = %q", got.Choices[0].FinishReason)
	}
	if f.sawModel != "sonnet4.6" {
		t.Errorf("model = %q", f.sawModel)
	}
}

func TestAStreamingChatEmitsRoleThenDeltasThenStop(t *testing.T) {
	f := &fakeCLI{chunks: []string{"one ", "two"}}
	resp := do(t, f, "POST", "fake://cli/v1/chat/completions",
		`{"model":"m","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}

	var roles, contents []string
	var finish string
	var done bool
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			done = true
			continue
		}
		var ev struct {
			Object  string `json:"object"`
			Choices []struct {
				Delta struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			t.Fatalf("chunk is not JSON: %s", payload)
		}
		if ev.Object != "chat.completion.chunk" {
			t.Errorf("object = %q", ev.Object)
		}
		c := ev.Choices[0]
		if c.Delta.Role != "" {
			roles = append(roles, c.Delta.Role)
		}
		if c.Delta.Content != "" {
			contents = append(contents, c.Delta.Content)
		}
		if c.FinishReason != nil {
			finish = *c.FinishReason
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(roles) != 1 || roles[0] != "assistant" {
		t.Errorf("roles = %v, want exactly one assistant chunk", roles)
	}
	if strings.Join(contents, "") != "one two" {
		t.Errorf("content = %q", strings.Join(contents, ""))
	}
	if finish != "stop" {
		t.Errorf("finish_reason = %q", finish)
	}
	if !done {
		t.Error("the stream never terminated with [DONE]")
	}
}

func TestAStreamThatFailsBeforeSayingAnythingReportsTheError(t *testing.T) {
	f := &fakeCLI{err: errors.New("auggie exited 1: not logged in")}
	resp := do(t, f, "POST", "fake://cli/v1/chat/completions",
		`{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "not logged in") {
		t.Errorf("the failure was swallowed: %s", b)
	}
	if !strings.Contains(string(b), "[DONE]") {
		t.Errorf("the stream did not terminate: %s", b)
	}
}

func TestASurfaceTheProgramDoesNotServeIs404(t *testing.T) {
	// Fatal rather than retryable: no number of attempts turns a CLI into an
	// embeddings endpoint.
	resp := do(t, &fakeCLI{}, "POST", "fake://cli/v1/embeddings", `{}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestThePromptKeepsWhoSaidWhat(t *testing.T) {
	// One string on stdin: without the labels the model reads its own previous
	// answer as something the user wrote.
	f := &fakeCLI{}
	do(t, f, "POST", "fake://cli/v1/chat/completions", `{"model":"m","messages":[
		{"role":"system","content":"be brief"},
		{"role":"user","content":"hello"},
		{"role":"assistant","content":"hi"},
		{"role":"user","content":[{"type":"text","text":"again"},{"type":"image_url"}]}]}`)
	want := "[System]\nbe brief\n\n[User]\nhello\n\n[Assistant]\nhi\n\n[User]\nagain"
	if f.sawPrompt != want {
		t.Errorf("prompt =\n%q\nwant\n%q", f.sawPrompt, want)
	}
}

func TestAnEmptyConversationStillSendsAPrompt(t *testing.T) {
	f := &fakeCLI{}
	do(t, f, "POST", "fake://cli/v1/chat/completions", `{"model":"m","messages":[]}`)
	if f.sawPrompt != "(empty)" {
		t.Errorf("prompt = %q; an empty stdin makes some CLIs wait forever", f.sawPrompt)
	}
}

func TestAModelNameThatCouldBecomeAFlagIsRefused(t *testing.T) {
	// The model reaches argv. Anything that could be read as an option must
	// not get there, and the check lives below the transport so every caller
	// gets it.
	for _, bad := range []string{"--print", "-m", "a b", "a;b", "a$(id)", "a|b", "a\nb"} {
		if _, err := validateModel(bad); err == nil {
			t.Errorf("%q was accepted as a model name", bad)
		}
	}
	for _, ok := range []string{"sonnet4.6", "gpt5.6-luna", "gemini-3.1-pro-preview", "a/b:c"} {
		if _, err := validateModel(ok); err != nil {
			t.Errorf("%q was refused: %v", ok, err)
		}
	}
	// An unnamed model is the CLI's own default rather than an error.
	got, err := validateModel("  ")
	if err != nil || got != "" {
		t.Errorf("empty model = (%q, %v)", got, err)
	}
}

func TestTheModelRosterIsReadFromTheCLIsOwnOutput(t *testing.T) {
	// `auggie model list` prints a human roster, not JSON.
	got := parseModelList("Available models:\n  Sonnet 4.6 [sonnet4.6]\n  GPT-5.4 [gpt5.4]\n" +
		"  duplicate [sonnet4.6]\n  malformed [ --flag ]\nno bracket here\n")
	if len(got) != 2 || got[0] != "sonnet4.6" || got[1] != "gpt5.4" {
		t.Fatalf("models = %v", got)
	}
}

// stubAuggie writes a script that answers the three invocations the real CLI
// answers, so the spawn path — argv, stdin, stdout, exit codes — is exercised
// without Augment's binary or an account.
func stubAuggie(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shell script")
	}
	path := filepath.Join(t.TempDir(), "auggie")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTheSpawnPathListsModels(t *testing.T) {
	bin := stubAuggie(t, `
case "$1 $2" in
  "model list") echo "Sonnet 4.6 [sonnet4.6]"; echo "GPT-5.4 [gpt5.4]"; exit 0 ;;
esac
exit 64
`)
	got, err := (&Auggie{Bin: bin}).Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "sonnet4.6" {
		t.Fatalf("models = %v", got)
	}
}

func TestTheSpawnPathSendsThePromptOnStdinAndReadsTheReply(t *testing.T) {
	// The prompt must never be an argument: it is arbitrary user text. The
	// stub proves where it actually arrives by echoing stdin back.
	bin := stubAuggie(t, `
printf 'argv:%s\n' "$*"
cat
`)
	var out strings.Builder
	if err := (&Auggie{Bin: bin}).Run(context.Background(), "sonnet4.6", "hello there", &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "argv:--print --quiet --model sonnet4.6 --") {
		t.Errorf("argv = %q", got)
	}
	if !strings.Contains(got, "hello there") {
		t.Errorf("the prompt did not reach stdin: %q", got)
	}
	if strings.Contains(strings.SplitN(got, "\n", 2)[0], "hello there") {
		t.Error("the prompt was passed as an argument")
	}
}

func TestAnEmptyRosterCarriesTheCLIsOwnDiagnostic(t *testing.T) {
	// `auggie model list` exits 0 when it is logged out and explains itself on
	// stderr. Reporting only "no models" would send the operator looking for an
	// empty account instead of running auggie login.
	bin := stubAuggie(t, `echo "You are not currently logged in to Augment." >&2; exit 0`)
	_, err := (&Auggie{Bin: bin}).Models(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not currently logged in") {
		t.Fatalf("error = %v", err)
	}
}

func TestAFailedSpawnSaysWhichFailureItWas(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-installed")
	err := (&Auggie{Bin: missing}).Run(context.Background(), "m", "hi", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "auggie login") {
		t.Fatalf("a missing binary must name the fix: %v", err)
	}

	bin := stubAuggie(t, `echo "not logged in" >&2; exit 1`)
	err = (&Auggie{Bin: bin}).Run(context.Background(), "m", "hi", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("the CLI's own diagnostic must survive: %v", err)
	}
}

func TestAConfiguredSessionReachesTheChildAsAnEnvironmentVariable(t *testing.T) {
	// The operator pastes their Augment session into the console; it is stored
	// and rotated like any other credential, arrives here in the Authorization
	// header the adapter wrote, and must reach the CLI as the environment
	// variable it reads. Never as an argument: argv is readable by every other
	// process on the box.
	bin := stubAuggie(t, `
cat >/dev/null
printf 'session=%s argv=%s' "$AUGMENT_SESSION_AUTH" "$*"
`)
	tr := &http.Transport{}
	Install(tr, &Auggie{Bin: bin})
	client := &http.Client{Transport: tr}

	req, err := http.NewRequest("POST", "auggie://cli/v1/chat/completions",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer {\"accessToken\":\"secret-session\"}")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var got struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	saw := got.Choices[0].Message.Content
	session, argv, _ := strings.Cut(saw, " argv=")
	if session != `session={"accessToken":"secret-session"}` {
		t.Errorf("the session did not reach the child: %q", saw)
	}
	if strings.Contains(argv, "secret-session") {
		t.Errorf("the session leaked into argv: %q", argv)
	}
}

func TestNoConfiguredSessionLeavesTheCLIsOwnLoginAlone(t *testing.T) {
	// A provider with no account added must not have the variable set at all:
	// an empty AUGMENT_SESSION_AUTH would override the login the operator did
	// inside the container with nothing.
	bin := stubAuggie(t, `
cat >/dev/null
printf 'set=%s' "${AUGMENT_SESSION_AUTH+yes}"
`)
	var out strings.Builder
	if err := (&Auggie{Bin: bin}).Run(context.Background(), "m", "hi", &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "set=yes") {
		t.Errorf("the variable was set with no session configured: %s", out.String())
	}
}

func TestTheTransportServesTheSpawnedCLIEndToEnd(t *testing.T) {
	// The whole path an operator's request takes: an OpenAI request in, a
	// process spawned, OpenAI SSE out.
	bin := stubAuggie(t, `cat >/dev/null; printf 'spawned reply'`)
	tr := &http.Transport{}
	Install(tr, &Auggie{Bin: bin})
	client := &http.Client{Transport: tr}

	req, err := http.NewRequest("POST", "auggie://cli/v1/chat/completions",
		strings.NewReader(`{"model":"sonnet4.6","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("the client could not reach the scheme: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "spawned reply") {
		t.Errorf("the CLI's output did not reach the stream: %s", b)
	}
	if !strings.Contains(string(b), `"finish_reason":"stop"`) {
		t.Errorf("the stream did not finish cleanly: %s", b)
	}
}
