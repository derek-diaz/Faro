import { Activity, Ban, CheckCircle2, ChevronLeft, ChevronRight, Filter, Globe2, Search, Settings2, ShieldCheck, ShieldX, X } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { api, type ActivityPage, type FaroEvent } from "../api/client";
import { DomainFavicon } from "../components/DomainFavicon";
import { EmptyState } from "../components/EmptyState";
import { ResolutionSource } from "../components/ResolutionSource";
import { formatDate, formatTime } from "../utils/dateFormatting";

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
  counts: { all: 0, dns: 0, cache: 0, upstream: 0, blocked: 0, system: 0 }
};

export function QueryLog({ onDomainSelect, onDeviceSelect }: QueryLogProps) {
  const [searchInput, setSearchInput] = useState("");
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState<EventFilter>("all");
  const [page, setPage] = useState(1);
  const [activity, setActivity] = useState<ActivityPage>(emptyActivity);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [busy, setBusy] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setLoadError("");
    api.events(search, filter, page, PAGE_SIZE)
      .then((result) => { if (active) setActivity(result); })
      .catch((error_) => { if (active) setLoadError(error_ instanceof Error ? error_.message : "Activity could not be loaded."); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [search, filter, page, refreshVersion]);

  useEffect(() => {
    if (page !== 1) return undefined;
    const timer = window.setInterval(() => {
      void api.events(search, filter, 1, PAGE_SIZE).then(setActivity).catch(() => undefined);
    }, 5000);
    return () => window.clearInterval(timer);
  }, [search, filter, page]);

  const counts = activity.counts;
  const visibleEvents = activity.items;
  const firstResult = activity.total === 0 ? 0 : (activity.page - 1) * activity.page_size + 1;
  const lastResult = Math.min(activity.page * activity.page_size, activity.total);

  async function addRule(domain: string, action: "allow" | "block") {
    setBusy(`${action}:${domain}`);
    try {
      if (action === "allow") await api.addAllow(domain);
      else await api.addBlock(domain);
      setRefreshVersion((current) => current + 1);
    } finally {
      setBusy(null);
    }
  }

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

  return (
    <div className="activity-explorer">
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
          <input value={searchInput} onChange={(event) => setSearchInput(event.target.value)} placeholder="Search domains, devices, or events" />
          <button className="clear-search" type="button" disabled={!searchInput && !search} onClick={clearSearch} aria-label="Clear search"><X size={16} /></button>
          <button className="search-submit" type="submit">Search</button>
        </form>
      </section>

      <section className="activity-summary" aria-label="Activity summary">
        <ActivityStat icon={<Activity size={16} />} label="All events" value={counts.all} tone="all" />
        <ActivityStat icon={<Globe2 size={16} />} label="DNS requests" value={counts.dns} tone="dns" />
        <ActivityStat icon={<ShieldX size={16} />} label="Blocked" value={counts.blocked} tone="blocked" />
        <ActivityStat icon={<Settings2 size={16} />} label="System changes" value={counts.system} tone="system" />
      </section>

      <section className="panel activity-results-panel">
        <div className="activity-results-toolbar">
          <div className="filter-label"><Filter size={16} /><span>Filter</span></div>
          <fieldset className="event-filter-tabs">
            <legend className="sr-only">Filter activity</legend>
            <FilterButton active={filter === "all"} label="All" count={counts.all} onClick={() => selectFilter("all")} />
            <FilterButton active={filter === "dns"} label="DNS" count={counts.dns} onClick={() => selectFilter("dns")} />
            <FilterButton active={filter === "cache"} label="Cache" count={counts.cache} onClick={() => selectFilter("cache")} />
            <FilterButton active={filter === "upstream"} label="Upstream" count={counts.upstream} onClick={() => selectFilter("upstream")} />
            <FilterButton active={filter === "blocked"} label="Blocked" count={counts.blocked} onClick={() => selectFilter("blocked")} />
            <FilterButton active={filter === "system"} label="System" count={counts.system} onClick={() => selectFilter("system")} />
          </fieldset>
          <span className="results-count">{loading ? "Loading…" : `Showing ${firstResult}–${lastResult} of ${activity.total}`}</span>
        </div>

        {loadError && <EmptyState title="Activity unavailable" body={loadError} />}
        {!loadError && visibleEvents.length === 0 && <EmptyState title="No matching activity" body="Try another filter or point a device at Faro to begin collecting DNS activity." />}
        {!loadError && visibleEvents.length > 0 && (
          <div className="activity-table-wrap">
            <table className="monitor-table event-table">
              <thead>
                <tr>
                  <th>Time</th>
                  <th>Result</th>
                  <th>Domain or event</th>
                  <th>Device</th>
                  <th>Type</th>
                  <th>Source</th>
                  <th><span className="sr-only">Actions</span></th>
                </tr>
              </thead>
              <tbody>
                {visibleEvents.map((event) => {
                  const isDNS = event.type === "dns.query" || event.type === "dns.blocked";
                  const isBlocked = event.type === "dns.blocked";
                  const domain = event.domain ?? "";
                  return (
                    <tr key={event.id}>
                      <td className="time-cell">
                        <strong>{formatTime(event.timestamp)}</strong>
                        <span>{formatDate(event.timestamp)}</span>
                      </td>
                      <td><EventResult event={event} /></td>
                      <td className="event-subject-cell">
                        {domain ? (
                          <button className="table-domain-link" type="button" onClick={() => onDomainSelect(domain)}>
                            <DomainFavicon domain={domain} />
                            <span>{domain}</span>
                          </button>
                        ) : (
                          <span className="system-event-title"><EventMark event={event} /><strong>{event.title}</strong></span>
                        )}
                        <small>{isDNS ? event.description : event.description || event.title}</small>
                      </td>
                      <td>
                        {event.client_ip ? (
                          <button className="device-link" type="button" onClick={() => onDeviceSelect(event.client_ip!)}>{event.client_ip}</button>
                        ) : <span className="empty-cell">—</span>}
                      </td>
                      <td><EventType event={event} /></td>
                      <td><ResolutionSource source={event.source} upstream={eventUpstream(event)} /></td>
                      <td className="event-actions-cell">
                        {domain && (
                          <div className="table-icon-actions">
                            <button type="button" title={`Block ${domain}`} aria-label={`Block ${domain}`} disabled={busy !== null || isBlocked} onClick={() => void addRule(domain, "block")}><Ban size={16} /></button>
                            <button type="button" title={`Allow ${domain}`} aria-label={`Allow ${domain}`} disabled={busy !== null} onClick={() => void addRule(domain, "allow")}><ShieldCheck size={16} /></button>
                          </div>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
        {activity.total > 0 && (
          <div className="activity-pagination" aria-label="Activity pages">
            <span>{firstResult}–{lastResult} of {activity.total.toLocaleString()} events</span>
            <div>
              <button type="button" disabled={loading || page <= 1} onClick={() => setPage((current) => Math.max(1, current - 1))}><ChevronLeft size={16} /> Newer</button>
              <strong>Page {activity.page} of {Math.max(activity.total_pages, 1)}</strong>
              <button type="button" disabled={loading || page >= activity.total_pages} onClick={() => setPage((current) => current + 1)}>Older <ChevronRight size={16} /></button>
            </div>
          </div>
        )}
      </section>
    </div>
  );
}

function eventUpstream(event: FaroEvent) {
  const value = event.metadata?.upstream;
  return typeof value === "string" ? value : null;
}

function ActivityStat({ icon, label, value, tone = "all" }: { readonly icon: ReactNode; readonly label: string; readonly value: number; readonly tone?: "all" | "dns" | "blocked" | "system" }) {
  return <div className={`activity-stat ${tone}`}><span className="activity-stat-icon" aria-hidden="true">{icon}</span><div className="activity-stat-copy"><span>{label}</span><strong>{value}</strong></div></div>;
}

function FilterButton({ active, label, count, onClick }: { readonly active: boolean; readonly label: string; readonly count: number; readonly onClick: () => void }) {
  return <button className={active ? "active" : ""} type="button" onClick={onClick}>{label}<span>{count}</span></button>;
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
        <small>{family}</small>
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
