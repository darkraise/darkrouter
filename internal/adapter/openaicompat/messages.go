package openaicompat

import (
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter/xlate"
	"github.com/darkraise/darkrouter/internal/ir"
)

// renderMessages converts the IR conversation to OpenAI's messages array.
//
// System content arrives from two places and both are kept: req.System becomes
// a leading system message, and RoleSystem turns stay where they are, because
// OpenAI permits several and their position carries meaning. Only targets with
// a single system field need xlate.CollectSystem.
//
// foldSystem serves an upstream with no system role at all: every system
// block, wherever it sat, is gathered in order and prepended to the first user
// turn, which is the only place such an upstream will read an instruction.
func renderMessages(req *ir.Request, target string, foldSystem bool) ([]any, []ir.Warning) {
	var (
		out   []any
		warns []ir.Warning
	)
	msgs := req.Messages
	var folded string
	if foldSystem {
		var w []ir.Warning
		folded, msgs, w = foldSystemTurns(req, target)
		warns = append(warns, w...)
	} else if len(req.System) > 0 {
		text, w := blocksText(req.System, "system[]", target)
		warns = append(warns, w...)
		// System blocks carry cache markers too, and a cached system prompt is
		// the most valuable thing a client caches. Flattening to a string drops
		// the marker, so it has to be recorded here as well as in the turns.
		for _, b := range req.System {
			warns = append(warns, cacheWarning(b, target)...)
		}
		if text != "" {
			out = append(out, map[string]any{"role": "system", "content": text})
		}
	}

	// A conversation ending in a text-only assistant turn is Anthropic's prefill
	// idiom, which constrains the next completion. OpenAI reads it as a finished
	// turn and answers the wrong question, so it is dropped rather than
	// mistranslated. A trailing assistant turn carrying tool calls is not a
	// prefill — it is an agentic step whose results have yet to arrive — and
	// dropping it would erase the calls the next turn answers.
	if n := len(msgs); n > 0 && isTextOnlyAssistant(msgs[n-1]) {
		msgs = msgs[:n-1]
		warns = append(warns, ir.Warning{
			Field: "messages[last].assistant_prefill", Target: target,
			Reason: "assistant prefill has no equivalent; the turn was dropped",
		})
	}

	for i, m := range msgs {
		rendered, w := renderMessage(i, m, target)
		out = append(out, rendered...)
		warns = append(warns, w...)
	}
	if folded != "" {
		out = prependToFirstUser(out, folded)
	}
	return out, warns
}

