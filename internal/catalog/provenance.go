package catalog

import (
	_ "embed"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed provenance.yaml
var provenanceYAML []byte

// ProvenanceDoc is which upstream supplied each structural field, plus the
// commits it was read from.
type ProvenanceDoc struct {
	OmniRouteSHA  string                       `yaml:"omniroute_sha"`
	NineRouterSHA string                       `yaml:"ninerouter_sha"`
	GeneratedAt   string                       `yaml:"generated_at"`
	Presets       map[string]map[string]string `yaml:"presets"`
}

var (
	provOnce sync.Once
	provDoc  ProvenanceDoc
)

// Provenance parses the embedded manifest once. A parse failure degrades to an
// empty document, because refusing to boot over a provenance label is worse
// than showing none.
func Provenance() ProvenanceDoc {
	provOnce.Do(func() { provDoc = parseProvenance(provenanceYAML) })
	return provDoc
}

func parseProvenance(raw []byte) ProvenanceDoc {
	var doc ProvenanceDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil || doc.Presets == nil {
		return ProvenanceDoc{Presets: map[string]map[string]string{}}
	}
	return doc
}

// FieldOrigin names the upstream a preset's field came from.
func FieldOrigin(presetID, field string) (string, bool) {
	src, ok := Provenance().Presets[presetID][field]
	return src, ok
}
