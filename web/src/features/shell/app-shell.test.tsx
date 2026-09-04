import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router"
import { ThemeProvider } from "darkraise-ui/theme"
import { RouterAdapterProvider, type RouterAdapter } from "darkraise-ui/router"
import { AppShell } from "./app-shell"
import { nav, settingsItem } from "./nav"
import { themeConfig } from "../../theme.config"

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
