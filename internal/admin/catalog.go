package admin

import (
	"context"
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
	// Source and Grade let the console mark a price the seller quoted itself,
	// and caution one that was guessed. Grade is derived rather than stored so
	// the console never has to know the source vocabulary.
	Source string `json:"price_source"`
	Grade  string `json:"price_grade"`
}

// freeTierView is upstream's free-allowance record for a model. Null rather
// than a zeroed object when the model has no free tier: an uncapped allowance
// and no allowance at all are both zero, and only one of them is free.
type freeTierView struct {
	FreeType      string `json:"free_type"`
	MonthlyTokens int64  `json:"monthly_tokens"`
	CreditTokens  int64  `json:"credit_tokens"`
	PoolKey       string `json:"pool_key"`
	// ToS is upstream's verdict on how the vendor regards this access: ok,
	// caution, ambiguous, avoid or unknown.
	ToS string `json:"tos"`
	// OptInRequired says the router refuses at least one provider serving this
	// model over its free tier, and the operator can lift that by allowing
	// unsanctioned tiers on the provider.
	//
	// Folded here rather than left to the console, which has the model list
	// and not the provider list: without it the row can only read the vendor's
	// verdict, and every `avoid` row goes on telling an operator who has
	// already opted in to go and opt in.
	OptInRequired bool `json:"opt_in_required"`
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
	// FreeTier is folded across every provider serving the model rather than
	// taken from one of them — see foldTier.
	FreeTier *freeTierView `json:"free_tier"`
	// Publisher and MergeSource come from whichever provider the snapshot
	// lists first, which is provider id order, not priority. The row folds
	// several providers into one and they seldom disagree; a fixed order is
	// there so the value does not reshuffle between polls, not because the
	// first provider is the one a request would reach.
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
		optedIn, err := s.unsanctionedOptIns(r.Context())
		if err != nil {
			internalError(w, r, err)
			return
		}
		models = s.collectModels(r, optedIn)
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

// unsanctionedOptIns names the providers the operator has accepted unsanctioned
// free tiers on.
//
// Read from the stored rows rather than the live provider set because this is
// the operator's decision, and a provider that is disabled or has lost its
// credential has still had it made. A row that told them to make it again
// would be asking for a switch already thrown.
func (s *Server) unsanctionedOptIns(ctx context.Context) (map[string]bool, error) {
	rows, err := s.deps.DB.ProviderRows(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, p := range rows {
		if p.AllowUnsanctionedFree {
			out[p.ID] = true
		}
	}
	return out, nil
}

// foldTier keeps the free-tier record the row has to show, out of the several
// its providers carry.
//
// An unsanctioned tier outranks a sanctioned one because the router's veto is
// per candidate: one provider serving this model on terms the vendor has not
// sanctioned is one the router refuses, whatever the others are graded. Taking
// whichever record the snapshot listed first made that an accident of provider
// id ordering — a row warned or stayed silent depending on how its providers
// happened to sort.
func foldTier(kept, next catalog.FreeTier) catalog.FreeTier {
	// ToS, not FreeType, marks the absent record: every upstream row carries a
	// verdict, while a discontinued tier is a real record whose withdrawal a
	// reader still needs to see.
	if next.ToS == "" {
		return kept
	}
	if kept.ToS == "" || (next.Unsanctioned() && !kept.Unsanctioned()) {
		return next
	}
	return kept
}

// collectModels folds the per-provider catalog rows into one row per model name,
// which is the shape the screen shows: "which providers serve this model".
func (s *Server) collectModels(r *http.Request, optedIn map[string]bool) []modelView {
	q := r.URL.Query()
	search := strings.ToLower(q.Get("q"))
	surface := q.Get("surface")
	minContext, _ := strconv.Atoi(q.Get("min_context"))
	wantTools := q.Get("tools") == "true"

	byModel := map[string]*modelView{}
	tiers := map[string]catalog.FreeTier{}
	optInDue := map[string]bool{}
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
					Source:       string(m.Pricing.Source),
					Grade:        string(m.Pricing.Grade()),
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
		tiers[m.ModelID] = foldTier(tiers[m.ModelID], m.FreeTier)
		if m.FreeTier.Vetoed() && !optedIn[m.ProviderID] {
			optInDue[m.ModelID] = true
		}
	}

	out := make([]modelView, 0, len(order))
	for _, id := range order {
		v := byModel[id]
		sort.Strings(v.Providers)
		if t := tiers[id]; t.ToS != "" {
			v.FreeTier = &freeTierView{
				FreeType:      t.FreeType,
				MonthlyTokens: t.MonthlyTokens,
				CreditTokens:  t.CreditTokens,
				PoolKey:       t.PoolKey,
				ToS:           t.ToS,
				OptInRequired: optInDue[id],
			}
		}
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
