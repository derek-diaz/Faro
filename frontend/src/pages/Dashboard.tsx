import { Activity, CheckCircle2, ListFilter, MonitorSmartphone, RadioTower, ShieldCheck, ShieldX } from "lucide-react";
import type { ReactNode } from "react";
import type { DashboardSummary, Setting } from "../api/client";
import { DomainFavicon } from "../components/DomainFavicon";
import { Sparkline } from "../components/Sparkline";
import { StatusBadge } from "../components/StatusBadge";
import { TrafficChart } from "../components/TrafficChart";

type DashboardProps = {
  summary: DashboardSummary | null;
  settings: Setting[];
  loading: boolean;
  onDomainSelect: (domain: string) => void;
  onViewActivity: () => void;
  onViewDevices: () => void;
};

export function Dashboard({ summary, settings, loading, onDomainSelect, onViewActivity, onViewDevices }: DashboardProps) {
  if (loading || !summary) {
    return <div className="loading-panel">Loading dashboard...</div>;
  }

  const upstreams = (settings.find((setting) => setting.key === "upstream_dns")?.value ?? "1.1.1.1,9.9.9.9")
    .split(",")
    .map((upstream) => upstream.trim())
    .filter(Boolean);
  const activity = summary.sparklines?.activity ?? [];
  const blocked = summary.sparklines?.blocked ?? [];
  const deviceHealth = summary.health_cards?.find((card) => card.label.toLowerCase().includes("device"));
  const activeDevices = deviceHealth?.value.match(/[\d,]+/)?.[0] ?? String(summary.top_clients.length);

  return (
    <div className="observability-dashboard">
      <section className="dashboard-status-strip" aria-label="Network status">
        <div className="status-primary">
          <span className="status-dot" />
          <div>
            <strong>Network healthy</strong>
            <span>{summary.network_summary?.headline ?? "Everything looks normal."}</span>
          </div>
        </div>
        <div className="status-facts">
          <span><RadioTower size={15} /> DNS online</span>
          <span><CheckCircle2 size={15} /> {upstreams.length} upstream{upstreams.length === 1 ? "" : "s"}</span>
          <span><ListFilter size={15} /> {summary.enabled_blocklists} active list{summary.enabled_blocklists === 1 ? "" : "s"}</span>
        </div>
      </section>

      <div className="overview-metrics">
        <OverviewMetric label="Queries today" value={formatNumber(summary.total_queries_today)} detail="DNS requests" icon={<Activity size={18} />} sparkline={activity} />
        <OverviewMetric label="Blocked" value={formatNumber(summary.blocked_queries_today)} detail={`${summary.block_percentage.toFixed(1)}% of traffic`} tone="blocked" icon={<ShieldX size={18} />} sparkline={blocked} />
        <OverviewMetric label="Active devices" value={activeDevices} detail="Seen today" icon={<MonitorSmartphone size={18} />} />
        <OverviewMetric label="Filtering" value={formatNumber(summary.blocklist_entries)} detail={`${summary.enabled_blocklists} enabled blocklists`} icon={<ShieldCheck size={18} />} />
      </div>

      <div className="dashboard-main-grid">
        <section className="panel query-volume-panel">
          <div className="panel-title dashboard-panel-title">
            <div>
              <h2>Query volume</h2>
              <p>DNS activity over the last 24 hours</p>
            </div>
            <div className="chart-legend" aria-label="Chart legend">
              <span><i className="legend-total" /> Total</span>
              <span><i className="legend-blocked" /> Blocked</span>
            </div>
          </div>
          <TrafficChart activity={activity} blocked={blocked} />
        </section>

        <section className="panel service-panel">
          <div className="panel-title dashboard-panel-title">
            <div>
              <h2>DNS service</h2>
              <p>Current configuration</p>
            </div>
            <span className="service-state"><span /> Healthy</span>
          </div>
          <div className="service-list">
            <ServiceRow label="Resolver" value="Online" />
            <ServiceRow label="Upstreams" value={upstreams.join(", ") || "Not configured"} />
            <ServiceRow label="Blocklists" value={`${summary.enabled_blocklists} enabled`} />
            <ServiceRow label="Domains filtered" value={formatNumber(summary.blocklist_entries)} />
          </div>
        </section>
      </div>

      <div className="dashboard-rank-grid">
        <RankPanel title="Top domains" items={summary.top_queried_domains} empty="No DNS activity yet." showFavicons onSelect={onDomainSelect} />
        <RankPanel title="Blocked domains" items={summary.top_blocked_domains} empty="No domains blocked today." showFavicons tone="blocked" onSelect={onDomainSelect} />
        <RankPanel title="Top devices" items={summary.top_clients} empty="No device activity yet." onViewAll={onViewDevices} />
      </div>

      <section className="panel dashboard-activity-panel">
        <div className="panel-title dashboard-panel-title">
          <div>
            <h2>Recent activity</h2>
            <p>Latest DNS requests across your network</p>
          </div>
          <button className="text-action" type="button" onClick={onViewActivity}>View all activity</button>
        </div>
        {summary.recent_activity.length === 0 ? (
          <div className="compact-empty">
            <strong>No activity yet</strong>
            <span>Point a device or router at Faro to start seeing DNS requests.</span>
          </div>
        ) : (
          <div className="dashboard-table-wrap">
            <table className="monitor-table dashboard-activity-table">
              <thead>
                <tr>
                  <th>Time</th>
                  <th>Result</th>
                  <th>Domain</th>
                  <th>Device</th>
                  <th>Type</th>
                  <th>Source</th>
                </tr>
              </thead>
              <tbody>
                {summary.recent_activity.slice(0, 8).map((query) => (
                  <tr key={`${query.timestamp}-${query.domain}-${query.client_ip}-${query.query_type}`}>
                    <td className="time-cell stacked-time"><strong>{formatTime(query.timestamp)}</strong><span>{formatDate(query.timestamp)}</span></td>
                    <td><StatusBadge value={query.action} /></td>
                    <td>
                      <button className="table-domain-link" type="button" onClick={() => onDomainSelect(query.domain)}>
                        <DomainFavicon domain={query.domain} />
                        <span>{query.domain}</span>
                      </button>
                    </td>
                    <td>{query.client_ip}</td>
                    <td><span className="query-type-chip">{query.query_type}</span></td>
                    <td className="muted-column">{query.source}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
}

function OverviewMetric({ label, value, detail, icon, tone = "default", sparkline }: { label: string; value: string; detail: string; icon: ReactNode; tone?: "default" | "blocked"; sparkline?: number[] }) {
  return (
    <article className={`overview-metric ${tone}`}>
      <div className="overview-metric-heading">
        <span>{label}</span>
        <i>{icon}</i>
      </div>
      <strong>{value}</strong>
      <div className="overview-metric-footer">
        <span>{detail}</span>
        {sparkline && <Sparkline values={sparkline} tone={tone === "blocked" ? "blocked" : "accent"} />}
      </div>
    </article>
  );
}

function ServiceRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="service-row">
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function RankPanel({ title, items, empty, showFavicons = false, tone = "default", onSelect, onViewAll }: { title: string; items: { label: string; count: number }[]; empty: string; showFavicons?: boolean; tone?: "default" | "blocked"; onSelect?: (label: string) => void; onViewAll?: () => void }) {
  const max = Math.max(...items.map((item) => item.count), 1);
  return (
    <section className={`panel compact-rank-panel ${tone}`}>
      <div className="panel-title dashboard-panel-title">
        <h2>{title}</h2>
        {onViewAll && <button className="text-action" type="button" onClick={onViewAll}>View devices</button>}
      </div>
      {items.length === 0 ? (
        <div className="compact-empty"><span>{empty}</span></div>
      ) : (
        <div className="compact-rank-list">
          {items.map((item, index) => (
            <div className="compact-rank-row" key={item.label}>
              <span className="rank-position">{index + 1}</span>
              {showFavicons && <DomainFavicon domain={item.label} />}
              {onSelect ? (
                <button className="link-button" type="button" onClick={() => onSelect(item.label)}>{item.label}</button>
              ) : (
                <strong>{item.label}</strong>
              )}
              <span className="rank-count">{formatNumber(item.count)}</span>
              <span className="rank-meter"><i style={{ width: `${Math.max(5, (item.count / max) * 100)}%` }} /></span>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}

function formatNumber(value: number) {
  return new Intl.NumberFormat().format(value);
}

function formatTime(timestamp: string) {
  return new Date(timestamp).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

function formatDate(timestamp: string) {
  const date = new Date(timestamp);
  const today = new Date();
  return date.toDateString() === today.toDateString() ? "Today" : date.toLocaleDateString([], { month: "short", day: "numeric" });
}
