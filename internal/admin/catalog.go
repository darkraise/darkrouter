package admin

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/ir"
)

// pricingView is micro-dollars per million tokens. Null rather than a zeroed
// object when the catalog has no price: catalog.Pricing carries Known for
// exactly this reason, since a free model and an unpriced one are both zero
// and only one of them may be printed as a number.
type pricingView struct {
	InputMicros  int64 `json:"input_micros"`
	OutputMicros int64 `json:"output_micros"`
}

type modelView struct {
	Model         string   `json:"model"`
	Providers     []string `json:"providers"`
	Surfaces      []string `json:"surfaces"`
	ContextWindow int      `json:"context_window"`
	MaxOutput     int      `json:"max_output_tokens"`
	Tools         bool     `json:"tools"`
	Vision        bool     `json:"vision"`
	Reasoning     bool     `json:"reasoning"`
	// Inferred marks a row whose capabilities were guessed rather than read.
	// Master design §6.4 routes these with a warning, and an operator needs to
	// know which they are.
	Inferred bool         `json:"inferred"`
	State    string       `json:"state"`
	Pricing  *pricingView `json:"pricing"`
	// Publisher and MergeSource come from the first provider in catalog order,
	// which is priority order — the row folds several providers into one, and
	// where they disagree the one the router would reach first is the answer
	// that matches what a request would actually cost.
	Publisher   string `json:"publisher,omitempty"`
	MergeSource string `json:"merge_source"`
}

type aliasView struct {
	Name    string   `json:"name"`
	Targets []string `json:"targets"`
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	models := []modelView{}
	if s.deps.Catalog != nil {
		models = s.collectModels(r)
	}
	aliases := []aliasView{}
	if s.deps.Config != nil {
		for name, chain := range s.deps.Config.Current().Aliases {
			aliases = append(aliases, aliasView{Name: name, Targets: chain})
		}
		// Sorted: a map iteration order would reshuffle the list on every poll.
		sort.Slice(aliases, func(i, j int) bool { return aliases[i].Name < aliases[j].Name })
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models, "aliases": aliases})
}

// collectModels folds the per-provider catalog rows into one row per model name,
// which is the shape the screen shows: "which providers serve this model".
func (s *Server) collectModels(r *http.Request) []modelView {
	q := r.URL.Query()
	search := strings.ToLower(q.Get("q"))
	surface := q.Get("surface")
	minContext, _ := strconv.Atoi(q.Get("min_context"))
	wantTools := q.Get("tools") == "true"

	byModel := map[string]*modelView{}
	order := []string{}
	for _, m := range s.deps.Catalog.Snapshot().All() {
		if search != "" && !strings.Contains(strings.ToLower(m.ModelID), search) {
			continue
		}
		if surface != "" && !m.DeclaresSurface(ir.Surface(surface)) {
			continue
		}
		if minContext > 0 && m.ContextWindow < minContext {
			continue
		}
		if wantTools && !m.Capabilities.Tools {
			continue
		}
		v, ok := byModel[m.ModelID]
		if !ok {
			v = &modelView{
				Model: m.ModelID, Providers: []string{},
				Surfaces: surfaceNames(m.Surfaces), State: string(m.State),
				ContextWindow: m.ContextWindow, MaxOutput: m.MaxOutputTokens,
				Tools: m.Capabilities.Tools, Vision: m.Capabilities.Vision,
				Reasoning:   m.Capabilities.Reasoning,
				Publisher:   m.Publisher,
				MergeSource: string(m.Source),
			}
			if m.Pricing.Known {
				v.Pricing = &pricingView{
					InputMicros:  m.Pricing.InputMicrosPerMTok,
					OutputMicros: m.Pricing.OutputMicrosPerMTok,
				}
			}
			byModel[m.ModelID] = v
			order = append(order, m.ModelID)
		}
		v.Providers = append(v.Providers, m.ProviderID)
		// Inferred if ANY provider's row is a guess. The operator is warned
		// about the weakest row, because that is the one that may route badly.
		if !m.Capabilities.Known {
			v.Inferred = true
		}
	}

	out := make([]modelView, 0, len(order))
	for _, id := range order {
		v := byModel[id]
		sort.Strings(v.Providers)
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	return out
}

func surfaceNames(ss []ir.Surface) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, string(s))
	}
	return out
}

var _ = catalog.Model{}
