import { describe, it, expect } from "vitest"
import { passwordProblem, revokedText } from "./change-password-dialog"

describe("passwordProblem", () => {
  it("holds the server's floor so a typo costs no round trip", () => {
    expect(passwordProblem("short", "short")).toMatch(/12 characters/)
  })

  it("catches the mistyped confirmation", () => {
    expect(passwordProblem("correct horse battery", "correct horse bettery")).toMatch(
      /do not match/,
    )
  })

  it("passes a password that satisfies both", () => {
    expect(passwordProblem("correct horse battery", "correct horse battery")).toBeNull()
  })
})

describe("revokedText", () => {
  it("says so when there was nothing else to revoke", () => {
    // Silence here makes the next login failure elsewhere look like a fault.
    expect(revokedText(0)).toMatch(/no other sessions/i)
  })

  it("counts the sessions it signed out", () => {
    expect(revokedText(1)).toMatch(/1 other session\b/)
    expect(revokedText(3)).toMatch(/3 other sessions/)
  })
})
