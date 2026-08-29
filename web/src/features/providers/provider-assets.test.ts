import { describe, it, expect } from "vitest"
import { readdirSync } from "node:fs"
import { join } from "node:path"
import { BRAND_MARKS } from "./brand-marks"
import { PROVIDER_ASSETS } from "./provider-assets"

const files = new Set(readdirSync(join(__dirname, "../../../public/providers")))

describe("the provider logo manifest", () => {
  it("names a file that is actually shipped", () => {
    // The manifest exists so a tile never requests an image that is not
    // there: an entry with no file is a broken image on the screen whose
    // whole job is recognition.
    for (const [preset, asset] of Object.entries(PROVIDER_ASSETS)) {
      expect(files.has(asset.file), `${preset} -> ${asset.file}`).toBe(true)
    }
  })

  it("ships no file the manifest does not name", () => {
    // The other direction: a preset dropped from the registry must not leave
    // its logo behind to be embedded in every future binary.
    const named = new Set(Object.values(PROVIDER_ASSETS).map((a) => a.file))
    for (const file of files) {
      expect(named.has(file), file).toBe(true)
    }
  })

  it("does not duplicate a preset that already draws a brand mark", () => {
    // The mark wins at render time, so a file for the same preset would be an
    // asset nothing ever requests.
    for (const preset of Object.keys(PROVIDER_ASSETS)) {
      expect(BRAND_MARKS[preset], preset).toBeUndefined()
    }
  })

  it("covers the gateways an operator meets on the providers screen", () => {
    for (const id of ["chutes", "requesty", "nanogpt", "ovhcloud", "scaleway"]) {
      expect(PROVIDER_ASSETS[id], id).toBeDefined()
    }
  })
})
