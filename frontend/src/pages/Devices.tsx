import {
  Activity,
  Check,
  Car,
  Camera,
  ChevronDown,
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
  Network,
  Gamepad2,
  Lightbulb,
  Printer,
  Router,
  Search,
  Server,
  ShieldCheck,
  Speaker,
  Sparkles,
  Smartphone,
  Tablet,
  Tv,
  X
} from "lucide-react";
import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { api, type DeviceReplay as DeviceReplayData, type DeviceSummary, type DNSQuery, type Protection, type ReplayBucket } from "../api/client";
import { DeviceReplay } from "../components/DeviceReplay";
import { DomainFavicon } from "../components/DomainFavicon";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import { TrafficChart } from "../components/TrafficChart";
import { ProtectionIcon } from "./Protection";

type DevicesProps = {
  devices: DeviceSummary[];
  protections: Protection[];
  refresh: () => Promise<void>;
  selectedClientIP: string | null;
  onSelectClient: (clientIP: string | null) => void;
  onDomainSelect: (domain: string) => void;
};

type DeviceView = "overview" | "replay";
type DeviceEditForm = { name: string; location: string; notes: string; device_type: string };

const deviceTypeChoices = [
  "Computer",
  "Phone",
  "Tablet",
  "TV",
  "Game console",
  "Router",
  "Server / NAS",
  "Smart home",
  "Printer",
  "Camera",
  "Speaker",
  "Vehicle",
  "Other"
] as const;

