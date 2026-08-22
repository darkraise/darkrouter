// Package ir holds Darkrouter's canonical request, response, and stream types.
// It has no I/O and depends on no other internal package.
package ir

import "encoding/json"

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
type Surface string

const (
	SurfaceLLM         Surface = "llm"
	SurfaceEmbeddings  Surface = "embeddings"
	SurfaceImages      Surface = "images"
	SurfaceAudio       Surface = "audio"
	SurfaceRerank      Surface = "rerank"
	SurfaceModerations Surface = "moderations"
)

// ParseSurface converts a stored or inbound string. It reports failure rather
// than defaulting, because a request routed to the wrong surface fails in a
// much more confusing way than one refused up front.
func ParseSurface(s string) (Surface, bool) {
	switch Surface(s) {
	case SurfaceLLM, SurfaceEmbeddings, SurfaceImages,
		SurfaceAudio, SurfaceRerank, SurfaceModerations:
		return Surface(s), true
	default:
		return "", false
	}
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
)

type Media struct {
	MIME string
	Data string // base64
	URL  string
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
}

type ToolResult struct {
	ToolUseID string
	Content   string
	IsError   bool
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
	Effort string // "low" | "medium" | "high"
	Budget int
}

type ResponseFormat struct {
	Type   string // "json_schema"
	Schema json.RawMessage
}

type SafetySetting struct {
	Category  string
	Threshold string
}

type Request struct {
	Model          string
	System         []ContentBlock
	Messages       []Message
	Tools          []Tool
	ToolChoice     *ToolChoice
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
}

type Usage struct {
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	ReasoningTokens  int
	Estimated        bool
}

type Warning struct {
	Field  string
	Target string
	Reason string
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
		for _, b := range m.Content {
			if b.Type == BlockImage {
				n.Vision = true
			}
		}
	}
	return n
}
