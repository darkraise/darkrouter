// Package tokenize estimates prompt token counts for requests Darkrouter
// cannot forward to a provider's own counting endpoint.
//
// Clients budget context windows against this number, so the method is named
// rather than left to each caller: plausible approaches differ by a factor of
// two, and a silently different answer per route would be worse than a
// consistently approximate one.
package tokenize

import (
	"strings"
	"sync"

	"github.com/tiktoken-go/tokenizer"

	"github.com/darkraise/darkrouter/internal/ir"
)

type Encoding string

const (
	O200k  Encoding = "o200k_base"
	Cl100k Encoding = "cl100k_base"
	// Heuristic is characters divided by four, for a family whose real
	// tokenizer Darkrouter does not bundle.
	Heuristic Encoding = ""
)

// perMessageOverhead is the framing every message costs on top of its text.
// It is tiktoken's documented constant for chat formats and close enough for
// the others that a better guess would be false precision.
const perMessageOverhead = 4

// o200kPrefixes and cl100kPrefixes name the families whose tokenizer is
// bundled. Everything else falls back to the heuristic rather than borrowing a
// vocabulary that would be confidently wrong.
var (
	o200kPrefixes  = []string{"gpt-5", "gpt-4.1", "gpt-4o", "gpt-oss", "o1", "o3", "o4"}
	cl100kPrefixes = []string{"gpt-4", "gpt-3.5", "text-embedding"}
)

// EncodingFor picks a vocabulary from a model name, ignoring any
// provider/model prefix so that "openai/gpt-oss-120b" resolves like "gpt-oss".
func EncodingFor(model string) Encoding {
	name := strings.ToLower(model)
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	for _, p := range o200kPrefixes {
		if strings.HasPrefix(name, p) {
			return O200k
		}
	}
	for _, p := range cl100kPrefixes {
		if strings.HasPrefix(name, p) {
			return Cl100k
		}
	}
	return Heuristic
}

// Count estimates the prompt tokens a request will consume.
//
// Media is deliberately excluded: an image's cost depends on the model's tiling
// rules, and a number that is wrong in an unknowable direction is worse than one
// that is obviously incomplete. The response carries X-Darkrouter-Estimated so
// the client knows not to trust it to the token.
func Count(req *ir.Request, model string) int {
	count := counterFor(EncodingFor(model))

	total := 0
	total += count(blocksText(req.System))
	for _, m := range req.Messages {
		total += perMessageOverhead
		total += count(string(m.Role))
		total += count(blocksText(m.Content))
	}
	for _, t := range req.Tools {
		total += count(t.Name) + count(t.Description) + count(string(t.Schema))
	}
	for _, s := range req.StopSequences {
		total += count(s)
	}
	return total
}

// getCodec is the codec constructor, a variable so a test can count calls.
var getCodec = tokenizer.Get

// codecs holds one lazily built codec per bundled encoding. Building one
// loads a vocabulary of hundreds of thousands of entries, which is fine once
// and ruinous per request.
var codecs = map[Encoding]*codecOnce{
	O200k:  {enc: tokenizer.O200kBase},
	Cl100k: {enc: tokenizer.Cl100kBase},
}

type codecOnce struct {
	enc   tokenizer.Encoding
	once  sync.Once
	codec tokenizer.Codec
}

func (c *codecOnce) get() tokenizer.Codec {
	c.once.Do(func() { c.codec, _ = getCodec(c.enc) })
	return c.codec
}

// resetCodecs forgets every built codec. Test-only: the cache is otherwise
// process-wide by design.
func resetCodecs() {
	for k, c := range codecs {
		codecs[k] = &codecOnce{enc: c.enc}
	}
}

// counterFor returns a function counting one string. A tokenizer that fails to
// load falls back to the heuristic rather than failing the request: an
// approximate answer beats a 500 on an endpoint whose whole purpose is advisory.
func counterFor(enc Encoding) func(string) int {
	var codec tokenizer.Codec
	if c, ok := codecs[enc]; ok {
		codec = c.get()
	}
	if codec == nil {
		return heuristicCount
	}
	return func(s string) int {
		if s == "" {
			return 0
		}
		ids, _, err := codec.Encode(s)
		if err != nil {
			return heuristicCount(s)
		}
		return len(ids)
	}
}

func heuristicCount(s string) int { return (len(s) + 3) / 4 }

// blocksText flattens the text a block set carries, descending one level into
// tool results, whose content counts against the window like any other text.
func blocksText(blocks []ir.ContentBlock) string {
	var b strings.Builder
	for _, blk := range blocks {
		switch blk.Type {
		case ir.BlockText:
			b.WriteString(blk.Text)
		case ir.BlockThinking:
			if blk.Thinking != nil {
				b.WriteString(blk.Thinking.Text)
			}
		case ir.BlockToolUse:
			if blk.ToolUse != nil {
				b.WriteString(blk.ToolUse.Name)
				b.Write(blk.ToolUse.Input)
			}
		case ir.BlockToolResult:
			if blk.ToolResult != nil {
				b.WriteString(blk.ToolResult.Text())
			}
		}
	}
	return b.String()
}
