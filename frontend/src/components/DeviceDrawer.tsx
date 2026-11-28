import { Activity, Check, ChevronDown, Clock3, Edit3, Gauge, Globe2, History, LayoutDashboard, Play, ShieldCheck, Sparkles, WifiOff, X } from "lucide-react";
import { useEffect, useMemo, useState, type ReactNode, type SubmitEvent } from "react";
import { api, deviceReference, type DeviceReplay as DeviceReplayData, type DeviceSummary, type DNSQuery, type Protection, type ReplayBucket } from "../api/client";
import type { DeviceEditForm, DeviceView } from "../pages/Devices";
import { deviceDisplayName, deviceTypeIcon, formatLastSeen } from "../utils/devicePresentation";
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuGroup, DropdownMenuLabel, DropdownMenuRadioGroup, DropdownMenuRadioItem } from "./ui/dropdown-menu";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "./ui/tabs";
import { DialogSurface } from "./DialogSurface";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Troubleshooter } from "./Troubleshooter";
import { DeviceReplay } from "./DeviceReplay";
import { DomainFavicon } from "./DomainFavicon";
import { StatusBadge } from "./StatusBadge";
import { TrafficChart } from "./TrafficChart";
import { ProtectionIcon } from "./ProtectionIcon";

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

export default function DeviceDrawer({ onRulesChanged, selectedDeviceRef, detail, detailLoading, detailError, view, setView, protections, activeProtection, activeProtectionName, protectionBusy, protectionMenuOpen, setProtectionMenuOpen, changeProtection, editing, setEditing, form, setForm, aliasSaving, saveAlias, onSelectDevice, onDomainSelect }: {
  readonly onRulesChanged: () => Promise<void>;
  readonly selectedDeviceRef: string | null;
  readonly detail: DeviceSummary | null;
  readonly detailLoading: boolean;
  readonly detailError: string;
  readonly view: DeviceView;
  readonly setView: (view: DeviceView) => void;
  readonly protections: Protection[];
  readonly activeProtection: Protection | undefined | null;
  readonly activeProtectionName: string;
  readonly protectionBusy: boolean;
  readonly protectionMenuOpen: boolean;
  readonly setProtectionMenuOpen: (open: boolean) => void;
  readonly changeProtection: (protectionID: number) => Promise<void>;
  readonly editing: boolean;
  readonly setEditing: (editing: boolean) => void;
  readonly form: DeviceEditForm;
  readonly setForm: (form: DeviceEditForm) => void;
  readonly aliasSaving: boolean;
  readonly saveAlias: (event: SubmitEvent) => Promise<void>;
  readonly onSelectDevice: (clientIP: string | null) => void;
  readonly onDomainSelect: (domain: string) => void;
}) {
  if (!selectedDeviceRef) return null;
  return <DialogSurface onClose={() => onSelectDevice(null)}  className="drawer-backdrop device-drawer-backdrop" aria-label="Device details" ><aside className="device-detail-drawer"><header className="device-drawer-header"><div><strong>Device details</strong><span>Inspect identity, traffic, and history without losing your place.</span></div><Button variant="ghost" size="icon" className="icon-button" type="button" onClick={() => onSelectDevice(null)} aria-label="Close device details"><X size={18} /></Button></header><section className={`device-detail-panel ${view === "replay" ? "replay-active" : ""}`}>{detailLoading && <div className="device-detail-loading">Loading device details...</div>}{detailError && <div className="device-detail-error">{detailError}</div>}{detail && <DeviceDrawerContent onRulesChanged={onRulesChanged} detail={detail} view={view} setView={setView} protections={protections} activeProtection={activeProtection} activeProtectionName={activeProtectionName} protectionBusy={protectionBusy} protectionMenuOpen={protectionMenuOpen} setProtectionMenuOpen={setProtectionMenuOpen} changeProtection={changeProtection} editing={editing} setEditing={setEditing} form={form} setForm={setForm} aliasSaving={aliasSaving} saveAlias={saveAlias} onDomainSelect={onDomainSelect} />}</section></aside></DialogSurface>;
}