export function Devices({ devices, protections, refresh, selectedClientIP, onSelectClient, onDomainSelect }: DevicesProps) {
  const [detail, setDetail] = useState<DeviceSummary | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState("");
  const [form, setForm] = useState<DeviceEditForm>({ name: "", location: "", notes: "", device_type: "" });
  const [editing, setEditing] = useState(false);
  const [aliasSaving, setAliasSaving] = useState(false);
  const [view, setView] = useState<DeviceView>("overview");
  const [search, setSearch] = useState("");
  const [protectionBusy, setProtectionBusy] = useState(false);
  const [protectionMenuOpen, setProtectionMenuOpen] = useState(false);
  const protectionMenuRef = useRef<HTMLDivElement>(null);
  const mostActiveDevice = useMemo(() => devices.reduce<DeviceSummary | null>((current, device) => {
    if (!current || device.total_queries_today > current.total_queries_today) return device;
    return current;
  }, null), [devices]);
  const activeProtection = detail ? protections.find((protection) => protection.id === detail.protection_id) : null;
  const activeProtectionName = activeProtection?.name ?? detail?.protection ?? detail?.profile ?? "Protection";

  useEffect(() => {
    if (!selectedClientIP) {
      setDetail(null);
      setEditing(false);
      setView("overview");
      setProtectionMenuOpen(false);
      return;
    }
    let cancelled = false;
    setDetail(null);
    setDetailLoading(true);
    setDetailError("");
    setEditing(false);
    setView("overview");
    setProtectionMenuOpen(false);
    api.device(selectedClientIP)
      .then((nextDetail) => {
        if (!cancelled) {
          setDetail(nextDetail);
          setForm({ name: nextDetail.name || "", location: nextDetail.location ?? "", notes: nextDetail.notes ?? "", device_type: nextDetail.type_source === "manual" ? nextDetail.device_type : "" });
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

  useEffect(() => {
    if (!selectedClientIP) return;
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    function onKeyDown(event: KeyboardEvent) {
      if (event.key !== "Escape") return;
      if (protectionMenuOpen) setProtectionMenuOpen(false);
      else onSelectClient(null);
    }
    window.addEventListener("keydown", onKeyDown);
    return () => {
      document.body.style.overflow = previousOverflow;
      window.removeEventListener("keydown", onKeyDown);
    };
  }, [onSelectClient, protectionMenuOpen, selectedClientIP]);

  useEffect(() => {
    if (!protectionMenuOpen) return;
    function onPointerDown(event: PointerEvent) {
      if (!protectionMenuRef.current?.contains(event.target as Node)) setProtectionMenuOpen(false);
    }
    document.addEventListener("pointerdown", onPointerDown);
    return () => document.removeEventListener("pointerdown", onPointerDown);
  }, [protectionMenuOpen]);

  const filteredDevices = useMemo(() => {
    const term = search.trim().toLowerCase();
    if (!term) return devices;
    return devices.filter((device) => [device.name, device.display_name, device.client_ip, ...(device.addresses ?? []), device.device_type, device.location, device.profile]
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
    setAliasSaving(true);
    setDetailError("");
    try {
      await api.updateDeviceAlias(selectedClientIP, form);
      setEditing(false);
      await refresh();
      setDetail(await api.device(selectedClientIP));
    } catch (caught) {
      setDetailError(caught instanceof Error ? caught.message : "Could not save this device.");
    } finally {
      setAliasSaving(false);
    }
  }

  async function changeProtection(protectionID: number) {
    if (!selectedClientIP || protectionID === detail?.protection_id) return;
    setProtectionMenuOpen(false);
    setProtectionBusy(true);
    setDetailError("");
    try {
      await api.assignDeviceProtection(selectedClientIP, protectionID);
      await refresh();
      setDetail(await api.device(selectedClientIP));
    } catch (caught) {
      setDetailError(caught instanceof Error ? caught.message : "Could not change protection.");
    } finally {
      setProtectionBusy(false);
    }
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
        <DeviceSummaryMetric icon={<Clock3 size={18} />} label="Most active" value={deviceDisplayName(mostActiveDevice) || "None"} detail={`${mostActiveDevice?.total_queries_today.toLocaleString() ?? "0"} requests today`} compact />
      </section>

	  {devices.length === 1 && totals.requests > 0 && (
		<section className="device-visibility-note" aria-label="Device visibility status">
		  <Network size={19} />
		  <div><strong>Faro currently sees one DNS source</strong><span>If that source is your router, Faro will keep protecting the network, but the router is hiding which device made each request.</span></div>
		</section>
	  )}

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
            <span>Device</span><span>Requests today</span><span>Blocked</span><span>Last seen</span><span>Protection</span><span />
          </div>
          {filteredDevices.map((device) => (
            <button
              className={selectedClientIP === device.client_ip ? "device-table-row active" : "device-table-row"}
              key={device.device_id || device.client_ip}
              type="button"
              onClick={() => onSelectClient(device.client_ip)}
              aria-pressed={selectedClientIP === device.client_ip}
            >
              <span className="device-table-identity">
                <span className="device-icon">{deviceTypeIcon(device.device_type)}</span>
                <span className="device-main">
                  <strong>{deviceDisplayName(device)}</strong>
                  <small>{device.device_type} <i /> {deviceIdentityCaption(device)}{device.location ? ` · ${device.location}` : ""}</small>
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

      {selectedClientIP && (
        <div className="drawer-backdrop device-drawer-backdrop" onClick={() => onSelectClient(null)}>
          <aside className="device-detail-drawer" role="dialog" aria-modal="true" aria-label="Device details" onClick={(event) => event.stopPropagation()}>
            <header className="device-drawer-header">
              <div><strong>Device details</strong><span>Inspect identity, traffic, and history without losing your place.</span></div>
              <button className="icon-button" type="button" onClick={() => onSelectClient(null)} aria-label="Close device details"><X size={18} /></button>
            </header>
            <section className={`device-detail-panel ${view === "replay" ? "replay-active" : ""}`}>
              {detailLoading && <div className="device-detail-loading">Loading device details...</div>}
              {detailError && <div className="device-detail-error">{detailError}</div>}
              {!detailLoading && detail && (
                <>
                  <div className="device-detail-header">
                    <div className="device-detail-identity">
                      <span className="device-detail-icon">{deviceTypeIcon(detail.device_type)}</span>
                      <div>
                        <div className="device-detail-context">
                          <div className="device-protection-picker" ref={protectionMenuRef}>
                            <button
                              type="button"
                              className="device-protection-trigger"
                              aria-label={`Protection: ${activeProtectionName}`}
                              aria-haspopup="listbox"
                              aria-expanded={protectionMenuOpen}
                              aria-controls="device-protection-options"
                              disabled={protectionBusy || protections.length === 0}
                              onClick={() => setProtectionMenuOpen((open) => !open)}
                            >
                              <ProtectionIcon name={activeProtection?.icon ?? detail.protection_icon} size={15} />
                              <span>{protectionBusy ? "Applying…" : activeProtectionName}</span>
                              <ChevronDown className={protectionMenuOpen ? "open" : ""} size={14} />
                            </button>
                            {protectionMenuOpen && (
                              <div className="device-protection-menu" id="device-protection-options" role="listbox" aria-label="Choose protection">
                                <div className="device-protection-menu-heading"><strong>Choose protection</strong><span>Changes apply to this device immediately.</span></div>
                                {protections.map((protection) => {
                                  const selected = protection.id === detail.protection_id;
                                  const assignedCount = protection.device_ips.length;
                                  return (
                                    <button
                                      type="button"
                                      role="option"
                                      aria-selected={selected}
                                      className={selected ? "selected" : ""}
                                      key={protection.id}
                                      onClick={() => void changeProtection(protection.id)}
                                    >
                                      <span className="device-protection-option-icon"><ProtectionIcon name={protection.icon} size={17} /></span>
                                      <span><strong>{protection.name}</strong><small>{protection.is_default ? "Default for unassigned devices" : `${assignedCount} assigned device${assignedCount === 1 ? "" : "s"}`}</small></span>
                                      <Check size={15} />
                                    </button>
                                  );
                                })}
                              </div>
                            )}
                          </div>
                          <span>{detail.client_ip}</span>
                        </div>
                        <h2>{deviceDisplayName(detail)}</h2>
                        <p>{detail.device_type} · {detail.type_source === "manual" ? "Type chosen by you" : deviceIdentityDescription(detail)}</p>
                      </div>
                    </div>
                    {view === "overview" && <button className="secondary device-edit-button" type="button" onClick={() => setEditing((value) => !value)}><Edit3 size={16} /><span>{editing ? "Cancel" : "Edit device"}</span></button>}
                  </div>

                  <div className="device-view-tabs" role="tablist" aria-label="Device views">
                    <button className={view === "overview" ? "active" : ""} type="button" role="tab" aria-selected={view === "overview"} onClick={() => setView("overview")}><LayoutDashboard size={16} /><span>Overview</span></button>
                    <button className={view === "replay" ? "active" : ""} type="button" role="tab" aria-selected={view === "replay"} onClick={() => { setEditing(false); setView("replay"); }}><History size={16} /><span>Activity replay</span></button>
                  </div>

                  {view === "overview" ? (
                    <DeviceOverview detail={detail} form={form} setForm={setForm} editing={editing} saving={aliasSaving} saveAlias={saveAlias} onDomainSelect={onDomainSelect} onOpenReplay={() => setView("replay")} />
                  ) : (
                    <DeviceReplay clientIP={detail.client_ip} deviceName={deviceDisplayName(detail)} onDomainSelect={onDomainSelect} />
                  )}
                </>
              )}
            </section>
          </aside>
        </div>
      )}
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

function DeviceOverview({ detail, form, setForm, editing, saving, saveAlias, onDomainSelect, onOpenReplay }: {
  detail: DeviceSummary;
  form: DeviceEditForm;
  setForm: (form: DeviceEditForm) => void;
  editing: boolean;
  saving: boolean;
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
          <div className="alias-form-heading"><div><strong>Edit device</strong><span>Give this device a recognizable name and correct Faro when automatic detection gets it wrong.</span></div></div>
          <div className="alias-form-fields">
            <label>Friendly name<input value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} placeholder={detail.client_ip} /></label>
            <label>Location<input value={form.location} onChange={(event) => setForm({ ...form, location: event.target.value })} placeholder="Living room" /></label>
            <label>Notes<input value={form.notes} onChange={(event) => setForm({ ...form, notes: event.target.value })} placeholder="Optional notes" /></label>
          </div>
          <fieldset className="device-type-picker">
            <legend>Device type &amp; icon</legend>
            <p>Choose what looks right, or leave Faro on Automatic.</p>
            <div>
              <button type="button" className={form.device_type === "" ? "selected" : ""} aria-pressed={form.device_type === ""} onClick={() => setForm({ ...form, device_type: "" })}>
                <span><Sparkles size={18} />{form.device_type === "" && <i><Check size={10} /></i>}</span><strong>Automatic</strong><small>Faro decides</small>
              </button>
              {deviceTypeChoices.map((type) => (
                <button type="button" key={type} className={form.device_type === type ? "selected" : ""} aria-pressed={form.device_type === type} onClick={() => setForm({ ...form, device_type: type })}>
                  <span>{deviceTypeIcon(type, 18)}{form.device_type === type && <i><Check size={10} /></i>}</span><strong>{type}</strong>
                </button>
              ))}
            </div>
          </fieldset>
          <div className="alias-form-actions">
            <span>{form.device_type ? "Your choice will not be replaced by automatic detection." : `Currently detected as ${detail.device_type}. Faro will keep improving this automatically.`}</span>
            <button type="submit" disabled={saving}>{saving ? "Saving…" : "Save device"}</button>
          </div>
        </form>
      )}

      <section className="device-overview-story">
        <span className={`device-story-icon ${detail.blocked_queries_today > 0 ? "blocked" : "healthy"}`}><ShieldCheck size={21} /></span>
        <div><small>Today at a glance</small><h3>{story.headline}</h3><p>{story.detail}</p></div>
        <button className="secondary" type="button" onClick={onOpenReplay}><History size={16} /><span>Open replay</span></button>
      </section>

	  <section className="device-identity-evidence">
		<div className="device-section-heading"><div><h3>How Faro recognizes this device</h3><p>{detail.identity_source || "DNS activity"} · address changes stay connected automatically when Faro has a strong match</p></div></div>
		<div className="device-address-history">
		  {(detail.address_history?.length ? detail.address_history : (detail.addresses ?? [detail.client_ip]).map((address) => ({ address, family: address.includes(":") ? "ipv6" : "ipv4", source: "dns", confidence: "observed", first_seen: detail.first_seen ?? "", last_seen: detail.last_seen ?? "" }))).map((item, index) => (
			<div key={item.address}><span className={index === 0 ? "current" : ""}>{index === 0 ? "Current" : "Seen before"}</span><code>{item.address}</code><small>{item.family.toUpperCase()} · Last seen {formatLastSeen(item.last_seen, true)}</small></div>
		  ))}
		</div>
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

function deviceTypeIcon(type: string, size = 20) {
  switch (type) {
    case "Apple TV":
    case "TV": return <Tv size={size} />;
    case "Apple Device":
    case "Android Device":
    case "Android Phone":
    case "Phone": return <Smartphone size={size} />;
    case "Tablet": return <Tablet size={size} />;
    case "Smart TV":
    case "Roku": return <Tv size={size} />;
    case "Xbox":
    case "PlayStation":
    case "Nintendo":
    case "Game console": return <Gamepad2 size={size} />;
    case "Tesla":
    case "Vehicle": return <Car size={size} />;
    case "Windows PC":
    case "Mac":
    case "Computer": return <Laptop size={size} />;
    case "Linux Server": return <Server size={size} />;
    case "NAS": return <HardDrive size={size} />;
    case "Server / NAS": return <HardDrive size={size} />;
    case "Router": return <Router size={size} />;
    case "Smart home": return <Lightbulb size={size} />;
    case "Printer": return <Printer size={size} />;
    case "Camera": return <Camera size={size} />;
    case "Speaker": return <Speaker size={size} />;
    default: return <MonitorSmartphone size={size} />;
  }
}

function deviceDisplayName(device?: DeviceSummary | null) {
  return device?.display_name || device?.name || device?.client_ip || "";
}

function deviceIdentityCaption(device: DeviceSummary) {
	const additionalAddresses = Math.max(0, (device.addresses?.length ?? 1) - 1);
	const addressLabel = additionalAddresses > 0 ? `${device.client_ip} + ${additionalAddresses} more` : device.client_ip;
  if (device.type_source === "manual") return `Chosen by you · ${addressLabel}`;
  if (!device.display_name && !device.name) return "Name not discovered";
  if (device.name_source === "manual" || device.name) return addressLabel;
  return `${addressLabel} · Auto-detected`;
}

function deviceIdentityDescription(device: DeviceSummary) {
  if (device.location) return device.location;
  if (device.name_source === "local_dns") return "Name discovered from Local DNS";
  if (device.name_source === "reverse_dns") return "Name discovered from reverse DNS";
  if (device.name_source === "manual" || device.name) return "Friendly name configured";
  return "No reliable hostname discovered yet";
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
