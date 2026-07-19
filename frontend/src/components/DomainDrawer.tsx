import { Ban, CheckCircle2, Database, GitBranch, Route, Server, ShieldCheck, ShieldX, X } from "lucide-react";
import { useEffect, useMemo, useState, type ReactNode } from "react";
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
  const latestQuery = summary?.recent_queries[0];

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
                  <span>Block in Home</span>
                </button>
              )}
              {summary.status !== "Allowed" && (
                <button type="button" className="secondary" onClick={() => void addRule("allow")} disabled={busy !== null}>
                  <ShieldCheck size={16} />
                  <span>Allow in Home</span>
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

            {latestQuery && <DecisionTrace query={latestQuery} />}

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

function DecisionTrace({ query }: { query: DNSQuery }) {
  const decision = query.decision;
  const hasDecision = Boolean(decision && (
    decision.captured_at || decision.reason || decision.allowlist || decision.manual_block ||
    decision.local_record || decision.blocklists?.length
  ));
  const rules = decision?.blocklists ?? [];
  const protectionName = decision?.protection?.name ?? "Home";
  const policyDetail = decision?.allowlist
    ? `${protectionName}: an allow exception bypassed filtering.`
    : decision?.manual_block
      ? `${protectionName}: matched a custom domain block.`
      : rules.length > 0
        ? `${protectionName}: matched ${rules.map((rule) => rule.name).join(", ")}.`
        : decision?.local_record
          ? `Matched Local DNS ${decision.local_record.type} record ${decision.local_record.value}.`
          : hasDecision
            ? "No blocking rule matched when Faro handled this request."
            : "This request predates stored rule provenance.";
  const resolution = resolutionDetail(query);
  const responseCode = query.rcode || decision?.response_code || "Response recorded";
  const latency = typeof query.latency_ms === "number" ? `${formatLatency(query.latency_ms)} ms` : "Latency unavailable";
  const confidence = !hasDecision
    ? "Historical request with limited provenance."
    : decision?.confidence === "inferred"
    ? "Cache classification inferred from the absence of an upstream hop."
    : decision?.confidence === "configuration_snapshot"
      ? "Rule provenance captured from Faro's active configuration."
      : decision?.confidence === "observed"
        ? "Upstream path observed in the CoreDNS response log."
        : "Decision captured from Faro's local data.";

  return (
    <section className="inspector-section decision-trace-section">
      <div className="inspector-section-heading">
        <div><h3>Why Faro did this</h3><p>{query.decision_reason || decision?.reason || fallbackReason(query)}</p></div>
        <time>{formatActivityTime(query.timestamp)}</time>
      </div>
      <div className="decision-trace" aria-label="DNS decision trace">
        <TraceStep icon={<Route size={15} />} label="Request received" detail={`${query.client_ip} requested ${query.query_type} ${query.domain}.`} />
        <TraceStep icon={query.action === "blocked" ? <ShieldX size={15} /> : <ShieldCheck size={15} />} label="Protection" detail={policyDetail} tone={query.action === "blocked" ? "blocked" : "allowed"} />
        <TraceStep icon={query.source === "cache" ? <Database size={15} /> : query.source === "upstream" ? <Server size={15} /> : <GitBranch size={15} />} label="Resolution path" detail={resolution} />
        <TraceStep icon={<CheckCircle2 size={15} />} label="Response" detail={`${responseCode} · ${latency}`} />
      </div>
      <span className="decision-confidence">{confidence}</span>
    </section>
  );
}

function TraceStep({ icon, label, detail, tone = "default" }: { icon: ReactNode; label: string; detail: string; tone?: "default" | "allowed" | "blocked" }) {
  return <div className={`decision-trace-step ${tone}`}><span className="decision-trace-icon">{icon}</span><div><strong>{label}</strong><p>{detail}</p></div></div>;
}

function resolutionDetail(query: DNSQuery) {
  if (query.source === "manual") return "Answered locally by Faro's manual blocking rule.";
  if (query.source === "blocklist") return "Answered locally by Faro filtering; no upstream was contacted.";
  if (query.source === "local") return "Answered by Faro Local DNS; no upstream was contacted.";
  if (query.source === "cache") return "Answered from Faro's DNS cache; no upstream was contacted.";
  if (query.source === "upstream") return query.upstream ? `Forwarded to upstream resolver ${query.upstream}.` : "Forwarded to a configured upstream resolver.";
  return `Handled by ${query.source || "Faro"}.`;
}

function fallbackReason(query: DNSQuery) {
  if (query.action === "blocked") return "Faro filtering blocked this request.";
  if (query.source === "cache") return "Faro answered this request from its local cache.";
  if (query.source === "local") return "Faro answered this request using Local DNS.";
  return query.upstream ? `Faro forwarded this request to ${query.upstream}.` : "Faro allowed this request.";
}

function formatLatency(value: number) {
  if (value < 1) return value.toFixed(2);
  if (value < 10) return value.toFixed(1);
  return Math.round(value).toString();
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
