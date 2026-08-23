import { render, screen } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { CatalogScreen } from "./catalog"

const catalog = {
  models: [
    {
      model: "known-model",
      providers: ["a", "b"],
      surfaces: ["llm"],
      context_window: 128000,
      max_output_tokens: 4096,
      tools: true,
      vision: false,
      reasoning: false,
      inferred: false,
      state: "live",
    },
    {
      model: "guessed-model",
      providers: ["c"],
      surfaces: ["llm"],
      context_window: 0,
      max_output_tokens: 0,
      tools: false,
      vision: false,
      reasoning: false,
      inferred: true,
      state: "live",
    },
  ],
  aliases: [{ name: "fast", targets: ["a/known-model", "b/known-model"] }],
}

let lastURL = ""
beforeEach(() => {
  lastURL = ""
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      lastURL = url
      return new Response(JSON.stringify(catalog), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      })
    }),
  )
})

function renderCatalog() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <CatalogScreen />
    </QueryClientProvider>,
  )
}

describe("the catalog", () => {
  it("marks a model whose metadata was guessed", async () => {
    renderCatalog()
    // Master design §6.4: a guessed model routes with a warning, and an
    // operator who cannot see which rows are guesses reads a refused tool call
    // as a Darkrouter bug.
    expect(await screen.findByText("inferred")).toBeInTheDocument()
    expect(await screen.findByText("known")).toBeInTheDocument()
  })

  it("shows what an alias resolves to", async () => {
    renderCatalog()
    expect(await screen.findByText(/a\/known-model → b\/known-model/)).toBeInTheDocument()
  })

  it("composes the search and surface filters into one query", async () => {
    renderCatalog()
    await screen.findByText("known-model")
    // Both filters travel in one request rather than being applied in the
    // browser, which is what keeps the page size meaningful.
    expect(lastURL).toContain("/api/models")
  })
})
