import { Activity, AlertTriangle, CheckCircle2, Database, Gauge, ListFilter, MonitorSmartphone, RadioTower, RefreshCw, Server, ShieldX } from "lucide-react";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { api, type DashboardSummary, type Setting, type UpstreamProbe } from "../api/client";
import { DomainFavicon } from "../components/DomainFavicon";
import { ProviderLogo } from "../components/ProviderLogo";
import { ResolutionSource } from "../components/ResolutionSource";
import { Sparkline } from "../components/Sparkline";
import { StatusBadge } from "../components/StatusBadge";
import { TrafficChart } from "../components/TrafficChart";
import { findUpstreamAddress, parseUpstreamServers } from "../data/upstreams";

type DashboardProps = {
  summary: DashboardSummary | null;
  settings: Setting[];
  loading: boolean;
  onDomainSelect: (domain: string) => void;
  onViewActivity: () => void;
  onViewDevices: () => void;
  onManageUpstreams: () => void;
};

export function Dashboard({ summary, settings, loading, onDomainSelect, onViewActivity, onViewDevices, onManageUpstreams }: DashboardProps) {
  const upstreams = useMemo(() => parseUpstreamServers(settings.find((setting) => setting.key === "upstream_dns")?.value ?? "1.1.1.1,9.9.9.9"), [settings]);
  const [upstreamProbes, setUpstreamProbes] = useState<Record<string, UpstreamProbe>>({});
  const [probingUpstreams, setProbingUpstreams] = useState(false);
  const [probeCheckedAt, setProbeCheckedAt] = useState<string | null>(null);
  const [upstreamProbeError, setUpstreamProbeError] = useState("");
  const serverProbeKey = (summary?.upstream_probes ?? []).map((probe) => `${probe.address}:${probe.status}:${probe.latency_ms ?? ""}`).join("|");

  useEffect(() => {
    const probes = summary?.upstream_probes ?? [];
    setUpstreamProbes(Object.fromEntries(probes.map((probe) => [probe.address, probe])));
    setProbeCheckedAt(summary?.upstream_checked_at ?? probes[0]?.checked_at ?? null);
  }, [serverProbeKey, summary?.upstream_checked_at]);

  async function refreshUpstreamLatency() {
    if (!upstreams.length) return;
    setProbingUpstreams(true);
    setUpstreamProbeError("");
    try {
      const response = await api.probeUpstreams(upstreams);
      setUpstreamProbes(Object.fromEntries(response.items.map((probe) => [probe.address, probe])));
      setProbeCheckedAt(response.items[0]?.checked_at ?? new Date().toISOString());
    } catch {
      setUpstreamProbeError("Latency check unavailable");
    } finally {
      setProbingUpstreams(false);
    }
  }

  if (loading || !summary) {
    return <div className="loading-panel">Loading dashboard...</div>;
  }

  const bestUpstream = bestDashboardProbe(upstreams, upstreamProbes);
  const onlineUpstreams = upstreams.filter((address) => upstreamProbes[address]?.status === "online").length;
  const dnsHealth = summary.health_cards?.find((card) => card.label === "DNS");
  const networkHealthStatus = dnsHealth?.status === "critical" ? "critical" : summary.upstream_health_status;
  const networkStatusLabel = dnsHealth?.status === "critical"
    ? "DNS needs attention"
    : summary.upstream_health_status === "critical"
      ? "Upstreams unavailable"
      : summary.upstream_health_status === "degraded"
        ? "Network degraded"
        : summary.upstream_health_status === "healthy"
          ? "Network healthy"
          : "Checking network";
  const activity = summary.sparklines?.activity ?? [];
  const blocked = summary.sparklines?.blocked ?? [];
  const deviceHealth = summary.health_cards?.find((card) => card.label.toLowerCase().includes("device"));
  const activeDevices = deviceHealth?.value.match(/[\d,]+/)?.[0] ?? String(summary.top_clients.length);

  return (
    <div className="observability-dashboard">
      <section className={`dashboard-status-strip ${networkHealthStatus}`} aria-label="Network status">
        <div className="status-primary">
          <span className="status-dot" />
          <div>
            <strong>{networkStatusLabel}</strong>
            <span>{summary.network_summary?.headline ?? "Everything looks normal."}</span>
          </div>
        </div>
        <div className="status-facts">
          <span>{summary.cache.metrics_available ? <RadioTower size={15} /> : <AlertTriangle size={15} />} {summary.cache.metrics_available ? "DNS online" : "DNS status unavailable"}</span>
          <span>{summary.upstream_health_status === "healthy" ? <CheckCircle2 size={15} /> : summary.upstream_health_status === "unknown" ? <RefreshCw size={15} /> : <AlertTriangle size={15} />} {summary.upstream_health_status === "unknown" ? "Checking upstreams" : `${onlineUpstreams} of ${upstreams.length} upstreams online`}</span>
          <span><Gauge size={15} /> {bestUpstream?.latency_ms !== null && bestUpstream?.latency_ms !== undefined ? `${formatLatency(bestUpstream.latency_ms)} ms fastest` : probingUpstreams ? "Testing latency" : "Latency unavailable"}</span>
          <span><ListFilter size={15} /> {summary.enabled_blocklists} active list{summary.enabled_blocklists === 1 ? "" : "s"}</span>
        </div>
      </section>

      <div className="overview-metrics">
        <OverviewMetric label="Queries today" value={formatNumber(summary.total_queries_today)} detail="DNS requests" icon={<Activity size={18} />} sparkline={activity} />
        <OverviewMetric label="Blocked" value={formatNumber(summary.blocked_queries_today)} detail={`${summary.block_percentage.toFixed(1)}% of traffic`} tone="blocked" icon={<ShieldX size={18} />} sparkline={blocked} />
        <OverviewMetric label="Active devices" value={activeDevices} detail="Seen today" icon={<MonitorSmartphone size={18} />} />
        <OverviewMetric label="Cache hit rate" value={summary.cache.enabled ? `${summary.cache.hit_rate_today.toFixed(1)}%` : "Off"} detail={summary.cache.enabled ? summary.cache.hits_today > 0 ? `${formatNumber(summary.cache.hits_today)} avoided · ${formatLatency(summary.cache.average_cache_latency_ms)} ms avg` : "Warming up" : "Enable in Settings"} icon={<Database size={18} />} />
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

        <section className="panel service-panel upstream-dashboard-panel">
          <div className="panel-title dashboard-panel-title">
            <div>
              <h2>Upstream resolvers</h2>
              <p>Live response time · {formatNumber(summary.cache.upstream_queries_today)} calls · {formatLatency(summary.cache.average_upstream_latency_ms)} ms avg</p>
            </div>
            <div className="dashboard-upstream-actions">
              <span className={`service-state ${summary.upstream_health_status}`}><span /> {probingUpstreams ? "Testing" : summary.upstream_health_status === "healthy" ? "Healthy" : summary.upstream_health_status === "degraded" ? "Degraded" : summary.upstream_health_status === "critical" ? "Unavailable" : "Checking"}</span>
              <button className="icon-button" type="button" onClick={() => void refreshUpstreamLatency()} disabled={probingUpstreams} aria-label="Refresh dashboard upstream latency"><RefreshCw className={probingUpstreams ? "spinning" : ""} size={15} /></button>
            </div>
          </div>
          <div className="dashboard-upstream-list">
            {upstreams.map((address) => {
              const match = findUpstreamAddress(address);
              return <div className="dashboard-upstream-row" key={address}>
                <span className="dashboard-provider-logo">{match ? <ProviderLogo providerID={match.provider.id} providerName={match.provider.name} /> : <Server size={17} />}</span>
                <div><strong>{match?.provider.name ?? "Custom resolver"}</strong><span>{match?.profile.name ?? "Custom DNS"}</span><code>{address}</code></div>
                <DashboardProbeBadge probe={upstreamProbes[address]} loading={probingUpstreams && !upstreamProbes[address]} />
              </div>;
            })}
          </div>
          <div className="dashboard-upstream-footer"><span className={upstreamProbeError ? "dashboard-upstream-error" : ""}>{upstreamProbeError || (probeCheckedAt ? `Server checked ${new Date(probeCheckedAt).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })}` : "Server health check starting")}</span><button className="text-action" type="button" onClick={onManageUpstreams}>Compare providers</button></div>
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
                    <td><ResolutionSource source={query.source} upstream={query.upstream} /></td>
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

function DashboardProbeBadge({ probe, loading }: { probe?: UpstreamProbe; loading: boolean }) {
  if (!probe) return <span className="dashboard-probe-badge pending"><span />{loading ? "Testing" : "Not tested"}</span>;
  if (probe.status !== "online" || probe.latency_ms === null) return <span className="dashboard-probe-badge offline"><span />Unavailable</span>;
  return <span className={`dashboard-probe-badge ${latencyTone(probe.latency_ms)}`}><span />{formatLatency(probe.latency_ms)} ms</span>;
}

function bestDashboardProbe(addresses: string[], probes: Record<string, UpstreamProbe>) {
  return addresses.map((address) => probes[address]).filter((probe): probe is UpstreamProbe => probe?.status === "online" && probe.latency_ms !== null).sort((left, right) => (left.latency_ms ?? Infinity) - (right.latency_ms ?? Infinity))[0];
}

function formatLatency(value: number) {
  return value >= 100 ? Math.round(value).toString() : value.toFixed(value >= 10 ? 0 : 1);
}

function latencyTone(value: number) {
  if (value < 40) return "fast";
  if (value < 100) return "moderate";
  return "slow";
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
