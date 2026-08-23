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
import type { ReactNode, MouseEvent, CSSProperties } from "react"
import { OverviewScreen } from "../routes/overview"
import { RequestsScreen } from "../routes/requests"
import { CatalogScreen } from "../routes/catalog"
import { PlaygroundScreen } from "../routes/playground"

/**
 * routerAdapter plugs a concrete router into darkraise-ui.
 *
 * darkraise-ui/router is an adapter interface, not a router: it declares Link,
 * useNavigate, usePathname, useBack and useInvalidate and expects something to
 * satisfy them. This is TanStack Router doing that.
 */
export const routerAdapter = {
  Link: ({
    to,
    children,
    className,
    activeClassName,
    style,
    onClick,
  }: {
    to: string
    children: ReactNode
    className?: string
    activeClassName?: string
    style?: CSSProperties
    onClick?: (e: MouseEvent<HTMLAnchorElement>) => void
  }) => (
    <Link
      to={to}
      className={className}
      activeProps={activeClassName ? { className: activeClassName } : undefined}
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

const rootRoute = createRootRoute({ component: Outlet })

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
