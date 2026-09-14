package adapter

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func TestValidateEmbeddings(t *testing.T) {
	f := func(i int, v ...float32) ir.Embedding { return ir.Embedding{Index: i, Float: v} }
	cases := []struct {
		name string
		in   []ir.Embedding
		ok   bool
	}{
		{"two float vectors", []ir.Embedding{f(0, 1, 2), f(1, 3, 4)}, true},
		{"base64 of two float32s", []ir.Embedding{{Index: 0, Base64: "AACAPwAAAEA="}}, true},
		{"indices out of order", []ir.Embedding{f(1, 1), f(0, 2)}, true},
		{"an empty vector", []ir.Embedding{f(0, 1), {Index: 1}}, false},
		{"mixed dimensions", []ir.Embedding{f(0, 1, 2), f(1, 3)}, false},
		{"a duplicate index", []ir.Embedding{f(0, 1), f(0, 2)}, false},
		{"an index past the batch", []ir.Embedding{f(0, 1), f(2, 2)}, false},
		{"invalid base64", []ir.Embedding{{Index: 0, Base64: "not base64!"}}, false},
		{"base64 not a float32 multiple", []ir.Embedding{{Index: 0, Base64: "AAA="}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateEmbeddings(c.in)
			if (err == nil) != c.ok {
				t.Fatalf("err = %v, want ok=%v", err, c.ok)
			}
		})
	}
}
