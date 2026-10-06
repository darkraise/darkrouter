import { useEffect, useRef, useState, type MouseEvent, type ReactNode } from "react"
import { Link, useRouterState } from "@tanstack/react-router"
import {
  Avatar,
  AvatarFallback,
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "darkraise-ui"
import { SidebarNav, SidebarProvider, SkipLink, useSidebar } from "darkraise-ui/layout"
import { KeyRound, LogOut, Menu, PanelLeft, PanelLeftClose, Search, Settings } from "lucide-react"
import { IdentityMark } from "./identity-mark"
import type { NavGroup, NavItem } from "./nav"
import { ThemeDrawerRow, ThemeSwitcherButton } from "./theme-customizer"

const SHORTCUT = /Mac|iPhone|iPad/i.test(navigator.platform) ? "⌘K" : "Ctrl K"

/**
 * Which screen is showing: the path of the first route under the root.
 *
 * Not the pathname, because a trace is a child of /requests: /requests and
 * /requests/01J… are one screen with a drawer open over it, and whatever is
 * keyed on "the screen changed" -- its error boundary, where focus goes --
 * must not fire when a trace opens or closes. /providers/groq and
 * /providers/nebius are separate routes' matches, so they stay two screens.
 */
export function useScreenKey(): string {
  return useRouterState({ select: (s) => s.matches[1]?.pathname ?? s.location.pathname })
}

/**
 * Moves focus into the content when the screen changes.
 *
 * darkraise's own layouts call `useRouteFocus` for this, and it was lost when
 * the shell was composed by hand. That hook keys on the pathname, though,
 * which here would pull focus out of a trace drawer the moment it opened and
 * out of the table row it returned to when it closed; this keys on the
 * screen instead. Without it a keyboard user who picks Requests in the rail
 * is left on the rail link, and a screen reader announces nothing.
 */
function useScreenFocus(screen: string) {
  const previous = useRef<string | null>(null)
  useEffect(() => {
    const first = previous.current === null
    const changed = previous.current !== screen
    previous.current = screen
    if (first || !changed) return
    document.getElementById("main-content")?.focus()
  }, [screen])
}

/**
 * The console's chrome: rail, header and content pane.
 *
 * darkraise-ui's SidebarLayout ships a brand logo, a search palette and a
 * notification bell with no prop to leave any of them out. This console has
 * its own mark, its own palette and nothing to put in a bell, so the shell
 * is composed here from the same primitives and the same class names the
 * library styles, and stays visually the library's layout.
 */
export function AppShell({
  nav,
  footerNav,
  headerSlot,
  onSearch,
  onChangePassword,
  onSettings,
  onLogout,
  children,
}: {
  nav: NavGroup[]
  footerNav: NavGroup[]
  headerSlot?: ReactNode
  onSearch: () => void
  onChangePassword: () => void
  onSettings: () => void
  onLogout: () => void
  children: ReactNode
}) {
  const [collapsed, setCollapsed] = useState(false)
  useScreenFocus(useScreenKey())
  const toggle = (
    <Button
      variant="ghost"
      size="icon"
      className="dr-sidebar-nav-item dr-sidebar-layout-toggle"
      onClick={() => setCollapsed((v) => !v)}
      aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
    >
      {collapsed ? (
        <PanelLeft className="size-[var(--icon-size)]" />
      ) : (
        <PanelLeftClose className="size-[var(--icon-size)]" />
      )}
    </Button>
  )
  const account = (
    <AccountMenu
      onChangePassword={onChangePassword}
      onSettings={onSettings}
      onLogout={onLogout}
    />
  )
  return (
    <TooltipProvider>
      <SidebarProvider collapsed={collapsed}>
        <div className="dr-sidebar-layout">
          <SkipLink>Skip to content</SkipLink>
          <aside
            aria-label="Primary"
            aria-expanded={!collapsed}
            className="dr-sidebar-layout-aside sidebar-gradient-overlay theme-transition bg-surface-sidebar"
            data-collapsed={collapsed ? "true" : undefined}
          >
            <div className="dr-sidebar-layout-aside-header">
              {collapsed ? (
                // One square is all the collapsed rail has, so the mark and
                // the toggle share it: the mark at rest, the toggle on hover
                // and on focus, since a keyboard user has no hover.
                <div className="dr-sidebar-layout-brand-slot">
                  <Brand collapsed />
                  {toggle}
                </div>
              ) : (
                <>
                  <Brand />
                  {toggle}
                </>
              )}
            </div>
            <div className="dr-sidebar-layout-search">
              <Button
                variant="outline"
                size={collapsed ? "icon" : undefined}
                className="dr-search-command-trigger"
                data-collapsed={collapsed ? "true" : undefined}
                onClick={onSearch}
                aria-label={`Search (${SHORTCUT})`}
                title={collapsed ? `Search (${SHORTCUT})` : undefined}
              >
                <Search className="size-[var(--icon-size)]" />
                {!collapsed && (
                  <>
                    <span>Search</span>
                    <kbd className="dr-search-command-shortcut">{SHORTCUT}</kbd>
                  </>
                )}
              </Button>
            </div>
            <div className="dr-sidebar-layout-nav-scroll">
              <RailNav nav={nav} />
            </div>
            <div className="dr-sidebar-layout-aside-section" data-position="footer">
              <RailNav nav={footerNav} />
            </div>
          </aside>
          <div className="dr-sidebar-layout-main">
            <header className="dr-layout-header header-gradient-overlay theme-transition">
              <NavDrawer
                nav={nav}
                footerNav={footerNav}
                onSearch={onSearch}
                onChangePassword={onChangePassword}
                onLogout={onLogout}
              />
              <div className="dr-layout-header-end">
                {headerSlot}
                <div className="app-header-actions">
                  <ThemeSwitcherButton />
                  {account}
                </div>
              </div>
            </header>
            <main
              id="main-content"
              tabIndex={-1}
              className="dr-sidebar-layout-content"
              data-content
            >
              {children}
            </main>
          </div>
        </div>
      </SidebarProvider>
    </TooltipProvider>
  )
}

/**
 * The rail, below `md`, as a drawer.
 *
 * darkraise's MobileDrawer is an uncontrolled sheet: nothing outside it can
 * close it, so choosing a destination changed the page underneath and left
 * the drawer covering it, and "Change password" opened its dialog on top of
 * a drawer that was still open. Composed here from the same primitives and
 * classes, with the open state in hand, it closes on anything that takes the
 * operator somewhere: a link, Search, or an account action.
 */
function NavDrawer({
  nav,
  footerNav,
  onSearch,
  onChangePassword,
  onLogout,
}: {
  nav: NavGroup[]
  footerNav: NavGroup[]
  onSearch: () => void
  onChangePassword: () => void
  onLogout: () => void
}) {
  const [open, setOpen] = useState(false)
  // Closed in the same update as the action runs, so the drawer has let go of
  // focus before the dialog the action opens takes it.
  const then = (action: () => void) => () => {
    setOpen(false)
    action()
  }
  // Every destination in the drawer is a link, the library's SidebarNav's as
  // much as ours, so one listener closes it for all of them -- including the
  // link to the page already showing, which navigates nowhere.
  const closeOnLink = (e: MouseEvent) => {
    if ((e.target as Element).closest("a[href]")) setOpen(false)
  }
  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>
        <Button variant="ghost" size="icon" className="dr-mobile-drawer-trigger">
          <Menu className="size-[var(--icon-size-lg)]" aria-hidden="true" />
          <span className="sr-only">Open menu</span>
        </Button>
      </SheetTrigger>
      <SheetContent side="left" className="dr-mobile-drawer-content">
        <SheetHeader>
          <SheetTitle>Navigation</SheetTitle>
        </SheetHeader>
        <SidebarProvider collapsed={false}>
          {/* Search and the theme row scroll with the nav rather than being
              pinned beside it: on a phone the pinned sections already take
              most of the height, and the open theme panel would leave the
              nav none. */}
          <div className="dr-mobile-drawer-body" onClickCapture={closeOnLink}>
            {/* The rail's Search goes where the rail goes. Without it a
                touch operator had no way into the palette at all: ⌘K needs
                a keyboard. */}
            <div className="app-drawer-actions">
              <DrawerAction icon={Search} label="Search" onClick={then(onSearch)} />
            </div>
            <SidebarNav nav={nav} activeBar={ACTIVE_BAR} />
            {/* Below `sm` the header has no room for its actions, so the
                drawer carries them; above it they are back in the header and
                would be the same offer twice. */}
            <div className="app-drawer-actions sm:hidden">
              <ThemeDrawerRow />
            </div>
          </div>
          <div
            className="dr-mobile-drawer-section"
            data-position="footer"
            onClickCapture={closeOnLink}
          >
            <RailNav nav={footerNav} />
            <div className="app-drawer-actions sm:hidden">
              <DrawerAction icon={KeyRound} label="Change password" onClick={then(onChangePassword)} />
              <DrawerAction icon={LogOut} label="Log out" onClick={then(onLogout)} />
            </div>
          </div>
        </SidebarProvider>
      </SheetContent>
    </Sheet>
  )
}

