import { useState } from "react";
import { api, type DomainEntry } from "../api/client";
import { EmptyState } from "../components/EmptyState";

type ListsProps = {
  allowlist: DomainEntry[];
  blocklist: DomainEntry[];
  refresh: () => Promise<void>;
};

export function Lists({ allowlist, blocklist, refresh }: ListsProps) {
  return (
    <div className="two-column">
      <DomainEditor title="Manual allowlist" entries={allowlist} actionLabel="Allow domain" add={api.addAllow} remove={api.deleteAllow} refresh={refresh} />
      <DomainEditor title="Manual blocklist" entries={blocklist} actionLabel="Block domain" add={api.addBlock} remove={api.deleteBlock} refresh={refresh} />
    </div>
  );
}

type DomainEditorProps = {
  title: string;
  entries: DomainEntry[];
  actionLabel: string;
  add: (domain: string) => Promise<unknown>;
  remove: (id: number) => Promise<unknown>;
  refresh: () => Promise<void>;
};

function DomainEditor({ title, entries, actionLabel, add, remove, refresh }: DomainEditorProps) {
  const [domain, setDomain] = useState("");

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    await add(domain);
    setDomain("");
    await refresh();
  }

  return (
    <section className="panel">
      <div className="panel-title">
        <h2>{title}</h2>
      </div>
      <form className="inline-form" onSubmit={(event) => void submit(event)}>
        <input value={domain} onChange={(event) => setDomain(event.target.value)} placeholder="domain.com" />
        <button type="submit">{actionLabel}</button>
      </form>
      <div className="domain-list">
        {entries.length === 0 ? (
          <EmptyState
            title={title.includes("allow") ? "No allowed domains yet" : "No manually blocked domains yet"}
            body={title.includes("allow") ? "Allowed domains you add will override blocking rules." : "Manual blocks you add will appear here."}
          />
        ) : (
        entries.map((entry) => (
          <div className="domain-entry" key={entry.id}>
            <span>{entry.domain}</span>
            <button
              type="button"
              className="danger"
              onClick={async () => {
                await remove(entry.id);
                await refresh();
              }}
            >
              Remove
            </button>
          </div>
        ))
        )}
      </div>
    </section>
  );
}
