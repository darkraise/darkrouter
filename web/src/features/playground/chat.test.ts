import { describe, it, expect } from "vitest"
import { drainSSE, extractUnaryText } from "./lib/stream"
import { parseTools, chatBody, seedFromTrace } from "./lib/request"
import type { RequestTrace } from "../../lib/api-types"

describe("parseTools", () => {
  it("accepts a JSON array of objects", () => {
    expect(parseTools('[{"type":"function"}]')).toEqual({ tools: [{ type: "function" }] })
  })

  it("treats an empty box as no tools rather than as an error", () => {
    expect(parseTools("   ")).toEqual({})
  })

  it("names a parse failure instead of sending nothing", () => {
    // Silently dropping malformed tools would answer a different question
    // from the one the operator asked, and look like the model ignoring them.
    const out = parseTools("{not json")
    expect(out.tools).toBeUndefined()
    expect(out.error).toMatch(/json/i)
  })

  it("refuses a bare object, which is not the wire shape", () => {
    expect(parseTools('{"type":"function"}').error).toMatch(/array/i)
  })
})

describe("chatBody", () => {
  it("sends the transcript, not just the last turn", () => {
    const body = chatBody({
      model: "m", dialect: "openai", system: "", stream: true,
      temperature: "", maxTokens: "", toolsRaw: "",
      messages: [
        { role: "user", content: "hi" },
        { role: "assistant", content: "hello" },
        { role: "user", content: "go on" },
      ],
    })
    expect(body.messages).toHaveLength(3)
  })

  it("omits empty numeric fields rather than sending zero", () => {
    // A temperature of 0 is a real setting. An empty box is not, and sending
    // zero for it would quietly make every run deterministic.
    const body = chatBody({
      model: "m", dialect: "openai", system: "", stream: true,
      temperature: "", maxTokens: "", toolsRaw: "", messages: [],
    })
    expect(body.temperature).toBeUndefined()
    expect(body.max_tokens).toBeUndefined()
  })

  it("sends an explicit zero temperature", () => {
    const body = chatBody({
      model: "m", dialect: "openai", system: "", stream: true,
      temperature: "0", maxTokens: "", toolsRaw: "", messages: [],
    })
    expect(body.temperature).toBe(0)
  })

  it("carries the dialect through", () => {
    const body = chatBody({
      model: "m", dialect: "anthropic", system: "be terse", stream: false,
      temperature: "", maxTokens: "", toolsRaw: "", messages: [],
    })
    expect(body.dialect).toBe("anthropic")
    expect(body.system).toBe("be terse")
    expect(body.stream).toBe(false)
  })
})

describe("seedFromTrace", () => {
  it("takes the model the client asked for, not the one that served", () => {
    // Replaying against the serving provider would not reproduce the routing
    // decision, which is usually the thing under investigation.
    const seeded = seedFromTrace({
      id: "r1", model: "fast", final_model: "llama-3.3", provider: "groq",
    } as RequestTrace)
    expect(seeded.model).toBe("fast")
  })

  it("carries the dialect the request arrived on", () => {
    const seeded = seedFromTrace({ id: "r1", model: "m", dialect: "anthropic" } as RequestTrace)
    expect(seeded.dialect).toBe("anthropic")
  })

  it("falls back to openai for a dialect the playground cannot send", () => {
    // The log records every inbound dialect, including the OpenAI Responses
    // wire, which this screen has no control for.
    const seeded = seedFromTrace({ id: "r1", model: "m", dialect: "responses" } as RequestTrace)
    expect(seeded.dialect).toBe("openai")
  })
})

// Fixtures below mirror the exact wire shapes internal/edge/{openai,
// anthropic,gemini}/stream.go and write.go emit — not a guessed shape.

describe("drainSSE", () => {
  it("reads an openai chunk's choices[0].delta.content", () => {
    const frame = 'data: {"choices":[{"index":0,"delta":{"content":"Hi"},"finish_reason":null}]}\n\n'
    expect(drainSSE(frame, "openai").text).toBe("Hi")
  })

  it("ignores the openai [DONE] sentinel", () => {
    expect(drainSSE("data: [DONE]\n\n", "openai")).toEqual({ text: "", rest: "" })
  })

  it("reads text only from an anthropic content_block_delta frame", () => {
    // message_start and content_block_start carry no delta text and must be
    // skipped rather than crashing the parse.
    const buffer =
      'event: message_start\ndata: {"type":"message_start","message":{"id":"m1"}}\n\n' +
      'event: content_block_start\ndata: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}\n\n' +
      'event: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}\n\n' +
      'event: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}\n\n' +
      'event: content_block_stop\ndata: {"type":"content_block_stop","index":0}\n\n'
    expect(drainSSE(buffer, "anthropic").text).toBe("Hello")
  })

  it("ignores an anthropic thinking_delta, which carries no `text`", () => {
    const frame =
      'event: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"reasoning..."}}\n\n'
    expect(drainSSE(frame, "anthropic").text).toBe("")
  })

  it("reads text parts from a gemini candidate chunk", () => {
    const frame =
      'data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hi"}]},"index":0}],"modelVersion":"m"}\n\n'
    expect(drainSSE(frame, "gemini").text).toBe("Hi")
  })

  it("excludes a gemini thought part from the rendered text", () => {
    // A thought part carries thought: true alongside its own text field, and
    // is not the answer.
    const frame =
      'data: {"candidates":[{"content":{"role":"model","parts":[{"text":"thinking","thought":true},{"text":"answer"}]},"index":0}]}\n\n'
    expect(drainSSE(frame, "gemini").text).toBe("answer")
  })

  it("holds an incomplete frame back for the next chunk", () => {
    const { text, rest } = drainSSE('data: {"choices":[{"delta":{"content":"Hi"}}]}\n\ndata: {"choi', "openai")
    expect(text).toBe("Hi")
    expect(rest).toBe('data: {"choi')
  })
})

describe("extractUnaryText", () => {
  it("reads an openai unary response's choices[0].message.content", () => {
    const body = JSON.stringify({
      id: "chatcmpl-1", object: "chat.completion", model: "m",
      choices: [{ index: 0, message: { role: "assistant", content: "Hello" }, finish_reason: "stop" }],
    })
    expect(extractUnaryText("openai", body)).toBe("Hello")
  })

  it("reads a null openai content as no text rather than throwing", () => {
    const body = JSON.stringify({ choices: [{ message: { role: "assistant", content: null } }] })
    expect(extractUnaryText("openai", body)).toBe("")
  })

  it("reads an anthropic unary response's text content blocks", () => {
    const body = JSON.stringify({
      id: "msg_1", type: "message", role: "assistant", model: "m",
      content: [{ type: "text", text: "Hello" }],
      stop_reason: "end_turn", stop_sequence: null,
    })
    expect(extractUnaryText("anthropic", body)).toBe("Hello")
  })

  it("skips an anthropic tool_use block, which carries no text", () => {
    const body = JSON.stringify({
      content: [
        { type: "tool_use", id: "t1", name: "lookup", input: {} },
        { type: "text", text: "done" },
      ],
    })
    expect(extractUnaryText("anthropic", body)).toBe("done")
  })

  it("reads a gemini unary response's candidate parts", () => {
    const body = JSON.stringify({
      candidates: [{ content: { role: "model", parts: [{ text: "Hello" }] }, finishReason: "STOP", index: 0 }],
      modelVersion: "m", responseId: "r1",
    })
    expect(extractUnaryText("gemini", body)).toBe("Hello")
  })
})
