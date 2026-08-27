import { describe, it, expect } from "vitest"
import { maskSecret, parseBulkSecrets } from "./account-fields"

describe("parseBulkSecrets", () => {
  it("takes one key per line, in the order they were pasted", () => {
    expect(parseBulkSecrets("sk-a\nsk-b\nsk-c")).toEqual(["sk-a", "sk-b", "sk-c"])
  })

  it("ignores blank lines and surrounding whitespace", () => {
    // A paste out of a terminal or a spreadsheet brings both.
    expect(parseBulkSecrets("  sk-a  \n\n\n\tsk-b\n  ")).toEqual(["sk-a", "sk-b"])
  })

  it("strips the punctuation a CSV or JSON paste carries in", () => {
    expect(parseBulkSecrets('"sk-a",\n\'sk-b\',')).toEqual(["sk-a", "sk-b"])
  })

  it("drops a repeated key rather than making two accounts of it", () => {
    // Two credentials holding one key cool and fail as one while presenting
    // as two working accounts, which is the worst of both readings.
    expect(parseBulkSecrets("sk-a\nsk-b\nsk-a")).toEqual(["sk-a", "sk-b"])
  })

  it("reads an empty box as nothing to import", () => {
    expect(parseBulkSecrets("")).toEqual([])
    expect(parseBulkSecrets("\n\n")).toEqual([])
  })

  it("handles the CRLF a Windows paste brings", () => {
    expect(parseBulkSecrets("sk-a\r\nsk-b")).toEqual(["sk-a", "sk-b"])
  })
})

describe("maskSecret", () => {
  it("shows enough to recognise a key and never enough to use one", () => {
    expect(maskSecret("sk-abcdefghijkl")).toBe("sk-a••••••ijkl")
  })

  it("shows nothing at all of a short one", () => {
    // Four of eight characters is most of a short key.
    expect(maskSecret("sk-abcd")).toBe("•••••••")
  })
})
