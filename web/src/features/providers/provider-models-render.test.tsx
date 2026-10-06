import { render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { ProviderModels } from "./provider-models"
import type { Model } from "../../lib/api-types"

const model = (over: Partial<Model> & { model: string }): Model => ({
  providers: ["groq"],
  surfaces: ["llm"],
  context_window: 8192,
  max_output_tokens: 4096,
  tools: false,
  vision: false,
  reasoning: false,
  inferred: false,
  state: "live",
  pricing: null,
  free_tier: null,
  merge_source: "models_dev",
  ...over,
})

describe("the provider's models table", () => {
  it("prints both prices through the console's one per-million formatter", () => {
    // A zero price is free and says so; the old cell printed $0.0000, which
    // is the string every other screen reserves for "rounds to nothing".
    render(
      <ProviderModels
        models={[
          model({
            model: "paid",
            pricing: {
              input_micros: 150000,
              output_micros: 600000,
              price_source: "models_dev",
              price_grade: "indexed",
            },
          }),
          model({
            model: "gratis",
            pricing: {
              input_micros: 0,
              output_micros: 0,
              price_source: "models_dev",
              price_grade: "indexed",
            },
          }),
        ]}
        loading={false}
      />,
    )
    expect(screen.getByText("$0.1500 / $0.6000")).toBeInTheDocument()
    expect(screen.getByText("free / free")).toBeInTheDocument()
  })

  it("never reads a model serving chat and embeddings as embedding alone", () => {
    render(
      <ProviderModels
        models={[
          model({ model: "chat" }),
          model({ model: "embed-only", surfaces: ["embedding"] }),
          model({ model: "both", surfaces: ["llm", "embedding"] }),
        ]}
        loading={false}
      />,
    )
    // The capabilities cell's badges, each kept on one line.
    const cells = (name: string) =>
      screen.getByText(name).closest("tr")!.querySelectorAll("td")[1]!
        .querySelectorAll(".whitespace-nowrap")
    expect(cells("chat")).toHaveLength(0)
    expect([...cells("embed-only")].map((b) => b.textContent)).toEqual(["embedding"])
    expect([...cells("both")].map((b) => b.textContent)).toEqual(["llm", "embedding"])
  })

  it("says a failing sweep is failing rather than that nothing has asked", () => {
    render(
      <ProviderModels
        models={[]}
        loading={false}
        discovery={{
          provider_id: "aihorde", total: 0, live: 0, stale: 0, removed_upstream: 0,
          max_missing_streak: 0, filtered_out: 0, consecutive_failures: 1, last_error: "Forbidden",
        }}
      />,
    )
    expect(screen.getByText("Discovery has failed once")).toBeInTheDocument()
    expect(screen.getByText(/Forbidden/)).toBeInTheDocument()
    expect(screen.queryByText(/nothing has asked/i)).toBeNull()
  })

  it("tells an empty answer apart from a sweep that never ran", () => {
    const { unmount } = render(<ProviderModels models={[]} loading={false} />)
    expect(screen.getByText(/nothing has asked/i)).toBeInTheDocument()
    expect(screen.queryByText(/one of its own keys/)).toBeNull()
    unmount()
    render(
      <ProviderModels
        models={[]}
        loading={false}
        discovery={{
          provider_id: "lmstudio", total: 0, live: 0, stale: 0, removed_upstream: 0,
          max_missing_streak: 0, filtered_out: 0, consecutive_failures: 0,
        }}
      />,
    )
    expect(screen.getByText("The last sweep found nothing to import")).toBeInTheDocument()
  })

  it("names the unit once, in the header, and gives the name column room", () => {
    render(<ProviderModels models={[model({ model: "a-rather-long-model-name" })]} loading={false} />)
    expect(screen.getByRole("columnheader", { name: /\$ \/ M tokens/ })).toBeInTheDocument()
    expect(screen.getByRole("columnheader", { name: "Model" })).toHaveClass("min-w-[16rem]")
  })
})
