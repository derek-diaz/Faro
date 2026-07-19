import { CheckCircle2, Plus, ShieldCheck, Trash2 } from "lucide-react";
import { useState, type FormEvent } from "react";
import { api, type DomainEntry } from "../api/client";
import { EmptyState } from "../components/EmptyState";

type AllowlistProps = {
  entries: DomainEntry[];
  refresh: () => Promise<void>;
};

export function Allowlist({ entries, refresh }: AllowlistProps) {
  const [domain, setDomain] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function add(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      await api.addAllow(domain);
      setDomain("");
      await refresh();
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Could not add the domain.");
    } finally {
      setBusy(false);
    }
  }

  async function remove(entry: DomainEntry) {
    setBusy(true);
    setError("");
    try {
      await api.deleteAllow(entry.id);
      await refresh();
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : `Could not remove ${entry.domain}.`);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="allowlist-page">
      <section className="allowlist-summary-strip">
        <div><ShieldCheck size={18} /><span>Allowed domains</span><strong>{entries.length}</strong></div>
        <div><CheckCircle2 size={18} /><span>Rule behavior</span><strong>Overrides blocking</strong></div>
        <p>Use this for domains that must resolve even when they appear in an installed blocklist or a manual block.</p>
      </section>

      <section className="panel allowlist-panel">
        <div className="allowlist-heading">
          <div><h2>Always allow</h2><p>Faro excludes each exact hostname from every blocking source. Add subdomains separately.</p></div>
          <form className="allowlist-add-form" onSubmit={(event) => void add(event)}>
            <input required value={domain} onChange={(event) => setDomain(event.target.value)} placeholder="example.com" aria-label="Domain to always allow" />
            <button type="submit" disabled={busy || !domain.trim()}><Plus size={16} /><span>Add exception</span></button>
          </form>
        </div>
        {error && <div className="allowlist-error" role="alert">{error}</div>}
        {entries.length === 0 ? (
          <EmptyState title="No allowlist exceptions" body="Domains added here will override installed blocklists and manual blocks." />
        ) : (
          <div className="allowlist-table">
            <div className="allowlist-columns" aria-hidden="true"><span>Domain</span><span>Added</span><span>Actions</span></div>
            {entries.map((entry) => (
              <div className="allowlist-row" key={entry.id}>
                <div><span className="allowlist-mark"><ShieldCheck size={15} /></span><strong>{entry.domain}</strong></div>
                <span>{formatAdded(entry.created_at)}</span>
                <button type="button" className="icon-button danger-icon" title="Remove exception" aria-label={`Remove ${entry.domain} from allowlist`} disabled={busy} onClick={() => void remove(entry)}><Trash2 size={16} /></button>
              </div>
            ))}
          </div>
        )}
      </section>
    </div>
  );
}

function formatAdded(value?: string) {
  if (!value) return "Unknown";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "Unknown" : date.toLocaleDateString([], { month: "short", day: "numeric", year: "numeric" });
}
