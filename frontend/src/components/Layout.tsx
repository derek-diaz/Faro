import {
	Activity,
	Ban,
	BarChart3,
	Bell,
	CheckCircle2,
	MonitorSmartphone,
	Network,
	RefreshCw,
	Router,
	Search,
	Settings,
	Shield
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import type { NotificationsResponse } from "../api/client";
import type { Page } from "../App";

const navItems: { id: Page; label: string; description: string; icon: LucideIcon }[] = [
  { id: "dashboard", label: "Dashboard", description: "Live network health and DNS traffic at a glance.", icon: BarChart3 },
  { id: "queries", label: "Activity", description: "Explore DNS requests, blocks, and system changes.", icon: Activity },
  { id: "devices", label: "Devices", description: "See which devices are active on your network.", icon: MonitorSmartphone },
  { id: "records", label: "Local DNS", description: "Manage friendly names for services on your network.", icon: Router },
  { id: "upstreams", label: "Upstreams", description: "Choose the DNS providers Faro uses for public lookups.", icon: Network },
  { id: "blocklists", label: "Blocklists", description: "Manage the lists Faro uses to filter domains.", icon: Shield },
  { id: "lists", label: "Allowlist / Blocklist", description: "Create precise rules for individual domains.", icon: Ban },
  { id: "settings", label: "Settings", description: "Configure DNS behavior and Faro preferences.", icon: Settings }
];

type LayoutProps = {
  page: Page;
  setPage: (page: Page) => void;
  children: ReactNode;
  apiState: "checking" | "online" | "offline";
  onReload: () => Promise<void>;
  onOpenSearch: () => void;
  notifications: NotificationsResponse;
  onOpenNotifications: () => void;
};

export function Layout({ page, setPage, children, apiState, onReload, onOpenSearch, notifications, onOpenNotifications }: LayoutProps) {
  const currentPage = navItems.find((item) => item.id === page) ?? navItems[0];
  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-mark" aria-hidden="true">
            <span />
          </div>
          <div>
            <strong>Faro</strong>
            <span>Network observability</span>
          </div>
        </div>

        <nav>
          {navItems.map((item) => {
            const Icon = item.icon;
            return (
              <button
                key={item.id}
                className={page === item.id ? "nav-item active" : "nav-item"}
                onClick={() => setPage(item.id)}
                type="button"
              >
                <Icon size={18} />
                <span>{item.label}</span>
              </button>
            );
          })}
        </nav>

        <div className="sidebar-status">
          <CheckCircle2 size={18} />
          <div>
            <strong>DNS Healthy</strong>
            <span>{apiState === "online" ? "Engine running" : "Waiting for API"}</span>
          </div>
          <button className="reload-button" type="button" onClick={() => void onReload()} aria-label="Reload DNS">
            <RefreshCw size={16} />
            <span>Reload DNS</span>
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
            <div className="system-status">
              <CheckCircle2 size={17} />
              <span>{apiState === "online" ? "All systems normal" : apiState === "offline" ? "API offline" : "Checking systems"}</span>
            </div>
            <button className="icon-button notification-button" type="button" onClick={onOpenNotifications} aria-label="Notifications">
              <Bell size={18} />
              {notifications.unread_count > 0 && <span>{Math.min(notifications.unread_count, 9)}</span>}
            </button>
          </div>
        </header>
        {children}
      </main>
    </div>
  );
}
