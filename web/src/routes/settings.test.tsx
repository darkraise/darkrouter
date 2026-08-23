import { render, screen } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { SettingsScreen } from "./settings"

beforeEach(() => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      const body = url.includes("/api/config")
        ? {
            valid: false,
            error: 'provider "groq": base_url is required',
            serving: "the previous configuration is still serving",
            warnings: [],
            aliases: {},
          }
        : url.includes("/api/presets")
          ? { presets: [] }
          : { providers: [] }
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      })
    }),
  )
})

describe("the settings screen", () => {
  it("shows a validation error and says the previous config is still serving", async () => {
    // An operator seeing a red error and nothing else assumes the gateway is
    // down. It is not, and saying so is the difference between a calm fix and
    // a panicked restart.
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <SettingsScreen />
      </QueryClientProvider>,
    )
    expect(await screen.findByText(/Configuration is invalid/i)).toBeInTheDocument()
    expect(await screen.findByText(/base_url is required/)).toBeInTheDocument()
    expect(
      await screen.findByText(/previous configuration is still serving/i),
    ).toBeInTheDocument()
  })
})