function DeviceDrawerContent({ onRulesChanged, detail, view, setView, protections, activeProtection, activeProtectionName, protectionBusy, protectionMenuOpen, setProtectionMenuOpen, changeProtection, editing, setEditing, form, setForm, aliasSaving, saveAlias, onDomainSelect }: {
  readonly onRulesChanged: () => Promise<void>;
  readonly detail: DeviceSummary;
  readonly view: DeviceView;
  readonly setView: (view: DeviceView) => void;
  readonly protections: Protection[];
  readonly activeProtection: Protection | undefined | null;
  readonly activeProtectionName: string;
  readonly protectionBusy: boolean;
  readonly protectionMenuOpen: boolean;
  readonly setProtectionMenuOpen: (open: boolean) => void;
  readonly changeProtection: (protectionID: number) => Promise<void>;
  readonly editing: boolean;
  readonly setEditing: (editing: boolean) => void;
  readonly form: DeviceEditForm;
  readonly setForm: (form: DeviceEditForm) => void;
  readonly aliasSaving: boolean;
  readonly saveAlias: (event: SubmitEvent) => Promise<void>;
  readonly onDomainSelect: (domain: string) => void;
}) {
  return <Tabs value={view} onValueChange={(value) => { if (value !== "overview") setEditing(false); setView(value as DeviceView); }}><div className="device-detail-header"><div className="device-detail-identity"><span className="device-detail-icon">{deviceTypeIcon(detail.device_type)}</span><div><div className="device-detail-context"><DeviceProtectionPicker detail={detail} protections={protections} activeProtection={activeProtection} activeProtectionName={activeProtectionName} protectionBusy={protectionBusy} protectionMenuOpen={protectionMenuOpen} setProtectionMenuOpen={setProtectionMenuOpen} changeProtection={changeProtection} /><span>{detail.client_ip || "No current address"}</span></div><h2>{deviceDisplayName(detail)}</h2><p>{detail.device_type} · {detail.type_source === "manual" ? "Type chosen by you" : deviceIdentityDescription(detail)}</p></div></div>{view === "overview" && <Button variant="outline" className="secondary device-edit-button" type="button" onClick={() => setEditing(!editing)}><Edit3 size={16} /><span>{editing ? "Cancel" : "Edit device"}</span></Button>}</div><TabsList className="device-view-tabs"  aria-label="Device views"><TabsTrigger value="overview"     ><LayoutDashboard size={16} /><span>Overview</span></TabsTrigger><TabsTrigger value="replay"     ><History size={16} /><span>Activity replay</span></TabsTrigger><TabsTrigger value="troubleshoot"     ><ShieldCheck size={16} /><span>Fix a broken site</span></TabsTrigger></TabsList><TabsContent value={view}>{view === "overview" ? <DeviceOverview detail={detail} form={form} setForm={setForm} editing={editing} saving={aliasSaving} saveAlias={saveAlias} onDomainSelect={onDomainSelect} onOpenReplay={() => setView("replay")} /> : view === "troubleshoot" ? <Troubleshooter onRulesChanged={onRulesChanged} key={detail.device_id} clientIP={deviceReference(detail)} deviceName={deviceDisplayName(detail)} onDomainSelect={onDomainSelect} /> : <DeviceReplay clientIP={deviceReference(detail)} deviceName={deviceDisplayName(detail)} onDomainSelect={onDomainSelect} />}</TabsContent></Tabs>;
}

function DeviceProtectionPicker({ detail, protections, activeProtection, activeProtectionName, protectionBusy, protectionMenuOpen, setProtectionMenuOpen, changeProtection }: {
  readonly detail: DeviceSummary;
  readonly protections: Protection[];
  readonly activeProtection: Protection | undefined | null;
  readonly activeProtectionName: string;
  readonly protectionBusy: boolean;
  readonly protectionMenuOpen: boolean;
  readonly setProtectionMenuOpen: (open: boolean) => void;
  readonly changeProtection: (protectionID: number) => Promise<void>;
}) {
  return <DropdownMenu open={protectionMenuOpen} onOpenChange={setProtectionMenuOpen}>
    <DropdownMenuTrigger render={<Button variant="outline" className="device-protection-trigger" />} aria-label={`Protection: ${activeProtectionName}`} disabled={protectionBusy || protections.length === 0}>
      <ProtectionIcon name={activeProtection?.icon ?? detail.protection_icon} size={15} />
      <span>{protectionBusy ? "Applying…" : activeProtectionName}</span><ChevronDown size={14} />
    </DropdownMenuTrigger>
    <DropdownMenuContent className="w-72" aria-label="Choose protection">
      <DropdownMenuGroup>
        <DropdownMenuLabel>Choose protection</DropdownMenuLabel>
        <p className="px-2 pb-2 text-xs text-muted-foreground">Changes apply to this device immediately.</p>
        <DropdownMenuRadioGroup value={String(detail.protection_id)} onValueChange={(value) => { void changeProtection(Number(value)); }}>
          {protections.map((protection) => <DropdownMenuRadioItem key={protection.id} value={String(protection.id)} closeOnClick disabled={protectionBusy} className="gap-3 py-2">
            <ProtectionIcon name={protection.icon} size={17} />
            <span className="grid gap-1"><strong className="font-medium">{protection.name}</strong><small className="text-muted-foreground">{protectionAssignmentLabel(protection.is_default, protection.device_ids.length)}</small></span>
          </DropdownMenuRadioItem>)}
        </DropdownMenuRadioGroup>
      </DropdownMenuGroup>
    </DropdownMenuContent>
  </DropdownMenu>;
}

