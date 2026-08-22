// Package xlate holds the conversions every outbound adapter shares.
//
// They live here rather than in each adapter because a divergence would be
// silent: the request still succeeds, only with a differently assembled prompt
// or a differently sized thinking budget per target. Spec §4.6 names that
// specifically for the effort-to-budget table.
package xlate

import (
	"strings"

	"github.com/darkraise/darkrouter/internal/ir"
)

// systemSources walks every source of system content exactly once: req.System
// first, then RoleSystem messages in conversation order, one group per source.
//
// Both public collectors are defined over this. The grouping is what the string
// form needs — sources join with a blank line while blocks within one source
// concatenate — and a flat block slice destroys it, which is why the split is
// here rather than at the block level.
func systemSources(req *ir.Request, target string) ([][]ir.ContentBlock, []ir.Warning) {
	var (
		groups    [][]ir.ContentBlock
		warns     []ir.Warning
		seenTurn  bool
		misplaced bool
	)
	add := func(blocks []ir.ContentBlock, field string) {
		var g []ir.ContentBlock
		for _, b := range blocks {
			if b.Type == ir.BlockText {
				g = append(g, b)
				continue
			}
			warns = append(warns, ir.Warning{
				Field:  field + "." + string(b.Type),
				Target: target,
				Reason: "system content accepts text only",
			})
		}
		if len(g) > 0 {
			groups = append(groups, g)
		}
	}

	add(req.System, "system[]")
	for _, m := range req.Messages {
		if m.Role != ir.RoleSystem {
			seenTurn = true
			continue
		}
		// A system message that arrived after a non-system turn had a position
		// that carried meaning, and no target with one system field can express
		// it. One warning covers all of them: the client's fix is the same
		// however many there were.
		if seenTurn {
			misplaced = true
		}
		add(m.Content, "messages[].system")
	}
	if misplaced {
		warns = append(warns, ir.Warning{
			Field:  "messages[].role=system",
			Target: target,
			Reason: "a system message after a conversation turn was moved to the front",
		})
	}
	return groups, warns
}

// CollectSystem flattens to the single string Gemini's systemInstruction takes.
func CollectSystem(req *ir.Request, target string) (string, []ir.Warning) {
	groups, warns := systemSources(req, target)
	parts := make([]string, 0, len(groups))
	for _, g := range groups {
		var b strings.Builder
		for _, blk := range g {
			b.WriteString(blk.Text)
		}
		if text := b.String(); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n"), warns
}

// CollectSystemBlocks is CollectSystem for a target whose system field is an
// array. Blocks keep their cache_control, which is what lets an Anthropic
// client's cached system prompt stay cached through the gateway.
func CollectSystemBlocks(req *ir.Request, target string) ([]ir.ContentBlock, []ir.Warning) {
	groups, warns := systemSources(req, target)
	var out []ir.ContentBlock
	for _, g := range groups {
		out = append(out, g...)
	}
	return out, warns
}

// NonSystemMessages is the conversation with system turns removed, for targets
// that carry system content in a field of its own.
func NonSystemMessages(msgs []ir.Message) []ir.Message {
	out := make([]ir.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role != ir.RoleSystem {
			out = append(out, m)
		}
	}
	return out
}
