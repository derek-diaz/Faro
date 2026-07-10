import { Ban, ShieldCheck, X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { api, type DNSQuery, type DomainSummary } from "../api/client";
import { DomainFavicon } from "./DomainFavicon";
import { StatusBadge } from "./StatusBadge";

type DomainDrawerProps = {
  domain: string | null;
  onClose: () => void;
  onChanged: () => Promise<void>;
};

export function DomainDrawer({ domain, onClose, onChanged }: DomainDrawerProps) {
  const [summary, setSummary] = useState<DomainSummary | null>(null);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState<"allow" | "block" | null>(null);

  useEffect(() => {
    if (!domain) {
      setSummary(null);
      return;
    }
    let cancelled = false;
    setLoading(true);
    api
      .domainSummary(domain)
      .then((nextSummary) => {
        if (!cancelled) setSummary(nextSummary);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [domain]);

  const recentActivity = useMemo(() => groupRecentQueries(summary?.recent_queries ?? []), [summary?.recent_queries]);

  async function addRule(action: "allow" | "block") {
    if (!domain) return;
    setBusy(action);
    try {
      if (action === "allow") await api.addAllow(domain);
      else await api.addBlock(domain);
      setSummary(await api.domainSummary(domain));
      await onChanged();
    } finally {
      setBusy(null);
    }
  }

  if (!domain) return null;

  return (
    <div className="drawer-backdrop" onClick={onClose}>
      <aside className="domain-drawer domain-inspector" onClick={(event) => event.stopPropagation()} aria-label={`${domain} details`}>
        <header className="domain-inspector-header">
          <div className="drawer-domain-title">
            <DomainFavicon domain={domain} />
            <div>
              <strong>{domain}</strong>
              <span className={`domain-status ${summary?.status.toLowerCase() ?? "loading"}`}>{summary?.status ?? "Loading"}</span>
            </div>
          </div>
          <button className="icon-button" type="button" onClick={onClose} aria-label="Close domain details">
            <X size={18} />
          </button>
        </header>

        {loading && <div className="drawer-loading">Loading domain activity...</div>}

        {!loading && summary && (
          <div className="domain-inspector-body">
            <div className="domain-action-bar">
              {summary.status !== "Blocked" && (
                <button type="button" onClick={() => void addRule("block")} disabled={busy !== null}>
                  <Ban size={16} />
                  <span>{summary.status === "Mixed" ? "Block all" : "Block domain"}</span>
                </button>
              )}
              {summary.status !== "Allowed" && (
                <button type="button" className="secondary" onClick={() => void addRule("allow")} disabled={busy !== null}>
                  <ShieldCheck size={16} />
                  <span>{summary.status === "Mixed" ? "Allow all" : "Allow domain"}</span>
                </button>
              )}
            </div>

            <section className="domain-overview" aria-label="Domain overview">
              <OverviewItem label="Queries today" value={summary.total_queries_today} />
              <OverviewItem label="Blocked today" value={summary.blocked_queries_today} />
              <OverviewItem label="First seen" value={formatDate(summary.first_seen)} />
              <OverviewItem label="Last seen" value={formatDate(summary.last_seen)} />
            </section>

            <section className="inspector-section">
              <h3>Observed traffic</h3>
              <div className="domain-fact-row">
                <span>Devices</span>
                <div className="fact-chips">
                  {summary.clients.length ? summary.clients.map((item) => <span key={item.label}>{item.label}<strong>{item.count}</strong></span>) : <em>None yet</em>}
                </div>
              </div>
              <div className="domain-fact-row">
                <span>Query types</span>
                <div className="fact-chips">
                  {summary.query_types.length ? summary.query_types.map((item) => <span key={item.label}>{item.label}<strong>{item.count}</strong></span>) : <em>None yet</em>}
                </div>
              </div>
            </section>

            <section className="inspector-section domain-recent-section">
              <div className="inspector-section-heading">
                <h3>Recent activity</h3>
                <span>{recentActivity.length} request{recentActivity.length === 1 ? "" : "s"}</span>
              </div>
              {recentActivity.length === 0 ? (
                <div className="inspector-empty">This domain has not appeared in local query data.</div>
              ) : (
                <div className="domain-activity-list">
                  {recentActivity.map((query) => (
                    <div className="domain-activity-row" key={query.key}>
                      <div className="activity-result-time">
                        <StatusBadge value={query.action} />
                        <time>{formatActivityTime(query.timestamp)}</time>
                      </div>
                      <strong>{query.client_ip}</strong>
                      <span>{query.query_types.join(" · ")}</span>
                    </div>
                  ))}
                </div>
              )}
            </section>
          </div>
        )}
      </aside>
    </div>
  );
}

function OverviewItem({ label, value }: { label: string; value: string | number }) {
  return <div><span>{label}</span><strong>{value}</strong></div>;
}

type GroupedQuery = {
  key: string;
  timestamp: string;
  client_ip: string;
  action: DNSQuery["action"];
  query_types: string[];
};

function groupRecentQueries(queries: DNSQuery[]): GroupedQuery[] {
  const grouped = new Map<string, GroupedQuery>();
  for (const query of queries) {
    const timestamp = new Date(query.timestamp);
    timestamp.setMilliseconds(0);
    const key = `${timestamp.toISOString()}-${query.client_ip}-${query.action}`;
    const existing = grouped.get(key);
    if (existing) {
      if (!existing.query_types.includes(query.query_type)) existing.query_types.push(query.query_type);
      continue;
    }
    grouped.set(key, {
      key,
      timestamp: query.timestamp,
      client_ip: query.client_ip,
      action: query.action,
      query_types: [query.query_type]
    });
  }
  return Array.from(grouped.values()).slice(0, 8);
}

function formatDate(value?: string | null) {
  if (!value) return "Not seen";
  return new Date(value).toLocaleDateString([], { month: "short", day: "numeric", year: "numeric" });
}

function formatActivityTime(value: string) {
  const date = new Date(value);
  return `${date.toLocaleDateString([], { month: "short", day: "numeric" })} · ${date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`;
}
