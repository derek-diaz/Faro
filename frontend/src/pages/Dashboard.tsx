import { Activity, AlertTriangle, ArrowRight, CheckCircle2, Database, Gauge, Globe2, ListFilter, MonitorSmartphone, RadioTower, RefreshCw, Server, ShieldX, Sparkles } from "lucide-react";
import { tableFeatures, useTable } from "@tanstack/react-table";
import type { ColumnDef } from "@tanstack/react-table";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { api, type DashboardStory, type DashboardSummary, type DNSQuery, type EncryptedUpstreamEndpoint, type Setting, type UpstreamProbe, type WhatsNewItem } from "../api/client";
import { DomainFavicon } from "../components/DomainFavicon";
import { LoadingState } from "../components/LoadingState";
import { ProviderLogo } from "../components/ProviderLogo";
import { ResolutionSource } from "../components/ResolutionSource";
import { Sparkline } from "../components/Sparkline";
import { StatusBadge } from "../components/StatusBadge";
import { TrafficChart } from "../components/TrafficChart";
import { findUpstreamAddress, parseUpstreamServers } from "../data/upstreams";
import { formatDate, formatTime } from "../utils/dateFormatting";
import { formatLatency, formatNumber, latencyTone } from "../utils/formatting";

type DashboardProps = {
  readonly summary: DashboardSummary | null;
  readonly settings: Setting[];
  readonly loading: boolean;
  readonly onDomainSelect: (domain: string) => void;
  readonly onDeviceSelect: (clientIP: string) => void;
  readonly onViewActivity: () => void;
  readonly onViewDevices: () => void;
  readonly onViewBlocklists: () => void;
  readonly onViewLocalDns: () => void;
  readonly onManageUpstreams: () => void;
};

const dashboardActivityFeatures = tableFeatures({});

