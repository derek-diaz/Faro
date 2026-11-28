import "./devices.css";
import { Activity, AlertTriangle, RefreshCw, ArrowDown, ArrowUp, ChevronRight, Clock3, MonitorSmartphone, Network, Search, ShieldCheck, X } from "lucide-react";
import { Fragment, lazy, Suspense, useCallback, useEffect, useEffectEvent, useMemo, useRef, useState, type ReactNode, type SubmitEvent } from "react";
import { rowSortingFeature, tableFeatures, useTable } from "@tanstack/react-table";
import type { Column, ColumnDef, SortingState } from "@tanstack/react-table";
import { api, deviceReference, type DeviceInventoryPage, type DeviceSummary, type Protection } from "../api/client";
import { EmptyState } from "../components/EmptyState";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { NativeSelect } from "../components/ui/native-select";
import { deviceDisplayName, deviceTypeIcon, formatLastSeen } from "../utils/devicePresentation";

const DeviceDrawer = lazy(() => import("../components/DeviceDrawer"));

type DevicesProps = {
  readonly protections: Protection[];
  readonly refresh: () => Promise<void>;
  readonly selectedDeviceRef: string | null;
  readonly onSelectDevice: (reference: string | null, replace?: boolean) => void;
  readonly view: DeviceView;
  readonly onViewChange: (view: DeviceView) => void;
  readonly onDomainSelect: (domain: string) => void;
};

export type DeviceView = "overview" | "replay" | "troubleshoot";
type DeviceActivityScope = "history" | "today" | "all";
export type DeviceEditForm = { name: string; location: string; notes: string; device_type: string };
type DeviceSortKey = "device" | "requests" | "blocked" | "last_seen" | "protection";
type SortDirection = "asc" | "desc";

function isDeviceSortKey(value: string): value is DeviceSortKey {
  return value === "device" || value === "requests" || value === "blocked" || value === "last_seen" || value === "protection";
}

const deviceTableFeatures = tableFeatures({ rowSortingFeature });

