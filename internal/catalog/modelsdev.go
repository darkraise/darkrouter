package catalog

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// snapshotJSON is the embedded fallback metadata for Darkrouter's first run.
// Regenerate it with tools/presetgen when the binary ships: it is only the
// cold-start fallback, but a long-lived offline install runs on its numbers
// indefinitely.
//
//go:embed models_snapshot.json
var snapshotJSON []byte

// Metadata is what a metadata source knows about one model. It is deliberately
// flat and comparable, so a test can assert two sources agree with ==.
type Metadata struct {
	ContextWindow   int
	MaxOutputTokens int

	InputMicrosPerMTok      int64
	OutputMicrosPerMTok     int64
	CacheReadMicrosPerMTok  int64
	CacheWriteMicrosPerMTok int64
	// PriceKnown separates a free model from an unpriced one. Both are zero.
	PriceKnown bool

	Capabilities Capabilities
}

// Doc is a metadata source keyed by models.dev provider id, then model id.
type Doc map[string]map[string]Metadata

// Metadata looks one model up. The miss is reported rather than returning a
// zero value, because a zero context window is a fact the merge acts on.
// ModelsFor enumerates one provider's models, sorted so a seeded catalog is
// stable across restarts. Vertex has no listing endpoint, spec §4.3, so this is
// the only source of model ids for it.
func (d Doc) ModelsFor(modelsDevID string) []string {
	models, ok := d[modelsDevID]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(models))
	for id := range models {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func (d Doc) Metadata(modelsDevID, modelID string) (Metadata, bool) {
	models, ok := d[modelsDevID]
	if !ok {
		return Metadata{}, false
	}
	m, ok := models[modelID]
	return m, ok
}

// --- the live document ---

type liveModel struct {
	Cost struct {
		Input      *float64 `json:"input"`
		Output     *float64 `json:"output"`
		CacheRead  *float64 `json:"cache_read"`
		CacheWrite *float64 `json:"cache_write"`
	} `json:"cost"`
	Limit struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
	ToolCall   bool `json:"tool_call"`
	Reasoning  bool `json:"reasoning"`
	Modalities struct {
		Input []string `json:"input"`
	} `json:"modalities"`
}

type liveProvider struct {
	Models map[string]liveModel `json:"models"`
}

// ParseModelsDev reads the document as models.dev publishes it.
//
// An empty result is an error rather than an empty catalog: a truncated CDN
// response would otherwise wipe every price on the next sync, and the sync's
// whole failure contract is that a bad fetch leaves the cache alone.
func ParseModelsDev(raw []byte) (Doc, error) {
	var doc map[string]liveProvider
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse models.dev document: %w", err)
	}
	out := make(Doc, len(doc))
	for pid, p := range doc {
		if len(p.Models) == 0 {
			continue
		}
		models := make(map[string]Metadata, len(p.Models))
		for mid, m := range p.Models {
			models[mid] = m.metadata()
		}
		out[pid] = models
	}
	if len(out) == 0 {
		return nil, errors.New("models.dev document contains no providers")
	}
	return out, nil
}

func (m liveModel) metadata() Metadata {
	out := Metadata{
		ContextWindow:   m.Limit.Context,
		MaxOutputTokens: m.Limit.Output,
		Capabilities: Capabilities{
			Tools:     m.ToolCall,
			Reasoning: m.Reasoning,
			// models.dev has no vision flag; it is expressed through the input
			// modalities. Looking for one leaves every multimodal model marked
			// text-only.
			Vision: hasImageInput(m.Modalities.Input),
			// A models.dev negative is knowledge, not absence of it, so the
			// router filters on it rather than admitting with a warning.
			Known: true,
		},
	}
	if m.Cost.Input != nil {
		out.InputMicrosPerMTok, out.PriceKnown = micros(*m.Cost.Input), true
	}
	if m.Cost.Output != nil {
		out.OutputMicrosPerMTok, out.PriceKnown = micros(*m.Cost.Output), true
	}
	if m.Cost.CacheRead != nil {
		out.CacheReadMicrosPerMTok = micros(*m.Cost.CacheRead)
	}
	if m.Cost.CacheWrite != nil {
		out.CacheWriteMicrosPerMTok = micros(*m.Cost.CacheWrite)
	}
	return out
}

func hasImageInput(inputs []string) bool {
	for _, in := range inputs {
		if in == "image" {
			return true
		}
	}
	return false
}

// micros converts USD per million tokens to micro-dollars per million tokens.
// Rounded rather than truncated: the source is a float, and 0.14 arriving as
// 0.13999999999999999 must not lose a micro-dollar.
func micros(usdPerMTok float64) int64 {
	if usdPerMTok < 0 {
		return 0
	}
	return int64(usdPerMTok*1_000_000 + 0.5)
}

// --- the embedded fallback ---

// fallbackModel is the trimmed shape tools/presetgen emits. The short keys are
// what keeps a 4.2 MB document under a megabyte in the binary.
type fallbackModel struct {
	InputMicros      int64 `json:"i,omitempty"`
	OutputMicros     int64 `json:"o,omitempty"`
	CacheReadMicros  int64 `json:"c,omitempty"`
	CacheWriteMicros int64 `json:"x,omitempty"`
	Context          int   `json:"w,omitempty"`
	MaxOutput        int   `json:"m,omitempty"`
	Tools            bool  `json:"t,omitempty"`
	Reasoning        bool  `json:"r,omitempty"`
	Vision           bool  `json:"v,omitempty"`
	// ZeroPriced distinguishes a model models.dev prices at zero from one it
	// does not price at all. Both write no price at all through the omitempty
	// fields above, and the free-models filter turns on telling them apart.
	ZeroPriced bool `json:"z,omitempty"`
}

var (
	fallbackOnce sync.Once
	fallback     Doc
)

// FallbackDoc is the snapshot embedded at build time. It is what makes
// "Darkrouter starts and serves with no outbound access to models.dev" true on
// a first run, rather than only after one successful sync.
//
// A parse failure degrades to an empty Doc rather than panicking: the embedded
// file is generated, its shape is asserted by this package's tests, and a live
// gateway refusing to boot over it would be exactly the failure mode spec §4
// exists to prevent.
func FallbackDoc() Doc {
	fallbackOnce.Do(func() {
		var doc map[string]map[string]fallbackModel
		if err := json.Unmarshal(snapshotJSON, &doc); err != nil {
			fallback = Doc{}
			return
		}
		fallback = make(Doc, len(doc))
		for pid, models := range doc {
			out := make(map[string]Metadata, len(models))
			for mid, m := range models {
				out[mid] = Metadata{
					ContextWindow:           m.Context,
					MaxOutputTokens:         m.MaxOutput,
					InputMicrosPerMTok:      m.InputMicros,
					OutputMicrosPerMTok:     m.OutputMicros,
					CacheReadMicrosPerMTok:  m.CacheReadMicros,
					CacheWriteMicrosPerMTok: m.CacheWriteMicros,
					// A nonzero price is self-evidently known; a zero one says
					// so through its own flag. Inferring the flag from the
					// numbers alone read every free model as unpriced, which
					// is the one verdict that keeps it out of a free-only
					// import.
					PriceKnown: m.InputMicros != 0 || m.OutputMicros != 0 || m.ZeroPriced,
					Capabilities: Capabilities{
						Tools: m.Tools, Reasoning: m.Reasoning, Vision: m.Vision, Known: true,
					},
				}
			}
			fallback[pid] = out
		}
	})
	return fallback
}
