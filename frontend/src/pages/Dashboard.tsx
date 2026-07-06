import { CheckCircle2, ListChecks, ShieldCheck, ShieldOff, TrendingUp } from "lucide-react";
import type { Blocklist, DashboardSummary, Setting } from "../api/client";
import { DomainList } from "../components/DomainList";
import { MetricCard } from "../components/MetricCard";
import { StatusBadge } from "../components/StatusBadge";

type DashboardProps = {
  summary: DashboardSummary | null;
  blocklists: Blocklist[];
  settings: Setting[];
  loading: boolean;
};

export function Dashboard({ summary, blocklists, settings, loading }: DashboardProps) {
  if (loading || !summary) {
    return <div className="loading-panel">Loading dashboard...</div>;
  }

  const upstreams = (settings.find((setting) => setting.key === "upstream_dns")?.value ?? "1.1.1.1,9.9.9.9")
    .split(",")
    .map((upstream) => upstream.trim())
    .filter(Boolean);
  const hasActivity = summary.recent_activity.length > 0;

  return (
    <div className="page-stack">
      <div className="metrics-grid">
        <MetricCard label="Total queries today" value={summary.total_queries_today} detail="Live CoreDNS traffic" icon={<TrendingUp size={27} />} />
        <MetricCard
          label="Blocked queries today"
          value={summary.blocked_queries_today}
          tone="blocked"
          detail="Blocklist and manual rules"
          icon={<ShieldOff size={27} />}
        />
        <MetricCard
          label="Block percentage"
          value={`${summary.block_percentage.toFixed(1)}%`}
          tone="warn"
          detail="Today"
          icon={<ShieldCheck size={27} />}
        />
        <MetricCard
          label="Enabled blocklists"
          value={`${summary.enabled_blocklists}`}
          tone="safe"
          detail={`${summary.blocklist_entries} domains`}
          icon={<ListChecks size={27} />}
        />
      </div>

      <div className="dashboard-grid">
        <DomainList title="Top queried domains" items={summary.top_queried_domains} empty="No queries yet." showFavicons />
        <DomainList title="Top blocked domains" items={summary.top_blocked_domains} empty="No blocked queries yet." tone="blocked" showFavicons />
        <DomainList title="Top clients" items={summary.top_clients} empty="No client activity yet." />
      </div>

      <div className="dashboard-bottom-grid">
        <section className="panel upstream-panel">
          <div className="panel-title">
            <h2>Upstream health</h2>
          </div>
          <div className="upstream-list">
            {upstreams.map((upstream) => (
              <div className="upstream-row" key={upstream}>
                <CheckCircle2 size={18} />
                <strong>{upstream}</strong>
                <span>Configured</span>
                <small>pending check</small>
              </div>
            ))}
          </div>
          <div className="upstream-note">
            <CheckCircle2 size={17} />
            <span>Health checks are configured as a placeholder for the next iteration.</span>
          </div>
        </section>

        <section className="panel activity-panel">
          <div className="panel-title">
            <h2>Recent activity</h2>
            {hasActivity && <a href="#query-log">View query log</a>}
          </div>
          {hasActivity ? (
            <div className="activity-list">
              {summary.recent_activity.slice(0, 5).map((query) => (
                <div className="activity-row" key={`${query.timestamp}-${query.domain}-${query.client_ip}`}>
                  <StatusBadge value={query.action} />
                  <strong>{query.domain}</strong>
                  <span>{query.client_ip}</span>
                  <small>{new Date(query.timestamp).toLocaleTimeString()}</small>
                </div>
              ))}
            </div>
          ) : (
            <div className="empty-state">
              <strong>No DNS queries yet</strong>
              <span>Point a client or UniFi DHCP DNS at Faro, then activity will appear here.</span>
            </div>
          )}
        </section>

        <section className="panel blocklist-summary-panel">
          <div className="panel-title">
            <h2>Blocklist summary</h2>
            <a href="#blocklists">Manage blocklists</a>
          </div>
          <div className="summary-table">
            <div className="summary-header">
              <span>Blocklist</span>
              <span>Status</span>
              <span>Domains</span>
              <span>Updated</span>
            </div>
            {blocklists.length === 0 ? (
              <p className="empty">No blocklists configured.</p>
            ) : (
              blocklists.slice(0, 5).map((blocklist) => (
                <div className="summary-row" key={blocklist.id}>
                  <strong>{blocklist.name}</strong>
                  <span className={blocklist.enabled ? "enabled-pill" : "disabled-pill"}>{blocklist.enabled ? "Enabled" : "Disabled"}</span>
                  <span>{blocklist.entry_count ?? 0}</span>
                  <span>{blocklist.last_refreshed_at ? new Date(blocklist.last_refreshed_at).toLocaleDateString() : "Not refreshed"}</span>
                </div>
              ))
            )}
          </div>
        </section>
      </div>
    </div>
  );
}
