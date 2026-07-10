import { useState } from "react";
import { api, type Blocklist } from "../api/client";
import { EmptyState } from "../components/EmptyState";

type BlocklistsProps = {
  blocklists: Blocklist[];
  refresh: () => Promise<void>;
};

export function Blocklists({ blocklists, refresh }: BlocklistsProps) {
  const [form, setForm] = useState({ name: "", url: "", enabled: true });
  const [busy, setBusy] = useState<number | null>(null);
  const [installing, setInstalling] = useState<string | null>(null);

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

  async function installRecommended(item: RecommendedBlocklist) {
    setInstalling(item.name);
    try {
      await api.createBlocklist({ name: item.name, url: item.url, enabled: true });
      await refresh();
    } finally {
      setInstalling(null);
    }
  }

  return (
    <div className="page-stack">
      <section className="panel">
        <div className="panel-title">
          <h2>Recommended blocklists</h2>
        </div>
        <div className="recommended-grid">
          {recommendedBlocklists.map((item) => {
            const installed = blocklists.some((blocklist) => blocklist.url === item.url || blocklist.name === item.name);
            return (
              <article className="recommended-card" key={item.name}>
                <span className="category-badge">{item.category}</span>
                <h3>{item.name}</h3>
                <p>{item.description}</p>
                <button type="button" className="secondary" disabled={installed || installing === item.name} onClick={() => void installRecommended(item)}>
                  {installed ? "Installed" : "Install"}
                </button>
              </article>
            );
          })}
        </div>
      </section>

      <div className="two-column">
      <section className="panel">
        <div className="panel-title">
          <h2>Blocklists</h2>
        </div>
        <div className="blocklist-grid">
          {blocklists.length === 0 ? (
            <EmptyState title="No blocklists yet" body="Install a recommended list or add a custom URL to start blocking noisy domains." />
          ) : (
          blocklists.map((blocklist) => (
            <article className="list-card" key={blocklist.id}>
              <div>
                <span className="category-badge">{categoryFor(blocklist)}</span>
                <h3>{blocklist.name}</h3>
                <p>{blocklist.url}</p>
                <div className="blocklist-health-row">
                  <span>Healthy</span>
                  <span>Auto-update enabled</span>
                  <span>Next refresh: manual</span>
                </div>
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
          ))
          )}
        </div>
      </section>

      <section className="panel form-panel">
        <div className="panel-title">
          <h2>Add custom blocklist</h2>
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
    </div>
  );
}

type RecommendedBlocklist = {
  name: string;
  description: string;
  category: string;
  url: string;
};

const recommendedBlocklists: RecommendedBlocklist[] = [
  {
    name: "OISD Small",
    description: "Balanced blocking list with a low false-positive profile.",
    category: "Balanced",
    url: "https://small.oisd.nl/"
  },
  {
    name: "HaGeZi Normal",
    description: "Privacy-focused list for ads, trackers, and telemetry.",
    category: "Privacy",
    url: "https://raw.githubusercontent.com/hagezi/dns-blocklists/main/hosts/multi.txt"
  },
  {
    name: "StevenBlack Unified",
    description: "Classic hosts list combining well-known ad and malware sources.",
    category: "Classic",
    url: "https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts"
  }
];

function categoryFor(blocklist: Blocklist) {
  const probe = `${blocklist.name} ${blocklist.url}`.toLowerCase();
  if (probe.includes("hagezi")) return "Privacy";
  if (probe.includes("stevenblack")) return "Classic";
  if (probe.includes("oisd")) return "Balanced";
  if (probe.includes("sample")) return "Starter";
  return "Custom";
}
