import { describe, expect, it } from "vitest"
import {
  configOfConversation,
  messagesOfTurns,
  routesOfTurns,
  titleFromPrompt,
} from "./conversations"
import type { PlaygroundConversation, PlaygroundStoredTurn } from "../../../lib/api-types"

function conversation(over: Partial<PlaygroundConversation> = {}): PlaygroundConversation {
  return {
    id: "c1",
    title: "New chat",
    dialect: "anthropic",
    model: "claude",
    config: {},
    preview: "",
    created_at: "2026-08-30T10:00:00Z",
    updated_at: "2026-08-30T10:00:00Z",
    ...over,
  }
}

describe("titleFromPrompt", () => {
  it("keeps a short prompt whole", () => {
    expect(titleFromPrompt("summarise this")).toBe("summarise this")
  })

  it("truncates on a word boundary rather than mid-word", () => {
    const long =
      "explain the difference between speculative decoding and ordinary autoregressive sampling"
    const title = titleFromPrompt(long)
    expect(title.length).toBeLessThanOrEqual(52)
    expect(long.startsWith(title.replace(/…$/, ""))).toBe(true)
    expect(title).not.toMatch(/\s…$/)
    expect(title.endsWith("…")).toBe(true)
  })

  it("falls back on a single word longer than the limit", () => {
    const title = titleFromPrompt("x".repeat(80))
    expect(title.length).toBeLessThanOrEqual(52)
  })

  it("never returns an empty title, because the rail would draw a blank row", () => {
    expect(titleFromPrompt("   ")).toBe("New chat")
    expect(titleFromPrompt("")).toBe("New chat")
  })
})

describe("configOfConversation", () => {
  it("restores the system prompt that shaped the transcript", () => {
    const config = configOfConversation(
      conversation({ config: { system: "answer in one line", topK: "40" } }),
    )
    expect(config.system).toBe("answer in one line")
    expect(config.topK).toBe("40")
    expect(config.model).toBe("claude")
    expect(config.dialect).toBe("anthropic")
  })

  it("drops a wrong-typed stored field rather than crashing on it", () => {
    // chatBody calls .split and .trim on these without checking, so a value of
    // the wrong type is a crash rather than a degraded setting.
    const config = configOfConversation(conversation({ config: { stopRaw: 42, system: "ok" } }))
    expect(config.stopRaw).toBe("")
    expect(config.system).toBe("ok")
  })

  it("falls back to openai for a dialect the pane cannot render", () => {
    // A row can be written by hand with curl, and dialect-support.ts has no
    // fallback case for a fourth wire.
    const config = configOfConversation(
      conversation({ dialect: "mistral" as PlaygroundConversation["dialect"] }),
    )
    expect(config.dialect).toBe("openai")
  })
})

describe("messagesOfTurns and routesOfTurns", () => {
  const turns: PlaygroundStoredTurn[] = [
    { seq: 0, role: "user", content: "hello", request_id: "", created_at: "2026-08-30T10:00:00Z" },
    { seq: 1, role: "assistant", content: "hi", request_id: "req-1", created_at: "2026-08-30T10:00:01Z" },
    { seq: 2, role: "user", content: "again", request_id: "", created_at: "2026-08-30T10:00:02Z" },
    { seq: 3, role: "assistant", content: "sure", request_id: "", created_at: "2026-08-30T10:00:03Z" },
  ]

  it("reopens the transcript in order", () => {
    expect(messagesOfTurns(turns)).toEqual([
      { role: "user", content: "hello" },
      { role: "assistant", content: "hi" },
      { role: "user", content: "again" },
      { role: "assistant", content: "sure" },
    ])
  })

  it("keeps the trace link on the turn it explains, and only there", () => {
    const routes = routesOfTurns(turns)
    expect(Object.keys(routes)).toEqual(["1"])
    expect(routes[1]?.requestId).toBe("req-1")
    // Nothing else survives a reopen: the readings came from the trace, and
    // fabricating them here would print numbers nobody measured.
    expect(routes[1]?.totalMs).toBeNull()
    expect(routes[1]?.provider).toBe("")
  })

  it("treats a missing trace as ordinary", () => {
    // The request log's retention sweep outlives plenty of conversations.
    expect(routesOfTurns(turns)[3]).toBeUndefined()
  })
})
