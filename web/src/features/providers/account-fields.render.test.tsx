import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { AccountFields, emptyAccounts } from "./account-fields"

describe("the account field", () => {
  it("is asked for only where the endpoint carries one", () => {
    // Every other provider is reached by a key alone, and a field asking for
    // an account nobody has is a form an operator cannot finish.
    render(<AccountFields value={emptyAccounts} onChange={() => {}} />)
    expect(screen.queryByLabelText(/account id/i)).not.toBeInTheDocument()
  })

  it("appears for a provider whose endpoint carries an account", () => {
    render(<AccountFields value={emptyAccounts} onChange={() => {}} needsAccount />)
    expect(screen.getByLabelText(/account id/i)).toBeInTheDocument()
  })

  it("is not asked for in bulk, where each line carries its own", async () => {
    // One field applied to a paste of five keys would send four of them to the
    // wrong account's address.
    render(
      <AccountFields value={{ ...emptyAccounts, mode: "bulk" }} onChange={() => {}} needsAccount />,
    )
    expect(screen.queryByLabelText(/account id/i)).not.toBeInTheDocument()
    expect(screen.getByText(/name\|account\|key/i)).toBeInTheDocument()
  })

  it("reports what was typed into it", async () => {
    const onChange = vi.fn()
    const user = userEvent.setup()
    render(<AccountFields value={emptyAccounts} onChange={onChange} needsAccount />)
    await user.type(screen.getByLabelText(/account id/i), "a")
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ accountId: "a" }))
  })
})