export function Dashboard({ summary, settings, loading, onDomainSelect, onDeviceSelect, onViewActivity, onViewDevices, onViewBlocklists, onViewLocalDns, onManageUpstreams }: DashboardProps) {
  const upstreams = useMemo(() => parseUpstreamServers(settings.find((setting) => setting.key === "upstream_dns")?.value ?? "1.1.1.1,9.9.9.9"), [settings]);
  const configuredTransport = settings.find((setting) => setting.key === "upstream_transport")?.value === "encrypted" ? "encrypted" : "standard";
  const [upstreamProbes, setUpstreamProbes] = useState<Record<string, UpstreamProbe>>({});
  const [encryptedEndpoints, setEncryptedEndpoints] = useState<EncryptedUpstreamEndpoint[]>([]);
  const [probingUpstreams, setProbingUpstreams] = useState(false);
  const [probeCheckedAt, setProbeCheckedAt] = useState<string | null>(null);
  const [upstreamProbeError, setUpstreamProbeError] = useState("");
  const serverProbeKey = (summary?.upstream_probes ?? []).map((probe) => `${probe.address}:${probe.status}:${probe.latency_ms ?? ""}`).join("|");

  useEffect(() => {
    const probes = summary?.upstream_probes ?? [];
    setUpstreamProbes(Object.fromEntries(probes.map((probe) => [probe.address, probe])));
    setProbeCheckedAt(summary?.upstream_checked_at ?? probes[0]?.checked_at ?? null);
  }, [serverProbeKey, summary?.upstream_checked_at]);

  useEffect(() => {
    if (configuredTransport !== "encrypted") {
      setEncryptedEndpoints([]);
      return;
    }
    let cancelled = false;
    api.upstreamCatalog()
      .then((response) => {
        if (!cancelled) setEncryptedEndpoints(response.encrypted_endpoints);
      })
      .catch(() => {
        if (!cancelled) setEncryptedEndpoints([]);
      });
    return () => {
      cancelled = true;
    };
  }, [configuredTransport]);

  async function refreshUpstreamLatency() {
    if (!upstreams.length) return;
    setProbingUpstreams(true);
    setUpstreamProbeError("");
    try {
      const response = await api.probeUpstreams(upstreams, configuredTransport);
      setUpstreamProbes(Object.fromEntries(response.items.map((probe) => [probe.address, probe])));
      setProbeCheckedAt(response.items[0]?.checked_at ?? new Date().toISOString());
    } catch {
      setUpstreamProbeError("Latency check unavailable");
    } finally {
      setProbingUpstreams(false);
    }
  }

  if (loading || !summary) {
    return <LoadingState className="dashboard-loading" title="Loading dashboard" description="Gathering live network health and DNS traffic…" />;
  }

  const selectedEncryptedEndpoints = encryptedEndpoints.filter((endpoint) => endpoint.bootstrap_ips.some((address) => upstreams.includes(address)));
  const bestUpstream = bestDashboardProbe(upstreams, upstreamProbes);
  const groupedEncrypted = configuredTransport === "encrypted" && selectedEncryptedEndpoints.length > 0;
  const upstreamCount = groupedEncrypted ? selectedEncryptedEndpoints.length : upstreams.length;
  const onlineUpstreams = groupedEncrypted
    ? selectedEncryptedEndpoints.filter((endpoint) => endpoint.bootstrap_ips.some((address) => upstreamProbes[address]?.status === "online")).length
    : upstreams.filter((address) => upstreamProbes[address]?.status === "online").length;
  const upstreamUnit = groupedEncrypted ? pluralize("encrypted provider", upstreamCount) : pluralize("upstream", upstreamCount);
  const dnsHealth = summary.health_cards?.find((card) => card.label === "DNS");
  const networkHealthStatus = dnsHealth?.status === "critical" ? "critical" : summary.upstream_health_status;
  const networkStatusLabel = dashboardNetworkStatusLabel(dnsHealth?.status, summary.upstream_health_status);
  const showNetworkStatus = networkHealthStatus === "degraded" || networkHealthStatus === "critical";
  const activity = summary.sparklines?.activity ?? [];
  const blocked = summary.sparklines?.blocked ?? [];
  const deviceHealth = summary.health_cards?.find((card) => card.label.toLowerCase().includes("device"));
  const activeDevices = deviceHealth?.value.match(/[\d,]+/)?.[0] ?? String(summary.top_clients.length);

  return (
    <div className="observability-dashboard">
      {showNetworkStatus && <DashboardStatusStrip summary={summary} networkHealthStatus={networkHealthStatus} networkStatusLabel={networkStatusLabel} onlineUpstreams={onlineUpstreams} upstreamCount={upstreamCount} upstreamUnit={upstreamUnit} bestUpstream={bestUpstream} probingUpstreams={probingUpstreams} />}
      <DashboardDigest summary={summary} onDomainSelect={onDomainSelect} onDeviceSelect={onDeviceSelect} onViewBlocklists={onViewBlocklists} onViewLocalDns={onViewLocalDns} />
      <DashboardMetrics summary={summary} activity={activity} blocked={blocked} activeDevices={activeDevices} />

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
        <DashboardUpstreamPanel summary={summary} groupedEncrypted={groupedEncrypted} selectedEncryptedEndpoints={selectedEncryptedEndpoints} upstreams={upstreams} upstreamProbes={upstreamProbes} probingUpstreams={probingUpstreams} upstreamProbeError={upstreamProbeError} probeCheckedAt={probeCheckedAt} onRefresh={refreshUpstreamLatency} onManage={onManageUpstreams} />
      </div>

      <div className="dashboard-rank-grid">
        <RankPanel title="Top domains" items={summary.top_queried_domains} empty="No DNS activity yet." showFavicons onSelect={onDomainSelect} />
        <RankPanel title="Blocked domains" items={summary.top_blocked_domains} empty="No domains blocked today." showFavicons tone="blocked" onSelect={onDomainSelect} />
        <RankPanel title="Top devices" items={summary.top_clients} empty="No device activity yet." onViewAll={onViewDevices} />
      </div>

      <DashboardRecentActivity summary={summary} onDomainSelect={onDomainSelect} onViewActivity={onViewActivity} />
    </div>
  );
}

type DashboardDigestProps = {
  readonly summary: DashboardSummary;
  readonly onDomainSelect: (domain: string) => void;
  readonly onDeviceSelect: (clientIP: string) => void;
  readonly onViewBlocklists: () => void;
  readonly onViewLocalDns: () => void;
};

