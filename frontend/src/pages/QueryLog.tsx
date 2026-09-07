import { Table } from "../components/ui/table";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { AlertTriangle, Ban, CheckCircle2, ChevronLeft, ChevronRight, RefreshCw, Search, ShieldCheck, X } from "lucide-react";
import { tableFeatures, useTable } from "@tanstack/react-table";
import type { ColumnDef } from "@tanstack/react-table";
import { lazy, Suspense, useCallback, useEffect, useMemo, useState } from "react";
import { api, type ActivityPage, type ActivityRows, type ActivitySummary, type FaroEvent } from "../api/client";
import { ActivityTimePicker, activityTimeRangeLabel, type ActivityTimeRange } from "../components/ActivityTimePicker";
import { ActivityTableLoading, ActivityTimelineLoading } from "../components/ActivityLoading";
import { DomainFavicon } from "../components/DomainFavicon";
import { EmptyState } from "../components/EmptyState";
import { ResolutionSource } from "../components/ResolutionSource";
import { formatDate, formatTime } from "../utils/dateFormatting";

const ActivityTimeline = lazy(async () => {
  const module = await import("../components/ActivityTimeline");
  return { default: module.ActivityTimeline };
});

type QueryLogProps = {
  readonly onDomainSelect: (domain: string) => void;
  readonly onDeviceSelect: (clientIP: string) => void;
};

type EventFilter = "all" | "dns" | "cache" | "upstream" | "blocked" | "system";

const PAGE_SIZE = 50;
const emptyActivity: ActivityPage = {
  items: [],
  page: 1,
  page_size: PAGE_SIZE,
  total: 0,
  total_pages: 0,
  counts: { all: 0, dns: 0, cache: 0, upstream: 0, blocked: 0, system: 0 },
  timeline: null
};

const queryLogFeatures = tableFeatures({});

