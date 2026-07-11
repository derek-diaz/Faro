import {
  Activity,
  Car,
  ChevronRight,
  Clock3,
  Gauge,
  Globe2,
  Edit3,
  HardDrive,
  History,
  Laptop,
  LayoutDashboard,
  MonitorSmartphone,
  Search,
  Server,
  ShieldCheck,
  Smartphone,
  Tv
} from "lucide-react";
import { useEffect, useMemo, useState, type FormEvent } from "react";
import { api, type DeviceReplay as DeviceReplayData, type DeviceSummary, type DNSQuery, type ReplayBucket } from "../api/client";
import { DeviceReplay } from "../components/DeviceReplay";
import { DomainFavicon } from "../components/DomainFavicon";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import { TrafficChart } from "../components/TrafficChart";

type DevicesProps = {
  devices: DeviceSummary[];
  refresh: () => Promise<void>;
  selectedClientIP: string | null;
  onSelectClient: (clientIP: string | null) => void;
  onDomainSelect: (domain: string) => void;
};

type DeviceView = "overview" | "replay";

export function Devices({ devices, refresh, selectedClientIP, onSelectClient, onDomainSelect }: DevicesProps) {
  const [detail, setDetail] = useState<DeviceSummary | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState("");
  const [form, setForm] = useState({ name: "", location: "", notes: "" });
  const [editing, setEditing] = useState(false);
  const [view, setView] = useState<DeviceView>("overview");
  const [search, setSearch] = useState("");
  const mostActiveDevice = useMemo(() => devices.reduce<DeviceSummary | null>((current, device) => {
    if (!current || device.total_queries_today > current.total_queries_today) return device;
    return current;
  }, null), [devices]);

  useEffect(() => {
    if (!mostActiveDevice) return;
    if (!selectedClientIP || !devices.some((device) => device.client_ip === selectedClientIP)) {
      onSelectClient(mostActiveDevice.client_ip);
    }
  }, [devices, mostActiveDevice, onSelectClient, selectedClientIP]);

  useEffect(() => {
    if (!selectedClientIP) {
      setDetail(null);
      setEditing(false);
      setView("overview");
      return;
    }
    let cancelled = false;
    setDetail(null);
    setDetailLoading(true);
    setDetailError("");
    setEditing(false);
    setView("overview");
    api.device(selectedClientIP)
      .then((nextDetail) => {
        if (!cancelled) {
          setDetail(nextDetail);
          setForm({ name: nextDetail.name || "", location: nextDetail.location ?? "", notes: nextDetail.notes ?? "" });
        }
      })
      .catch((caught: unknown) => {
        if (!cancelled) setDetailError(caught instanceof Error ? caught.message : "Failed to load device details.");
      })
      .finally(() => {
        if (!cancelled) setDetailLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [selectedClientIP]);

  const filteredDevices = useMemo(() => {
    const term = search.trim().toLowerCase();
    if (!term) return devices;
    return devices.filter((device) => [device.name, device.client_ip, device.device_type, device.location, device.profile]
      .some((value) => value?.toLowerCase().includes(term)));
  }, [devices, search]);

  const totals = useMemo(() => {
    const requests = devices.reduce((sum, device) => sum + device.total_queries_today, 0);
    const blocked = devices.reduce((sum, device) => sum + device.blocked_queries_today, 0);
    return {
      active: devices.filter((device) => device.total_queries_today > 0).length,
      requests,
      blocked,
      blockedRate: requests > 0 ? (blocked / requests) * 100 : 0
    };
  }, [devices]);

  async function saveAlias(event: FormEvent) {
    event.preventDefault();
    if (!selectedClientIP) return;
    await api.updateDeviceAlias(selectedClientIP, form);
    setEditing(false);
    await refresh();
    setDetail(await api.device(selectedClientIP));
  }

  if (devices.length === 0) {
    return <EmptyState title="No devices yet" body="Point a device or router at Faro to start seeing clients, names, blocked requests, and top domains." />;
  }

  return (
    <div className="devices-page">
      <section className="device-summary-strip" aria-label="Device activity summary">
        <DeviceSummaryMetric icon={<MonitorSmartphone size={18} />} label="Observed devices" value={devices.length} detail={`${totals.active} active today`} />
        <DeviceSummaryMetric icon={<Activity size={18} />} label="Requests today" value={totals.requests.toLocaleString()} detail="Across all devices" />
        <DeviceSummaryMetric icon={<ShieldCheck size={18} />} label="Blocked today" value={totals.blocked.toLocaleString()} detail={`${totals.blockedRate.toFixed(1)}% of requests`} tone={totals.blocked > 0 ? "blocked" : "default"} />
        <DeviceSummaryMetric icon={<Clock3 size={18} />} label="Most active" value={mostActiveDevice?.name || mostActiveDevice?.client_ip || "None"} detail={`${mostActiveDevice?.total_queries_today.toLocaleString() ?? "0"} requests today`} compact />
      </section>

      <section className="panel device-inventory-panel">
        <div className="device-inventory-header">
          <div>
            <h2>Device inventory</h2>
            <p>Select a device to inspect its domains, activity, and history.</p>
          </div>
          <label className="device-search">
            <span className="sr-only">Search devices</span>
            <Search size={16} />
            <input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Search devices" />
            <kbd>{filteredDevices.length}</kbd>
          </label>
        </div>

        <div className="device-table">
          <div className="device-table-header" aria-hidden="true">
            <span>Device</span><span>Requests today</span><span>Blocked</span><span>Last seen</span><span>Profile</span><span />
          </div>
          {filteredDevices.map((device) => (
            <button
              className={selectedClientIP === device.client_ip ? "device-table-row active" : "device-table-row"}
              key={device.client_ip}
              type="button"
              onClick={() => onSelectClient(device.client_ip)}
              aria-pressed={selectedClientIP === device.client_ip}
            >
              <span className="device-table-identity">
                <span className="device-icon">{deviceTypeIcon(device.device_type)}</span>
                <span className="device-main">
                  <strong>{device.name || device.client_ip}</strong>
                  <small>{device.device_type} <i /> {device.name ? device.client_ip : "Unnamed device"}{device.location ? ` · ${device.location}` : ""}</small>
                </span>
              </span>
              <span className="device-table-value"><small>Requests today</small><strong>{device.total_queries_today.toLocaleString()}</strong></span>
              <span className={`device-table-value ${device.blocked_queries_today > 0 ? "blocked" : ""}`}><small>Blocked</small><strong>{device.blocked_queries_today.toLocaleString()} <em>{device.block_percentage.toFixed(1)}%</em></strong></span>
              <span className="device-table-value"><small>Last seen</small><strong>{formatLastSeen(device.last_seen)}</strong></span>
              <span className="device-profile-badge">{device.profile}</span>
              <ChevronRight className="device-row-arrow" size={17} />
            </button>
          ))}
        </div>

        {filteredDevices.length === 0 && (
          <div className="device-filter-empty"><Search size={20} /><strong>No matching devices</strong><span>Try a name, IP address, device type, or location.</span></div>
        )}
      </section>

      <section className={`panel device-detail-panel ${view === "replay" ? "replay-active" : ""}`}>
        {detailLoading && <div className="device-detail-loading">Loading device details...</div>}
        {detailError && <div className="device-detail-error">{detailError}</div>}
        {!detailLoading && detail && (
          <>
            <div className="device-detail-header">
              <div className="device-detail-identity">
                <span className="device-detail-icon">{deviceTypeIcon(detail.device_type)}</span>
                <div>
                  <div className="device-detail-context"><span className="device-profile-badge">{detail.profile} profile</span><span>{detail.client_ip}</span></div>
                  <h2>{detail.name || detail.client_ip}</h2>
                  <p>{detail.device_type}{detail.location ? ` · ${detail.location}` : detail.name ? " · Friendly name configured" : " · Add a friendly name to recognize this device"}</p>
                </div>
              </div>
              {view === "overview" && <button className="secondary device-edit-button" type="button" onClick={() => setEditing((value) => !value)}><Edit3 size={16} /><span>{editing ? "Cancel" : "Edit device"}</span></button>}
            </div>

            <div className="device-view-tabs" role="tablist" aria-label="Device views">
              <button className={view === "overview" ? "active" : ""} type="button" role="tab" aria-selected={view === "overview"} onClick={() => setView("overview")}><LayoutDashboard size={16} /><span>Overview</span></button>
              <button className={view === "replay" ? "active" : ""} type="button" role="tab" aria-selected={view === "replay"} onClick={() => { setEditing(false); setView("replay"); }}><History size={16} /><span>Activity replay</span></button>
            </div>

            {view === "overview" ? (
              <DeviceOverview detail={detail} form={form} setForm={setForm} editing={editing} saveAlias={saveAlias} onDomainSelect={onDomainSelect} onOpenReplay={() => setView("replay")} />
            ) : (
              <DeviceReplay clientIP={detail.client_ip} deviceName={detail.name || detail.client_ip} onDomainSelect={onDomainSelect} />
            )}
          </>
        )}
      </section>
    </div>
  );
}

function DeviceSummaryMetric({ icon, label, value, detail, tone = "default", compact = false }: {
  icon: React.ReactNode;
  label: string;
  value: string | number;
  detail: string;
  tone?: "default" | "blocked";
  compact?: boolean;
}) {
  return <div className={`device-summary-metric ${tone} ${compact ? "compact" : ""}`}><span className="device-summary-icon">{icon}</span><span><small>{label}</small><strong>{value}</strong><em>{detail}</em></span></div>;
}

function DeviceOverview({ detail, form, setForm, editing, saveAlias, onDomainSelect, onOpenReplay }: {
  detail: DeviceSummary;
  form: { name: string; location: string; notes: string };
  setForm: (form: { name: string; location: string; notes: string }) => void;
  editing: boolean;
  saveAlias: (event: FormEvent) => Promise<void>;
  onDomainSelect: (domain: string) => void;
  onOpenReplay: () => void;
}) {
  const [traffic, setTraffic] = useState<DeviceReplayData | null>(null);

  useEffect(() => {
    let cancelled = false;
    setTraffic(null);
    api.deviceReplay(detail.client_ip, "24h").then((nextTraffic) => {
      if (!cancelled) setTraffic(nextTraffic);
    }).catch(() => {
      if (!cancelled) setTraffic(null);
    });
    return () => {
      cancelled = true;
    };
  }, [detail.client_ip]);

  const chart = useMemo(() => overviewChartSeries(traffic?.buckets ?? []), [traffic?.buckets]);
  const recentActivity = useMemo(() => groupOverviewQueries(detail.recent_activity ?? []).slice(0, 7), [detail.recent_activity]);
  const topDomain = detail.top_domains[0];
  const primarySource = traffic?.sources[0];
  const story = overviewStory(detail, topDomain?.label);
  const frequency = overviewFrequency(traffic?.queries_per_minute);

  return (
    <div className="device-overview-dashboard">
      {editing && (
        <form className="alias-form" onSubmit={(event) => void saveAlias(event)}>
          <label>Friendly name<input value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} placeholder="Living room TV" /></label>
          <label>Location<input value={form.location} onChange={(event) => setForm({ ...form, location: event.target.value })} placeholder="Living room" /></label>
          <label>Notes<input value={form.notes} onChange={(event) => setForm({ ...form, notes: event.target.value })} placeholder="Optional notes" /></label>
          <button type="submit">Save device</button>
        </form>
      )}

      <section className="device-overview-story">
        <span className={`device-story-icon ${detail.blocked_queries_today > 0 ? "blocked" : "healthy"}`}><ShieldCheck size={21} /></span>
        <div><small>Today at a glance</small><h3>{story.headline}</h3><p>{story.detail}</p></div>
        <button className="secondary" type="button" onClick={onOpenReplay}><History size={16} /><span>Open replay</span></button>
      </section>

      <div className="device-overview-kpis">
        <OverviewKpi icon={<Activity size={17} />} label="Queries today" value={detail.total_queries_today.toLocaleString()} detail="DNS requests" />
        <OverviewKpi icon={<ShieldCheck size={17} />} label="Blocked" value={detail.blocked_queries_today.toLocaleString()} detail={`${detail.block_percentage.toFixed(1)}% of requests`} tone={detail.blocked_queries_today > 0 ? "blocked" : "default"} />
        <OverviewKpi icon={<Globe2 size={17} />} label="Unique domains" value={traffic?.unique_domains.toLocaleString() ?? "--"} detail="In the last 24 hours" />
        <OverviewKpi icon={<Clock3 size={17} />} label="Last seen" value={formatLastSeen(detail.last_seen)} detail={detail.last_seen ? new Date(detail.last_seen).toLocaleDateString([], { month: "short", day: "numeric" }) : "No activity yet"} compact />
      </div>

      <div className="device-overview-analytics">
        <section className="device-volume-section">
          <div className="device-section-heading">
            <div><h3>Request volume</h3><p>Activity from this device over the last 24 hours</p></div>
            <div className="chart-legend" aria-label="Chart legend"><span><i className="total" />Queries</span><span><i className="blocked" />Blocked</span></div>
          </div>
          <TrafficChart activity={chart.total} blocked={chart.blocked} />
        </section>

        <section className="device-traffic-profile">
          <div className="device-section-heading"><div><h3>Traffic profile</h3><p>How this device has been resolving names</p></div></div>
          <div className="device-profile-metrics">
            <div><span><Gauge size={16} />Average frequency</span><strong>{frequency.value}</strong><small>{frequency.unit}</small></div>
            <div><span><Globe2 size={16} />Most requested</span><strong>{topDomain?.label ?? "No domains yet"}</strong><small>{topDomain ? `${topDomain.count} requests today` : "Waiting for activity"}</small></div>
            <div><span><History size={16} />Primary path</span><strong>{primarySource ? friendlyOverviewSource(primarySource.label) : "No path yet"}</strong><small>{primarySource ? `${primarySource.count} requests in 24 hours` : "Waiting for activity"}</small></div>
          </div>
        </section>
      </div>

      <div className="device-overview-lists">
        <section className="device-domain-section">
          <div className="device-section-heading"><div><h3>Top domains</h3><p>Most requested destinations today</p></div></div>
          {detail.top_domains.length === 0 ? <p className="empty">No domains for this device yet.</p> : (
            <div className="device-domain-ranks">{detail.top_domains.map((domain) => {
              const max = detail.top_domains[0]?.count || 1;
              return <button type="button" key={domain.label} onClick={() => onDomainSelect(domain.label)}><DomainFavicon domain={domain.label} /><span><strong>{domain.label}</strong><i><b style={{ width: `${Math.max(6, (domain.count / max) * 100)}%` }} /></i></span><em>{domain.count}</em></button>;
            })}</div>
          )}
        </section>

        <section className="device-recent-section">
          <div className="device-section-heading"><div><h3>Recent activity</h3><p>Latest requests, grouped across A and AAAA lookups</p></div></div>
          {recentActivity.length ? (
            <div className="device-overview-activity">
              <div className="device-activity-header"><span>Time</span><span>Result</span><span>Domain</span><span>Type</span><span>Path</span></div>
              {recentActivity.map((query) => <div className="device-activity-row" key={query.key}><time>{new Date(query.timestamp).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })}</time><StatusBadge value={query.action} /><button type="button" onClick={() => onDomainSelect(query.domain)}><DomainFavicon domain={query.domain} /><span>{query.domain}</span></button><span>{query.queryTypes.join(" · ")}</span><small>{friendlyOverviewSource(query.source)}</small></div>)}
            </div>
          ) : <p className="empty">No recent activity for this device.</p>}
        </section>
      </div>
    </div>
  );
}