function DashboardDigest({ summary, onDomainSelect, onDeviceSelect, onViewBlocklists, onViewLocalDns }: DashboardDigestProps) {
  const groups = [
    { id: "devices", label: "Devices", icon: <MonitorSmartphone size={15} />, items: summary.whats_new?.devices ?? [], onSelect: onDeviceSelect },
    { id: "domains", label: "Domains", icon: <Globe2 size={15} />, items: summary.whats_new?.domains ?? [], onSelect: onDomainSelect },
    { id: "blocklists", label: "Blocklists", icon: <ListFilter size={15} />, items: summary.whats_new?.blocklists ?? [], onSelect: () => onViewBlocklists() },
    { id: "local-records", label: "Local DNS", icon: <Database size={15} />, items: summary.whats_new?.local_records ?? [], onSelect: () => onViewLocalDns() }
  ].filter((group) => group.items.length > 0);
  const changeCount = groups.reduce((total, group) => total + group.items.length, 0);
  const stories = (summary.stories ?? []).slice(0, 4);

  return (
    <section className="panel dashboard-digest" aria-labelledby="dashboard-digest-title">
      <header className="dashboard-digest-heading">
        <div>
          <span className="dashboard-digest-mark"><Sparkles size={17} /></span>
          <div>
            <h2 id="dashboard-digest-title">What changed today</h2>
            <p>Health, activity, and first-time observations in one brief.</p>
          </div>
        </div>
        <span className={`dashboard-change-count ${changeCount > 0 ? "active" : ""}`}>{changeCount > 0 ? `${changeCount} new` : "No new items"}</span>
      </header>

      <div className="dashboard-digest-grid">
        <div className="dashboard-story-list" aria-label="Today's network brief">
          {stories.map((item, index) => <DashboardStoryRow item={item} key={`${item.title}-${index}`} />)}
        </div>

        <div className="dashboard-new-today">
          <div className="dashboard-new-heading"><strong>New today</strong><span>First seen since midnight</span></div>
          {groups.length > 0 ? (
            <div className="dashboard-new-groups">
              {groups.map((group) => (
                <section className="dashboard-new-group" key={group.id} aria-label={`New ${group.label.toLowerCase()}`}>
                  <div className="dashboard-new-group-label">{group.icon}<span>{group.label}</span><strong>{group.items.length}</strong></div>
                  <div className="dashboard-new-items">
                    {group.items.slice(0, 3).map((item) => <DashboardNewItem item={item} key={`${group.id}-${item.label}`} onSelect={() => group.onSelect(item.label)} />)}
                    {group.items.length > 3 && <span className="dashboard-new-more">+{group.items.length - 3} more</span>}
                  </div>
                </section>
              ))}
            </div>
          ) : (
            <div className="dashboard-new-empty"><CheckCircle2 size={18} /><span><strong>Nothing unfamiliar yet</strong><small>New devices, domains, lists, and local records will appear here.</small></span></div>
          )}
        </div>
      </div>
    </section>
  );
}

function DashboardStoryRow({ item }: { readonly item: DashboardStory }) {
  const icon = item.tone === "critical" || item.tone === "warning"
    ? <AlertTriangle size={16} />
    : item.tone === "success" ? <CheckCircle2 size={16} /> : <Activity size={16} />;
  return <article className={`dashboard-story-row ${item.tone}`}><span>{icon}</span><div><strong>{item.title}</strong><p>{item.body}</p></div></article>;
}

function DashboardNewItem({ item, onSelect }: { readonly item: WhatsNewItem; readonly onSelect: () => void }) {
  return <button type="button" onClick={onSelect}><span><strong>{item.label}</strong>{item.subtitle && <small>{item.subtitle}</small>}</span><ArrowRight size={14} /></button>;
}