export function QueryLog({ onDomainSelect, onDeviceSelect }: QueryLogProps) {
  const [searchInput, setSearchInput] = useState("");
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState<EventFilter>("all");
  const [page, setPage] = useState(1);
  const [activity, setActivity] = useState<ActivityPage>(emptyActivity);
  const [loading, setLoading] = useState(true);
  const [hasLoaded, setHasLoaded] = useState(false);
  const [summaryLoading, setSummaryLoading] = useState(true);
  const [summaryError, setSummaryError] = useState("");
  const [hasMore, setHasMore] = useState(false);
  const [loadError, setLoadError] = useState("");
  const [timeRange, setTimeRange] = useState<ActivityTimeRange>({ preset: "24h" });
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [busy, setBusy] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    let inFlight = false;
    setLoading(true);
    setLoadError("");
    const loadRows = async () => {
      if (inFlight || controller.signal.aborted) return;
      inFlight = true;
      try {
        const result = await api.events<ActivityRows>(search, filter, page, PAGE_SIZE, timeRange.preset, timeRange.from, timeRange.to, controller.signal, "rows");
        if (!controller.signal.aborted) {
          setActivity((current) => ({ ...current, items: result.items, page: result.page, page_size: result.page_size }));
          setHasMore(Boolean(result.has_more));
          setHasLoaded(true);
          setLoadError("");
        }
      } catch (failure) {
        if (!controller.signal.aborted) { setLoadError(failure instanceof Error ? failure.message : "Activity could not be loaded."); setHasLoaded(true); }
      } finally {
        inFlight = false;
        if (!controller.signal.aborted) setLoading(false);
      }
    };
    void loadRows();
    const timer = window.setInterval(() => { if (page === 1 && document.visibilityState === "visible") void loadRows(); }, 5000);
    return () => { controller.abort(); window.clearInterval(timer); };
  }, [search, filter, page, refreshVersion, timeRange]);

  useEffect(() => {
    const controller = new AbortController();
    let inFlight = false;
    setSummaryLoading(true);
    setSummaryError("");
    const loadSummary = async () => {
      if (inFlight || controller.signal.aborted) return;
      inFlight = true;
      try {
        const result = await api.events<ActivitySummary>(search, filter, 1, PAGE_SIZE, timeRange.preset, timeRange.from, timeRange.to, controller.signal, "summary");
        if (!controller.signal.aborted) {
          setActivity((current) => ({ ...current, counts: result.counts, timeline: result.timeline, total: result.total, total_pages: result.total_pages }));
          setSummaryError("");
        }
      } catch (failure) {
        if (!controller.signal.aborted) setSummaryError(failure instanceof Error ? failure.message : "Activity totals are unavailable.");
      } finally {
        inFlight = false;
        if (!controller.signal.aborted) setSummaryLoading(false);
      }
    };
    void loadSummary();
    const timer = window.setInterval(() => { if (document.visibilityState === "visible") void loadSummary(); }, 30000);
    return () => { controller.abort(); window.clearInterval(timer); };
  }, [search, filter, refreshVersion, timeRange]);

  const counts = activity.counts;
  const visibleEvents = activity.items;
  const firstResult = activity.items.length === 0 ? 0 : (activity.page - 1) * activity.page_size + 1;
  const lastResult = firstResult ? firstResult + activity.items.length - 1 : 0;
  const totalsReady = !summaryLoading && !summaryError;
  const resultTotal = totalsReady ? ` of ${Math.max(activity.total, lastResult).toLocaleString()}` : "";
  const initialLoading = loading && !hasLoaded && !loadError;
  const loadingRows = loading && !loadError && visibleEvents.length === 0;

  const addRule = useCallback(async (domain: string, action: "allow" | "block") => {
    setBusy(`${action}:${domain}`);
    try {
      if (action === "allow") await api.addAllow(domain);
      else await api.addBlock(domain);
      setRefreshVersion((current) => current + 1);
    } finally {
      setBusy(null);
    }
  }, []);

  function clearSearch() {
    setSearchInput("");
    setSearch("");
    setPage(1);
    setRefreshVersion((current) => current + 1);
  }

  function selectFilter(nextFilter: EventFilter) {
    setFilter(nextFilter);
    setPage(1);
  }

  const eventColumns = useMemo<ColumnDef<typeof queryLogFeatures, FaroEvent>[]>(() => [
    {
      accessorKey: "timestamp",
      header: "Time",
      cell: ({ row }) => <><strong>{formatTime(row.original.timestamp)}</strong><span>{formatDate(row.original.timestamp)}</span></>
    },
    {
      id: "result",
      header: "Result",
      cell: ({ row }) => <EventResult event={row.original} />
    },
    {
      id: "domain",
      header: "Domain or event",
      cell: ({ row }) => {
        const event = row.original;
        const isDNS = event.type === "dns.query" || event.type === "dns.blocked";
        const domain = event.domain ?? "";
        return domain ? (
          <>
            <button className="table-domain-link" type="button" onClick={() => onDomainSelect(domain)}>
              <DomainFavicon domain={domain} />
              <span>{domain}</span>
            </button>
            <small>{isDNS ? event.description : event.description || event.title}</small>
          </>
        ) : (
          <span className="system-event-title"><EventMark event={event} /><strong>{event.title}</strong></span>
        );
      }
    },
    {
      id: "device",
      header: "Device",
      cell: ({ row }) => row.original.client_ip
        ? <button className="device-link" type="button" onClick={() => onDeviceSelect(row.original.client_ip!)} title={row.original.client_ip}>{row.original.device_name || row.original.client_ip}</button>
        : <span className="empty-cell">—</span>
    },
    {
      id: "type",
      header: "Type",
      cell: ({ row }) => <EventType event={row.original} />
    },
    {
      id: "source",
      header: "Source",
      cell: ({ row }) => <ResolutionSource source={row.original.source} upstream={eventUpstream(row.original)} />
    },
    {
      id: "actions",
      header: () => <span className="sr-only">Actions</span>,
      enableSorting: false,
      cell: ({ row }) => {
        const event = row.original;
        const domain = event.domain ?? "";
        const isBlocked = event.type === "dns.blocked";
        return domain ? (
          <div className="table-icon-actions">
            <Button variant="ghost" size="icon-sm" type="button" title={`Block ${domain}`} aria-label={`Block ${domain}`} disabled={busy !== null || isBlocked} onClick={() => void addRule(domain, "block")}><Ban size={16} /></Button>
            <Button variant="ghost" size="icon-sm" type="button" title={`Allow ${domain}`} aria-label={`Allow ${domain}`} disabled={busy !== null} onClick={() => void addRule(domain, "allow")}><ShieldCheck size={16} /></Button>
          </div>
        ) : null;
      }
    }
  ], [addRule, busy, onDeviceSelect, onDomainSelect]);

  const eventTable = useTable({
    features: queryLogFeatures,
    data: visibleEvents,
    columns: eventColumns,
    getRowId: (event) => event.id
  });

  const rangeLabel = activityTimeRangeLabel(timeRange);

  return (
    <div className="activity-explorer" data-loading={initialLoading ? "initial" : loading ? "refreshing" : undefined}>
      <section className="activity-controls">
        <form
          className="activity-search"
          onSubmit={(event) => {
            event.preventDefault();
            setSearch(searchInput.trim());
            setPage(1);
            setRefreshVersion((current) => current + 1);
          }}
        >
          <Search size={17} />
          <Input variant="embedded" aria-label="Search activity" value={searchInput} onChange={(event) => setSearchInput(event.target.value)} placeholder="Search domains, devices, or events" />
          {(searchInput || search) && <Button variant="ghost" size="icon-sm" type="button" onClick={clearSearch} aria-label="Clear search"><X size={16} /></Button>}
          <Button type="submit">Search</Button>
        </form>
      </section>

      <section className="panel activity-timeline-panel" aria-label="Activity timeline" aria-busy={summaryLoading}>
        <div className="activity-timeline-header">
          <h2>Activity over time</h2>
          <ActivityTimePicker value={timeRange} onChange={(nextRange) => { setTimeRange(nextRange); setPage(1); }} />
        </div>
        <Suspense fallback={<ActivityTimelineLoading />}>
          {summaryError ? <div className="activity-chart-unavailable">The timeline couldn’t be loaded. Use Retry below to refresh activity.</div> : <ActivityTimeline timeline={activity.timeline} rangeLabel={rangeLabel} loading={summaryLoading} />}
        </Suspense>
      </section>

      <section className="activity-summary" aria-label="Activity summary" aria-busy={summaryLoading}>
        <ActivityStat label="All events" value={counts.all} loading={!totalsReady} />
        <ActivityStat label="DNS requests" value={counts.dns} loading={!totalsReady} />
        <ActivityStat label="Blocked" value={counts.blocked} loading={!totalsReady} />
        <ActivityStat label="System changes" value={counts.system} loading={!totalsReady} />
      </section>

      <section className="panel activity-results-panel" aria-busy={loading}>
        <div className="activity-results-toolbar">
          <fieldset className="event-filter-tabs">
            <legend className="sr-only">Filter activity</legend>
            <FilterButton active={filter === "all"} label="All" count={counts.all} loading={!totalsReady} onClick={() => selectFilter("all")} />
            <FilterButton active={filter === "dns"} label="DNS" count={counts.dns} loading={!totalsReady} onClick={() => selectFilter("dns")} />
            <FilterButton active={filter === "cache"} label="Cache" count={counts.cache} loading={!totalsReady} onClick={() => selectFilter("cache")} />
            <FilterButton active={filter === "upstream"} label="Upstream" count={counts.upstream} loading={!totalsReady} onClick={() => selectFilter("upstream")} />
            <FilterButton active={filter === "blocked"} label="Blocked" count={counts.blocked} loading={!totalsReady} onClick={() => selectFilter("blocked")} />
            <FilterButton active={filter === "system"} label="System" count={counts.system} loading={!totalsReady} onClick={() => selectFilter("system")} />
          </fieldset>
          <span className="results-count" role={loading ? "status" : undefined}>{loading ? (initialLoading ? "Preparing activity…" : "Updating activity…") : `Showing ${firstResult}–${lastResult}${resultTotal}`}</span>
        </div>

        {(loadError || summaryError) && <div className="activity-refresh-error" role="alert">
          <AlertTriangle size={18} aria-hidden="true" />
          <div><strong>{loadError ? "Couldn’t refresh activity" : "Activity totals unavailable"}</strong><p>{loadError ? (visibleEvents.length ? "The last loaded events are still shown. Try again in a moment." : "Try again in a moment to load events.") : "The event list is available, but totals and the timeline couldn’t be updated."}</p><details><summary>Technical details</summary><p>{[loadError, summaryError].filter(Boolean).join(" · ")}</p></details></div>
          <Button variant="outline" size="sm" disabled={loading || summaryLoading} onClick={() => setRefreshVersion((value) => value + 1)}><RefreshCw size={14} />Retry</Button>
        </div>}
        {!loadError && loadingRows && <ActivityTableLoading />}
        {!loadError && !loading && visibleEvents.length === 0 && <EmptyState title="No matching activity" body="Try another filter or point a device at Faro to begin collecting DNS activity." />}
        {!loadingRows && visibleEvents.length > 0 && (
          <div className="activity-table-wrap">
            <Table className="monitor-table event-table">
              <thead>
                {eventTable.getHeaderGroups().map((headerGroup) => (
                  <tr key={headerGroup.id}>
                    {headerGroup.headers.map((header) => (
                      <th key={header.id}>
                        {header.isPlaceholder ? null : <eventTable.FlexRender header={header} />}
                      </th>
                    ))}
                  </tr>
                ))}
              </thead>
              <tbody>
                {eventTable.getRowModel().rows.map((row) => (
                  <tr key={row.id}>
                    {row.getAllCells().map((cell) => (
                      <td key={cell.id} className={eventCellClass(cell.column.id)}>
                        <eventTable.FlexRender cell={cell} />
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </Table>
          </div>
        )}
        {activity.items.length > 0 && (
          <div className="activity-pagination" aria-label="Activity pages">
            <span>{firstResult}–{lastResult}{resultTotal} events</span>
            <div>
              <Button variant="outline" type="button" disabled={loading || page <= 1} onClick={() => setPage((current) => Math.max(1, current - 1))}><ChevronLeft size={16} /> Newer</Button>
              <strong>Page {activity.page}{totalsReady ? ` of ${Math.max(activity.total_pages, activity.page)}` : ""}</strong>
              <Button variant="outline" type="button" disabled={loading || !hasMore} onClick={() => setPage((current) => current + 1)}>Older <ChevronRight size={16} /></Button>
            </div>
          </div>
        )}
      </section>
    </div>
  );
}

function eventCellClass(columnID: string) {
  if (columnID === "timestamp") return "time-cell";
  if (columnID === "domain") return "event-subject-cell";
  if (columnID === "actions") return "event-actions-cell";
  return undefined;
}

function eventUpstream(event: FaroEvent) {
  const value = event.metadata?.upstream;
  return typeof value === "string" ? value : null;
}

function ActivityStat({ label, value, loading = false }: { readonly label: string; readonly value: number; readonly loading?: boolean }) {
  return <dl className="activity-metric"><dt>{label}</dt><dd>{loading ? <><span className="activity-count-skeleton" aria-hidden="true" /><span className="sr-only">Loading</span></> : value.toLocaleString()}</dd></dl>;
}

function FilterButton({ active, label, count, loading = false, onClick }: { readonly active: boolean; readonly label: string; readonly count: number; readonly loading?: boolean; readonly onClick: () => void }) {
  return <Button variant="ghost" size="sm" className={active ? "bg-secondary text-accent-foreground" : undefined} aria-pressed={active} type="button" onClick={onClick}>{label}{loading ? <span className="activity-count-skeleton" aria-hidden="true" /> : <span>{count.toLocaleString()}</span>}</Button>;
}

function EventResult({ event }: { readonly event: FaroEvent }) {
  if (event.type === "dns.blocked") return <span className="event-result blocked">Blocked</span>;
  if (event.type === "dns.query") return <span className="event-result allowed">Allowed</span>;
  if (event.severity === "critical") return <span className="event-result critical">Failed</span>;
  return <span className="event-result system">System</span>;
}

function EventMark({ event }: { readonly event: FaroEvent }) {
  return event.severity === "critical" ? <Ban size={16} /> : <CheckCircle2 size={16} />;
}

function EventType({ event }: { readonly event: FaroEvent }) {
  const type = typeof event.metadata?.query_type === "string" ? event.metadata.query_type.toUpperCase() : "";
  if ((event.type === "dns.query" || event.type === "dns.blocked") && type) {
    const family = recordFamily(type);
    return (
      <span className="event-type-chip dns-record-type" title={`${type} record request (${family})`}>
        <strong>{type}</strong>
      </span>
    );
  }
  return <span className="event-type-chip">{friendlyType(event.type)}</span>;
}

function friendlyType(type: string) {
  const labels: Record<string, string> = {
    "dns.query": "DNS query",
    "dns.blocked": "DNS blocked",
    "device.first_seen": "New device",
    "device.alias_updated": "Device update",
    "blocklist.installed": "Blocklist",
    "blocklist.updated": "Blocklist",
    "dns.reload": "DNS reload",
    "dns.reload_failed": "DNS reload",
    "upstream.changed": "Upstream"
  };
  return labels[type] ?? type.replace(/\./g, " ").replace(/_/g, " ");
}

function recordFamily(type: string) {
  if (type === "A") return "IPv4";
  if (type === "AAAA") return "IPv6";
  return "DNS";
}
