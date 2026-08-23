import { render, screen } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import userEvent from "@testing-library/user-event"
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

const oauthProvider = {
  id: "sub",
  name: "Claude subscription",
  preset: "anthropic-oauth",
  kind: "anthropic",
  base_url: "https://api.anthropic.com/v1",
  auth_style: "oauth",
  priority: 0,
  enabled: true,
  credentials: [] as {
    id: string
    label: string
    masked: string
    enabled: boolean
    cooling: boolean
  }[],
}

// renderSettings stubs every endpoint the screen loads, so a case only has to
// say what it wants to differ.
function renderSettings(opts: {
  providers?: unknown[]
  startResponse?: unknown
}) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      const body = url.includes("/api/config")
        ? { valid: true, warnings: [], aliases: {} }
        : url.includes("/api/presets")
          ? { presets: [] }
          : url.includes("/oauth/start")
            ? opts.startResponse
            : { providers: opts.providers ?? [] }
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      })
    }),
  )
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <SettingsScreen />
    </QueryClientProvider>,
  )
}

describe("the oauth connect flow", () => {
  it("offers a connect button instead of a key form", async () => {
    // A static key form is useless here: there is no key to type.
    renderSettings({ providers: [oauthProvider] })
    expect(await screen.findByRole("button", { name: /connect/i })).toBeInTheDocument()
    expect(screen.queryByPlaceholderText("secret")).not.toBeInTheDocument()
  })

  it("shows the authorize link and a paste box after starting", async () => {
    const user = userEvent.setup()
    renderSettings({
      providers: [oauthProvider],
      startResponse: {
        authorize_url: "https://claude.ai/oauth/authorize?state=abc",
        state: "abc",
        redirect_uri: "http://localhost:54545/callback",
        style: "localhost",
      },
    })
    await user.click(await screen.findByRole("button", { name: /connect/i }))

    const link = await screen.findByRole("link", { name: /authorize/i })
    expect(link.getAttribute("href")).toContain("claude.ai/oauth/authorize")
    // Always shown, even on the localhost path: the listener only works when
    // Darkrouter and the browser are on the same machine.
    expect(await screen.findByPlaceholderText(/paste/i)).toBeInTheDocument()
  })

  it("says an account needs reconnection and offers the way out", async () => {
    renderSettings({
      providers: [
        {
          ...oauthProvider,
          credentials: [
            { id: "c1", label: "personal", masked: "…", enabled: false, cooling: false },
          ],
        },
      ],
    })
    expect(await screen.findByText(/needs reconnection/i)).toBeInTheDocument()
    expect(await screen.findByRole("button", { name: /reconnect/i })).toBeInTheDocument()
  })
})