// foldSystemTurns gathers every system block into one string and returns the
// conversation without its system turns. The text is what prependToFirstUser
// places, once the turns are rendered.
func foldSystemTurns(req *ir.Request, target string) (string, []ir.Message, []ir.Warning) {
	var (
		parts []string
		warns []ir.Warning
		rest  []ir.Message
	)
	collect := func(blocks []ir.ContentBlock, field string) {
		text, w := blocksText(blocks, field, target)
		warns = append(warns, w...)
		for _, b := range blocks {
			warns = append(warns, cacheWarning(b, target)...)
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	collect(req.System, "system[]")
	for _, m := range req.Messages {
		if m.Role == ir.RoleSystem {
			collect(m.Content, "messages[].system")
			continue
		}
		rest = append(rest, m)
	}
	if len(parts) == 0 {
		return "", rest, warns
	}
	warns = append(warns, ir.Warning{
		Field: "system", Target: target,
		Reason: "the target has no system role; the instruction was folded into the first user message",
	})
	return strings.Join(parts, "\n\n"), rest, warns
}

// prependToFirstUser puts folded system text ahead of the first user message's
// content, or ahead of the whole conversation when there is no user turn to
// carry it.
func prependToFirstUser(msgs []any, text string) []any {
	for i, raw := range msgs {
		m, ok := raw.(map[string]any)
		if !ok || m["role"] != "user" {
			continue
		}
		switch c := m["content"].(type) {
		case string:
			m["content"] = text + "\n\n" + c
		case []any:
			m["content"] = append([]any{map[string]any{"type": "text", "text": text}}, c...)
		default:
			m["content"] = text
		}
		msgs[i] = m
		return msgs
	}
	return append([]any{map[string]any{"role": "user", "content": text}}, msgs...)
}

// isTextOnlyAssistant identifies the prefill idiom. Text and nothing else is
// what makes a trailing assistant turn a constraint on the next completion
// rather than a completed step.
func isTextOnlyAssistant(m ir.Message) bool {
	if m.Role != ir.RoleAssistant {
		return false
	}
	for _, b := range m.Content {
		if b.Type != ir.BlockText {
			return false
		}
	}
	return true
}

// renderMessage emits every OpenAI message one IR turn becomes.
//
// A turn holding tool results becomes one `tool` message per result followed by
// a `user` message carrying whatever else it held. OpenAI requires each result
// in its own message keyed by tool_call_id, immediately after the assistant
// message that made the calls, and rejects a mixed turn with a 400.
func renderMessage(turn int, m ir.Message, target string) ([]any, []ir.Warning) {
	var (
		out   []any
		warns []ir.Warning
	)
	for _, b := range m.Content {
		warns = append(warns, cacheWarning(b, target)...)
	}

	switch m.Role {
	case ir.RoleSystem:
		text, w := blocksText(m.Content, "messages[].system", target)
		warns = append(warns, w...)
		if text != "" {
			out = append(out, map[string]any{"role": "system", "content": text})
		}
		return out, warns

	case ir.RoleAssistant:
		var (
			text  strings.Builder
			calls []any
		)
		for _, b := range m.Content {
			switch b.Type {
			case ir.BlockText:
				text.WriteString(b.Text)
			case ir.BlockToolUse:
				if b.ToolUse == nil {
					continue
				}
				id := b.ToolUse.ID
				if id == "" {
					id = xlate.SyntheticToolCallID(turn, len(calls))
				}
				args := string(b.ToolUse.Input)
				if args == "" {
					args = "{}"
				}
				calls = append(calls, map[string]any{
					"id": id, "type": "function",
					"function": map[string]any{"name": b.ToolUse.Name, "arguments": args},
				})
			default:
				warns = append(warns, ir.Warning{
					Field: "messages[].assistant." + string(b.Type), Target: target,
					Reason: "assistant turns carry text and tool calls only",
				})
			}
		}
		msg := map[string]any{"role": "assistant"}
		// The key is always present, even as null: several compatible providers
		// reject an assistant message without one.
		if text.Len() > 0 {
			msg["content"] = text.String()
		} else {
			msg["content"] = nil
		}
		if len(calls) > 0 {
			msg["tool_calls"] = calls
		}
		return append(out, msg), warns
	}

	// user and tool turns share a shape: results first, then everything else.
	var rest, hoisted []ir.ContentBlock
	for _, b := range m.Content {
		if b.Type != ir.BlockToolResult || b.ToolResult == nil {
			rest = append(rest, b)
			continue
		}
		tr := b.ToolResult
		out = append(out, map[string]any{
			"role": "tool", "tool_call_id": tr.ToolUseID, "content": tr.Text(),
		})
		for _, inner := range tr.Content {
			if inner.Type == ir.BlockText {
				continue
			}
			// Spec §7: an OpenAI tool message is text-only, so the media moves
			// into the user turn that follows rather than being discarded.
			hoisted = append(hoisted, inner)
			warns = append(warns, ir.Warning{
				Field: "messages[].tool_result." + string(inner.Type), Target: target,
				Reason: "tool messages are text-only; moved into a following user message",
			})
		}
	}

	blocks := make([]ir.ContentBlock, 0, len(hoisted)+len(rest))
	blocks = append(blocks, hoisted...)
	blocks = append(blocks, rest...)
	if len(blocks) == 0 {
		return out, warns
	}
	content, w := renderContent(blocks, target)
	warns = append(warns, w...)
	return append(out, map[string]any{"role": "user", "content": content}), warns
}

// blocksText flattens to a plain string, warning about anything that is not
// text. It serves the two places OpenAI takes a bare string: system messages
// and tool results.
func blocksText(blocks []ir.ContentBlock, field, target string) (string, []ir.Warning) {
	var (
		b     strings.Builder
		warns []ir.Warning
	)
	for _, blk := range blocks {
		if blk.Type == ir.BlockText {
			b.WriteString(blk.Text)
			continue
		}
		warns = append(warns, ir.Warning{
			Field: field + "." + string(blk.Type), Target: target,
			Reason: "this position accepts text only",
		})
	}
	return b.String(), warns
}

// cacheWarning records a cache-control marker the target cannot express. A
// user whose paid caching stops working on failover has to see it in the trace
// rather than infer it from a bill.
func cacheWarning(b ir.ContentBlock, target string) []ir.Warning {
	if b.CacheControl == nil {
		return nil
	}
	return []ir.Warning{{
		Field: "cache_control", Target: target,
		Reason: "no explicit prompt-caching control in this dialect",
	}}
}

// renderContent emits a plain string when every block is text and the
// multi-part form otherwise. Some compatible providers reject the multi-part
// form for text-only messages.
func renderContent(blocks []ir.ContentBlock, target string) (any, []ir.Warning) {
	onlyText := true
	for _, b := range blocks {
		if b.Type != ir.BlockText {
			onlyText = false
			break
		}
	}
	if onlyText {
		var b strings.Builder
		for _, blk := range blocks {
			b.WriteString(blk.Text)
		}
		return b.String(), nil
	}

	var warns []ir.Warning
	parts := make([]any, 0, len(blocks))
	for _, blk := range blocks {
		switch blk.Type {
		case ir.BlockText:
			parts = append(parts, map[string]any{"type": "text", "text": blk.Text})
		case ir.BlockImage:
			if blk.Media == nil {
				continue
			}
			url := blk.Media.URL
			if url == "" && blk.Media.Data != "" {
				url = "data:" + blk.Media.MIME + ";base64," + blk.Media.Data
			}
			if url == "" {
				warns = append(warns, ir.Warning{
					Field: "messages[].image", Target: target,
					Reason: "image carried neither data nor a URL",
				})
				continue
			}
			parts = append(parts, map[string]any{
				"type": "image_url", "image_url": map[string]any{"url": url},
			})
		case ir.BlockDocument:
			if blk.Media == nil {
				continue
			}
			file := map[string]any{}
			switch {
			case blk.Media.FileID != "":
				file["file_id"] = blk.Media.FileID
			case blk.Media.Data != "":
				file["filename"] = documentFilename(blk.Media.MIME)
				file["file_data"] = "data:" + blk.Media.MIME + ";base64," + blk.Media.Data
			default:
				warns = append(warns, ir.Warning{
					Field: "messages[].document", Target: target,
					Reason: "document carried neither data nor a file id",
				})
				continue
			}
			parts = append(parts, map[string]any{"type": "file", "file": file})

		case ir.BlockAudio:
			if blk.Media == nil {
				continue
			}
			format, ok := audioFormat(blk.Media.MIME)
			if !ok || blk.Media.Data == "" {
				warns = append(warns, ir.Warning{
					Field: "messages[].audio", Target: target,
					Reason: "OpenAI input_audio accepts inline wav or mp3 only",
				})
				continue
			}
			parts = append(parts, map[string]any{
				"type":        "input_audio",
				"input_audio": map[string]any{"data": blk.Media.Data, "format": format},
			})

		default:
			warns = append(warns, ir.Warning{
				Field: "messages[]." + string(blk.Type), Target: target,
				Reason: "unsupported content block",
			})
		}
	}
	return parts, warns
}

// audioFormat maps a MIME type onto OpenAI's short format name. The API takes
// only these two, so anything else is a drop rather than a guess.
func audioFormat(mime string) (string, bool) {
	switch mime {
	case "audio/wav", "audio/x-wav", "audio/wave":
		return "wav", true
	case "audio/mp3", "audio/mpeg":
		return "mp3", true
	default:
		return "", false
	}
}

// documentFilename supplies the name OpenAI requires alongside inline file
// data. The extension is what several providers sniff to decide how to parse it.
func documentFilename(mime string) string {
	switch mime {
	case "application/pdf":
		return "document.pdf"
	case "text/plain":
		return "document.txt"
	case "text/markdown":
		return "document.md"
	case "text/csv":
		return "document.csv"
	default:
		return "document"
	}
}
