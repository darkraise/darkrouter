import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from "@tanstack/react-router"
import { ThemeProvider } from "darkraise-ui/theme"
import { RouterAdapterProvider, type RouterAdapter } from "darkraise-ui/router"
import { AppShell } from "./app-shell"
import { nav, settingsItem } from "./nav"
import { themeConfig } from "../../theme.config"
import { routerAdapter } from "../../lib/router"

// The mobile drawer renders darkraise's own SidebarNav, which reads the
// library's router adapter; a stub satisfying the interface is enough here.
const stubAdapter: RouterAdapter = {
  Link: ({ children }) => <>{children}</>,
  useNavigate: () => () => {},
  usePathname: () => "/",
  useBack: () => () => {},
  useInvalidate: () => () => {},
}

function mount(ui: () => React.ReactNode) {
  const rootRoute = createRootRoute({
    component: () => <RouterAdapterProvider value={stubAdapter}>{ui()}</RouterAdapterProvider>,
  })
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ["/"] }),
  })
  render(
    <ThemeProvider config={themeConfig}>
      <RouterProvider router={router} />
    </ThemeProvider>,
  )
}

function shell(overrides: Partial<React.ComponentProps<typeof AppShell>> = {}) {
  const props = {
    nav,
    footerNav: [{ label: "Settings", items: [settingsItem] }],
    onSearch: vi.fn(),
    onChangePassword: vi.fn(),
    onSettings: vi.fn(),
    onLogout: vi.fn(),
    ...overrides,
  }
  mount(() => <AppShell {...props}>content</AppShell>)
  return props
}

describe("the app shell", () => {
  it("carries the darkrouter mark, not a generic app logo", async () => {
    shell()
    const aside = await screen.findByRole("complementary", { name: "Primary" })
    expect(within(aside).getByRole("img", { name: "darkrouter" })).toBeInTheDocument()
    expect(within(aside).getByText("darkrouter")).toBeInTheDocument()
    expect(screen.queryByText(/^App$/)).not.toBeInTheDocument()
  })

  it("has no notification bell, since nothing feeds one", async () => {
    shell()
    await screen.findByRole("complementary", { name: "Primary" })
    expect(screen.queryByText("Notifications")).not.toBeInTheDocument()
  })

  it("names the avatar button and offers the password change", async () => {
    const props = shell()
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "Account menu" }))
    await user.click(await screen.findByRole("menuitem", { name: /change password/i }))
    expect(props.onChangePassword).toHaveBeenCalled()
  })

  it("opens the app's own palette from the rail's Search button", async () => {
    const props = shell()
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: /search/i }))
    expect(props.onSearch).toHaveBeenCalled()
  })

  it("carries the account actions into the mobile drawer", async () => {
    // Below 640px the header actions are hidden by the stylesheet, so the
    // drawer is the only place a phone can reach them.
    const props = shell()
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: /open menu/i }))
    const drawer = await screen.findByRole("dialog")
    await user.click(within(drawer).getByRole("button", { name: /change password/i }))
    expect(props.onChangePassword).toHaveBeenCalled()
  })

  it("keeps every rail link named once the rail is collapsed", async () => {
    shell()
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: /collapse sidebar/i }))
    const aside = screen.getByRole("complementary", { name: "Primary" })
    for (const item of [...nav.flatMap((g) => g.items), settingsItem]) {
      expect(within(aside).getByRole("link", { name: item.label })).toBeInTheDocument()
    }
  })
})

describe("the sidebar's active indicator", () => {
  // The console renders the rail in the library's markup rather than through
  // SidebarNav, so the attribute the library's CSS keys on has to be set here
  // too — otherwise the rail silently falls back to the preset's own look and
  // the drawer and the rail disagree.
  it("asks for both the rail and the ring, in the rail and the drawer alike", async () => {
    shell()
    const aside = await screen.findByRole("complementary", { name: "Primary" })
    for (const nav of aside.querySelectorAll(".dr-sidebar-nav")) {
      expect(nav.getAttribute("data-active-bar")).toBe("both")
    }
    expect(aside.querySelectorAll(".dr-sidebar-nav").length).toBeGreaterThan(0)
  })
})

/**
 * The shell inside a router that has somewhere to go: the overview, two
 * screens, and a trace under Requests the way the console's own route table
 * nests it. The rail and the drawer link through the console's real adapter,
 * so a click is a real navigation.
 */