function DeviceOverview({ detail, form, setForm, editing, saving, saveAlias, onDomainSelect, onOpenReplay }: {
  readonly detail: DeviceSummary;
  readonly form: DeviceEditForm;
  readonly setForm: (form: DeviceEditForm) => void;
  readonly editing: boolean;
  readonly saving: boolean;
  readonly saveAlias: (event: SubmitEvent) => Promise<void>;
  readonly onDomainSelect: (domain: string) => void;
  readonly onOpenReplay: () => void;
}) {
  const [traffic, setTraffic] = useState<DeviceReplayData | null>(null);
  const [trafficLoading, setTrafficLoading] = useState(true);
  const [trafficError, setTrafficError] = useState("");

  useEffect(() => {
    let cancelled = false;
    setTraffic(null);
    setTrafficLoading(true);
    setTrafficError("");
    api.deviceReplay(deviceReference(detail), "24h").then((nextTraffic) => {
      if (!cancelled) setTraffic(nextTraffic);
    }).catch(() => {
      if (!cancelled) setTrafficError("Could not load activity. Reopen this device to retry.");
    }).finally(() => { if (!cancelled) setTrafficLoading(false); });
    return () => {
      cancelled = true;
    };
  }, [detail.device_id, detail.client_ip]);

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
            <label>Friendly name<Input value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} placeholder={detail.client_ip} /></label>
            <label>Location<Input value={form.location} onChange={(event) => setForm({ ...form, location: event.target.value })} placeholder="Living room" /></label>
            <label>Notes<Input value={form.notes} onChange={(event) => setForm({ ...form, notes: event.target.value })} placeholder="Optional notes" /></label>
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
            <Button variant="default" type="submit" disabled={saving}>{saving ? "Saving…" : "Save device"}</Button>
          </div>
        </form>
      )}

      <DeviceDNSPause detail={detail} />

      <section className="device-overview-story">
        <span className={`device-story-icon ${detail.blocked_queries_today > 0 ? "blocked" : "healthy"}`}><ShieldCheck size={21} /></span>
        <div><small>Today at a glance</small><h3>{story.headline}</h3><p>{story.detail}</p></div>
        <Button variant="outline" className="secondary" type="button" onClick={onOpenReplay}><History size={16} /><span>Open replay</span></Button>
      </section>

      <section className="device-identity-evidence">
        <div className="device-section-heading"><div><h3>How Faro recognizes this device</h3><p>{detail.identity_source || "DNS activity"} · address changes stay connected automatically when Faro has a strong match</p></div></div>
        {detail.classification && (
          <div className={`device-classification ${detail.type_source === "manual" ? "manual" : ""}`}>
            <span className="device-classification-icon">{detail.type_source === "manual" ? <Check size={17} /> : <Sparkles size={17} />}</span>
            <div>
              <small>{detail.type_source === "manual" ? "Chosen by you" : "Automatic detection"}</small>
              <strong>{detail.type_source === "manual" ? detail.device_type : detail.classification.predicted_type}</strong>
              <p>
                {classificationEvidence(detail)}
              </p>
            </div>
            <span className={`device-confidence ${detail.classification.confidence}`}>
              {detail.type_source === "manual" ? "Manual" : `${detail.classification.confidence} confidence`}
            </span>
          </div>
        )}
        <div className="device-address-history">
          {(detail.address_history?.length ? detail.address_history : (detail.addresses ?? [detail.client_ip]).map((address) => ({ address, family: address.includes(":") ? "ipv6" : "ipv4", source: "dns", confidence: "observed", current: true, first_seen: detail.first_seen ?? "", last_seen: detail.last_seen ?? "" }))).map((item, index) => (
            <div key={item.address}><span className={(item.current ?? index === 0) ? "current" : ""}>{(item.current ?? index === 0) ? "Current" : "Seen before"}</span><code>{item.address}</code><small>{item.family.toUpperCase()} · Last seen {formatLastSeen(item.last_seen, true)}</small></div>
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
          {trafficLoading ? <p role="status">Loading device activity…</p> : trafficError ? <p role="alert">{trafficError}</p> : <TrafficChart activity={chart.total} blocked={chart.blocked} />}
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
              {recentActivity.map((query) => <div className="device-activity-row" key={query.key}><time>{new Date(query.timestamp).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })}</time><StatusBadge value={query.action} /><Button variant="default" type="button" onClick={() => onDomainSelect(query.domain)}><DomainFavicon domain={query.domain} /><span>{query.domain}</span></Button><span>{query.queryTypes.join(" · ")}</span><small>{friendlyOverviewSource(query.source)}</small></div>)}
            </div>
          ) : <p className="empty">No recent activity for this device.</p>}
        </section>
      </div>
    </div>
  );
}

