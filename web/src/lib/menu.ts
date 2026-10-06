import { useState } from "react"

/**
 * A dropdown menu that closes when an item is chosen from the keyboard.
 *
 * darkraise's menu item closes the menu only when the event that chose it was
 * not default-prevented, and its own keydown handler prevents Enter and Space
 * before it gets that far. So a keyboard pick ran the item and left the menu
 * open over whatever the item opened or navigated to. darkraise-ui is pinned
 * (CLAUDE.md), so until the check reads the consumer's onSelect rather than
 * its own keydown, the menu is controlled here and `pick` shuts it itself.
 *
 * Spread `menu` onto the DropdownMenu; wrap each item's handler in `pick`.
 */
export function useClosingMenu() {
  const [open, setOpen] = useState(false)
  const pick = (run: () => void) => () => {
    setOpen(false)
    run()
  }
  return { menu: { open, onOpenChange: setOpen }, pick }
}
