import { useState, type ReactNode } from "react"
import { Link } from "@tanstack/react-router"
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
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "darkraise-ui"
import { MobileDrawer, SidebarProvider, SkipLink, useSidebar } from "darkraise-ui/layout"
import { ThemeSwitcher } from "darkraise-ui/theme"
import { KeyRound, LogOut, PanelLeft, PanelLeftClose, Search, Settings } from "lucide-react"
import { IdentityMark } from "./identity-mark"
import type { NavGroup, NavItem } from "./nav"

const SHORTCUT = /Mac|iPhone|iPad/i.test(navigator.platform) ? "⌘K" : "Ctrl K"

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
              <MobileDrawer
                nav={nav}
                activeBar={ACTIVE_BAR}
                footer={
                  <>
                    <RailNav nav={footerNav} />
                    {/* Below `sm` the header has no room for its actions, so
                        the drawer carries them; above it they are back in the
                        header and would be the same offer twice. */}
                    <div className="app-drawer-account sm:hidden">
                      <button type="button" className="dr-sidebar-nav-item dr-sidebar-nav-link" onClick={onChangePassword}>
                        <span className="dr-sidebar-nav-icon">
                          <KeyRound className="dr-sidebar-nav-icon-svg" />
                        </span>
                        <span className="dr-sidebar-nav-label">Change password</span>
                      </button>
                      <button type="button" className="dr-sidebar-nav-item dr-sidebar-nav-link" onClick={onLogout}>
                        <span className="dr-sidebar-nav-icon">
                          <LogOut className="dr-sidebar-nav-icon-svg" />
                        </span>
                        <span className="dr-sidebar-nav-label">Log out</span>
                      </button>
                    </div>
                  </>
                }
              />
              <div className="dr-layout-header-end">
                {headerSlot}
                <div className="app-header-actions">
                  <ThemeSwitcher />
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
          <p className="dr-user-menu-name">Administrator</p>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem onClick={onChangePassword}>
          <KeyRound className="dr-user-menu-item-icon" />
          Change password
        </DropdownMenuItem>
        <DropdownMenuItem onClick={onSettings}>
          <Settings className="dr-user-menu-item-icon" />
          Settings
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem onClick={onLogout}>
          <LogOut className="dr-user-menu-item-icon" />
          Log out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