function DeviceDNSPause({ detail }: { readonly detail: DeviceSummary }) {
  const [pausedUntil, setPausedUntil] = useState(detail.dns_paused ? detail.dns_paused_until ?? null : null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const paused = Boolean(pausedUntil && new Date(pausedUntil).getTime() > Date.now());

  async function update(preset: "5m" | "1h" | "tomorrow" | "resume") {
    setBusy(true);
    setError("");
    try {
      const result = await api.pauseDeviceDNS(deviceReference(detail), pauseDeviceUntil(preset));
      setPausedUntil(result.paused_until ?? null);
    } catch (error_) {
      setError(error_ instanceof Error ? error_.message : "Could not change this device's DNS pause.");
    } finally {
      setBusy(false);
    }
  }

  return <section className={`device-dns-pause ${paused ? "paused" : ""}`}>
    <span className="device-dns-pause-icon">{paused ? <WifiOff size={19} /> : <ShieldCheck size={19} />}</span>
    <div><small>DEVICE INTERNET PAUSE</small><strong>{paused ? `Internet access paused until ${formatDevicePause(pausedUntil)}` : "Internet access is on"}</strong><p>{paused ? "Faro is blocking DNS for this device, so most websites and apps cannot make new connections. Existing connections may continue briefly." : "Pause most new website and app connections for this device. This is stronger than turning off domain blocking."}</p>{error && <em>{error}</em>}</div>
    <div>{paused ? <Button variant="default" type="button" disabled={busy} onClick={() => void update("resume")}><Play size={14} />Restore internet access</Button> : <><Button variant="outline" className="secondary" type="button" disabled={busy} onClick={() => void update("5m")}>Pause 5 min</Button><Button variant="outline" className="secondary" type="button" disabled={busy} onClick={() => void update("1h")}>Pause 1 hour</Button><Button variant="outline" className="secondary" type="button" disabled={busy} onClick={() => void update("tomorrow")}>Pause until tomorrow</Button></>}</div>
  </section>;
}

function pauseDeviceUntil(preset: "5m" | "1h" | "tomorrow" | "resume") { if (preset === "resume") return ""; const until = new Date(); if (preset === "5m") until.setMinutes(until.getMinutes() + 5); else if (preset === "1h") until.setHours(until.getHours() + 1); else { until.setDate(until.getDate() + 1); until.setHours(7, 0, 0, 0); } return until.toISOString(); }
function formatDevicePause(value: string | null) { return value ? new Intl.DateTimeFormat(undefined, { weekday: "short", hour: "numeric", minute: "2-digit" }).format(new Date(value)) : "later"; }

function OverviewKpi({ icon, label, value, detail, tone = "default", compact = false }: {
  readonly icon: ReactNode;
  readonly label: string;
  readonly value: string;
  readonly detail: string;
  readonly tone?: "default" | "blocked";
  readonly compact?: boolean;
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

function deviceIdentityDescription(device: DeviceSummary) {
  if (device.location) return device.location;
  if (device.name_source === "unifi") return "Name from UniFi";
  if (device.name_source === "local_dns") return "Name discovered from Local DNS";
  if (device.name_source === "reverse_dns") return "Name discovered from reverse DNS";
  if (device.name_source === "manual" || device.name) return "Friendly name configured";
  return "No reliable hostname discovered yet";
}

function protectionAssignmentLabel(isDefault: boolean, assignedCount: number) {
  if (isDefault) return "Default for unassigned devices";
  return `${assignedCount} assigned device${assignedCount === 1 ? "" : "s"}`;
}

function classificationEvidence(detail: DeviceSummary) {
  if (detail.type_source === "manual") return "Faro will keep your choice and will not replace it automatically.";
  if (detail.classification?.evidence.length) return `Based on ${detail.classification.evidence.map((item) => item.description.toLowerCase()).join(" and ")}.`;
  return "Faro has not seen enough distinctive activity to identify this device yet.";
}
