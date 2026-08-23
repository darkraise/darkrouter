package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type catalogBody struct {
	Models []struct {
		Model     string   `json:"model"`
		Providers []string `json:"providers"`
		Surfaces  []string `json:"surfaces"`
		Inferred  bool     `json:"inferred"`
	} `json:"models"`
	Aliases []struct {
		Name    string   `json:"name"`
		Targets []string `json:"targets"`
	} `json:"aliases"`
}

func getCatalog(t *testing.T, s *Server, query string) catalogBody {
	t.Helper()
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "GET", "/api/models"+query, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body catalogBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestTheCatalogListsModelsAcrossProviders(t *testing.T) {
	s, _ := testServerWithCatalog(t, "")
	body := getCatalog(t, s, "")

	var shared bool
	for _, m := range body.Models {
		if m.Model == "shared-model" {
			shared = true
			if len(m.Providers) != 2 {
				t.Errorf("shared-model providers = %v, want two", m.Providers)
			}
		}
	}
	if !shared {
		t.Fatalf("shared-model is missing: %+v", body.Models)
	}
	if len(body.Models) != 3 {
		t.Errorf("got %d rows for 4 catalog entries; the fold by model name is wrong",
			len(body.Models))
	}
}

func TestTheCatalogMarksInferredMetadata(t *testing.T) {
	// Master design §6.4 routes a guessed model with a warning. An operator
	// reading the catalog needs to see which rows are guesses, or a guessed
	// row that refuses tool calls looks like a Darkrouter bug.
	s, _ := testServerWithCatalog(t, "")
	body := getCatalog(t, s, "")

	var found bool
	for _, m := range body.Models {
		if m.Model == "guessed-model" {
			found = true
			if !m.Inferred {
				t.Error("guessed-model is not marked inferred")
			}
		}
		if m.Model == "known-model" && m.Inferred {
			t.Error("known-model is marked inferred")
		}
	}
	if !found {
		t.Error("guessed-model is missing")
	}
}

func TestTheCatalogFiltersBySurface(t *testing.T) {
	s, _ := testServerWithCatalog(t, "")
	body := getCatalog(t, s, "?surface=embedding")

	if len(body.Models) != 1 || body.Models[0].Model != "guessed-model" {
		t.Fatalf("the embedding filter returned %+v", body.Models)
	}
}

func TestTheCatalogSearchesBySubstring(t *testing.T) {
	s, _ := testServerWithCatalog(t, "")
	body := getCatalog(t, s, "?q=guess")

	if len(body.Models) != 1 || body.Models[0].Model != "guessed-model" {
		t.Errorf("search = %+v", body.Models)
	}
}

func TestTheCatalogFiltersByContextWindow(t *testing.T) {
	s, _ := testServerWithCatalog(t, "")
	body := getCatalog(t, s, "?min_context=100000")

	if len(body.Models) != 1 || body.Models[0].Model != "shared-model" {
		t.Errorf("min_context = %+v", body.Models)
	}
}

func TestTheCatalogReportsWhatEachAliasResolvesTo(t *testing.T) {
	// The chain lives in the configuration and the catalog lives in the
	// database. Joining them in the browser would duplicate resolution rules
	// the router already owns.
	s, _ := testServerWithCatalog(t, "  fast:\n    - a/shared-model\n    - b/shared-model\n")
	body := getCatalog(t, s, "")

	if len(body.Aliases) != 1 || body.Aliases[0].Name != "fast" {
		t.Fatalf("aliases = %+v", body.Aliases)
	}
	if len(body.Aliases[0].Targets) != 2 || body.Aliases[0].Targets[0] != "a/shared-model" {
		t.Errorf("targets = %v", body.Aliases[0].Targets)
	}
}

func TestAnEmptyCatalogReturnsArraysNotNull(t *testing.T) {
	// Both lists are ranged over by the screen.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "GET", "/api/models", "")
	got := w.Body.String()
	if !strings.Contains(got, `"models":[]`) || !strings.Contains(got, `"aliases":[]`) {
		t.Errorf("body = %s", got)
	}
}
