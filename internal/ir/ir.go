// Package ir holds Darkrouter's canonical request, response, and stream types.
// It has no I/O and depends on no other internal package.
package ir

import (
	"encoding/json"
	"strings"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Surface is the kind of work a request asks for. It lives here rather than in
// the router or the catalog because both need it and either import would
// create a cycle.
//
// The seven values are master design §6's, and the strings are data: presets
// declare them and the models.surfaces column stores them. tts and stt are
// separate because a provider can serve /v1/audio/speech without serving
// /v1/audio/transcriptions, and one "audio" surface cannot express that.
type Surface string

const (
	SurfaceLLM        Surface = "llm"
	SurfaceEmbedding  Surface = "embedding"
	SurfaceImage      Surface = "image"
	SurfaceTTS        Surface = "tts"
	SurfaceSTT        Surface = "stt"
	SurfaceRerank     Surface = "rerank"
	SurfaceModeration Surface = "moderation"
)

// AllSurfaces returns the vocabulary in master design order. Callers that
// enumerate surfaces — the adapter matrix test, the admin API — use this rather
// than repeating the list.
func AllSurfaces() []Surface {
	return []Surface{
		SurfaceLLM, SurfaceEmbedding, SurfaceImage,
		SurfaceTTS, SurfaceSTT, SurfaceRerank, SurfaceModeration,
	}
}

// ParseSurface converts a stored or inbound string. It reports failure rather
// than defaulting, because a request routed to the wrong surface fails in a
// much more confusing way than one refused up front.
func ParseSurface(s string) (Surface, bool) {
	for _, known := range AllSurfaces() {
		if Surface(s) == known {
			return known, true
		}
	}
	return "", false
}

type BlockType string

const (
	BlockText             BlockType = "text"
	BlockImage            BlockType = "image"
	BlockAudio            BlockType = "audio"
	BlockDocument         BlockType = "document"
	BlockThinking         BlockType = "thinking"
	BlockRedactedThinking BlockType = "redacted_thinking"
	BlockToolUse          BlockType = "tool_use"
	BlockToolResult       BlockType = "tool_result"
)

type StopReason string

const (
	StopEndTurn       StopReason = "end_turn"
	StopMaxTokens     StopReason = "max_tokens"
	StopToolUse       StopReason = "tool_use"
	StopStopSequence  StopReason = "stop_sequence"
	StopContentFilter StopReason = "content_filter"
	StopPauseTurn     StopReason = "pause_turn"
	StopError         StopReason = "error"
)

type ErrorType string

const (
	ErrInvalidRequest ErrorType = "invalid_request"
	ErrAuthentication ErrorType = "authentication"
	ErrPermission     ErrorType = "permission"
	ErrNotFound       ErrorType = "not_found"
	ErrRateLimit      ErrorType = "rate_limit"
	ErrOverloaded     ErrorType = "overloaded"
	ErrAPI            ErrorType = "api_error"
	ErrContentFilter  ErrorType = "content_filter"
	ErrDarkrouter     ErrorType = "darkrouter"
	// ErrPayloadTooLarge is an inbound body over the configured cap. It is
	// distinct from ErrInvalidRequest because the two tell a client different
	// things: one says the request is malformed and retrying is pointless, the
	// other says send something smaller.
	ErrPayloadTooLarge ErrorType = "payload_too_large"
	// ErrUnsupportedMedia is an inbound body Darkrouter will not decode —
	// today, one carrying a Content-Encoding other than identity. Distinct
	// from ErrInvalidRequest because a compressed body reaching a JSON parser
	// comes back as "invalid JSON", which sends the client looking for a
	// syntax error that is not there.
	ErrUnsupportedMedia ErrorType = "unsupported_media"
)

type Media struct {
	MIME string
	Data string // base64
	URL  string

	// FileID is a provider-side handle — an OpenAI file_id, a Gemini
	// fileData.fileUri, an Anthropic file source. It is not interchangeable
	// with URL: a target that accepts its own handle will reject a public
	// address and vice versa.
	FileID string
}

type Thinking struct {
	Text      string
	Signature string
	Data      string // redacted payload
}

type ToolUse struct {
	ID    string
	Name  string
	Input json.RawMessage
	// Signature is an opaque token some providers attach to a tool call and
	// require back on the next turn (Gemini's thoughtSignature on a
	// functionCall part). It is carried untouched and re-emitted only by the
	// dialect that produced it.
	Signature string
}

type ToolResult struct {
	ToolUseID string
	Content   []ContentBlock
	IsError   bool
}

// Text flattens the text blocks. OpenAI tool messages and Gemini
// functionResponse payloads are text-only, so both need this and both must
// separately account for whatever it leaves behind.
func (t *ToolResult) Text() string {
	if t == nil {
		return ""
	}
	var b strings.Builder
	for _, blk := range t.Content {
		if blk.Type == BlockText {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}

type CacheControl struct {
	Type string // "ephemeral"
	TTL  string // "5m" | "1h"
}

type ContentBlock struct {
	Type         BlockType
	Text         string
	Media        *Media
	Thinking     *Thinking
	ToolUse      *ToolUse
	ToolResult   *ToolResult
	CacheControl *CacheControl
	Extra        map[string]json.RawMessage
}

type Message struct {
	Role    Role
	Content []ContentBlock
}

type Tool struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

type ToolChoice struct {
	Mode string // "auto" | "none" | "any" | "tool"
	Name string
}

type Reasoning struct {
	Effort string // "minimal" | "low" | "medium" | "high" | "xhigh" | "max"
	Budget int
	// Disabled records an explicit client request to turn thinking off
	// (Gemini thinkingBudget: 0, Anthropic thinking.type: "disabled"). Budget
	// zero alone means unset, so without this flag an explicit off was
	// indistinguishable from silence.
	Disabled bool
}

type ResponseFormat struct {
	Type   string // "json_schema" | "json_object"
	Schema json.RawMessage
	// Name and Strict travel with a json_schema format. Strict is a pointer
	// because OpenAI's strict mode changes what schemas are accepted, so a
	// client that did not ask for it must not be given it.
	Name   string
	Strict *bool
}

type SafetySetting struct {
	Category  string
	Threshold string
}

type Request struct {
	Model      string
	System     []ContentBlock
	Messages   []Message
	Tools      []Tool
	ToolChoice *ToolChoice

	// ParallelToolCalls is a pointer because false and unset differ: Anthropic
	// inverts it into disable_parallel_tool_use, and sending that unasked
	// changes behavior.
	ParallelToolCalls *bool

	MaxTokens      *int
	Temperature    *float64
	TopP           *float64
	TopK           *int
	StopSequences  []string
	Stream         bool
	Reasoning      *Reasoning
	ResponseFormat *ResponseFormat
	Safety         []SafetySetting
	Metadata       map[string]string
	Extra          map[string]json.RawMessage

	// Warnings are losses the inbound parse discovered. Until phase 5 every
	// warning came from an adapter rendering outbound; a dialect that drops
	// something on the way in had nowhere to say so.
	Warnings []Warning
}

type Usage struct {
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	ReasoningTokens  int
	Estimated        bool
}

// PromptTokens is the inclusive prompt count that OpenAI-compatible and
// Gemini dialects report. InputTokens excludes cache reads so that pricing
// can charge each at its own rate without overlap; a dialect that reports
// one combined number adds them back here.
func (u Usage) PromptTokens() int { return u.InputTokens + u.CacheReadTokens }

type Warning struct {
	Field  string
	Target string
	Reason string
}

func (w Warning) String() string {
	return w.Field + " -> " + w.Target + ": " + w.Reason
}

type Response struct {
	ID         string
	Model      string
	Content    []ContentBlock
	StopReason StopReason
	Usage      Usage
	Warnings   []Warning
	Extra      map[string]json.RawMessage
}

type Error struct {
	Type    ErrorType
	Message string
	Code    string
}

func (e *Error) Error() string { return string(e.Type) + ": " + e.Message }

type EventType string

const (
	EventMessageStart EventType = "message_start"
	EventBlockStart   EventType = "content_block_start"
	EventContentDelta EventType = "content_delta"
	EventBlockStop    EventType = "content_block_stop"
	EventMessageDelta EventType = "message_delta"
	EventMessageStop  EventType = "message_stop"
	EventPing         EventType = "ping"
	EventError        EventType = "error"
)

// Delta carries incremental content for exactly one block kind.
type Delta struct {
	Type      BlockType
	Text      string
	Thinking  string
	Signature string // thinking-block signature fragment
	ToolInput string // JSON fragment
	ToolID    string
	ToolName  string
}

type StreamEvent struct {
	Type       EventType
	Index      int
	Delta      *Delta
	Usage      *Usage
	StopReason StopReason
	Err        *Error

	// ID and Model are carried on EventMessageStart only. Without them an edge
	// dialect has no way to learn either, and every streamed chunk would report
	// an empty model.
	ID    string
	Model string

	// Warnings record what this event's translation could not express. The
	// unary path carries them on Response; without this the streaming path,
	// which is what every CLI uses by default, could only lose things silently.
	Warnings []Warning
}

// Needs reports the capabilities a target must have to serve this request.
// The router consults it; it is the reason every request is parsed to IR even
// on the passthrough path.
type Needs struct {
	Tools     bool
	Vision    bool
	Reasoning bool
}

func (r *Request) Needs() Needs {
	n := Needs{
		Tools:     len(r.Tools) > 0,
		Reasoning: r.Reasoning != nil,
	}
	for _, m := range r.Messages {
		if blocksHaveImage(m.Content) {
			n.Vision = true
		}
	}
	return n
}

// blocksHaveImage descends one level into tool results. A tool that returns a
// screenshot needs vision exactly as much as a user who attaches one, and the
// router would otherwise pick a text-only model for it.
func blocksHaveImage(blocks []ContentBlock) bool {
	for _, b := range blocks {
		if b.Type == BlockImage {
			return true
		}
		if b.ToolResult != nil {
			for _, inner := range b.ToolResult.Content {
				if inner.Type == BlockImage {
					return true
				}
			}
		}
	}
	return false
}
