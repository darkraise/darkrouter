package bedrock

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/darkraise/darkrouter/internal/catalog"
)

// awsFake serves both control-plane listings from one handler, so the test can
// assert which paths were actually called.
func awsFake(t *testing.T, models, profiles string) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/foundation-models":
			_, _ = w.Write([]byte(models))
		case "/inference-profiles":
			_, _ = w.Write([]byte(profiles))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &paths
}

const foundationModels = `{"modelSummaries":[
  {"modelId":"anthropic.claude-3-5-sonnet-20241022-v2:0","modelName":"Claude 3.5 Sonnet",
   "inferenceTypesSupported":["INFERENCE_PROFILE"],"modelLifecycle":{"status":"ACTIVE"}},
  {"modelId":"amazon.titan-text-express-v1","modelName":"Titan Text Express",
   "inferenceTypesSupported":["ON_DEMAND"],"modelLifecycle":{"status":"ACTIVE"}},
  {"modelId":"amazon.retired-v1","modelName":"Retired",
   "inferenceTypesSupported":["ON_DEMAND"],"modelLifecycle":{"status":"LEGACY"}},
  {"modelId":"amazon.provisioned-only-v1","modelName":"Provisioned",
   "inferenceTypesSupported":["PROVISIONED"],"modelLifecycle":{"status":"ACTIVE"}}
]}`

const inferenceProfiles = `{"inferenceProfileSummaries":[
  {"inferenceProfileId":"us.anthropic.claude-3-5-sonnet-20241022-v2:0",
   "inferenceProfileName":"US Claude 3.5 Sonnet","status":"ACTIVE","type":"SYSTEM_DEFINED",
   "models":[{"modelArn":"arn:aws:bedrock:us-east-1::foundation-model/anthropic.claude-3-5-sonnet-20241022-v2:0"}]}
]}`

func listerProbe(base string) catalog.Probe {
	return catalog.Probe{
		ProviderID: "bed", Kind: "bedrock", BaseURL: base, Region: "us-east-1",
		Authorize: func(context.Context, *http.Request) error { return nil },
	}
}

func TestListerCallsBothEndpoints(t *testing.T) {
	srv, paths := awsFake(t, foundationModels, inferenceProfiles)
	if _, err := NewLister(srv.Client()).List(context.Background(), listerProbe(srv.URL)); err != nil {
		t.Fatal(err)
	}
	if len(*paths) != 2 {
		t.Fatalf("called %v, want both listings", *paths)
	}
}

func TestProfileIdsAreTheRoutableIdentifiers(t *testing.T) {
	srv, _ := awsFake(t, foundationModels, inferenceProfiles)
	got, err := NewLister(srv.Client()).List(context.Background(), listerProbe(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, d := range got {
		ids[d.ModelID] = true
	}
	// The profile id, because that is what an invocation must name.
	if !ids["us.anthropic.claude-3-5-sonnet-20241022-v2:0"] {
		t.Errorf("the inference profile is missing: %v", ids)
	}
	// The bare id it covers must NOT be catalogued: routing to it returns a
	// 400 telling the operator to use a profile, which is a worse error than
	// not offering the model.
	if ids["anthropic.claude-3-5-sonnet-20241022-v2:0"] {
		t.Errorf("the bare id behind a profile was catalogued: %v", ids)
	}
	// An on-demand model with no profile is catalogued as itself.
	if !ids["amazon.titan-text-express-v1"] {
		t.Errorf("an on-demand model is missing: %v", ids)
	}
}

func TestARetiredModelIsNotCatalogued(t *testing.T) {
	srv, _ := awsFake(t, foundationModels, inferenceProfiles)
	got, _ := NewLister(srv.Client()).List(context.Background(), listerProbe(srv.URL))
	for _, d := range got {
		if d.ModelID == "amazon.retired-v1" {
			t.Error("a LEGACY model was catalogued")
		}
	}
}

func TestAProvisionedOnlyModelIsNotCatalogued(t *testing.T) {
	// It cannot be invoked by id, so storing it stores an identifier that 400s.
	srv, _ := awsFake(t, foundationModels, inferenceProfiles)
	got, _ := NewLister(srv.Client()).List(context.Background(), listerProbe(srv.URL))
	for _, d := range got {
		if d.ModelID == "amazon.provisioned-only-v1" {
			t.Error("a PROVISIONED-only model was catalogued")
		}
	}
}

func TestListerSignsBothCalls(t *testing.T) {
	// The control plane refuses an unsigned request. A lister that signed one
	// call and not the other would half-work and be diagnosed as a permissions
	// problem.
	srv, _ := awsFake(t, foundationModels, inferenceProfiles)
	signed := 0
	p := listerProbe(srv.URL)
	p.Authorize = func(_ context.Context, r *http.Request) error {
		signed++
		r.Header.Set("Authorization", "signed")
		return nil
	}
	if _, err := NewLister(srv.Client()).List(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if signed != 2 {
		t.Errorf("signed %d requests, want 2", signed)
	}
}

func TestListerRefusesWithoutAnAuthorizer(t *testing.T) {
	srv, _ := awsFake(t, foundationModels, inferenceProfiles)
	p := listerProbe(srv.URL)
	p.Authorize = nil
	if _, err := NewLister(srv.Client()).List(context.Background(), p); err == nil {
		t.Fatal("an unsigned control-plane call must be refused rather than sent")
	}
}

func TestListerUsesTheControlPlaneNotTheRuntime(t *testing.T) {
	// The two are different hosts sharing one signing name. A listing sent to
	// the runtime host is a 404 that reads like a missing model.
	p := listerProbe("https://bedrock-runtime.us-east-1.amazonaws.com")
	p.Authorize = func(context.Context, *http.Request) error { return nil }
	var seen string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seen = r.URL.Host
		return nil, errStop
	})}
	_, _ = NewLister(client).List(context.Background(), p)
	if seen != "bedrock.us-east-1.amazonaws.com" {
		t.Errorf("called %q, want the control plane", seen)
	}
}

var errStop = &stopError{}

type stopError struct{}

func (*stopError) Error() string { return "stop" }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
