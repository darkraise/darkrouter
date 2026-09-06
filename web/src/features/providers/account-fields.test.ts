import { describe, it, expect } from "vitest"
import {
  type AccountDraft,
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

describe("a provider whose endpoint carries an account", () => {
  it("takes the account from the single-credential field", () => {
    const draft: AccountDraft = { ...emptyAccounts, secret: "sk-one", accountId: "abc123" }
    expect(draftAccounts(draft, true)).toEqual([
      { label: "default", secret: "sk-one", account_id: "abc123" },
    ])
  })

  it("reads a different account per line in a bulk paste", () => {
    // Each Cloudflare token belongs to its own account, so a paste of five
    // keys is five accounts. One field applied to all of them would send four
    // of the five to the wrong address.
    const draft: AccountDraft = {
      ...emptyAccounts,
      mode: "bulk",
      bulk: "acct-a|sk-one\nacct-b|sk-two",
      accountId: "ignored",
    }
    expect(draftAccounts(draft, true)).toEqual([
      { label: "key-1", secret: "sk-one", account_id: "acct-a" },
      { label: "key-2", secret: "sk-two", account_id: "acct-b" },
    ])
  })

  it("takes a name before the account when the line carries three fields", () => {
    const out = parseBulkAccounts("work|acct-a|sk-one", "key", true)
    expect(out).toEqual([{ label: "work", secret: "sk-one", account_id: "acct-a" }])
  })

  it("drops a line that names no account", () => {
    // It cannot address the provider, and creating it would leave a credential
    // the gateway refuses on every request.
    expect(parseBulkAccounts("sk-lonely", "key", true)).toEqual([])
  })

  it("drops a line whose account is empty", () => {
    // A leading pipe, which is what a column pasted with a blank first field
    // looks like. The pipe makes it well-formed; the empty account still
    // cannot address anything.
    expect(parseBulkAccounts("|sk-lonely", "key", true)).toEqual([])
    expect(parseBulkAccounts("work||sk-lonely", "key", true)).toEqual([])
  })

  it("still splits name|key for the rest of the catalogue", () => {
    expect(parseBulkAccounts("work|sk-one", "key")).toEqual([
      { label: "work", secret: "sk-one" },
    ])
  })

  it("omits the field for the rest of the catalogue", () => {
    // Sending an empty account_id everywhere would make every provider look
    // like one that needs it.
    const out = draftAccounts({ ...emptyAccounts, secret: "sk-one" })
    expect(out[0]).not.toHaveProperty("account_id")
  })
})
