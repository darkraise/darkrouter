import { useRouterState } from "@tanstack/react-router"
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

export type PageIdentity = {
  title: string
  description: string
  icon: LucideIcon
}

/**
 * What each destination is called, and what it answers.
 *
 * One table rather than a title written into each screen, because the name now
 * renders in the app header beside the mark for the same section in the rail.
 * Two copies of "Providers" drift, and the one an operator reads is whichever
 * the header happened to get.
 *
 * Keyed by the section's own path. A detail page under a section inherits its
 * identity: /providers/groq is still the Providers section, and the provider's
 * own name is the heading on the page itself rather than the name of the
 * place it is in.
 */
const PAGES: { path: string; identity: PageIdentity }[] = [
  {
    path: "/",
    identity: {
      title: "Overview",
      description: "Is it working, and what did it just do",
      icon: Gauge,
    },
  },
  {
    path: "/requests",
    identity: {
      title: "Requests",
      description: "What it just did, and which provider actually served",
      icon: ListTree,
    },
  },
  {
    path: "/usage",
    identity: {
      title: "Usage",
      description: "What it cost, and where it went",
      icon: Activity,
    },
  },
  {
    path: "/providers",
    identity: {
      title: "Providers",
      description: "What it can route to, and whether it is answering",
      icon: Server,
    },
  },
  {
    path: "/models",
    identity: {
      title: "Models",
      description: "What it can route to, and which providers serve each one",
      icon: Boxes,
    },
  },
  {
    path: "/routing",
    identity: {
      title: "Routing",
      description: "How it chooses, and what it would choose right now",
      icon: Split,
    },
  },
  {
    path: "/playground",
    identity: {
      title: "Playground",
      description: "Send a real request, and see what it cost",
      icon: TerminalSquare,
    },
  },
  {
    path: "/connect",
    identity: {
      title: "Connect",
      description: "How to point a client at this gateway",
      icon: Cable,
    },
  },
  {
    path: "/settings",
    identity: {
      title: "Settings",
      description: "What the gateway is set to, and where each setting comes from",
      icon: Settings,
    },
  },
]

/** The section a path belongs to, by longest matching prefix. "/" is matched
 *  exactly, since it prefixes everything. */
export function identityFor(pathname: string): PageIdentity | undefined {
  let best: { path: string; identity: PageIdentity } | undefined
  for (const page of PAGES) {
    if (page.path === "/") {
      if (pathname === "/") return page.identity
      continue
    }
    const inSection = pathname === page.path || pathname.startsWith(`${page.path}/`)
    if (inSection && (best === undefined || page.path.length > best.path.length)) best = page
  }
  return best?.identity
}

/**
 * The page's name, mark and purpose, in the app header.
 *
 * It sits in the chrome rather than at the top of the content because it is
 * chrome: it says where you are, which does not change as you scroll, and a
 * screen whose first job is a full-height transcript or a two-hundred-row
 * table has no room to spend a heading block saying its own name.
 */
export function PageIdentityBar() {
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const page = identityFor(pathname)
  if (page === undefined) return null
  const Icon = page.icon
  return (
    <div className="mr-auto flex min-w-0 items-center gap-3">
      <span
        className="flex size-8 shrink-0 items-center justify-center rounded-[var(--radius)] border bg-[hsl(var(--muted))]"
        aria-hidden="true"
      >
        <Icon className="size-[var(--icon-size)]" />
      </span>
      {/* Tight leading, so the name and its description both sit inside the
          header's original 3.5rem rather than making the bar taller than
          every other darkraise app's. */}
      <span className="flex min-w-0 flex-col leading-tight">
        <h1 className="truncate text-base font-medium">{page.title}</h1>
        <span className="truncate text-sm text-[hsl(var(--muted-foreground))]">
          {page.description}
        </span>
      </span>
    </div>
  )
}
