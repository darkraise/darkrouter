import { render, screen } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { PolicyEditor, RESTART_ONLY_TIMEOUTS } from "./policy-editor"

const POLICY = {
  cooldown: { trip_after: 3, max: "5m0s" },
  retry: { max_attempts: 2 },
  timeout: { connect: "5s", first_byte: "10s", total: "2m0s", idle: "30s" },
}

function mountWithPolicy() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <PolicyEditor />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.unstubAllGlobals()
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      new Response(JSON.stringify(POLICY), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    ),
  )
})

describe("the policy editor", () => {
  it("offers the two timeouts a running request re-reads", async () => {
    mountWithPolicy()
    expect(await screen.findByLabelText(/total/i)).toBeEnabled()
    expect(screen.getByLabelText(/idle/i)).toBeEnabled()
  })

  it("disables the two that need a restart, and says why", async () => {
    // These configure the one shared transport built at startup. An input
    // that accepts a value the API refuses is a lie the screen tells first.
    mountWithPolicy()
    expect(await screen.findByLabelText(/connect/i)).toBeDisabled()
    expect(screen.getByLabelText(/first byte/i)).toBeDisabled()
    expect(screen.getAllByText(/restart/i).length).toBeGreaterThan(0)
  })

  it("names the four fields the API accepts", () => {
    expect(RESTART_ONLY_TIMEOUTS).toEqual(["connect", "first_byte"])
  })
})