/** A drawer row that acts rather than links, in the nav items' own markup. */
function DrawerAction({
  icon: Icon,
  label,
  onClick,
}: {
  icon: NavItem["icon"]
  label: string
  onClick: () => void
}) {
  return (
    <button type="button" className="dr-sidebar-nav-item dr-sidebar-nav-link" onClick={onClick}>
      <span className="dr-sidebar-nav-icon">
        <Icon className="dr-sidebar-nav-icon-svg" />
      </span>
      <span className="dr-sidebar-nav-label">{label}</span>
    </button>
  )
}

function Brand({ collapsed = false }: { collapsed?: boolean }) {
  return (
    <div className="dr-brand-logo" data-collapsed={collapsed ? "true" : undefined}>
      <IdentityMark size={32} />
      {!collapsed && <span className="dr-brand-logo-label">darkrouter</span>}
    </div>
  )
}

/**
 * Indicator style for the active nav item.
 *
 * The library keys its CSS on an ancestor attribute, and SidebarNav sets it
 * from an `activeBar` prop. RailNav is our own markup, so it has to set the
 * same attribute or the rail falls back to the preset's look while the
 * drawer — which does go through SidebarNav — follows the prop.
 */
const ACTIVE_BAR = "both"

/**
 * The rail's links, in the library's own markup.
 *
 * Rendered here rather than through SidebarNav so a collapsed link — an icon
 * and nothing else — still has a name a screen reader can announce. The
 * library gives it a tooltip, which is not an accessible name.
 */