async function routed(overrides: Partial<React.ComponentProps<typeof AppShell>> = {}) {
  const props = {
    nav,
    footerNav: [{ label: "Settings", items: [settingsItem] }],
    onSearch: vi.fn(),
    onChangePassword: vi.fn(),
    onSettings: vi.fn(),
    onLogout: vi.fn(),
    ...overrides,
  }
  const rootRoute = createRootRoute({
    component: () => (
      <RouterAdapterProvider value={routerAdapter}>
        <AppShell {...props}>
          <Outlet />
        </AppShell>
      </RouterAdapterProvider>
    ),
  })
  const page = (name: string) => () => <p>{name} screen</p>
  const requests = createRoute({
    getParentRoute: () => rootRoute,
    path: "/requests",
    component: page("Requests"),
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([
      createRoute({ getParentRoute: () => rootRoute, path: "/", component: page("Overview") }),
      createRoute({ getParentRoute: () => rootRoute, path: "/models", component: page("Models") }),
      requests.addChildren([createRoute({ getParentRoute: () => requests, path: "$id" })]),
    ]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  })
  await router.load()
  render(
    <ThemeProvider config={themeConfig}>
      <RouterProvider router={router} />
    </ThemeProvider>,
  )
  return { props, router }
}

describe("the account menu from the keyboard", () => {
  // darkraise's menu item runs onClick only for the pointer; Enter and Space
  // go through onSelect. With onClick, a keyboard operator could highlight
  // "Log out" and press Enter to no effect.
  it.each([
    ["Change password", "onChangePassword"],
    ["Settings", "onSettings"],
    ["Log out", "onLogout"],
  ] as const)("runs %s on Enter", async (label, handler) => {
    const props = shell()
    const user = userEvent.setup()
    ;(await screen.findByRole("button", { name: "Account menu" })).focus()
    await user.keyboard("{Enter}")
    const item = await screen.findByRole("menuitem", { name: label })
    for (let i = 0; i < 5 && document.activeElement !== item; i++) {
      await user.keyboard("{ArrowDown}")
    }
    expect(item).toHaveFocus()
    await user.keyboard("{Enter}")
    expect(props[handler]).toHaveBeenCalled()
  })
})

describe("the mobile drawer", () => {
  async function openDrawer() {
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: /open menu/i }))
    return { user, drawer: await screen.findByRole("dialog", { name: "Navigation" }) }
  }

  it("closes once a destination is chosen, rather than covering it", async () => {
    const { router } = await routed()
    const { user, drawer } = await openDrawer()
    await user.click(within(drawer).getByRole("link", { name: "Models" }))
    await waitFor(() => expect(router.state.location.pathname).toBe("/models"))
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
  })

  it("closes for the page already showing too, which navigates nowhere", async () => {
    await routed()
    const { user, drawer } = await openDrawer()
    await user.click(within(drawer).getByRole("link", { name: "Overview" }))
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
  })

  it("closes before an account action opens its own dialog", async () => {
    // Two role=dialog at once was the bug: the password dialog stacked on a
    // drawer that was still open behind it.
    const { props } = await routed()
    const { user, drawer } = await openDrawer()
    await user.click(within(drawer).getByRole("button", { name: /change password/i }))
    expect(props.onChangePassword).toHaveBeenCalled()
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
  })

  it("offers Search, which a touch operator cannot reach with ⌘K", async () => {
    const { props } = await routed()
    const { user, drawer } = await openDrawer()
    await user.click(within(drawer).getByRole("button", { name: "Search" }))
    expect(props.onSearch).toHaveBeenCalled()
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
  })

  it("offers the theme settings, with the chosen swatch announced", async () => {
    await routed()
    const { user, drawer } = await openDrawer()
    const row = within(drawer).getByRole("button", { name: /customize theme/i })
    expect(row).toHaveAttribute("aria-expanded", "false")
    await user.click(row)
    expect(row).toHaveAttribute("aria-expanded", "true")
    const panel = within(drawer).getByRole("group", { name: "Theme settings" })
    expect(panel.querySelectorAll(".dr-theme-switcher-swatch").length).toBeGreaterThan(0)
    const pressed = panel.querySelectorAll('.dr-theme-switcher-swatch[aria-pressed="true"]')
    // One accent and one surface are in force at a time.
    expect(pressed).toHaveLength(2)
    for (const swatch of pressed) expect(swatch).toHaveAttribute("data-active", "true")

    // Picking another moves the announced state with the ring.
    const other = panel.querySelector<HTMLElement>(
      '.dr-theme-switcher-swatch:not([data-active="true"])',
    )!
    await user.click(other)
    await waitFor(() => expect(other).toHaveAttribute("aria-pressed", "true"))
    expect(panel.querySelectorAll('.dr-theme-switcher-swatch[aria-pressed="true"]')).toHaveLength(2)
  })
})

describe("focus on navigation", () => {
  it("moves into the content when the screen changes", async () => {
    // darkraise's own layouts do this with useRouteFocus; the hand-composed
    // shell had dropped it, and focus stayed on the rail link.
    const { router } = await routed()
    await screen.findByText("Overview screen")
    await router.navigate({ to: "/models" })
    await screen.findByText("Models screen")
    await waitFor(() => expect(document.getElementById("main-content")).toHaveFocus())
  })

  it("leaves focus alone when a trace opens over the same screen", async () => {
    // A trace drawer takes focus itself, and hands it back to the row it
    // came from when it closes; moving it to the content would undo both.
    const { router } = await routed()
    await router.navigate({ to: "/requests" })
    await screen.findByText("Requests screen")
    const row = document.createElement("button")
    document.body.appendChild(row)
    row.focus()
    await router.navigate({ to: "/requests/$id", params: { id: "01M47PJFXTQNG6Z3WZ39X4QZM2" } })
    await router.navigate({ to: "/requests" })
    expect(row).toHaveFocus()
    row.remove()
  })
})