function OverviewKpi({ icon, label, value, detail, tone = "default", compact = false }: {
  icon: React.ReactNode;
  label: string;
  value: string;
  detail: string;
  tone?: "default" | "blocked";
  compact?: boolean;
}) {
  return <div className={`device-overview-kpi ${tone} ${compact ? "compact" : ""}`}><span>{icon}</span><div><small>{label}</small><strong>{value}</strong><em>{detail}</em></div></div>;
}

type OverviewQuery = {
  key: string;
  timestamp: string;
  domain: string;
  action: DNSQuery["action"];
  source: string;
  queryTypes: string[];
};

function groupOverviewQueries(queries: DNSQuery[]) {
  const grouped = new Map<string, OverviewQuery>();
  queries.forEach((query) => {
    const timestamp = new Date(query.timestamp);
    timestamp.setMilliseconds(0);
    const key = `${timestamp.toISOString()}-${query.domain}-${query.action}-${query.source}`;
    const existing = grouped.get(key);
    if (existing) {
      if (!existing.queryTypes.includes(query.query_type)) existing.queryTypes.push(query.query_type);
      return;
    }
    grouped.set(key, { key, timestamp: query.timestamp, domain: query.domain, action: query.action, source: query.source, queryTypes: [query.query_type] });
  });
  return Array.from(grouped.values());
}