export function RailNav({ nav }: { nav: NavGroup[] }) {
  const { collapsed } = useSidebar()
  return (
    <nav
      className="dr-sidebar-nav"
      data-collapsed={collapsed || undefined}
      data-active-bar={ACTIVE_BAR}
    >
      {nav.map((group, i) => (
        <div
          key={group.label}
          className="dr-sidebar-nav-group"
          data-position={i > 0 ? "subsequent" : undefined}
        >
          <p
            className="dr-sidebar-nav-group-label"
            data-collapsed={collapsed ? "true" : undefined}
            aria-hidden={collapsed || undefined}
          >
            {group.label}
          </p>
          {group.items.map((item) => (
            <RailLink key={item.href} item={item} collapsed={collapsed} />
          ))}
        </div>
      ))}
    </nav>
  )
}

function RailLink({ item, collapsed }: { item: NavItem; collapsed: boolean }) {
  const Icon = item.icon
  const link = (
    <Link
      to={item.href}
      className="dr-sidebar-nav-item dr-sidebar-nav-link"
      data-collapsed={collapsed ? "true" : undefined}
      activeProps={{ className: "active" }}
      // Only "/" matches exactly: every other item owns its subtree, so
      // Providers stays lit on /providers/groq.
      activeOptions={{ exact: item.href === "/" }}
      aria-label={collapsed ? item.label : undefined}
    >
      <span className="dr-sidebar-nav-icon">
        <Icon className="dr-sidebar-nav-icon-svg" />
      </span>
      {!collapsed && <span className="dr-sidebar-nav-label">{item.label}</span>}
    </Link>
  )
  if (!collapsed) return link
  return (
    <Tooltip>
      <TooltipTrigger asChild>{link}</TooltipTrigger>
      <TooltipContent side="right">{item.label}</TooltipContent>
    </Tooltip>
  )
}

function AccountMenu({
  onChangePassword,
  onSettings,
  onLogout,
}: {
  onChangePassword: () => void
  onSettings: () => void
  onLogout: () => void
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          className="dr-user-menu-trigger"
          aria-label="Account menu"
        >
          <Avatar className="dr-user-menu-avatar">
            <AvatarFallback>A</AvatarFallback>
          </Avatar>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent className="dr-user-menu-content" align="end">
        <DropdownMenuLabel className="dr-user-menu-label">
          <p className="dr-user-menu-name">Signed in</p>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        {/* onSelect, not onClick: darkraise's item calls onClick only from
            the pointer, while Enter and Space go through onSelect alone, so
            a keyboard operator could highlight "Log out" and not run it.
            The pointer reaches onSelect too, so one handler serves both. */}
        <DropdownMenuItem onSelect={onChangePassword}>
          <KeyRound className="dr-user-menu-item-icon" />
          Change password
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={onSettings}>
          <Settings className="dr-user-menu-item-icon" />
          Settings
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={onLogout}>
          <LogOut className="dr-user-menu-item-icon" />
          Log out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
