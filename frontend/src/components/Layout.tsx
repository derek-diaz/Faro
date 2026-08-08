import {
	Activity,
	BarChart3,
	Bell,
	CheckCircle2,
	ChevronDown,
	Database,
	ExternalLink,
	MonitorSmartphone,
	LogOut,
	Menu,
	Network,
	Router,
	Search,
	Settings,
	ShieldCheck,
	Moon,
	Sun,
	SunMoon,
	X
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import { useState } from "react";
import type { AppVersion, NotificationsResponse, ReleaseInfo } from "../api/client";
import type { Page } from "../App";
import { BrandLogo } from "./BrandLogo";
import type { ThemeMode } from "../theme";

const navItems: { id: Page; label: string; description: string; href: string; icon: LucideIcon }[] = [
  { id: "dashboard", label: "Dashboard", description: "Live network health and DNS traffic at a glance.", href: "/", icon: BarChart3 },
  { id: "queries", label: "Activity", description: "Explore DNS requests, blocks, and system changes.", href: "/activity", icon: Activity },
  { id: "devices", label: "Devices", description: "See which devices are active on your network.", href: "/devices", icon: MonitorSmartphone },
  { id: "records", label: "Local DNS", description: "Manage friendly names for services on your network.", href: "/local-dns", icon: Router },
  { id: "upstreams", label: "Upstreams", description: "Choose the DNS providers Faro uses for public lookups.", href: "/upstreams", icon: Network },
  { id: "protection", label: "Protection", description: "Home covers every device by default. Add a setup only when some devices need different blocking or exceptions.", href: "/protection", icon: ShieldCheck },
  { id: "blocklists", label: "Blocklists", description: "Install, update, pause, and remove filtering sources.", href: "/blocklists", icon: Database },
  { id: "settings", label: "Settings", description: "Configure DNS behavior and Faro preferences.", href: "/settings", icon: Settings }
];

type LayoutProps = {
  readonly page: Page;
  readonly setPage: (page: Page) => void;
  readonly themeMode: ThemeMode;
  readonly onThemeModeChange: (mode: ThemeMode) => void;
  readonly children: ReactNode;
  readonly apiState: "checking" | "online" | "offline";
  readonly onOpenSearch: () => void;
  readonly notifications: NotificationsResponse;
  readonly onOpenNotifications: () => void;
  readonly username: string;
  readonly onSignOut: () => Promise<void>;
  readonly appVersion: AppVersion | null;
  readonly releaseUpdate: ReleaseInfo | null;
};

export function Layout({ page, setPage, themeMode, onThemeModeChange, children, apiState, onOpenSearch, notifications, onOpenNotifications, username, onSignOut, appVersion, releaseUpdate }: LayoutProps) {
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  const currentPage = navItems.find((item) => item.id === page) ?? navItems[0];
  const apiStatusTitle = getApiStatusTitle(apiState);
  const apiStatusLabel = getApiStatusLabel(apiState);
  return (
    <div className="app-shell">
      <aside className={mobileNavOpen ? "sidebar mobile-nav-open" : "sidebar"}>
        <div className="brand">
          <BrandLogo />
          <strong className="brand-wordmark">Faro</strong>
          <button className="mobile-nav-toggle" type="button" aria-expanded={mobileNavOpen} aria-controls="primary-navigation" onClick={() => setMobileNavOpen((open) => !open)}>
            {mobileNavOpen ? <X size={18} /> : <Menu size={18} />}
            <span>{mobileNavOpen ? "Close" : "Menu"}</span>
          </button>
        </div>

        <nav id="primary-navigation" aria-label="Primary navigation">
          {navItems.map((item) => {
            const Icon = item.icon;
            return (
              <a
                key={item.id}
                className={page === item.id ? "nav-item active" : "nav-item"}
                href={item.href}
                onClick={(event) => {
                  event.preventDefault();
                  setPage(item.id);
                  setMobileNavOpen(false);
                }}
              >
                <Icon size={18} />
                <span>{item.label}</span>
              </a>
            );
          })}
        </nav>
        <div className="sidebar-footer" title="Faro application version">
          <span>Version</span>
          <small>{appVersion?.display ?? "Checking"}</small>
        </div>
      </aside>

      <main className="main">
        <header className="topbar">
          <div>
            <h1>{currentPage.label}</h1>
            <p>{currentPage.description}</p>
          </div>
          <div className="topbar-actions">
            <button className="search-trigger" type="button" onClick={onOpenSearch}>
              <Search size={17} />
              <span>Search</span>
              <kbd>Ctrl K</kbd>
            </button>
            <div className={`system-status ${apiState}`} title={apiStatusTitle}>
              <CheckCircle2 size={17} />
              <span>{apiStatusLabel}</span>
            </div>
            <button className="icon-button notification-button" type="button" onClick={onOpenNotifications} aria-label="Network updates">
              <Bell size={18} />
              {notifications.unread_count > 0 && <span>{Math.min(notifications.unread_count, 9)}</span>}
            </button>
            <details className="theme-menu" onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) event.currentTarget.removeAttribute("open"); }}>
              <summary className="icon-button" aria-label="Choose appearance" title="Choose appearance">
                {themeIcon(themeMode, 18)}
              </summary>
              <div className="theme-menu-popover" role="menu" aria-label="Appearance">
                <span>Appearance</span>
                {(["system", "light", "dark"] as ThemeMode[]).map((mode) => {
                  return (
                    <button
                      key={mode}
                      type="button"
                      role="menuitemradio"
                      aria-checked={themeMode === mode}
                      className={themeMode === mode ? "selected" : ""}
                      onClick={(event) => {
                        onThemeModeChange(mode);
                        event.currentTarget.closest("details")?.removeAttribute("open");
                      }}
                    >
                      {themeIcon(mode, 15)}
                      <span>{themeModeLabel(mode)}</span>
                    </button>
                  );
                })}
              </div>
            </details>
            <details className="account-menu" onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) event.currentTarget.removeAttribute("open"); }}>
              <summary
                aria-label={`Account menu for ${username}`}
                onKeyDown={(event) => {
                  if (event.key === "Escape") {
                    const details = event.currentTarget.closest("details");
                    details?.removeAttribute("open");
                    event.currentTarget.focus();
                  }
                }}
              >
                <UserBadge username={username} /><ChevronDown size={14} />
              </summary>
              <div className="account-menu-popover">
                <header><small>Signed in as</small><strong>{username}</strong></header>
                <button type="button" onClick={(event) => { event.currentTarget.closest("details")?.removeAttribute("open"); setPage("settings"); }}><Settings size={16} /><span>Settings</span></button>
                <button type="button" onClick={(event) => { event.currentTarget.closest("details")?.removeAttribute("open"); void onSignOut(); }}><LogOut size={16} /><span>Sign out</span></button>
              </div>
            </details>
          </div>
        </header>
        {releaseUpdate && (
          <output className="update-banner">
            <div className="update-banner-copy">
              <strong>Faro {releaseUpdate.display} is available.</strong>
              <span>You are running {appVersion?.display ?? "an earlier version"}.</span>
            </div>
            <a href={releaseUpdate.url} target="_blank" rel="noreferrer">
              View release <ExternalLink size={15} />
            </a>
          </output>
        )}
        <div className="main-content">{children}</div>
      </main>
    </div>
  );
}

function UserBadge({ username }: { readonly username: string }) {
  return <span className="user-badge" aria-hidden="true">{username.slice(0, 1).toUpperCase()}</span>;
}

function getApiStatusTitle(apiState: LayoutProps["apiState"]) {
  switch (apiState) {
    case "online":
      return "Faro's control plane is responding; DNS and upstream health are shown on the dashboard";
    case "offline":
      return "Faro API is not responding";
    default:
      return "Checking Faro services";
  }
}

function getApiStatusLabel(apiState: LayoutProps["apiState"]) {
  switch (apiState) {
    case "online":
      return "Faro is online";
    case "offline":
      return "Offline";
    default:
      return "Checking";
  }
}

function themeIcon(mode: ThemeMode, size: number) {
  switch (mode) {
    case "dark":
      return <Moon size={size} />;
    case "light":
      return <Sun size={size} />;
    default:
      return <SunMoon size={size} />;
  }
}

function themeModeLabel(mode: ThemeMode) {
  switch (mode) {
    case "dark":
      return "Dark";
    case "light":
      return "Light";
    default:
      return "System";
  }
}
