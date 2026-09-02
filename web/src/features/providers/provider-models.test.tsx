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
          model({ model: "paid", pricing: { input_micros: 150000, output_micros: 600000 } }),
          model({ model: "gratis", pricing: { input_micros: 0, output_micros: 0 } }),
        ]}
        loading={false}
      />,
    )
    expect(screen.getByText("$0.1500 / $0.6000")).toBeInTheDocument()
    expect(screen.getByText("free / free")).toBeInTheDocument()
  })

  it("names the unit once, in the header, and gives the name column room", () => {
    render(<ProviderModels models={[model({ model: "a-rather-long-model-name" })]} loading={false} />)
    expect(screen.getByRole("columnheader", { name: /\$ \/ M tokens/ })).toBeInTheDocument()
    expect(screen.getByRole("columnheader", { name: "Model" })).toHaveClass("min-w-[16rem]")
  })
})