export function Devices({ protections, refresh, selectedDeviceRef, onSelectDevice, view, onViewChange, onDomainSelect }: DevicesProps) {
  const [detail, setDetail] = useState<DeviceSummary | null>(null);
  const detailCache = useRef(new Map<string, DeviceSummary>());
  const detailSelection = useRef<{ reference: string; version: number; loaded: boolean } | null>(null);
  const [detailVersion, setDetailVersion] = useState(0);
  const [inventoryVersion, setInventoryVersion] = useState(0);
  const inventoryRevision = useRef<string | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState("");
  const [form, setForm] = useState<DeviceEditForm>({ name: "", location: "", notes: "", device_type: "" });
  const [editing, setEditing] = useState(false);
  const [aliasSaving, setAliasSaving] = useState(false);
  const [search, setSearch] = useState("");
  const [activityScope, setActivityScope] = useState<DeviceActivityScope>(view === "troubleshoot" ? "all" : "history");
  const [sorting, setSorting] = useState<SortingState>([{ id: "device", desc: false }]);
  const [page, setPage] = useState(1);
  const [inventory, setInventory] = useState(emptyInventory);
  const [inventoryLoading, setInventoryLoading] = useState(true);
  const [inventoryError, setInventoryError] = useState("");
  const [protectionBusy, setProtectionBusy] = useState(false);
  const [protectionMenuOpen, setProtectionMenuOpen] = useState(false);
  const inventoryRequest = useRef<AbortController | null>(null);
  const inventoryETag = useRef("");
  const inventoryBusy = useRef(false);
  const inventoryDevices = inventory.items;
  const activeProtection = detail ? protections.find((protection) => protection.id === detail.protection_id) : null;
  const activeProtectionName = activeProtection?.name ?? detail?.protection ?? detail?.profile ?? "Protection";
  const sort = useMemo<{ key: DeviceSortKey; direction: SortDirection }>(() => {
    const current = sorting[0];
    return {
      key: current && isDeviceSortKey(current.id) ? current.id : "device",
      direction: current?.desc ? "desc" : "asc"
    };
  }, [sorting]);

  const deviceColumns = useMemo<ColumnDef<typeof deviceTableFeatures, DeviceSummary>[]>(() => [
    {
      id: "device",
      accessorFn: (device) => deviceDisplayName(device),
      header: ({ column }) => <DeviceSortHeader column={column} label="Device" />,
      cell: ({ row }) => (
        <span className="device-table-identity">
          <span className="device-icon">{deviceTypeIcon(row.original.device_type)}</span>
          <span className="device-main">
            <strong>{deviceDisplayName(row.original)}</strong>
            <small>{deviceIdentityCaption(row.original)}</small>
          </span>
        </span>
      )
    },
    {
      id: "requests",
      accessorKey: "total_queries_today",
      sortDescFirst: true,
      header: ({ column }) => <DeviceSortHeader column={column} label="Requests today" />,
      cell: ({ row }) => <span className={`device-table-value ${row.original.total_queries_today === 0 ? "quiet" : ""}`}><small>Requests today</small><strong>{row.original.total_queries_today.toLocaleString()}</strong></span>
    },
    {
      id: "blocked",
      accessorKey: "blocked_queries_today",
      sortDescFirst: true,
      header: ({ column }) => <DeviceSortHeader column={column} label="Blocked" />,
      cell: ({ row }) => <span className={`device-table-value ${row.original.blocked_queries_today > 0 ? "blocked" : "quiet"}`}><small>Blocked</small><strong>{row.original.blocked_queries_today.toLocaleString()} {row.original.blocked_queries_today > 0 && <em>{row.original.block_percentage.toFixed(1)}%</em>}</strong></span>
    },
    {
      id: "last_seen",
      accessorKey: "last_seen",
      sortDescFirst: true,
      header: ({ column }) => <DeviceSortHeader column={column} label="Last DNS request" />,
      cell: ({ row }) => <span className="device-table-value"><small>Last DNS request</small><strong>{row.original.last_seen ? formatLastSeen(row.original.last_seen) : "No DNS requests"}</strong></span>
    },
    {
      id: "protection",
      accessorKey: "profile",
      header: ({ column }) => <DeviceSortHeader column={column} label="Protection" />,
      cell: ({ row }) => <span className="device-profile-badge">{row.original.profile}</span>
    },
    {
      id: "actions",
      header: () => <span aria-hidden="true" />,
      enableSorting: false,
      cell: () => <ChevronRight className="device-row-arrow" size={17} />
    }
  ], []);

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
        direction: sort.direction,
        activeToday: activityScope === "today",
        withHistory: activityScope === "history"
      }, conditional ? inventoryETag.current : "", controller.signal);
      if (controller.signal.aborted) return;
      if (result.page) {
        if (inventoryRevision.current !== null && inventoryRevision.current !== result.page.revision) {
          setDetailVersion((version) => version + 1);
        }
        inventoryRevision.current = result.page.revision;
        setInventory(result.page);
        inventoryETag.current = result.etag;
        if (result.page.page > Math.max(1, result.page.total_pages)) setPage(Math.max(1, result.page.total_pages));
      }
      setInventoryError("");
    } catch (error_) {
      if (error_ instanceof DOMException && error_.name === "AbortError") return;
      setInventoryError(error_ instanceof Error ? error_.message : "Could not refresh the device inventory.");
    } finally {
      if (inventoryRequest.current === controller) {
        inventoryBusy.current = false;
        setInventoryLoading(false);
      }
    }
  }, [activityScope, page, search, sort.direction, sort.key]);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      inventoryETag.current = "";
      void loadInventory(false);
    }, search ? 250 : 0);
    return () => {
      window.clearTimeout(timer);
      inventoryRequest.current?.abort();
    };
  }, [loadInventory, search, inventoryVersion]);

  useEffect(() => {
    const timer = window.setInterval(() => {
      if (document.visibilityState === "visible") void loadInventory(true);
    }, 15000);
    return () => window.clearInterval(timer);
  }, [loadInventory]);

  useEffect(() => {
    setPage(1);
  }, [activityScope, search, sorting]);

  const canonicalizeSelection = useEffectEvent((reference: string) => onSelectDevice(reference, true));
  useEffect(() => {
    if (!selectedDeviceRef) {
      detailSelection.current = null;
      setDetail(null);
      setEditing(false);
      setProtectionMenuOpen(false);
      return;
    }
    const previous = detailSelection.current;
    // Resolving a legacy IP replaces the URL with its stable ID. That detail is already here.
    if (previous?.reference === selectedDeviceRef && previous.version === detailVersion && previous.loaded) return;
    const selection = { reference: selectedDeviceRef, version: detailVersion, loaded: false };
    detailSelection.current = selection;
    const controller = new AbortController();
    if (previous?.reference !== selectedDeviceRef) {
      setDetail(detailCache.current.get(selectedDeviceRef) ?? null);
      setDetailLoading(true);
      setDetailError("");
      setEditing(false);
      setProtectionMenuOpen(false);
    }
    queueMicrotask(async () => {
      if (controller.signal.aborted) return;
      try {
        const updated = await api.device(selectedDeviceRef, controller.signal);
        if (controller.signal.aborted) return;
        selection.reference = deviceReference(updated);
        selection.loaded = true;
        const cache = detailCache.current;
        if (!cache.has(selection.reference) && cache.size >= 30) cache.delete(cache.keys().next().value!);
        cache.set(selection.reference, updated);
        setDetail(updated);
        setDetailError("");
        if (selectedDeviceRef !== selection.reference) canonicalizeSelection(selection.reference);
      } catch (failure) {
        if (!controller.signal.aborted) setDetailError(failure instanceof Error ? failure.message : "Failed to load device details.");
      } finally {
        if (!controller.signal.aborted) setDetailLoading(false);
      }
    });
    return () => controller.abort();
  }, [selectedDeviceRef, detailVersion]);

  const deviceTable = useTable({
    features: deviceTableFeatures,
    data: inventoryDevices,
    columns: deviceColumns,
    getRowId: (device) => device.device_id ? String(device.device_id) : device.client_ip,
    manualSorting: true,
    state: { sorting },
    onSortingChange: setSorting,
    enableSortingRemoval: false,
    enableMultiSort: false
  });

  const totals = {
    active: inventory.summary.active_today,
    requests: inventory.summary.requests_today,
    blocked: inventory.summary.blocked_today,
    blockedRate: inventory.summary.requests_today > 0
      ? (inventory.summary.blocked_today / inventory.summary.requests_today) * 100
      : 0
  };

  function editDevice(open: boolean) {
    if (open && detail) {
      setForm({ name: detail.name || "", location: detail.location ?? "", notes: detail.notes ?? "", device_type: detail.type_source === "manual" ? detail.device_type : "" });
    }
    setEditing(open);
  }

  function refreshAfterMutation() {
    inventoryRequest.current?.abort();
    inventoryRevision.current = null;
    setDetailVersion((version) => version + 1);
    setInventoryVersion((version) => version + 1);
    return refresh();
  }

  async function saveAlias(event: SubmitEvent) {
    event.preventDefault();
    if (!selectedDeviceRef || !detail) return;
    setAliasSaving(true);
    setDetailError("");
    try {
      await api.updateDeviceAlias(deviceReference(detail), form);
      if (detailSelection.current?.reference === deviceReference(detail)) setEditing(false);
      await refreshAfterMutation();
    } catch (error_) {
      if (detailSelection.current?.reference === deviceReference(detail)) setDetailError(error_ instanceof Error ? error_.message : "Could not save this device.");
    } finally {
      setAliasSaving(false);
    }
  }

  async function changeProtection(protectionID: number) {
    if (!selectedDeviceRef || !detail || protectionID === detail?.protection_id) return;
    setProtectionMenuOpen(false);
    setProtectionBusy(true);
    setDetailError("");
    try {
      await api.assignDeviceProtection(deviceReference(detail), protectionID);
      await refreshAfterMutation();
    } catch (error_) {
      if (detailSelection.current?.reference === deviceReference(detail)) setDetailError(error_ instanceof Error ? error_.message : "Could not change protection.");
    } finally {
      setProtectionBusy(false);
    }
  }

  if (!selectedDeviceRef && !inventoryLoading && !inventoryError && inventory.summary.observed === 0) {
    return <EmptyState title="No devices yet" body="Point a device or router at Faro to start seeing clients, names, blocked requests, and top domains." />;
  }

  return (
    <div className="devices-page">
      {view !== "troubleshoot" && <section className="device-summary-strip" aria-label="Device activity summary">
        <DeviceSummaryMetric icon={<MonitorSmartphone size={18} />} label="Known devices" value={inventory.summary.observed} detail={`${totals.active} active today`} />
        <DeviceSummaryMetric icon={<Activity size={18} />} label="Requests today" value={totals.requests.toLocaleString()} detail="Across all devices" />
        <DeviceSummaryMetric icon={<ShieldCheck size={18} />} label="Blocked today" value={totals.blocked.toLocaleString()} detail={`${totals.blockedRate.toFixed(1)}% of requests`} tone={totals.blocked > 0 ? "blocked" : "default"} />
        <DeviceSummaryMetric icon={<Clock3 size={18} />} label="Most active" value={inventory.summary.most_active_name || "None"} detail={`${inventory.summary.most_active_requests.toLocaleString()} requests today`} compact />
      </section>}

	  {view !== "troubleshoot" && inventory.summary.observed === 1 && totals.requests > 0 && (
		<section className="device-visibility-note" aria-label="Device visibility status">
		  <Network size={19} />
		  <div><strong>Faro currently sees one DNS source</strong><span>If that source is your router, Faro will keep protecting the network, but the router is hiding which device made each request.</span></div>
		</section>
	  )}

      <section className="panel device-inventory-panel" aria-label="Device inventory">
        <div className="device-inventory-header">
          {view === "troubleshoot" && <div>
            <h2>Fix a broken site</h2>
            <p>Choose the device where the site or app stopped working.</p>
          </div>}
          <div className="device-inventory-tools">
            <div className="device-search">
              <Search size={16} aria-hidden="true" />
              <Input variant="embedded" aria-label="Search devices" value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Search devices" />
              {search && <Button variant="ghost" size="icon-sm" aria-label="Clear search" onClick={() => setSearch("")}><X size={14} /></Button>}
            </div>
            <NativeSelect className="w-auto" aria-label="Filter devices by activity" value={activityScope} onChange={(event) => setActivityScope(event.target.value as DeviceActivityScope)}>
              <option value="history">With DNS history</option>
              <option value="today">Active today</option>
              <option value="all">All known devices</option>
            </NativeSelect>
          </div>
        </div>

        {inventoryError && <div className="device-refresh-error" role="alert">
          <AlertTriangle size={19} aria-hidden="true" />
          <div><strong>Couldn’t refresh devices</strong><p>{inventoryDevices.length > 0 ? "The last loaded devices are still shown. " : "The device inventory is unavailable. "}Try again in a moment.</p><details><summary>Technical details</summary><p>{inventoryError}</p></details></div>
          <Button variant="outline" size="sm" disabled={inventoryLoading} onClick={() => void loadInventory(false)}><RefreshCw size={14} />{inventoryLoading ? "Retrying…" : "Retry"}</Button>
        </div>}
        <div className="device-table" aria-busy={inventoryLoading}>
          {deviceTable.getHeaderGroups().map((headerGroup) => (
            <div className="device-table-header" key={headerGroup.id}>
              {headerGroup.headers.map((header) => (
                <Fragment key={header.id}>
                  {header.isPlaceholder ? null : <deviceTable.FlexRender header={header} />}
                </Fragment>
              ))}
            </div>
          ))}
          {deviceTable.getRowModel().rows.map((row) => (
            <button
              className={selectedDeviceRef === deviceReference(row.original) ? "device-table-row active" : "device-table-row"}
              key={row.id}
              type="button"
              onClick={() => onSelectDevice(deviceReference(row.original))}
              aria-pressed={selectedDeviceRef === deviceReference(row.original)}
            >
              {row.getAllCells().map((cell) => (
                <Fragment key={cell.id}>
                  <deviceTable.FlexRender cell={cell} />
                </Fragment>
              ))}
            </button>
          ))}
        </div>

        {inventoryLoading && inventoryDevices.length === 0 && <div className="device-inventory-loading" role="status"><RefreshCw size={18} className="animate-spin" />Loading devices…</div>}
        {!inventoryLoading && !inventoryError && inventoryDevices.length === 0 && (
          <div className="device-filter-empty">
            <Search size={20} />
            <strong>{search ? "No matching devices" : activityScope === "today" ? "No active devices today" : "No devices with DNS history"}</strong>
            <span>{search ? "Try a different name or IP address." : "All known devices includes clients discovered by UniFi."}</span>
            <Button variant="outline" type="button" onClick={() => { setActivityScope("all"); setSearch(""); }}>Show all known devices</Button>
          </div>
        )}
        {inventoryDevices.length > 0 && (
          <div className="device-pagination" aria-label="Device inventory pages">
            <span role="status">{inventoryLoading ? "Updating… · " : inventoryError ? "Last loaded · " : ""}Showing {(inventory.page - 1) * inventory.page_size + 1}–{Math.min(inventory.page * inventory.page_size, inventory.total)} of {inventory.total} devices</span>
            {inventory.total_pages > 1 && <div>
              <Button variant="outline" type="button" className="secondary" disabled={inventory.page <= 1 || inventoryLoading} onClick={() => setPage((current) => Math.max(1, current - 1))}>Previous</Button>
              <span>Page {inventory.page} of {inventory.total_pages}</span>
              <Button variant="outline" type="button" className="secondary" disabled={inventory.page >= inventory.total_pages || inventoryLoading} onClick={() => setPage((current) => current + 1)}>Next</Button>
            </div>}
          </div>
        )}
      </section>

      {selectedDeviceRef && <Suspense fallback={null}><DeviceDrawer onRulesChanged={refresh} selectedDeviceRef={selectedDeviceRef} detail={detail} detailLoading={detailLoading} detailError={detailError} view={view} setView={onViewChange} protections={protections} activeProtection={activeProtection} activeProtectionName={activeProtectionName} protectionBusy={protectionBusy} protectionMenuOpen={protectionMenuOpen} setProtectionMenuOpen={setProtectionMenuOpen} changeProtection={changeProtection} editing={editing} setEditing={editDevice} form={form} setForm={setForm} aliasSaving={aliasSaving} saveAlias={saveAlias} onSelectDevice={onSelectDevice} onDomainSelect={onDomainSelect} /></Suspense>}
    </div>
  );
}