function overviewChartSeries(buckets: ReplayBucket[]) {
  const bucketSize = Math.max(1, Math.ceil(buckets.length / 24));
  const total: number[] = [];
  const blocked: number[] = [];
  for (let index = 0; index < buckets.length; index += bucketSize) {
    const group = buckets.slice(index, index + bucketSize);
    total.push(group.reduce((sum, bucket) => sum + bucket.total, 0));
    blocked.push(group.reduce((sum, bucket) => sum + bucket.blocked, 0));
  }
  return { total: total.slice(-24), blocked: blocked.slice(-24) };
}

function overviewStory(detail: DeviceSummary, topDomain?: string) {
  if (detail.total_queries_today === 0) return { headline: "This device has been quiet today.", detail: "Faro has not observed any DNS requests from it yet." };
  if (detail.blocked_queries_today > 0) return { headline: `Faro blocked ${detail.blocked_queries_today.toLocaleString()} requests from this device.`, detail: topDomain ? `${topDomain} is its most requested domain today.` : "Open Activity Replay to inspect when those requests occurred." };
  return { headline: "No blocked requests from this device today.", detail: topDomain ? `${topDomain} is its most requested domain today.` : `${detail.total_queries_today.toLocaleString()} requests have been observed.` };
}

function friendlyOverviewSource(source: string) {
  const labels: Record<string, string> = { upstream: "Public upstream", cache: "Faro cache", local: "Local DNS", blocklist: "Faro filtering", manual: "Manual rule" };
  return labels[source] ?? source.replace(/_/g, " ").replace(/^./, (letter) => letter.toUpperCase());
}

