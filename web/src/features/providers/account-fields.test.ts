import { describe, it, expect } from "vitest"
import {
  draftAccounts,
  emptyAccounts,
  maskSecret,
  parseBulkAccounts,
  secretFieldFor,
} from "./account-fields"

describe("parseBulkAccounts", () => {
  it("reads name|key, one per line", () => {
    expect(parseBulkAccounts("work|sk-a\nspare|sk-b")).toEqual([
      { label: "work", secret: "sk-a" },
      { label: "spare", secret: "sk-b" },
    ])
  })

  it("treats a line with no pipe as all key", () => {
    // A column pasted out of a password manager has no names in it.
    expect(parseBulkAccounts("sk-a\nsk-b")).toEqual([
      { label: "key-1", secret: "sk-a" },
      { label: "key-2", secret: "sk-b" },
    ])
  })

  it("numbers the unnamed lines by their position among the unnamed", () => {
    // The fallback counts what it has produced, so a named line in the middle
    // does not leave a gap in the numbering.
    expect(parseBulkAccounts("work|sk-a\nsk-b").map((a) => a.label)).toEqual([
      "work",
      "key-2",
    ])
  })

  it("splits on the first pipe only, so a key may contain one", () => {
    expect(parseBulkAccounts("work|sk-a|b|c")).toEqual([
      { label: "work", secret: "sk-a|b|c" },
    ])
  })

  it("takes the prefix the operator chose", () => {
    expect(parseBulkAccounts("sk-a", "prod")).toEqual([{ label: "prod-1", secret: "sk-a" }])
  })

  it("ignores blank lines and surrounding whitespace", () => {
    expect(parseBulkAccounts("  work | sk-a  \n\n\n\tsk-b\n  ")).toEqual([
      { label: "work", secret: "sk-a" },
      { label: "key-2", secret: "sk-b" },
    ])
  })

  it("strips the punctuation a CSV or JSON paste carries in", () => {
    expect(parseBulkAccounts('"sk-a",\n\'sk-b\',')).toEqual([
      { label: "key-1", secret: "sk-a" },
      { label: "key-2", secret: "sk-b" },
    ])
  })

  it("drops a repeated key rather than making two accounts of it", () => {
    // Two credentials holding one key cool and fail as one while presenting
    // as two working accounts, which is the worst of both readings. The name
    // does not make them distinct.
    expect(parseBulkAccounts("work|sk-a\nspare|sk-a")).toEqual([
      { label: "work", secret: "sk-a" },
    ])
  })

  it("handles the CRLF a Windows paste brings", () => {
    expect(parseBulkAccounts("work|sk-a\r\nsk-b").map((a) => a.secret)).toEqual([
      "sk-a",
      "sk-b",
    ])
  })

  it("reads an empty box as nothing to import", () => {
    expect(parseBulkAccounts("")).toEqual([])
    expect(parseBulkAccounts("\n\n")).toEqual([])
  })
})

describe("draftAccounts", () => {
  it("labels a single unnamed account 'default'", () => {
    expect(draftAccounts({ ...emptyAccounts, secret: " sk-a " })).toEqual([
      { label: "default", secret: "sk-a" },
    ])
  })

  it("reads an empty single account as nothing", () => {
    expect(draftAccounts(emptyAccounts)).toEqual([])
  })

  it("reads the bulk box when the mode says so", () => {
    expect(
      draftAccounts({ ...emptyAccounts, mode: "bulk", bulk: "work|sk-a" }),
    ).toEqual([{ label: "work", secret: "sk-a" }])
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

describe("secretFieldFor", () => {
  it("asks for an API key by default", () => {
    const field = secretFieldFor("groq")
    expect(field.label).toBe("API key")
    expect(field.multiline).toBe(false)
  })

  it("asks a local CLI provider for the thing its vendor actually issues", () => {
    // Augment issues no API key. Labelling the box "API key" would send the
    // operator looking for something that does not exist, and a session
    // document does not fit a one-line password input.
    const field = secretFieldFor("auggie")
    expect(field.label).toBe("Augment session")
    expect(field.multiline).toBe(true)
    expect(field.help).toMatch(/auggie login/)
    // The empty case is legitimate here and the copy has to say so: a login
    // done inside the container needs no account at all.
    expect(field.help).toMatch(/Leave this empty/)
  })
})