function DashboardStatusStrip({ summary, networkHealthStatus, networkStatusLabel, onlineUpstreams, upstreamCount, upstreamUnit, bestUpstream, probingUpstreams }: { readonly summary: DashboardSummary; readonly networkHealthStatus: string; readonly networkStatusLabel: string; readonly onlineUpstreams: number; readonly upstreamCount: number; readonly upstreamUnit: string; readonly bestUpstream?: UpstreamProbe; readonly probingUpstreams: boolean }) {
  const dnsStatus = summary.cache.metrics_available ? "DNS online" : "DNS status unavailable";
  const upstreamStatus = summary.upstream_health_status === "unknown" ? "Checking upstreams" : `${onlineUpstreams} of ${upstreamCount} ${upstreamUnit} online`;
  return <section className={`dashboard-status-strip ${networkHealthStatus}`} aria-label="Network status"><div className="status-primary"><span className="status-dot" /><div><strong>{networkStatusLabel}</strong><span>{summary.network_summary?.headline ?? "Everything looks normal."}</span></div></div><div className="status-facts"><span>{summary.cache.metrics_available ? <RadioTower size={15} /> : <AlertTriangle size={15} />} {dnsStatus}</span><span>{upstreamHealthIcon(summary.upstream_health_status)} {upstreamStatus}</span><span><Gauge size={15} /> {dashboardLatencyLabel(bestUpstream, probingUpstreams)}</span><span><ListFilter size={15} /> {pluralize("active list", summary.enabled_blocklists)}</span></div></section>;
}

function DashboardMetrics({ summary, activity, blocked, activeDevices }: { readonly summary: DashboardSummary; readonly activity: number[]; readonly blocked: number[]; readonly activeDevices: string }) {
  return <div className="overview-metrics"><OverviewMetric label="Queries today" value={formatNumber(summary.total_queries_today)} detail="DNS requests" icon={<Activity size={18} />} sparkline={activity} /><OverviewMetric label="Blocked" value={formatNumber(summary.blocked_queries_today)} detail={`${summary.block_percentage.toFixed(1)}% of traffic`} tone="blocked" icon={<ShieldX size={18} />} sparkline={blocked} /><OverviewMetric label="Active devices" value={activeDevices} detail="Seen today" icon={<MonitorSmartphone size={18} />} /><OverviewMetric label="Cache hit rate" value={summary.cache.enabled ? `${summary.cache.hit_rate_today.toFixed(1)}%` : "Off"} detail={cacheDetail(summary.cache)} icon={<Database size={18} />} /></div>;
}

function DashboardUpstreamPanel({ summary, groupedEncrypted, selectedEncryptedEndpoints, upstreams, upstreamProbes, probingUpstreams, upstreamProbeError, probeCheckedAt, onRefresh, onManage }: { readonly summary: DashboardSummary; readonly groupedEncrypted: boolean; readonly selectedEncryptedEndpoints: EncryptedUpstreamEndpoint[]; readonly upstreams: string[]; readonly upstreamProbes: Record<string, UpstreamProbe>; readonly probingUpstreams: boolean; readonly upstreamProbeError: string; readonly probeCheckedAt: string | null; readonly onRefresh: () => Promise<void>; readonly onManage: () => void }) {
  return <section className="panel service-panel upstream-dashboard-panel"><div className="panel-title dashboard-panel-title"><div><h2>Upstream resolvers</h2><p>Live response time · {formatNumber(summary.cache.upstream_queries_today)} calls · {formatLatency(summary.cache.average_upstream_latency_ms)} ms avg</p></div><div className="dashboard-upstream-actions"><span className={`service-state ${summary.upstream_health_status}`}><span /> {upstreamStatusLabel(summary.upstream_health_status, probingUpstreams)}</span><button className="icon-button" type="button" onClick={() => void onRefresh()} disabled={probingUpstreams} aria-label="Refresh dashboard upstream latency"><RefreshCw className={probingUpstreams ? "spinning" : ""} size={15} /></button></div></div><DashboardUpstreamRows groupedEncrypted={groupedEncrypted} selectedEncryptedEndpoints={selectedEncryptedEndpoints} upstreams={upstreams} upstreamProbes={upstreamProbes} probingUpstreams={probingUpstreams} /><div className="dashboard-upstream-footer"><span className={upstreamProbeError ? "dashboard-upstream-error" : ""}>{upstreamProbeError || checkedLabel(probeCheckedAt)}</span><button className="text-action" type="button" onClick={onManage}>Compare providers</button></div></section>;
}

function DashboardUpstreamRows({ groupedEncrypted, selectedEncryptedEndpoints, upstreams, upstreamProbes, probingUpstreams }: { readonly groupedEncrypted: boolean; readonly selectedEncryptedEndpoints: EncryptedUpstreamEndpoint[]; readonly upstreams: string[]; readonly upstreamProbes: Record<string, UpstreamProbe>; readonly probingUpstreams: boolean }) {
  if (groupedEncrypted) return <div className="dashboard-upstream-list">{selectedEncryptedEndpoints.map((endpoint) => <DashboardEncryptedRow key={endpoint.url} endpoint={endpoint} upstreams={upstreams} upstreamProbes={upstreamProbes} probing={probingUpstreams} />)}</div>;
  return <div className="dashboard-upstream-list">{upstreams.map((address) => <DashboardStandardRow key={address} address={address} probe={upstreamProbes[address]} probing={probingUpstreams} />)}</div>;
}

