import {
  Activity,
  Boxes,
  Cable,
  Gauge,
  ListTree,
  Server,
  Settings,
  Split,
  TerminalSquare,
} from "lucide-react"
import type { LucideIcon } from "lucide-react"

/** A destination is one of the router's own paths, so a palette or a rail
 *  that navigates to it goes through the typed route table rather than a
 *  string that stops matching when a route is renamed. */
export type NavHref =
  | "/"
  | "/requests"
  | "/usage"
  | "/providers"
  | "/models"
  | "/routing"
  | "/playground"
  | "/connect"
  | "/settings"

export type NavItem = { label: string; href: NavHref; icon: LucideIcon }
export type NavGroup = { label: string; items: NavItem[] }

/**
 * §5's information architecture: eight destinations in three groups, plus
 * Settings apart from them.
 *
 * Eight items read as three because the rail groups them, and each group
 * answers one question. Settings sits apart because it holds knobs rather than
 * decisions.
 *
 * Breaker state, discovery health, preset browsing, aliases and model
 * overrides deliberately get **no destination**. Each is one panel beside the
 * subject it describes, and giving them pages means navigating away from
 * context to answer a question about the thing already on screen. A ninth rail
 * item is how a console ends up with twenty-three sections of which six are
 * stubs.
 */
export const nav: NavGroup[] = [
  {
    label: "Operate",
    items: [
      { label: "Overview", href: "/", icon: Gauge },
      { label: "Requests", href: "/requests", icon: ListTree },
      { label: "Usage", href: "/usage", icon: Activity },
    ],
  },
  {
    label: "Configure",
    items: [
      { label: "Providers", href: "/providers", icon: Server },
      { label: "Models", href: "/models", icon: Boxes },
      { label: "Routing", href: "/routing", icon: Split },
    ],
  },
  {
    label: "Use",
    items: [
      { label: "Playground", href: "/playground", icon: TerminalSquare },
      { label: "Connect", href: "/connect", icon: Cable },
    ],
  },
]

/** Pinned to the sidebar footer rather than joining a group. */
export const settingsItem: NavItem = {
  label: "Settings",
  href: "/settings",
  icon: Settings,
}
