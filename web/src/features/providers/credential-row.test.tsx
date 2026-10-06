import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { Table, TableBody } from "darkraise-ui"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { CredentialRow } from "./credential-row"
import type { Credential } from "../../lib/api-types"

const credential: Credential = {
  id: "k1", label: "primary", masked: "sk-…abcd", enabled: true, cooling: false, kind: "static",
}

function mount() {
  const fetchMock = vi.fn<typeof fetch>(
    async () =>
      new Response(JSON.stringify({}), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
  )
  vi.stubGlobal("fetch", fetchMock)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <Table>
        <TableBody>
          <CredentialRow providerId="groq" credential={credential} />
        </TableBody>
      </Table>
    </QueryClientProvider>,
  )
  return fetchMock
}

beforeEach(() => vi.unstubAllGlobals())

describe("replacing a credential's secret", () => {
  it("names the field after the credential it replaces", async () => {
    mount()
    await userEvent.click(screen.getByRole("button", { name: "Replace" }))
    expect(screen.getByLabelText("New secret for primary")).toHaveFocus()
  })

  it("saves on Enter", async () => {
    const fetchMock = mount()
    await userEvent.click(screen.getByRole("button", { name: "Replace" }))
    await userEvent.type(screen.getByLabelText("New secret for primary"), "sk-new{Enter}")

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/providers/groq/keys/k1",
        expect.objectContaining({ method: "PATCH", body: JSON.stringify({ secret: "sk-new" }) }),
      ),
    )
    // And the operator lands back where they started rather than at the top.
    await waitFor(() => expect(screen.getByRole("button", { name: "Replace" })).toHaveFocus())
  })

  it("returns focus to Replace on Escape rather than dropping it to the page", async () => {
    mount()
    await userEvent.click(screen.getByRole("button", { name: "Replace" }))
    await userEvent.keyboard("{Escape}")

    expect(screen.queryByLabelText("New secret for primary")).toBeNull()
    expect(screen.getByRole("button", { name: "Replace" })).toHaveFocus()
  })

  it("returns focus to Replace on Cancel too", async () => {
    mount()
    await userEvent.click(screen.getByRole("button", { name: "Replace" }))
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }))
    expect(screen.getByRole("button", { name: "Replace" })).toHaveFocus()
  })
})
