package gemini

import (
	"context"
	"encoding/json"

	"github.com/darkraise/darkrouter/internal/adapter/xlate"
	"github.com/darkraise/darkrouter/internal/ir"
)

// pendingCall remembers a function call so the response answering it can carry
// the name Gemini requires. Ids are optional in this dialect and usually
// absent, so the ordered slice — not the map — is the authority.
type pendingCall struct {
	id   string
	name string
}

// renderContents converts the IR conversation to Gemini's contents array.
func (f *Fetcher) renderContents(ctx context.Context, req *ir.Request) ([]any, []ir.Warning) {
	var (
		out     []any
		warns   []ir.Warning
		curRole string
		curPart []any
		pending []pendingCall
	)
	flush := func() {
		if curRole == "" {
			return
		}
		out = append(out, map[string]any{"role": curRole, "parts": curPart})
		curRole, curPart = "", nil
	}

	for turn, m := range xlate.NonSystemMessages(req.Messages) {
		role := "user"
		if m.Role == ir.RoleAssistant {
			role = "model"
		}
		ps, calls, w := f.renderParts(ctx, turn, m.Content, pending)
		warns = append(warns, w...)
		if len(calls) > 0 {
			pending = calls
		}
		if len(ps) == 0 {
			continue
		}
		if role != curRole {
			flush()
			curRole = role
		}
		curPart = append(curPart, ps...)
	}
	flush()
	return out, warns
}

// renderParts converts one turn. It returns the parts, and the function calls
// this turn made so the next turn's responses can be matched to them.
func (f *Fetcher) renderParts(ctx context.Context, turn int, blocks []ir.ContentBlock,
	pending []pendingCall) ([]any, []pendingCall, []ir.Warning) {

	var (
		out     []any
		calls   []pendingCall
		warns   []ir.Warning
		results int
	)
	for _, b := range blocks {
		if b.CacheControl != nil {
			warns = append(warns, ir.Warning{
				Field: "cache_control", Target: targetName,
				Reason: "Gemini caches explicitly through cachedContent, not per block",
			})
		}

		switch b.Type {
		case ir.BlockText:
			out = append(out, map[string]any{"text": b.Text})

		case ir.BlockThinking:
			if b.Thinking == nil {
				continue
			}
			// thought:true alone does not restore reasoning state on the next
			// turn — the signature is what does. Review finding F10.
			p := map[string]any{"text": b.Thinking.Text, "thought": true}
			if b.Thinking.Signature != "" {
				p["thoughtSignature"] = b.Thinking.Signature
			}
			out = append(out, p)

		case ir.BlockRedactedThinking:
			warns = append(warns, ir.Warning{
				Field: "messages[].redacted_thinking", Target: targetName,
				Reason: "no equivalent part; the block was dropped",
			})

		case ir.BlockImage, ir.BlockDocument, ir.BlockAudio:
			p, w := f.part(ctx, b.Media, string(b.Type))
			warns = append(warns, w...)
			if p != nil {
				out = append(out, p)
			}

		case ir.BlockToolUse:
			if b.ToolUse == nil {
				continue
			}
			id := b.ToolUse.ID
			if id == "" {
				id = xlate.SyntheticToolCallID(turn, len(calls))
			}
			calls = append(calls, pendingCall{id: id, name: b.ToolUse.Name})
			args := b.ToolUse.Input
			if len(args) == 0 {
				args = json.RawMessage(`{}`)
			}
			call := map[string]any{"name": b.ToolUse.Name, "args": args}
			if b.ToolUse.ID != "" {
				call["id"] = b.ToolUse.ID
			}
			out = append(out, map[string]any{"functionCall": call})

		case ir.BlockToolResult:
			if b.ToolResult == nil {
				continue
			}
			// Positional within the turn: parallel calls to one function are
			// indistinguishable by name, and that is exactly what an agentic
			// loop produces.
			name := ""
			if results < len(pending) {
				name = pending[results].name
			}
			results++

			resp := map[string]any{"name": name, "response": responseStruct(b.ToolResult.Text())}
			if b.ToolResult.ToolUseID != "" {
				resp["id"] = b.ToolResult.ToolUseID
			}
			out = append(out, map[string]any{"functionResponse": resp})

			for _, inner := range b.ToolResult.Content {
				if inner.Type == ir.BlockText {
					continue
				}
				// Spec §7: functionResponse.response is a struct, so media is
				// hoisted into the same turn as its own part rather than lost.
				p, w := f.part(ctx, inner.Media, "tool_result."+string(inner.Type))
				warns = append(warns, w...)
				if p != nil {
					out = append(out, p)
					warns = append(warns, ir.Warning{
						Field: "messages[].tool_result." + string(inner.Type), Target: targetName,
						Reason: "moved out of the function response, which accepts a JSON struct only",
					})
				}
			}

		default:
			warns = append(warns, ir.Warning{
				Field: "messages[]." + string(b.Type), Target: targetName,
				Reason: "unsupported content block",
			})
		}
	}
	return out, calls, warns
}

// responseStruct meets Gemini's requirement that a function response be an
// object. A tool that already returned one is passed through; prose is wrapped
// under "result" rather than being sent as a bare string, which is rejected.
func responseStruct(text string) any {
	var obj map[string]any
	if json.Unmarshal([]byte(text), &obj) == nil && obj != nil {
		return obj
	}
	return map[string]any{"result": text}
}