function overviewFrequency(rate?: number) {
  if (rate === undefined) return { value: "--", unit: "Waiting for activity" };
  if (rate >= 1) return { value: rate >= 10 ? rate.toFixed(0) : rate.toFixed(1), unit: "queries per minute" };
  const hourly = rate * 60;
  if (hourly >= 1) return { value: hourly >= 10 ? hourly.toFixed(0) : hourly.toFixed(1), unit: "queries per hour" };
  const daily = hourly * 24;
  return { value: daily >= 10 ? daily.toFixed(0) : daily.toFixed(1), unit: "queries per day" };
}

function deviceTypeIcon(type: string) {
  switch (type) {
    case "Apple TV": return <Tv size={20} />;
    case "Tesla": return <Car size={20} />;
    case "Windows PC":
    case "Mac": return <Laptop size={20} />;
    case "Linux Server": return <Server size={20} />;
    case "NAS": return <HardDrive size={20} />;
    case "Android Phone": return <Smartphone size={20} />;
    default: return <MonitorSmartphone size={20} />;
  }
}

function formatLastSeen(timestamp?: string | null, includeDate = false) {
  if (!timestamp) return "Not seen yet";
  const value = new Date(timestamp);
  const today = new Date();
  if (includeDate || value.toDateString() !== today.toDateString()) {
    return value.toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
  }
  return value.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}
