import { describe, it, expect } from "vitest"
import { locationPatch } from "./provider-detail"

describe("locationPatch", () => {
  it("sends only the field that was touched", () => {
    // region and project are pointer fields on the backend: a key present
    // with value "" means "set this to empty", not "leave alone". Sending
    // both unconditionally would wipe whichever one the operator did not
    // edit, since GET /api/providers never returns either to re-send back.
    expect(locationPatch("us-east1", null)).toEqual({ region: "us-east1" })
    expect(locationPatch(null, "my-gcp-project")).toEqual({ project: "my-gcp-project" })
  })

  it("sends nothing when neither field was touched", () => {
    expect(locationPatch(null, null)).toEqual({})
  })

  it("sends both when both were touched, including an intentional clear", () => {
    // Once an operator has focused a field, an empty string is a deliberate
    // clear rather than an unset value, and has to travel as one.
    expect(locationPatch("", "my-gcp-project")).toEqual({ region: "", project: "my-gcp-project" })
  })
})
