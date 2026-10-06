import { useEffect, useRef, useState } from "react"
import { Button, Popover, PopoverContent, PopoverTrigger } from "darkraise-ui"
import { ThemeSettingsPanel } from "darkraise-ui/theme"
import { ChevronDown, Palette } from "lucide-react"

const LABEL = "Customize theme"

/**
 * darkraise's theme panel, with the selected swatch announced.
 *
 * The library's accent and surface swatches are plain buttons that mark the
 * current one with `data-active` and a ring, so a screen reader hears
 * eighteen identical "button, coral"s and cannot tell which is in force. The
 * Mode row beside them already says so with radio semantics. darkraise-ui is
 * pinned (CLAUDE.md), so until the swatch carries its own state upstream this
 * mirrors `data-active` onto `aria-pressed` -- the state a toggle button
 * exposes -- and keeps it in step as the operator picks another one.
 */
export function ThemePanel() {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const root = ref.current
    if (!root) return
    const sync = () => {
      for (const swatch of root.querySelectorAll<HTMLElement>(".dr-theme-switcher-swatch")) {
        swatch.setAttribute("aria-pressed", swatch.dataset.active === "true" ? "true" : "false")
      }
    }
    sync()
    // Filtered to data-active, so writing aria-pressed does not wake it again.
    const observer = new MutationObserver(sync)
    observer.observe(root, {
      subtree: true,
      childList: true,
      attributes: true,
      attributeFilter: ["data-active"],
    })
    return () => observer.disconnect()
  }, [])
  return (
    <div ref={ref}>
      <ThemeSettingsPanel />
    </div>
  )
}

/** The header's theme control: the library's ThemeSwitcher, rebuilt around
 *  ThemePanel so its swatches carry their state. */
export function ThemeSwitcherButton() {
  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button variant="ghost" size="icon">
          <Palette className="size-[var(--icon-size)]" aria-hidden="true" />
          <span className="sr-only">{LABEL}</span>
        </Button>
      </PopoverTrigger>
      <PopoverContent
        className="w-[34rem] max-w-[calc(100vw-2rem)]"
        align="end"
        role="dialog"
        aria-label="Theme settings"
      >
        <ThemePanel />
      </PopoverContent>
    </Popover>
  )
}

/**
 * The same control in the phone drawer, as a row that opens the panel in
 * place. Inline rather than a popover: the drawer is already a modal layer,
 * and a popover over it on a 375px screen would cover the row that opened it.
 */
export function ThemeDrawerRow() {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button
        type="button"
        className="dr-sidebar-nav-item dr-sidebar-nav-link"
        aria-expanded={open}
        aria-controls={open ? "app-drawer-theme" : undefined}
        onClick={() => setOpen((v) => !v)}
      >
        <span className="dr-sidebar-nav-icon">
          <Palette className="dr-sidebar-nav-icon-svg" />
        </span>
        <span className="dr-sidebar-nav-label">{LABEL}</span>
        <ChevronDown
          className="dr-sidebar-nav-icon-svg ml-auto transition-transform data-[open=true]:rotate-180"
          data-open={open}
          aria-hidden="true"
        />
      </button>
      {open && (
        <div id="app-drawer-theme" role="group" aria-label="Theme settings" className="app-drawer-theme">
          <ThemePanel />
        </div>
      )}
    </>
  )
}