function DeviceSortHeader({ column, label }: { readonly column: Column<typeof deviceTableFeatures, DeviceSummary, unknown>; readonly label: string }) {
  const sorted = column.getIsSorted();
  const active = sorted !== false;
  const DirectionIcon = sorted === "desc" ? ArrowDown : ArrowUp;
  const directionLabel = sorted === "desc" ? "descending" : "ascending";
  const sortLabel = active ? `Sort by ${label}, currently ${directionLabel}` : `Sort by ${label}`;
  return <button type="button" className={active ? "device-sort-header active" : "device-sort-header"} onClick={column.getToggleSortingHandler()} aria-label={sortLabel}>
    <span>{label}</span>{active && <DirectionIcon size={13} strokeWidth={2.5} aria-hidden="true" />}
  </button>;
}

const emptyInventory: DeviceInventoryPage = {
  items: [], page: 1, page_size: 50, total: 0, total_pages: 0, revision: "",
  summary: { observed: 0, active_today: 0, requests_today: 0, blocked_today: 0, most_active_name: "", most_active_requests: 0 }
};

function DeviceSummaryMetric({ icon, label, value, detail, tone = "default", compact = false }: {
  readonly icon: ReactNode;
  readonly label: string;
  readonly value: string | number;
  readonly detail: string;
  readonly tone?: "default" | "blocked";
  readonly compact?: boolean;
}) {
  return <div className={`device-summary-metric ${tone} ${compact ? "compact" : ""}`}><span className="device-summary-icon">{icon}</span><span><small>{label}</small><strong>{value}</strong><em>{detail}</em></span></div>;
}

function deviceIdentityCaption(device: DeviceSummary) {
  const address = device.client_ip || (device.last_query_ip ? `Last IP ${device.last_query_ip}` : "No current address");
  const more = device.addresses && device.addresses.length > 1 ? `+${device.addresses.length - 1} more` : "";
  return [address !== deviceDisplayName(device) && address, more, device.device_type !== "Unknown" && device.device_type, device.location].filter(Boolean).join(" · ");
}
