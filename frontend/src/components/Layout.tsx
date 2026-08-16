import {
	Activity,
	ArrowUpRight,
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
	X
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import { useState } from "react";
import type { AppVersion, NotificationsResponse, ReleaseInfo } from "../api/client";
import { AboutDialog } from "./AboutDialog";
import type { Page } from "../App";
import { BrandLogo } from "./BrandLogo";
import { AppearanceMenu } from "./AppearanceMenu";
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
  readonly showReleaseUpdate: boolean;
  readonly onDismissReleaseUpdate: () => void;
};

export function Layout({ page, setPage, themeMode, onThemeModeChange, children, apiState, onOpenSearch, notifications, onOpenNotifications, username, onSignOut, appVersion, releaseUpdate, showReleaseUpdate, onDismissReleaseUpdate }: LayoutProps) {
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  const [aboutOpen, setAboutOpen] = useState(false);
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
        <div className={releaseUpdate ? "sidebar-footer has-release-update" : "sidebar-footer"}>
          <span>{releaseUpdate ? "Next version" : "Version"}</span>
          <button
            className={releaseUpdate ? "sidebar-version-button has-release-update" : "sidebar-version-button"}
            type="button"
            title={releaseUpdate ? `About Faro · ${releaseUpdate.display} available` : "About Faro"}
            aria-label={releaseUpdate ? `About Faro. Now ${appVersion?.display ?? "checking"}; next ${releaseUpdate.display} is available.` : `About Faro ${appVersion?.display ?? "application version"}`}
            onClick={() => {
              setMobileNavOpen(false);
              setAboutOpen(true);
            }}
          >
            {releaseUpdate ? (
              <>
                <span className="sidebar-version-current">Now {appVersion?.display ?? "checking"}</span>
                <span className="sidebar-version-next">Next {releaseUpdate.display}</span>
              </>
            ) : appVersion?.display ?? "Checking"}
          </button>
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
            <AppearanceMenu themeMode={themeMode} onThemeModeChange={onThemeModeChange} />
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
        {showReleaseUpdate && releaseUpdate && (
          <aside className="update-banner" role="status" aria-label={`Faro ${releaseUpdate.display} update available`}>
            <span className="update-banner-icon" aria-hidden="true"><ArrowUpRight size={18} /></span>
            <div className="update-banner-copy">
              <span className="update-banner-eyebrow">New release available</span>
              <strong>Faro {releaseUpdate.display} is available.</strong>
              <span className="update-banner-current">You’re running {appVersion?.display ?? "an earlier version"}.</span>
            </div>
            <div className="update-banner-actions">
              <a href={releaseUpdate.url} target="_blank" rel="noreferrer">
                View release <ExternalLink size={15} />
              </a>
              <button className="update-banner-dismiss" type="button" onClick={onDismissReleaseUpdate} aria-label={`Dismiss Faro ${releaseUpdate.display} update`} title="Dismiss update">
                <X size={16} />
              </button>
            </div>
          </aside>
        )}
        <div className="main-content">{children}</div>
      </main>
      <AboutDialog open={aboutOpen} onClose={() => setAboutOpen(false)} appVersion={appVersion} releaseUpdate={releaseUpdate} />
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
