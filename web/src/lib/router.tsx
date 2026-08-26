import {
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  Link,
  useNavigate,
  useRouterState,
} from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { RouterAdapterProvider } from "darkraise-ui/router"
import type { RouterAdapter } from "darkraise-ui/router"
import { SidebarLayout, SidebarNav } from "darkraise-ui/layout"
import type { ReactNode, MouseEvent, CSSProperties } from "react"
import { OverviewScreen } from "../features/overview/overview-screen"
import { nav, settingsItem } from "../features/shell/nav"
import { UsageScreen } from "../features/usage/usage-screen"
import { ProvidersScreen } from "../features/providers/providers-screen"
import { ProviderDetail } from "../features/providers/provider-detail"
import { ModelsScreen } from "../features/models/models-screen"
import { RoutingScreen } from "../features/routing/routing-screen"
import { ConnectScreen } from "../features/connect/connect-screen"
import { PlaygroundScreen } from "../features/playground/playground-screen"
import { CommandPalette } from "../features/shell/command-palette"
import { ScreenBoundary } from "../features/shell/screen-boundary"
import { RequestsScreen } from "../features/requests/requests-screen"
import { SettingsScreen } from "../features/settings/settings-screen"

/**
 * routerAdapter plugs a concrete router into darkraise-ui.
 *
 * darkraise-ui/router is an adapter interface, not a router: it declares Link,
 * useNavigate, usePathname, useBack and useInvalidate and expects something to
 * satisfy them. This is TanStack Router doing that.
 */
export const routerAdapter: RouterAdapter = {
  Link: ({
    to,
    children,
    className,
    activeClassName,
    activeExact,
    style,
    onClick,
  }: {
    to: string
    children: ReactNode
    className?: string
    activeClassName?: string
    activeExact?: boolean
    style?: CSSProperties
    onClick?: (e: MouseEvent<HTMLAnchorElement>) => void
  }) => (
    <Link
      to={to}
      className={className}
      activeProps={activeClassName ? { className: activeClassName } : undefined}
      // Without this every item whose path prefixes the current one lights up,
      // and "/" prefixes all of them — so Overview would read as active on
      // every screen.
      activeOptions={{ exact: activeExact ?? false }}
      style={style}
      onClick={onClick}
    >
      {children}
    </Link>
  ),
  useNavigate: () => {
    const navigate = useNavigate()
    return (to: string) => void navigate({ to })
  },
  usePathname: () => useRouterState({ select: (s) => s.location.pathname }),
  useBack: () => () => window.history.back(),
  useInvalidate: () => {
    const qc = useQueryClient()
    // Invalidate rather than refetch: a screen the operator is not looking at
    // should not fetch just because another one mutated.
    return () => void qc.invalidateQueries()
  },
}



/**
 * RootShell is the chrome every screen renders inside.
 *
 * It lives here, as the root route's component, rather than wrapping
 * RouterProvider from outside: SidebarLayout renders SidebarItem and
 * SearchCommand, both of which call darkraise-ui's useRouterAdapter, and the
 * adapter below is built on TanStack hooks. Mounted above RouterProvider it
 * would be reaching for a router context that does not exist yet.
 */
function RootShell() {
  return (
    <RouterAdapterProvider value={routerAdapter}>
      <SidebarLayout
        nav={nav}
        showThemeSwitcher
        sidebarFooter={
          <SidebarNav nav={[{ items: [settingsItem] }]} />
        }
      >
        <CommandPalette />
        <ScreenBoundary>
          <Outlet />
        </ScreenBoundary>
      </SidebarLayout>
    </RouterAdapterProvider>
  )
}

const rootRoute = createRootRoute({
  component: RootShell,
  // Every route inherits an open string-record search schema, because §5 puts
  // every filter in the URL and the filters differ per screen. Empty values
  // are dropped on the way in, so ?provider= and no provider key are the same
  // state rather than two.
  validateSearch: (search: Record<string, unknown>): Record<string, string> => {
    const out: Record<string, string> = {}
    for (const [k, v] of Object.entries(search)) {
      if (typeof v === "string" && v !== "") out[k] = v
    }
    return out
  },
})

// One route per destination in §5, plus the trace deep link. Written out
// rather than built by a helper: a helper that takes `path: string` erases the
// literal type TanStack infers, and every typed <Link to="/requests/$id"> in
// the app stops compiling.
const routes = [
  createRoute({ getParentRoute: () => rootRoute, path: "/", component: OverviewScreen }),
  createRoute({ getParentRoute: () => rootRoute, path: "/requests", component: RequestsScreen }),
  createRoute({ getParentRoute: () => rootRoute, path: "/usage", component: UsageScreen }),
  createRoute({ getParentRoute: () => rootRoute, path: "/providers", component: ProvidersScreen }),
  createRoute({ getParentRoute: () => rootRoute, path: "/providers/$id", component: ProviderDetail }),
  createRoute({ getParentRoute: () => rootRoute, path: "/models", component: ModelsScreen }),
  createRoute({ getParentRoute: () => rootRoute, path: "/routing", component: RoutingScreen }),
  createRoute({ getParentRoute: () => rootRoute, path: "/playground", component: PlaygroundScreen }),
  createRoute({ getParentRoute: () => rootRoute, path: "/connect", component: ConnectScreen }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings", component: SettingsScreen }),
  createRoute({
    // A deep link into one trace. The drawer opens from the table, but a
    // reloaded or shared URL has to land somewhere real rather than 404.
    getParentRoute: () => rootRoute,
    path: "/requests/$id",
    component: RequestsScreen,
  }),
]

export const router = createRouter({ routeTree: rootRoute.addChildren(routes) })

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router
  }
}
