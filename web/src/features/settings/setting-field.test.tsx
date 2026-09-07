import { useState } from "react"
import { describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { SettingField } from "./setting-field"
import type { SettingRow } from "./settings-catalog"

function row(over: Partial<SettingRow> = {}): SettingRow {
  return {
    field: "log.retention",
    meta: { name: "Keep request records for", description: "", group: "logging" },
    value: "720h0m0s",
    display: "30 days",
    source: "database",
    hotReloadable: true,
    kind: "duration",
    env: "",
    editable: true,
    ...over,
  }
}

describe("SettingField", () => {
  it("edits a duration in its stored spelling", async () => {
    const onChange = vi.fn()
    render(<SettingField row={row()} value="720h0m0s" onChange={onChange} onReset={null} />)
    const box = screen.getByLabelText("Keep request records for")
    await userEvent.clear(box)
    await userEvent.type(box, "48h")
    // The store parses what the box holds, so the box holds what the store
    // parses. A field that emitted "2 days" would round-trip to a 400.
    expect(onChange).toHaveBeenLastCalledWith("48h")
  })

  it("renders a boolean as a switch and emits a parseable bool", async () => {
    const onChange = vi.fn()
    render(
      <SettingField
        row={row({ field: "capture.bodies", kind: "bool", value: "false", display: "Off",
          meta: { name: "Record request bodies", description: "", group: "logging" } })}
        value="false"
        onChange={onChange}
        onReset={null}
      />,
    )
    await userEvent.click(screen.getByRole("switch", { name: "Record request bodies" }))
    expect(onChange).toHaveBeenLastCalledWith("true")
  })

  it("shows a size at its scale and emits bytes", async () => {
    const onChange = vi.fn()
    render(
      <SettingField
        row={row({ field: "capture.max_bytes", kind: "bytes", value: "256000", display: "250 KB",
          meta: { name: "Largest body recorded", description: "", group: "logging" } })}
        value="256000"
        onChange={onChange}
        onReset={null}
      />,
    )
    const box = screen.getByLabelText("Largest body recorded")
    await userEvent.clear(box)
    await userEvent.type(box, "1 MB")
    expect(onChange).toHaveBeenLastCalledWith("1048576")
  })

  it("keeps a lossy size at its stored digits", () => {
    // formatBytes rounds to one decimal, so 33554433 displays as "32.0 MB"
    // and parses back one byte short. Seeding the box from the display would
    // rewrite the stored value on a save the operator never made.
    render(
      <SettingField
        row={row({ field: "capture.max_bytes", kind: "bytes", value: "33554433", display: "32.0 MB",
          meta: { name: "Largest body recorded", description: "", group: "logging" } })}
        value="33554433"
        onChange={vi.fn()}
        onReset={null}
      />,
    )
    expect(screen.getByLabelText("Largest body recorded")).toHaveValue("33554433")
  })

  // The parent echoes back what the box emitted, in the stored spelling. The box
  // must keep the operator's own text: reseeding here would rewrite "1 MB" to
  // "1048576" under the cursor on every keystroke.
  it("keeps the typed text when the parent echoes the emitted value", async () => {
    function Stateful() {
      const [value, setValue] = useState("256000")
      return (
        <SettingField
          row={row({ field: "capture.max_bytes", kind: "bytes", value, display: "250 KB",
            meta: { name: "Largest body recorded", description: "", group: "logging" } })}
          value={value}
          onChange={setValue}
          onReset={null}
        />
      )
    }
    render(<Stateful />)
    const box = screen.getByLabelText("Largest body recorded")
    await userEvent.clear(box)
    await userEvent.type(box, "1 MB")
    expect((box as HTMLInputElement).value).toBe("1 MB")
  })

  it("takes a value it did not emit, dropping the typed text", async () => {
    // A reset, or a refetch: the prop moved on its own, so what is in the box
    // is stale and the new stored value wins.
    const bytes = (value: string, display: string) =>
      row({ field: "capture.max_bytes", kind: "bytes", value, display,
        meta: { name: "Largest body recorded", description: "", group: "logging" } })
    const { rerender } = render(
      <SettingField row={bytes("256000", "250 KB")} value="256000" onChange={vi.fn()} onReset={null} />,
    )
    const box = screen.getByLabelText("Largest body recorded")
    await userEvent.clear(box)
    await userEvent.type(box, "1 MB")

    rerender(
      <SettingField row={bytes("524288", "512 KB")} value="524288" onChange={vi.fn()} onReset={null} />,
    )
    expect((box as HTMLInputElement).value).toBe("512 KB")
  })

  it("edits a count and emits the typed digits", async () => {
    const onChange = vi.fn()
    const attempts = row({ field: "policy.retry.max_attempts", kind: "int", value: "3", display: "3",
      meta: { name: "Attempts per request", description: "", group: "requests" } })
    render(<SettingField row={attempts} value="3" onChange={onChange} onReset={null} />)
    const box = screen.getByLabelText("Attempts per request")
    await userEvent.clear(box)
    await userEvent.type(box, "5")
    expect(onChange).toHaveBeenLastCalledWith("5")
  })

  it("emits an empty string from a cleared count, not a zero", async () => {
    // "" and "0" are different settings, and a caller that read a cleared box
    // as 0 would write a real zero over the built-in default.
    const onChange = vi.fn()
    const attempts = row({ field: "policy.retry.max_attempts", kind: "int", value: "3", display: "3",
      meta: { name: "Attempts per request", description: "", group: "requests" } })
    render(<SettingField row={attempts} value="3" onChange={onChange} onReset={null} />)
    await userEvent.clear(screen.getByLabelText("Attempts per request"))
    expect(onChange).toHaveBeenLastCalledWith("")
  })

  it("renders an environment row read-only, naming its variable", () => {
    render(
      <SettingField
        row={row({
          field: "server.proxy_listen", kind: "string", value: ":18080", display: ":18080",
          source: "env", env: "DARKROUTER_PROXY_LISTEN", editable: false, hotReloadable: false,
          meta: { name: "Gateway address", description: "", group: "server" },
        })}
        value=":18080"
        onChange={vi.fn()}
        onReset={null}
      />,
    )
    expect(screen.queryByRole("textbox")).toBeNull()
    // The variable is the whole reason the row cannot be edited, so it is on
    // the row rather than in a tooltip nobody opens.
    expect(screen.getByText(/DARKROUTER_PROXY_LISTEN/)).toBeTruthy()
  })

  it("offers reset only when the key is stored", async () => {
    const onReset = vi.fn()
    const { rerender } = render(
      <SettingField row={row({ source: "default" })} value="720h0m0s" onChange={vi.fn()} onReset={null} />,
    )
    expect(screen.queryByRole("button", { name: /reset/i })).toBeNull()

    rerender(
      <SettingField row={row({ source: "database" })} value="48h" onChange={vi.fn()} onReset={onReset} />,
    )
    await userEvent.click(screen.getByRole("button", { name: /reset/i }))
    expect(onReset).toHaveBeenCalled()
  })

  it("shows the server's complaint against the field it names", () => {
    render(
      <SettingField
        row={row()}
        value="1h"
        onChange={vi.fn()}
        onReset={null}
        error="log.retention must be at least 48h, got 1h"
      />,
    )
    expect(screen.getByText(/must be at least 48h/)).toBeTruthy()
  })

  it("points the control at the complaint for a screen reader", () => {
    render(
      <SettingField
        row={row()}
        value="1h"
        onChange={vi.fn()}
        onReset={null}
        error="log.retention must be at least 48h, got 1h"
      />,
    )
    const described = screen
      .getByLabelText("Keep request records for")
      .getAttribute("aria-describedby")
    expect(described).toBeTruthy()
    expect(document.getElementById(described as string)?.textContent).toMatch(
      /must be at least 48h/,
    )
  })

  it("placeholders the public URL with a bare domain", () => {
    render(
      <SettingField
        row={row({
          field: "server.public_url", kind: "url", value: "", display: "",
          meta: { name: "Public URL", description: "", group: "server" },
        })}
        value=""
        onChange={vi.fn()}
        onReset={null}
      />,
    )
    expect(screen.getByLabelText("Public URL")).toHaveAttribute("placeholder", "llm.example.com")
  })

  it("does not offer a bare-domain placeholder on a catalogue URL", () => {
    // Only server.public_url normalises a bare domain to https; the write
    // path accepts a bare domain on the others and fails only at fetch time,
    // so their placeholder must not suggest one is safe to type.
    render(
      <SettingField
        row={row({
          field: "catalog.models_dev_url", kind: "url", value: "", display: "",
          meta: { name: "models.dev catalogue URL", description: "", group: "catalogue" },
        })}
        value=""
        onChange={vi.fn()}
        onReset={null}
      />,
    )
    const placeholder = screen
      .getByLabelText("models.dev catalogue URL")
      .getAttribute("placeholder")
    expect(placeholder).not.toBe("llm.example.com")
    expect(placeholder).toMatch(/^https?:\/\//)
  })
})
