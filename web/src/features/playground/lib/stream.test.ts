import { describe, expect, it } from "vitest"
import { drainSSE } from "./stream"

const delta = (text: string) => JSON.stringify({ choices: [{ delta: { content: text } }] })

describe("draining SSE frames", () => {
  it("splits frames on CRLF pairs as well as bare newlines", () => {
    // Some upstreams write \r\n line endings, and a parser that only knew
    // \n\n never found a frame boundary: the whole answer sat in `rest`.
    const out = drainSSE(`data: ${delta("Hel")}\r\n\r\ndata: ${delta("lo")}\r\n\r\n`, "openai")
    expect(out.text).toBe("Hello")
    expect(out.rest).toBe("")
  })

  it("accepts a data field with no space after the colon", () => {
    const out = drainSSE(`data:${delta("Hi")}\n\n`, "openai")
    expect(out.text).toBe("Hi")
  })

  it("joins a payload split over several data lines with newlines", () => {
    // The SSE spec concatenates data lines with \n; a JSON body pretty-printed
    // over three lines is one payload, not three broken ones.
    const body = JSON.stringify({ choices: [{ delta: { content: "joined" } }] }, null, 2)
    const lines = body.split("\n").map((l) => `data: ${l}`).join("\n")
    const out = drainSSE(`${lines}\n\n`, "openai")
    expect(out.text).toBe("joined")
  })

  it("ignores comment and event lines", () => {
    const out = drainSSE(`: keep-alive\nevent: message\ndata: ${delta("x")}\n\n`, "openai")
    expect(out.text).toBe("x")
  })

  it("surfaces an error frame as the run's error", () => {
    // A provider that fails mid-stream sends {"error": ...} as a frame and
    // then closes. Skipped as "not a delta", the run ended looking finished
    // with half an answer and no explanation.
    const out = drainSSE(
      `data: ${delta("par")}\n\ndata: {"error":{"message":"upstream exploded","type":"server_error"}}\n\n`,
      "openai",
    )
    expect(out.text).toBe("par")
    expect(out.error).toBe("upstream exploded")
  })

  it("reads a bare string error too", () => {
    const out = drainSSE(`data: {"error":"rate limited"}\n\n`, "openai")
    expect(out.error).toBe("rate limited")
  })

  it("reports no error for an ordinary frame", () => {
    expect(drainSSE(`data: ${delta("ok")}\n\n`, "openai").error).toBeUndefined()
  })

  it("holds a partial frame back whichever line ending it uses", () => {
    const out = drainSSE(`data: ${delta("Hi")}\r\n\r\ndata: {"choi`, "openai")
    expect(out.text).toBe("Hi")
    expect(out.rest).toBe('data: {"choi')
  })
})
