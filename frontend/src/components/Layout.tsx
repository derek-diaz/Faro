import {
	Activity,
	BarChart3,
	Bell,
	CheckCircle2,
	ChevronDown,
	Database,
	MonitorSmartphone,
	LogOut,
	Network,
	Router,
	Search,
	Settings,
	ShieldCheck,
	Moon,
	Sun,
	SunMoon
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import type { NotificationsResponse } from "../api/client";
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
  page: Page;
  setPage: (page: Page) => void;
  themeMode: ThemeMode;
  onThemeModeChange: (mode: ThemeMode) => void;
  children: ReactNode;
  apiState: "checking" | "online" | "offline";
  onOpenSearch: () => void;
  notifications: NotificationsResponse;
  onOpenNotifications: () => void;
  username: string;
  onSignOut: () => Promise<void>;
};

export function Layout({ page, setPage, themeMode, onThemeModeChange, children, apiState, onOpenSearch, notifications, onOpenNotifications, username, onSignOut }: LayoutProps) {
  const currentPage = navItems.find((item) => item.id === page) ?? navItems[0];
  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand">
          <BrandLogo />
          <strong className="brand-wordmark">Faro</strong>
        </div>

        <nav>
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
                }}
              >
                <Icon size={18} />
                <span>{item.label}</span>
              </a>
            );
          })}
        </nav>
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
            <div className={`system-status ${apiState}`} title={apiState === "online" ? "Faro's control plane is responding; DNS and upstream health are shown on the dashboard" : apiState === "offline" ? "Faro API is not responding" : "Checking Faro services"}>
              <CheckCircle2 size={17} />
              <span>{apiState === "online" ? "API online" : apiState === "offline" ? "Offline" : "Checking"}</span>
            </div>
            <button className="icon-button notification-button" type="button" onClick={onOpenNotifications} aria-label="Network updates">
              <Bell size={18} />
              {notifications.unread_count > 0 && <span>{Math.min(notifications.unread_count, 9)}</span>}
            </button>
            <details className="theme-menu" onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) event.currentTarget.removeAttribute("open"); }}>
              <summary className="icon-button" aria-label="Choose appearance" title="Choose appearance">
                {themeMode === "dark" ? <Moon size={18} /> : themeMode === "light" ? <Sun size={18} /> : <SunMoon size={18} />}
              </summary>
              <div className="theme-menu-popover" role="menu" aria-label="Appearance">
                <span>Appearance</span>
                {(["system", "light", "dark"] as ThemeMode[]).map((mode) => (
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
                    {mode === "system" ? <SunMoon size={15} /> : mode === "light" ? <Sun size={15} /> : <Moon size={15} />}
                    <span>{mode === "system" ? "System" : mode === "light" ? "Light" : "Dark"}</span>
                  </button>
                ))}
              </div>
            </details>
            <details className="account-menu" onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) event.currentTarget.removeAttribute("open"); }} onKeyDown={(event) => { if (event.key === "Escape") { event.currentTarget.removeAttribute("open"); event.currentTarget.querySelector("summary")?.focus(); } }}>
              <summary aria-label={`Account menu for ${username}`}><UserBadge username={username} /><ChevronDown size={14} /></summary>
              <div className="account-menu-popover">
                <header><small>Signed in as</small><strong>{username}</strong></header>
                <button type="button" onClick={(event) => { event.currentTarget.closest("details")?.removeAttribute("open"); setPage("settings"); }}><Settings size={16} /><span>Settings</span></button>
                <button type="button" onClick={(event) => { event.currentTarget.closest("details")?.removeAttribute("open"); void onSignOut(); }}><LogOut size={16} /><span>Sign out</span></button>
              </div>
            </details>
          </div>
        </header>
        <div className="main-content">{children}</div>
      </main>
    </div>
  );
}

function UserBadge({ username }: { username: string }) {
  return <span className="user-badge" aria-hidden="true">{username.slice(0, 1).toUpperCase()}</span>;
}
