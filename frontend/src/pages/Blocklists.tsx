import { useState } from "react";
import { api, type Blocklist } from "../api/client";

type BlocklistsProps = {
  blocklists: Blocklist[];
  refresh: () => Promise<void>;
};

export function Blocklists({ blocklists, refresh }: BlocklistsProps) {
  const [form, setForm] = useState({ name: "", url: "", enabled: true });
  const [busy, setBusy] = useState<number | null>(null);

  async function add(event: React.FormEvent) {
    event.preventDefault();
    await api.createBlocklist(form);
    setForm({ name: "", url: "", enabled: true });
    await refresh();
  }

  async function toggle(blocklist: Blocklist) {
    await api.updateBlocklist({ ...blocklist, enabled: !blocklist.enabled });
    await refresh();
  }

  async function refreshList(id: number) {
    setBusy(id);
    try {
      await api.refreshBlocklist(id);
      await refresh();
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="two-column">
      <section className="panel">
        <div className="panel-title">
          <h2>Blocklists</h2>
        </div>
        <div className="blocklist-grid">
          {blocklists.map((blocklist) => (
            <article className="list-card" key={blocklist.id}>
              <div>
                <h3>{blocklist.name}</h3>
                <p>{blocklist.url}</p>
              </div>
              <div className="list-card-meta">
                <strong>{blocklist.entry_count ?? 0}</strong>
                <span>domains</span>
              </div>
              <div className="row-actions">
                <button type="button" className={blocklist.enabled ? "secondary" : ""} onClick={() => void toggle(blocklist)}>
                  {blocklist.enabled ? "Disable" : "Enable"}
                </button>
                <button type="button" className="secondary" disabled={busy === blocklist.id} onClick={() => void refreshList(blocklist.id)}>
                  Refresh
                </button>
                <button
                  type="button"
                  className="danger"
                  onClick={async () => {
                    await api.deleteBlocklist(blocklist.id);
                    await refresh();
                  }}
                >
                  Delete
                </button>
              </div>
            </article>
          ))}
        </div>
      </section>

      <section className="panel form-panel">
        <div className="panel-title">
          <h2>Add blocklist</h2>
        </div>
        <form className="stack-form" onSubmit={(event) => void add(event)}>
          <label>
            Name
            <input value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} placeholder="OISD small" />
          </label>
          <label>
            URL
            <input value={form.url} onChange={(event) => setForm({ ...form, url: event.target.value })} placeholder="https://example.com/hosts.txt" />
          </label>
          <label className="checkbox-row">
            <input type="checkbox" checked={form.enabled} onChange={(event) => setForm({ ...form, enabled: event.target.checked })} />
            Enabled
          </label>
          <button type="submit">Add blocklist</button>
        </form>
      </section>
    </div>
  );
}
