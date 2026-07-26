import {
  Activity,
  ArrowDown,
  ArrowUp,
  Check,
  Car,
  Camera,
  ChevronDown,
  ChevronRight,
  Clock3,
  DoorOpen,
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
  PawPrint,
  Printer,
  Router,
  Search,
  Server,
  ShieldCheck,
  Snowflake,
  Speaker,
  Sparkles,
  Smartphone,
  Sun,
  Tablet,
  Tv,
  X
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { api, type DeviceInventoryPage, type DeviceReplay as DeviceReplayData, type DeviceSummary, type DNSQuery, type Protection, type ReplayBucket } from "../api/client";
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
type DeviceSortKey = "device" | "requests" | "blocked" | "last_seen" | "protection";
type SortDirection = "asc" | "desc";

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
  const [sort, setSort] = useState<{ key: DeviceSortKey; direction: SortDirection }>({ key: "device", direction: "asc" });
  const [page, setPage] = useState(1);
  const [inventory, setInventory] = useState<DeviceInventoryPage>(() => inventoryFromDevices(devices));
  const [inventoryLoading, setInventoryLoading] = useState(true);
  const [inventoryError, setInventoryError] = useState("");
  const [protectionBusy, setProtectionBusy] = useState(false);
  const [protectionMenuOpen, setProtectionMenuOpen] = useState(false);
  const protectionMenuRef = useRef<HTMLDivElement>(null);
  const inventoryRequest = useRef<AbortController | null>(null);
  const inventoryETag = useRef("");
  const inventoryBusy = useRef(false);
  const inventoryDevices = inventory.items;
  const activeProtection = detail ? protections.find((protection) => protection.id === detail.protection_id) : null;
  const activeProtectionName = activeProtection?.name ?? detail?.protection ?? detail?.profile ?? "Protection";

  const loadInventory = useCallback(async (conditional: boolean) => {
    if (conditional && inventoryBusy.current) return;
    if (!conditional) inventoryRequest.current?.abort();
    const controller = new AbortController();
    inventoryRequest.current = controller;
    inventoryBusy.current = true;
    if (!conditional) setInventoryLoading(true);
    try {
      const result = await api.deviceInventory({
        page,
        pageSize: 50,
        search: search.trim(),
        sort: sort.key,
        direction: sort.direction
      }, conditional ? inventoryETag.current : "", controller.signal);
      if (result.page) {
        setInventory(result.page);
        inventoryETag.current = result.etag;
      }
      setInventoryError("");
    } catch (caught) {
      if (caught instanceof DOMException && caught.name === "AbortError") return;
      setInventoryError(caught instanceof Error ? caught.message : "Could not refresh the device inventory.");
    } finally {
      if (inventoryRequest.current === controller) {
        inventoryBusy.current = false;
        setInventoryLoading(false);
      }
    }
  }, [page, search, sort.direction, sort.key]);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      inventoryETag.current = "";
      void loadInventory(false);
    }, search ? 250 : 0);
    return () => {
      window.clearTimeout(timer);
      inventoryRequest.current?.abort();
    };
  }, [loadInventory, search]);

  useEffect(() => {
    const timer = window.setInterval(() => {
      if (document.visibilityState === "visible") void loadInventory(true);
    }, 15000);
    return () => window.clearInterval(timer);
  }, [loadInventory]);

  useEffect(() => {
    setPage(1);
  }, [search, sort]);

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

  function changeSort(key: DeviceSortKey) {
    setSort((current) => current.key === key
      ? { key, direction: current.direction === "asc" ? "desc" : "asc" }
      : { key, direction: key === "device" || key === "protection" ? "asc" : "desc" });
  }

  const totals = {
    active: inventory.summary.active_today,
    requests: inventory.summary.requests_today,
    blocked: inventory.summary.blocked_today,
    blockedRate: inventory.summary.requests_today > 0
      ? (inventory.summary.blocked_today / inventory.summary.requests_today) * 100
      : 0
  };

  async function saveAlias(event: FormEvent) {
    event.preventDefault();
    if (!selectedClientIP) return;
    setAliasSaving(true);
    setDetailError("");
    try {
      await api.updateDeviceAlias(selectedClientIP, form);
      setEditing(false);
      await refresh();
      inventoryETag.current = "";
      await loadInventory(false);
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
      inventoryETag.current = "";
      await loadInventory(false);
      setDetail(await api.device(selectedClientIP));
    } catch (caught) {
      setDetailError(caught instanceof Error ? caught.message : "Could not change protection.");
    } finally {
      setProtectionBusy(false);
    }
  }

  if (!inventoryLoading && inventory.summary.observed === 0) {
    return <EmptyState title="No devices yet" body="Point a device or router at Faro to start seeing clients, names, blocked requests, and top domains." />;
  }

  return (
    <div className="devices-page">
      <section className="device-summary-strip" aria-label="Device activity summary">
        <DeviceSummaryMetric icon={<MonitorSmartphone size={18} />} label="Observed devices" value={inventory.summary.observed} detail={`${totals.active} active today`} />
        <DeviceSummaryMetric icon={<Activity size={18} />} label="Requests today" value={totals.requests.toLocaleString()} detail="Across all devices" />
        <DeviceSummaryMetric icon={<ShieldCheck size={18} />} label="Blocked today" value={totals.blocked.toLocaleString()} detail={`${totals.blockedRate.toFixed(1)}% of requests`} tone={totals.blocked > 0 ? "blocked" : "default"} />
        <DeviceSummaryMetric icon={<Clock3 size={18} />} label="Most active" value={inventory.summary.most_active_name || "None"} detail={`${inventory.summary.most_active_requests.toLocaleString()} requests today`} compact />
      </section>

	  {inventory.summary.observed === 1 && totals.requests > 0 && (
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
            <kbd>{inventory.total}</kbd>
          </label>
        </div>

        {inventoryError && <div className="device-inventory-error" role="alert">{inventoryError}</div>}
        <div className="device-table">
          <div className="device-table-header">
            <DeviceSortHeader label="Device" sortKey="device" sort={sort} onSort={changeSort} />
            <DeviceSortHeader label="Requests today" sortKey="requests" sort={sort} onSort={changeSort} />
            <DeviceSortHeader label="Blocked" sortKey="blocked" sort={sort} onSort={changeSort} />
            <DeviceSortHeader label="Last seen" sortKey="last_seen" sort={sort} onSort={changeSort} />
            <DeviceSortHeader label="Protection" sortKey="protection" sort={sort} onSort={changeSort} />
            <span aria-hidden="true" />
          </div>
          {inventoryDevices.map((device) => (
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

        {!inventoryLoading && inventoryDevices.length === 0 && (
          <div className="device-filter-empty"><Search size={20} /><strong>No matching devices</strong><span>Try a name, IP address, device type, or location.</span></div>
        )}
        {inventory.total_pages > 1 && (
          <div className="device-pagination" aria-label="Device inventory pages">
            <span>Showing {(inventory.page - 1) * inventory.page_size + 1}–{Math.min(inventory.page * inventory.page_size, inventory.total)} of {inventory.total}</span>
            <div>
              <button type="button" className="secondary" disabled={inventory.page <= 1 || inventoryLoading} onClick={() => setPage((current) => Math.max(1, current - 1))}>Previous</button>
              <span>Page {inventory.page} of {inventory.total_pages}</span>
              <button type="button" className="secondary" disabled={inventory.page >= inventory.total_pages || inventoryLoading} onClick={() => setPage((current) => current + 1)}>Next</button>
            </div>
          </div>
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

function DeviceSortHeader({ label, sortKey, sort, onSort }: { label: string; sortKey: DeviceSortKey; sort: { key: DeviceSortKey; direction: SortDirection }; onSort: (key: DeviceSortKey) => void }) {
  const active = sort.key === sortKey;
  const DirectionIcon = sort.direction === "asc" ? ArrowUp : ArrowDown;
  return <button type="button" className={active ? "device-sort-header active" : "device-sort-header"} onClick={() => onSort(sortKey)} aria-label={`Sort by ${label}${active ? `, currently ${sort.direction === "asc" ? "ascending" : "descending"}` : ""}`}>
    <span>{label}</span>{active && <DirectionIcon size={13} strokeWidth={2.5} aria-hidden="true" />}
  </button>;
}

function inventoryFromDevices(devices: DeviceSummary[]): DeviceInventoryPage {
  const requests = devices.reduce((sum, device) => sum + device.total_queries_today, 0);
  const blocked = devices.reduce((sum, device) => sum + device.blocked_queries_today, 0);
  const mostActive = devices.reduce<DeviceSummary | null>((current, device) =>
    !current || device.total_queries_today > current.total_queries_today ? device : current, null);
  return {
    items: devices.slice(0, 50),
    page: 1,
    page_size: 50,
    total: devices.length,
    total_pages: devices.length ? Math.ceil(devices.length / 50) : 0,
    revision: "",
    summary: {
      observed: devices.length,
      active_today: devices.filter((device) => device.total_queries_today > 0).length,
      requests_today: requests,
      blocked_today: blocked,
      most_active_name: deviceDisplayName(mostActive) || "None",
      most_active_requests: mostActive?.total_queries_today ?? 0
    }
  };
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
        {detail.classification && (
          <div className={`device-classification ${detail.type_source === "manual" ? "manual" : ""}`}>
            <span className="device-classification-icon">{detail.type_source === "manual" ? <Check size={17} /> : <Sparkles size={17} />}</span>
            <div>
              <small>{detail.type_source === "manual" ? "Chosen by you" : "Automatic detection"}</small>
              <strong>{detail.type_source === "manual" ? detail.device_type : detail.classification.predicted_type}</strong>
              <p>
                {detail.type_source === "manual"
                  ? "Faro will keep your choice and will not replace it automatically."
                  : detail.classification.evidence.length
                    ? `Based on ${detail.classification.evidence.map((item) => item.description.toLowerCase()).join(" and ")}.`
                    : "Faro has not seen enough distinctive activity to identify this device yet."}
              </p>
            </div>
            <span className={`device-confidence ${detail.classification.confidence}`}>
              {detail.type_source === "manual" ? "Manual" : `${detail.classification.confidence} confidence`}
            </span>
          </div>
        )}
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
    case "Enphase Solar System": return <Sun size={size} />;
    case "Midea Smart Appliance": return <Snowflake size={size} />;
    case "Petlibro Smart Pet Device": return <PawPrint size={size} />;
    case "Eufy Security Device": return <Camera size={size} />;
    case "Aqara Smart Home Device": return <Lightbulb size={size} />;
    case "Tuya Smart Home Device": return <Lightbulb size={size} />;
    case "MyQ Garage Door Controller": return <DoorOpen size={size} />;
    case "UniFi Network Device": return <Router size={size} />;
    case "TP-Link Smart Home Device": return <Lightbulb size={size} />;
    case "Meta Hardware": return <MonitorSmartphone size={size} />;
    case "Neakasa Smart Pet Device": return <PawPrint size={size} />;
    case "Amazon Alexa Device": return <Speaker size={size} />;
    case "Philips Hue Bridge": return <Lightbulb size={size} />;
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
