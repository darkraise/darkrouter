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
import { SidebarLayout } from "darkraise-ui/layout"
import type { NavGroup } from "darkraise-ui/layout"
import type { ReactNode, MouseEvent, CSSProperties } from "react"
import { OverviewScreen } from "../routes/overview"
import { RequestsScreen } from "../routes/requests"
import { CatalogScreen } from "../routes/catalog"
import { PlaygroundScreen } from "../routes/playground"
import { SettingsScreen } from "../routes/settings"

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

const nav: NavGroup[] = [
  {
    items: [
      { label: "Overview", href: "/" },
      { label: "Requests", href: "/requests" },
      { label: "Catalog", href: "/catalog" },
      { label: "Playground", href: "/playground" },
      { label: "Settings", href: "/settings" },
    ],
  },
]

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
      <SidebarLayout nav={nav} showThemeSwitcher>
        <Outlet />
      </SidebarLayout>
    </RouterAdapterProvider>
  )
}

const rootRoute = createRootRoute({ component: RootShell })

// One route per screen. Declared here rather than in a generated tree because
// there are five of them and a generator would be more machinery than routes.
const routes = [
  createRoute({ getParentRoute: () => rootRoute, path: "/", component: OverviewScreen }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/requests",
    component: RequestsScreen,
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/catalog",
    component: CatalogScreen,
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/playground",
    component: PlaygroundScreen,
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/settings",
    component: SettingsScreen,
  }),
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