function DashboardEncryptedRow({ endpoint, upstreams, upstreamProbes, probing }: { readonly endpoint: EncryptedUpstreamEndpoint; readonly upstreams: string[]; readonly upstreamProbes: Record<string, UpstreamProbe>; readonly probing: boolean }) {
  const selectedAddress = endpoint.bootstrap_ips.find((address) => upstreams.includes(address)) ?? endpoint.bootstrap_ips[0];
  const match = findUpstreamAddress(selectedAddress);
  const probe = bestDashboardProbe(endpoint.bootstrap_ips, upstreamProbes);
  return <div className="dashboard-upstream-row"><span className="dashboard-provider-logo">{match ? <ProviderLogo providerID={match.provider.id} providerName={match.provider.name} /> : <Server size={17} />}</span><div><strong>{match?.provider.name ?? endpoint.name}</strong><span>{match?.profile.name ?? "Encrypted DNS"}</span><code title={endpoint.url}>{endpoint.url}</code></div><DashboardProbeBadge probe={probe} loading={probing && !probe} /></div>;
}

function DashboardStandardRow({ address, probe, probing }: { readonly address: string; readonly probe?: UpstreamProbe; readonly probing: boolean }) {
  const match = findUpstreamAddress(address);
  return <div className="dashboard-upstream-row"><span className="dashboard-provider-logo">{match ? <ProviderLogo providerID={match.provider.id} providerName={match.provider.name} /> : <Server size={17} />}</span><div><strong>{match?.provider.name ?? "Custom resolver"}</strong><span>{match?.profile.name ?? "Custom DNS"}</span><code>{address}</code></div><DashboardProbeBadge probe={probe} loading={probing && !probe} /></div>;
}

function DashboardRecentActivity({ summary, onDomainSelect, onViewActivity }: { readonly summary: DashboardSummary; readonly onDomainSelect: (domain: string) => void; readonly onViewActivity: () => void }) {
  const recentActivity = useMemo(() => summary.recent_activity.slice(0, 8), [summary.recent_activity]);
  const activityColumns = useMemo<ColumnDef<typeof dashboardActivityFeatures, DNSQuery>[]>(() => [
    {
      accessorKey: "timestamp",
      header: "Time",
      cell: ({ row }) => <><strong>{formatTime(row.original.timestamp)}</strong><span>{formatDate(row.original.timestamp)}</span></>
    },
    {
      id: "result",
      header: "Result",
      cell: ({ row }) => <StatusBadge value={row.original.action} />
    },
    {
      id: "domain",
      header: "Domain",
      cell: ({ row }) => <button className="table-domain-link" type="button" onClick={() => onDomainSelect(row.original.domain)}><DomainFavicon domain={row.original.domain} /><span>{row.original.domain}</span></button>
    },
    {
      id: "device",
      header: "Device",
      cell: ({ row }) => row.original.client_ip
    },
    {
      id: "type",
      header: "Type",
      cell: ({ row }) => <span className="query-type-chip">{row.original.query_type}</span>
    },
    {
      id: "source",
      header: "Source",
      cell: ({ row }) => <ResolutionSource source={row.original.source} upstream={row.original.upstream} />
    }
  ], [onDomainSelect]);
  const activityTable = useTable({
    features: dashboardActivityFeatures,
    data: recentActivity,
    columns: activityColumns,
    getRowId: (query, index) => `${query.id ?? "activity"}-${query.timestamp}-${query.domain}-${query.client_ip}-${index}`
  });

  return <section className="panel dashboard-activity-panel"><div className="panel-title dashboard-panel-title"><div><h2>Recent activity</h2><p>Latest DNS requests across your network</p></div><button className="text-action" type="button" onClick={onViewActivity}>View all activity</button></div>{recentActivity.length === 0 ? <div className="compact-empty"><strong>No activity yet</strong><span>Point a device or router at Faro to start seeing DNS requests.</span></div> : <div className="dashboard-table-wrap"><table className="monitor-table dashboard-activity-table"><thead>{activityTable.getHeaderGroups().map((headerGroup) => <tr key={headerGroup.id}>{headerGroup.headers.map((header) => <th key={header.id}>{header.isPlaceholder ? null : <activityTable.FlexRender header={header} />}</th>)}</tr>)}</thead><tbody>{activityTable.getRowModel().rows.map((row) => <tr key={row.id}>{row.getAllCells().map((cell) => <td key={cell.id} className={dashboardActivityCellClass(cell.column.id)}><activityTable.FlexRender cell={cell} /></td>)}</tr>)}</tbody></table></div>}</section>;
}

