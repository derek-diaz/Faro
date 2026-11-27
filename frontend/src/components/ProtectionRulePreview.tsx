import { Ban, Check, ChevronDown, Eye, Info, LoaderCircle, RotateCw, Shield } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { api, type PreviewGroup, type ProtectionRules, type RulePreview } from "../api/client";
import { errorMessage } from "../utils/formatting";
import { Button } from "./ui/button";

export function ProtectionRulePreview({ id, rules }: { id: number; rules: ProtectionRules }) {
  const [result, setResult] = useState<RulePreview | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const pending = useRef<AbortController | null>(null);
  useEffect(() => () => pending.current?.abort(), []);

  async function preview() {
    const controller = new AbortController();
    pending.current = controller;
    setError("");
    setResult(null);
    setLoading(true);
    try {
      const next = await api.previewProtection(id, rules, controller.signal);
      if (!controller.signal.aborted) setResult(next);
    } catch (err) {
      if (!controller.signal.aborted) setError(err instanceof TypeError
        ? "Check your connection to Faro and try again."
        : errorMessage(err, "Try again in a moment."));
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  }

  return <section className="protection-editor-section rule-preview" aria-label="Rule preview">
    <header className="rule-preview-toolbar">
      <div className="rule-preview-title"><Eye size={20} aria-hidden="true" /><h3>Rule preview</h3></div>
      <p>Try your edits on recent activity. Your rules take effect when you save.</p>
      <Button type="button" variant="outline" className="secondary" disabled={loading} onClick={() => void preview()}>
        {loading && <LoaderCircle className="spinning" aria-hidden="true" />}
        {result && <RotateCw aria-hidden="true" />}
        {loading ? "Checking activity…" : error ? "Try again" : result ? "Run preview again" : "Preview rule changes"}
      </Button>
    </header>
    {error && <p role="alert" className="rule-preview-error"><strong>Preview unavailable.</strong> {error}</p>}
    <div role="status" aria-atomic="true" className="rule-preview-verdict">
      {loading && <span className="sr-only">Checking the last 24 hours of activity.</span>}
      {result && <>
        <h4>{previewSummary(result)}</h4>
        <p>{result.requests
          ? `Based on ${result.truncated ? "the latest " : ""}${requests(result.requests)} from the last 24 hours. Assumes blocking is on.`
          : "Use a device covered by this setup, then run the preview again."}</p>
      </>}
    </div>
    {result && (result.devices.total > 0 || result.still_blocked.total > 0) &&
      <div className="rule-preview-breakdown">
        <div className="rule-preview-domains">
          <h5>Domains</h5>
          <DomainGroup title="Would start blocking" icon={Ban} tone="blocked" group={result.newly_blocked} />
          <DomainGroup title="Would start allowing" icon={Check} tone="allowed" group={result.newly_allowed} />
          <DomainGroup title="Would stay blocked" icon={Shield} group={result.still_blocked} />
        </div>
        {result.devices.total > 0 && <div className="rule-preview-devices">
          <h5>Affected devices <span>({result.devices.total.toLocaleString()})</span></h5>
          <PreviewTable group={result.devices} devices />
          {result.devices.total > result.devices.items.length && <p>Showing the {result.devices.items.length} devices with the most changed requests.</p>}
        </div>}
      </div>}
    <details className="rule-preview-help">
      <summary><Info size={15} aria-hidden="true" />How the preview works<ChevronDown size={15} aria-hidden="true" /></summary>
      <p>Faro checks how your saved and edited rules would handle the same activity from devices currently in this setup. Each time a device looks up a domain, it counts as one request.</p>
    </details>
  </section>;
}

function requests(count: number) {
  return `${count.toLocaleString()} ${count === 1 ? "request" : "requests"}`;
}

function previewSummary(result: RulePreview) {
  if (!result.requests) return "No requests to preview in the last 24 hours";
  const blocked = result.newly_blocked.total;
  const allowed = result.newly_allowed.total;
  if (blocked && allowed) return `Faro would start blocking ${requests(blocked)} and stop blocking ${allowed.toLocaleString()}.`;
  if (blocked) return `Faro would start blocking ${requests(blocked)}.`;
  if (allowed) return `Faro would allow ${requests(allowed)} it currently blocks.`;
  return "Every request checked would get the same result.";
}

function DomainGroup({ title, icon: Icon, tone, group }: {
  title: string; icon: typeof Ban; tone?: "blocked" | "allowed"; group: PreviewGroup;
}) {
  if (!group.total) return null;
  return <details className="rule-preview-group" data-tone={tone}>
    <summary>
      <Icon size={17} aria-hidden="true" />
      <span>{title}<small>{group.items[0]?.label}</small></span>
      <span className="rule-preview-count">{requests(group.total)}</span>
      <ChevronDown size={16} aria-hidden="true" />
    </summary>
    <PreviewTable group={group} />
  </details>;
}

function PreviewTable({ group, devices = false }: { group: PreviewGroup; devices?: boolean }) {
  return <table className="rule-preview-table">
    <thead><tr><th scope="col">{devices ? "Device" : "Top domains"}</th><th scope="col">{devices ? "Changed requests" : "Requests"}</th></tr></thead>
    <tbody>{group.items.map(item => <tr key={JSON.stringify([item.label, item.address])}>
      <th scope="row">{item.label}{item.address && item.address !== item.label && <small>{item.address}</small>}</th>
      <td>{item.count.toLocaleString()}</td>
    </tr>)}</tbody>
  </table>;
}
