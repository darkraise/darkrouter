package catalog

import (
	"sort"
	"strconv"
	"strings"

	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

// MergeInput is every source the catalog resolves between.
type MergeInput struct {
	Providers []provider.Provider
	Presets   Presets
	Doc       Doc
	LiteLLM   LiteLLMDoc
	Rows      []store.ModelRow
	Overrides []store.ModelOverride
}

// Merge resolves spec §7's precedence table per field.
//
// Per field rather than per record is the whole point: a model whose
// capabilities are discovered still takes its context window from models.dev,
// and letting one source win a whole record would throw away three quarters of
// what is known about it.
// FreeCatalogKey is the catalogue entry a provider row reads.
//
// Keyed on the preset rather than the row's own id, because the curated
// catalogue is a fact about the upstream vendor: a provider row an operator
// named something else still reaches the same free tier. An uncatalogued row
// falls back to its id, which is what a hand-added provider is named after.
func FreeCatalogKey(p provider.Provider) string {
	if p.Preset != "" {
		return p.Preset
	}
	return p.ID
}

func Merge(in MergeInput) []Model {
	byID := make(map[string]provider.Provider, len(in.Providers))
	for _, p := range in.Providers {
		byID[p.ID] = p
	}
	overrides := make(map[[2]string]store.ModelOverride, len(in.Overrides))
	for _, o := range in.Overrides {
		overrides[[2]string{o.ProviderID, o.ModelID}] = o
	}

	out := make([]Model, 0, len(in.Rows))
	for _, row := range in.Rows {
		p, ok := byID[row.ProviderID]
		if !ok {
			// A provider deleted between the read and the merge leaves orphan
			// rows. Offering them would advertise models nothing can route to.
			continue
		}
		preset := in.Presets[p.Preset] // the zero Preset for an uncatalogued provider
		out = append(out, mergeOne(row, FreeCatalogKey(p), preset, in.Doc, in.LiteLLM,
			overrides[[2]string{row.ProviderID, row.ModelID}]))
	}
	// Deterministic order: a snapshot rebuild must not reorder the candidate
	// list a request sees.
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProviderID != out[j].ProviderID {
			return out[i].ProviderID < out[j].ProviderID
		}
		return out[i].ModelID < out[j].ModelID
	})
	return out
}

func mergeOne(row store.ModelRow, presetID string, preset Preset, doc Doc,
	litellm LiteLLMDoc, override store.ModelOverride) Model {

	m := Model{
		ProviderID: row.ProviderID,
		ModelID:    row.ModelID,
		Publisher:  row.Publisher,
		State:      state(row.State),
		Surfaces:   surfaces(row, preset, override),
		Traits:     traitsFor(preset, row.ModelID),
	}

	// Capabilities and limits: models.dev, then what the row already carries.
	meta, joined := Join(preset, doc, row.ModelID)
	if joined {
		m.Capabilities = meta.Capabilities
		m.Source = SourceModelsDev
		m.ContextWindow = meta.ContextWindow
		m.MaxOutputTokens = meta.MaxOutputTokens
	} else {
		m.ContextWindow = row.ContextWindow
		m.MaxOutputTokens = row.MaxOutputTokens
		m.Source = SourceInferred
	}

	// Price precedence cannot be written as a fixed argument list, because the
	// row holds a single slot whose stamp may be anything from an operator's
	// own figure down to a guess. The stored candidate therefore leads a
	// directory when its stamp outranks one and trails it otherwise.
	stored := Pricing{
		InputMicrosPerMTok:      row.InputMicrosPerMTok,
		OutputMicrosPerMTok:     row.OutputMicrosPerMTok,
		CacheReadMicrosPerMTok:  row.CacheReadMicrosPerMTok,
		CacheWriteMicrosPerMTok: row.CacheWriteMicrosPerMTok,
		Known:                   row.PriceKnown,
		Source:                  priceSource(row.PriceSource),
	}
	var fromDoc Pricing
	if joined && meta.PriceKnown {
		fromDoc = Pricing{
			InputMicrosPerMTok:      meta.InputMicrosPerMTok,
			OutputMicrosPerMTok:     meta.OutputMicrosPerMTok,
			CacheReadMicrosPerMTok:  meta.CacheReadMicrosPerMTok,
			CacheWriteMicrosPerMTok: meta.CacheWriteMicrosPerMTok,
			Known:                   true,
			Source:                  SourceModelsDev,
		}
	}
	var fromLiteLLM Pricing
	if p, ok := JoinLiteLLM(preset, litellm, row.ModelID); ok && p.Known {
		fromLiteLLM = p
	}
	if stored.Source.Authoritative() {
		m.Pricing = resolvePrice(stored, fromDoc, fromLiteLLM)
	} else {
		m.Pricing = resolvePrice(fromDoc, fromLiteLLM, stored)
	}

	// Carried on the model rather than looked up per request: the router has to
	// know how the vendor grades this access before it selects the model, and
	// the console shows the allowance behind one that is free.
	if tier, ok := FreeModels().Tier(presetID, row.ModelID); ok {
		m.FreeTier = tier
	}

	// A price read from a reseller's listing is that reseller's copy of
	// someone else's figure, whichever candidate won above.
	m.Pricing.Resold = preset.ResellsPrices()

	// A runtime that reports its own capabilities outranks a directory's guess
	// about a model of the same name — that is what makes Ollama's tool
	// support a fact rather than an inference.
	if row.CapabilitiesSource == string(SourceDiscovered) {
		m.Capabilities = Capabilities{
			Tools:     row.Capabilities.Tools,
			Vision:    row.Capabilities.Vision,
			Reasoning: row.Capabilities.Reasoning,
			Known:     true,
		}
		m.Source = SourceDiscovered
	}

	// The operator's override wins outright. Per (provider, model), never per
	// provider: one Ollama instance serves models with wildly different tool
	// support.
	if override.Capabilities != nil {
		m.Capabilities = Capabilities{
			Tools:     override.Capabilities.Tools,
			Vision:    override.Capabilities.Vision,
			Reasoning: override.Capabilities.Reasoning,
			Known:     true,
		}
		m.Source = SourceOverride
	}
	if override.ContextWindow != nil {
		m.ContextWindow = *override.ContextWindow
	}

	// context-override= is the preset's answer for an upstream that serves a
	// smaller window than the model's own. It loses to an operator override
	// and wins over models.dev, because a preset saying so is evidence about
	// this upstream rather than about the model.
	if v, ok := preset.QuirkValue("context-override"); ok && override.ContextWindow == nil {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			m.ContextWindow = n
		}
	}
	return m
}

