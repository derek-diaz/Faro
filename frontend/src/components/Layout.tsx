import {
	Activity,
	Ban,
	BarChart3,
	CheckCircle2,
	HelpCircle,
	RefreshCw,
	Router,
	Settings,
	Sun,
	Shield
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import type { Page } from "../App";

const navItems: { id: Page; label: string; icon: LucideIcon }[] = [
  { id: "dashboard", label: "Dashboard", icon: BarChart3 },
  { id: "queries", label: "Query Log", icon: Activity },
  { id: "records", label: "Local DNS", icon: Router },
  { id: "blocklists", label: "Blocklists", icon: Shield },
  { id: "lists", label: "Allowlist / Blocklist", icon: Ban },
  { id: "settings", label: "Settings", icon: Settings }
];

type LayoutProps = {
  page: Page;
  setPage: (page: Page) => void;
  children: ReactNode;
  apiState: "checking" | "online" | "offline";
  onReload: () => Promise<void>;
};

export function Layout({ page, setPage, children, apiState, onReload }: LayoutProps) {
  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-mark" aria-hidden="true">
            <span />
          </div>
          <div>
            <strong>Faro</strong>
            <span>DNS control plane</span>
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
            <strong>CoreDNS</strong>
            <span>{apiState === "online" ? "Running" : "Waiting for API"}</span>
          </div>
          <button className="reload-button" type="button" onClick={() => void onReload()} aria-label="Reload CoreDNS">
            <RefreshCw size={16} />
            <span>Reload CoreDNS</span>
          </button>
        </div>
      </aside>

      <main className="main">
        <header className="topbar">
          <div>
            <h1>{navItems.find((item) => item.id === page)?.label ?? "Dashboard"}</h1>
            <p>Local records, blocking, and query visibility without raw DNS clutter.</p>
          </div>
          <div className="topbar-actions">
            <div className="system-status">
              <CheckCircle2 size={17} />
              <span>{apiState === "online" ? "All systems normal" : apiState === "offline" ? "API offline" : "Checking systems"}</span>
            </div>
            <button className="icon-button" type="button" aria-label="Theme placeholder">
              <Sun size={18} />
            </button>
            <button className="icon-button" type="button" aria-label="Help placeholder">
              <HelpCircle size={18} />
            </button>
          </div>
        </header>
        {children}
      </main>
    </div>
  );
}