function dashboardActivityCellClass(columnID: string) {
  return columnID === "timestamp" ? "time-cell stacked-time" : undefined;
}

function OverviewMetric({ label, value, detail, icon, tone = "default", sparkline }: { readonly label: string; readonly value: string; readonly detail: string; readonly icon: ReactNode; readonly tone?: "default" | "blocked"; readonly sparkline?: number[] }) {
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

function DashboardProbeBadge({ probe, loading }: { readonly probe?: UpstreamProbe; readonly loading: boolean }) {
  if (!probe) return <span className="dashboard-probe-badge pending"><span />{loading ? "Testing" : "Not tested"}</span>;
  if (probe.status !== "online" || probe.latency_ms === null) return <span className="dashboard-probe-badge offline"><span />Unavailable</span>;
  return <span className={`dashboard-probe-badge ${latencyTone(probe.latency_ms)}`}><span />{formatLatency(probe.latency_ms)} ms</span>;
}

function bestDashboardProbe(addresses: string[], probes: Record<string, UpstreamProbe>) {
  return addresses.map((address) => probes[address]).filter((probe): probe is UpstreamProbe => probe?.status === "online" && probe.latency_ms !== null).sort((left, right) => (left.latency_ms ?? Infinity) - (right.latency_ms ?? Infinity))[0];
}

function RankPanel({ title, items, empty, showFavicons = false, tone = "default", onSelect, onViewAll }: { readonly title: string; readonly items: { readonly label: string; readonly count: number }[]; readonly empty: string; readonly showFavicons?: boolean; readonly tone?: "default" | "blocked"; readonly onSelect?: (label: string) => void; readonly onViewAll?: () => void }) {
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

function pluralize(label: string, count: number) {
  return `${label}${count === 1 ? "" : "s"}`;
}

function dashboardNetworkStatusLabel(dnsStatus: string | undefined, upstreamStatus: DashboardSummary["upstream_health_status"]) {
  if (dnsStatus === "critical") return "DNS needs attention";
  if (upstreamStatus === "critical") return "Upstreams unavailable";
  if (upstreamStatus === "degraded") return "Network degraded";
  if (upstreamStatus === "healthy") return "Network healthy";
  return "Checking network";
}

function upstreamHealthIcon(status: DashboardSummary["upstream_health_status"]) {
  if (status === "healthy") return <CheckCircle2 size={15} />;
  if (status === "unknown") return <RefreshCw size={15} />;
  return <AlertTriangle size={15} />;
}

function dashboardLatencyLabel(probe: UpstreamProbe | undefined, probing: boolean) {
  if (probe?.latency_ms !== null && probe?.latency_ms !== undefined) return `${formatLatency(probe.latency_ms)} ms fastest`;
  return probing ? "Testing latency" : "Latency unavailable";
}

function cacheDetail(cache: DashboardSummary["cache"]) {
  if (!cache.enabled) return "Enable in Settings";
  if (cache.hits_today === 0) return "Warming up";
  return `${formatNumber(cache.hits_today)} avoided · ${formatLatency(cache.average_cache_latency_ms)} ms avg`;
}

function checkedLabel(value: string | null) {
  return value ? `Server checked ${formatTime(value)}` : "Server health check starting";
}

function upstreamStatusLabel(status: DashboardSummary["upstream_health_status"], probing: boolean) {
  if (probing) return "Testing";
  if (status === "healthy") return "Healthy";
  if (status === "degraded") return "Degraded";
  if (status === "critical") return "Unavailable";
  return "Checking";
}