// JoinLiteLLM resolves one model's price through its provider's preset, on the
// same alias-exact-normalized ladder Join walks for models.dev. Sharing the
// ladder is the point: an id the metadata join reaches and the price join does
// not is a model that reads as fully described and unpriced, for no reason a
// reader could find.
//
// The index is keyed by the preset's own litellm_id, never by the provider id,
// so a provider with no key misses rather than matching a same-named bucket.
func JoinLiteLLM(p Preset, doc LiteLLMDoc, modelID string) (Pricing, bool) {
	if p.NoLiteLLM || p.LiteLLMID == "" {
		return Pricing{}, false
	}
	if alias, ok := p.ModelAliases[modelID]; ok {
		if price, ok := doc[p.LiteLLMID][alias]; ok {
			return price, true
		}
	}
	if price, ok := doc[p.LiteLLMID][modelID]; ok {
		return price, true
	}
	want := NormalizeModelID(modelID)
	if want == "" {
		return Pricing{}, false
	}
	// Sorted rather than a bare map range: two upstream ids can normalize to
	// the same key, and iteration order would otherwise decide which price the
	// model carries from one rebuild to the next.
	ids := make([]string, 0, len(doc[p.LiteLLMID]))
	for id := range doc[p.LiteLLMID] {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if NormalizeModelID(id) == want {
			return doc[p.LiteLLMID][id], true
		}
	}
	return Pricing{}, false
}

func state(s string) State {
	switch State(s) {
	case StateStale:
		return StateStale
	case StateRemovedUpstream:
		return StateRemovedUpstream
	default:
		// An unrecognized value routes rather than disappearing. A row written
		// by a newer binary must not become invisible on a rollback.
		return StateLive
	}
}

// surfaces resolves the override, then the preset, then the row, then llm.
//
// The preset outranks the row because the row's surfaces carry no information:
// discovery hardcodes '["llm"]' on insert and never updates the column
// (store.RecordDiscoverySuccess), and the models.dev sync echoes that value
// back unchanged. With the row winning, a widened preset had no effect on any
// discovered model — an embedding request against a provider whose discovery
// works was skipped as SkipSurface and reported "no provider offers this".
//
// The override still wins, and is the only per-model source that will ever
// carry an operator's intent. A future writer that genuinely learns a model's
// surfaces should write there rather than onto the row.
func surfaces(row store.ModelRow, preset Preset, override store.ModelOverride) []ir.Surface {
	for _, candidate := range [][]string{override.Surfaces, preset.Surfaces, row.Surfaces} {
		if parsed := parseSurfaces(candidate); len(parsed) > 0 {
			return parsed
		}
	}
	return []ir.Surface{ir.SurfaceLLM}
}

func parseSurfaces(raw []string) []ir.Surface {
	out := make([]ir.Surface, 0, len(raw))
	for _, s := range raw {
		if parsed, ok := ir.ParseSurface(s); ok {
			out = append(out, parsed)
		}
	}
	return out
}

// traitsFor picks the longest matching rule. Longest rather than first because
// "big-v2" contains "big", and the shorter rule would otherwise decide a wire
// shape that is a 400 on every reasoning request.
func traitsFor(preset Preset, modelID string) Traits {
	name := strings.ReplaceAll(strings.ToLower(modelID), ".", "-")
	best := -1
	var out Traits
	for _, rule := range preset.ModelTraits {
		match := strings.ReplaceAll(strings.ToLower(rule.Match), ".", "-")
		if match == "" || !strings.Contains(name, match) {
			continue
		}
		if len(match) > best {
			best = len(match)
			out = Traits{
				Adaptive:           rule.Adaptive,
				ManualBudget:       rule.ManualBudget,
				FreeSampling:       rule.FreeSampling,
				Known:              true,
				NoPrefill:          rule.NoPrefill,
				ThinkingAlwaysOn:   rule.ThinkingAlwaysOn,
				NoForcedToolChoice: rule.NoForcedToolChoice,
			}
		}
	}
	return out
}

// priceSource reads the stored authority, defaulting an empty column to a
// guess. A row written before 0018 carries "" and must not read as measured.
func priceSource(stored string) Source {
	switch Source(stored) {
	case SourceDiscovered, SourceOverride, SourceModelsDev, SourceLiteLLM, SourceRegistry:
		return Source(stored)
	default:
		return SourceInferred
	}
}

// resolvePrice returns the first candidate whose price is known. Callers pass
// candidates in precedence order; a source with nothing to say passes a
// zero-valued Pricing, whose false Known keeps it from shadowing a source that
// did have something to say — a known price of zero is a real price and must
// stay distinguishable from an absent one.
func resolvePrice(candidates ...Pricing) Pricing {
	for _, c := range candidates {
		if c.Known {
			return c
		}
	}
	return Pricing{Source: SourceInferred}
}
