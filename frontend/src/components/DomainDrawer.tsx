import { Ban, CheckCircle2, Database, GitBranch, Route, Server, ShieldCheck, ShieldX, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { api, type DecisionRule, type DNSDecision, type DNSQuery, type DomainSummary } from "../api/client";
import { DomainFavicon } from "./DomainFavicon";
import { StatusBadge } from "./StatusBadge";

type DomainDrawerProps = {
  readonly domain: string | null;
  readonly onClose: () => void;
  readonly onChanged: () => Promise<void>;
};

export function DomainDrawer({ domain, onClose, onChanged }: DomainDrawerProps) {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const previousFocus = useRef<HTMLElement | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  useEffect(() => {
    if (!domain) return;
    const dialog = dialogRef.current;
    previousFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    dialog?.showModal();
    return () => { dialog?.close(); previousFocus.current?.focus({ preventScroll: true }); };
  }, [domain]);
  const [summary, setSummary] = useState<DomainSummary | null>(null);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState<"allow" | "block" | null>(null);

  useEffect(() => {
    if (!domain) {
      setSummary(null);
      return;
    }
    let cancelled = false;
    const controller = new AbortController();
    setSummary(null);
    setError("");
    setNotice("");
    setLoading(true);
    api
      .domainSummary(domain, controller.signal)
      .then((nextSummary) => {
        if (!cancelled) setSummary(nextSummary);
      })
      .catch((error_: unknown) => { if (!cancelled) setError(error_ instanceof Error ? error_.message : "Could not load this domain."); })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true; controller.abort();
    };
  }, [domain]);

  const recentActivity = useMemo(() => groupRecentQueries(summary?.recent_queries ?? []), [summary?.recent_queries]);
  const latestQuery = summary?.recent_queries[0];

  async function addRule(action: "allow" | "block") {
    if (!domain) return;
    setBusy(action);
    setError("");
    try {
      if (action === "allow") await api.addAllow(domain);
      else await api.addBlock(domain);
      setSummary(await api.domainSummary(domain));
      await onChanged();
      setNotice(`Saved ${action} exception in Home. Historical results below describe earlier requests.`);
    } catch (error_) {
      setError(error_ instanceof Error ? error_.message : "Could not save the exception.");
    } finally {
      setBusy(null);
    }
  }

  if (!domain) return null;

  return (
    <dialog
      className="drawer-backdrop"
      ref={dialogRef}
      onCancel={(event) => { event.preventDefault(); onClose(); }}
      aria-label={`${domain} details`}
      onClick={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <aside className="domain-drawer domain-inspector">
        <header className="domain-inspector-header">
          <div className="drawer-domain-title">
            <DomainFavicon domain={domain} />
            <div>
              <strong>{domain}</strong>
              <span className={`domain-status ${summary?.status.toLowerCase() ?? "loading"}`}>{summary ? `Observed: ${summary.status}` : "Loading"}</span>
            </div>
          </div>
          <button className="icon-button" type="button" onClick={onClose} aria-label="Close domain details">
            <X size={18} />
          </button>
        </header>

        {error && <div className="error-banner" role="alert">{error}</div>}
        {notice && <p role="status">{notice}</p>}
        {loading && <div className="drawer-loading">Loading domain activity...</div>}

        {!loading && summary && (
          <div className="domain-inspector-body">
            <div className="domain-action-bar">
              {(
                <button type="button" onClick={() => void addRule("block")} disabled={busy !== null || Boolean(summary.current_home_decision.allowlist) || Boolean(summary.current_home_decision.manual_block)}>
                  <Ban size={16} />
                  <span>Block in Home</span>
                </button>
              )}
              {(
                <button type="button" className="secondary" onClick={() => void addRule("allow")} disabled={busy !== null || summary.home_allow_exception}>
                  <ShieldCheck size={16} />
                  <span>Allow in Home</span>
                </button>
              )}
            </div>

            <p className="domain-action-context">Permanent changes above apply to Home. For a device's own protection, open its “Fix a broken site” tab.<br />Current Home policy: {summary.current_home_decision.reason || "No blocking rule matches."}{summary.current_home_decision.allowlist && <><br />An allow exception takes priority over blocks. Remove it in Protection or undo the temporary test before blocking.</>}</p>
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
    </dialog>
  );
}

type DecisionTraceProps = {
  readonly query: DNSQuery;
};

function DecisionTrace({ query }: DecisionTraceProps) {
  const decision = query.decision;
  const hasDecision = Boolean(decision && (
    decision.captured_at || decision.reason || decision.allowlist || decision.manual_block ||
    decision.local_record || decision.blocklists?.length
  ));
  const rules = decision?.blocklists ?? [];
  const protectionName = decision?.protection?.name ?? "Home";
  const policyDetail = policyDetailFor(decision, rules, hasDecision, protectionName);
  const resolution = resolutionDetail(query);
  const responseCode = query.rcode || decision?.response_code || "Response recorded";
  const latency = typeof query.latency_ms === "number" ? `${formatLatency(query.latency_ms)} ms` : "Latency unavailable";
  const confidence = confidenceDetail(hasDecision, decision);

  return (
    <section className="inspector-section decision-trace-section">
      <div className="inspector-section-heading">
        <div><h3>Why Faro did this</h3><p>{query.decision_reason || decision?.reason || fallbackReason(query)}</p></div>
        <time>{formatActivityTime(query.timestamp)}</time>
      </div>
      <div className="decision-trace" aria-label="DNS decision trace">
        <TraceStep icon={<Route size={15} />} label="Request received" detail={`${query.client_ip} requested ${query.query_type} ${query.domain}.`} />
        <TraceStep icon={query.action === "blocked" ? <ShieldX size={15} /> : <ShieldCheck size={15} />} label="Protection" detail={policyDetail} tone={query.action === "blocked" ? "blocked" : "allowed"} />
        <TraceStep icon={resolutionPathIcon(query.source)} label="Resolution path" detail={resolution} />
        <TraceStep icon={<CheckCircle2 size={15} />} label="Response" detail={`${responseCode} · ${latency}`} />
      </div>
      <span className="decision-confidence">{confidence}</span>
    </section>
  );
}

type TraceStepProps = {
  readonly icon: ReactNode;
  readonly label: string;
  readonly detail: string;
  readonly tone?: "default" | "allowed" | "blocked";
};

function TraceStep({ icon, label, detail, tone = "default" }: TraceStepProps) {
  return <div className={`decision-trace-step ${tone}`}><span className="decision-trace-icon">{icon}</span><div><strong>{label}</strong><p>{detail}</p></div></div>;
}

function policyDetailFor(decision: DNSDecision | undefined, rules: DecisionRule[], hasDecision: boolean, protectionName: string) {
  if (decision?.allowlist) return `${protectionName}: an allow exception bypassed filtering.`;
  if (decision?.manual_block) return `${protectionName}: matched a custom domain block.`;
  if (rules.length > 0) return `${protectionName}: matched ${rules.map((rule) => rule.name).join(", ")}.`;
  if (decision?.local_record) return `Matched Local DNS ${decision.local_record.type} record ${decision.local_record.value}.`;
  if (hasDecision) return "No blocking rule matched when Faro handled this request.";
  return "This request predates stored rule provenance.";
}

function confidenceDetail(hasDecision: boolean, decision: DNSDecision | undefined) {
  if (!hasDecision) return "Historical request with limited provenance.";
  switch (decision?.confidence) {
    case "inferred":
      return "Cache classification inferred from the absence of an upstream hop.";
    case "configuration_snapshot":
      return "Rule provenance captured from Faro's active configuration.";
    case "observed":
      return "Upstream path observed in the CoreDNS response log.";
    default:
      return "Decision captured from Faro's local data.";
  }
}

function resolutionPathIcon(source: string) {
  if (source === "cache") return <Database size={15} />;
  if (source === "upstream") return <Server size={15} />;
  return <GitBranch size={15} />;
}

function resolutionDetail(query: DNSQuery) {
  if (query.source === "manual") return "Answered locally by Faro's manual blocking rule.";
  if (query.source === "blocklist") return "Answered locally by Faro filtering; no upstream was contacted.";
  if (query.source === "local") return "Answered by Faro Local DNS; no upstream was contacted.";
  if (query.source === "cache") return "Answered from Faro's DNS cache; no upstream was contacted.";
  if (query.source === "upstream") {
    if (query.upstream === "doh") return "Forwarded through Faro's encrypted DNS-over-HTTPS connection.";
    if (query.upstream) return `Forwarded to upstream resolver ${query.upstream}.`;
    return "Forwarded to a configured upstream resolver.";
  }
  return `Handled by ${query.source || "Faro"}.`;
}

function fallbackReason(query: DNSQuery) {
  if (query.action === "blocked") return "Faro filtering blocked this request.";
  if (query.source === "cache") return "Faro answered this request from its local cache.";
  if (query.source === "local") return "Faro answered this request using Local DNS.";
  if (query.upstream === "doh") return "Faro forwarded this request through encrypted DNS.";
  if (query.upstream) return `Faro forwarded this request to ${query.upstream}.`;
  return "Faro allowed this request.";
}

function formatLatency(value: number) {
  if (value < 1) return value.toFixed(2);
  if (value < 10) return value.toFixed(1);
  return Math.round(value).toString();
}

type OverviewItemProps = {
  readonly label: string;
  readonly value: string | number;
};

function OverviewItem({ label, value }: OverviewItemProps) {
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
