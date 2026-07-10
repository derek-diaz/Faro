import { Ban, CheckCircle2, Filter, Search, ShieldCheck, SlidersHorizontal, X } from "lucide-react";
import { useMemo, useState } from "react";
import { api, type FaroEvent } from "../api/client";
import { DomainFavicon } from "../components/DomainFavicon";
import { EmptyState } from "../components/EmptyState";

type QueryLogProps = {
  events: FaroEvent[];
  refresh: (search?: string) => Promise<void>;
  onDomainSelect: (domain: string) => void;
  onDeviceSelect: (clientIP: string) => void;
};

type EventFilter = "all" | "dns" | "blocked" | "system";

export function QueryLog({ events, refresh, onDomainSelect, onDeviceSelect }: QueryLogProps) {
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState<EventFilter>("all");
  const [busy, setBusy] = useState<string | null>(null);

  const counts = useMemo(() => ({
    all: events.length,
    dns: events.filter((event) => event.type === "dns.query" || event.type === "dns.blocked").length,
    blocked: events.filter((event) => event.type === "dns.blocked").length,
    system: events.filter((event) => event.type !== "dns.query" && event.type !== "dns.blocked").length
  }), [events]);

  const visibleEvents = useMemo(() => events.filter((event) => {
    if (filter === "dns") return event.type === "dns.query" || event.type === "dns.blocked";
    if (filter === "blocked") return event.type === "dns.blocked";
    if (filter === "system") return event.type !== "dns.query" && event.type !== "dns.blocked";
    return true;
  }), [events, filter]);

  async function addRule(domain: string, action: "allow" | "block") {
    setBusy(`${action}:${domain}`);
    try {
      if (action === "allow") await api.addAllow(domain);
      else await api.addBlock(domain);
      await refresh(search);
    } finally {
      setBusy(null);
    }
  }

  function clearSearch() {
    setSearch("");
    void refresh();
  }

  return (
    <div className="activity-explorer">
      <section className="activity-controls">
        <form
          className="activity-search"
          onSubmit={(event) => {
            event.preventDefault();
            void refresh(search);
          }}
        >
          <Search size={17} />
          <input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Search domains, devices, or events" />
          <button className="clear-search" type="button" disabled={!search} onClick={clearSearch} aria-label="Clear search"><X size={16} /></button>
          <button className="search-submit" type="submit">Search</button>
        </form>
        <div className="activity-scope"><SlidersHorizontal size={16} /><span>Latest network events</span></div>
      </section>

      <section className="activity-summary" aria-label="Activity summary">
        <ActivityStat label="All events" value={counts.all} />
        <ActivityStat label="DNS requests" value={counts.dns} />
        <ActivityStat label="Blocked" value={counts.blocked} tone="blocked" />
        <ActivityStat label="System changes" value={counts.system} />
      </section>

      <section className="panel activity-results-panel">
        <div className="activity-results-toolbar">
          <div className="filter-label"><Filter size={16} /><span>Filter</span></div>
          <div className="event-filter-tabs" role="group" aria-label="Filter activity">
            <FilterButton active={filter === "all"} label="All" count={counts.all} onClick={() => setFilter("all")} />
            <FilterButton active={filter === "dns"} label="DNS" count={counts.dns} onClick={() => setFilter("dns")} />
            <FilterButton active={filter === "blocked"} label="Blocked" count={counts.blocked} onClick={() => setFilter("blocked")} />
            <FilterButton active={filter === "system"} label="System" count={counts.system} onClick={() => setFilter("system")} />
          </div>
          <span className="results-count">Showing {visibleEvents.length} events</span>
        </div>

        {visibleEvents.length === 0 ? (
          <EmptyState title="No matching activity" body="Try another filter or point a device at Faro to begin collecting DNS activity." />
        ) : (
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
                      <td><span className="event-type-chip">{friendlyType(event.type)}</span></td>
                      <td className="muted-column">{event.source}</td>
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
      </section>
    </div>
  );
}

function ActivityStat({ label, value, tone = "default" }: { label: string; value: number; tone?: "default" | "blocked" }) {
  return <div className={`activity-stat ${tone}`}><span>{label}</span><strong>{value}</strong></div>;
}

function FilterButton({ active, label, count, onClick }: { active: boolean; label: string; count: number; onClick: () => void }) {
  return <button className={active ? "active" : ""} type="button" onClick={onClick}>{label}<span>{count}</span></button>;
}

function EventResult({ event }: { event: FaroEvent }) {
  if (event.type === "dns.blocked") return <span className="event-result blocked">Blocked</span>;
  if (event.type === "dns.query") return <span className="event-result allowed">Allowed</span>;
  if (event.severity === "critical") return <span className="event-result critical">Failed</span>;
  return <span className="event-result system">System</span>;
}

function EventMark({ event }: { event: FaroEvent }) {
  return event.severity === "critical" ? <Ban size={16} /> : <CheckCircle2 size={16} />;
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

function formatTime(timestamp: string) {
  return new Date(timestamp).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

function formatDate(timestamp: string) {
  const date = new Date(timestamp);
  const today = new Date();
  return date.toDateString() === today.toDateString() ? "Today" : date.toLocaleDateString([], { month: "short", day: "numeric" });
}
