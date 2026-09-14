package bedrock

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ListInferenceProfiles is paginated. An account with more profiles than one
// page holds would otherwise lose every profile after the first page.
func TestProfileListingFollowsEveryPage(t *testing.T) {
	var tokens []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/foundation-models":
			_, _ = w.Write([]byte(`{"modelSummaries":[]}`))
		case "/inference-profiles":
			token := r.URL.Query().Get("nextToken")
			tokens = append(tokens, token)
			switch token {
			case "":
				_, _ = w.Write([]byte(`{"inferenceProfileSummaries":[
				  {"inferenceProfileId":"us.anthropic.claude-opus-4-7","status":"ACTIVE"}],
				  "nextToken":"page+2/="}`))
			case "page+2/=":
				_, _ = w.Write([]byte(`{"inferenceProfileSummaries":[
				  {"inferenceProfileId":"eu.anthropic.claude-sonnet-5","status":"ACTIVE"}]}`))
			default:
				w.WriteHeader(http.StatusBadRequest)
			}
		}
	}))
	t.Cleanup(srv.Close)

	got, err := NewLister(srv.Client()).List(context.Background(), listerProbe(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, d := range got {
		ids[d.ModelID] = true
	}
	if !ids["us.anthropic.claude-opus-4-7"] || !ids["eu.anthropic.claude-sonnet-5"] {
		t.Errorf("profiles = %v, want both pages", ids)
	}
	if len(tokens) != 2 || tokens[1] != "page+2/=" {
		t.Errorf("requested tokens %q, want the first page and then page+2/=", tokens)
	}
}

// A control plane that hands back the token it was sent would page forever.
func TestProfileListingRefusesARepeatedPageToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/foundation-models" {
			_, _ = w.Write([]byte(`{"modelSummaries":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"inferenceProfileSummaries":[],"nextToken":"same"}`))
	}))
	t.Cleanup(srv.Close)

	if _, err := NewLister(srv.Client()).List(context.Background(), listerProbe(srv.URL)); err == nil {
		t.Fatal("a repeated page token was followed without end or error")
	}
}
