import { useState } from "react";
import { api, type DNSQuery } from "../api/client";
import { DomainFavicon } from "../components/DomainFavicon";
import { StatusBadge } from "../components/StatusBadge";

type QueryLogProps = {
  queries: DNSQuery[];
  refresh: (search?: string) => Promise<void>;
};

export function QueryLog({ queries, refresh }: QueryLogProps) {
  const [search, setSearch] = useState("");
  const [busyDomain, setBusyDomain] = useState<string | null>(null);

  async function addRule(domain: string, action: "allow" | "block") {
    setBusyDomain(domain);
    try {
      if (action === "allow") {
        await api.addAllow(domain);
      } else {
        await api.addBlock(domain);
      }
      await refresh(search);
    } finally {
      setBusyDomain(null);
    }
  }

  return (
    <section className="panel">
      <div className="panel-title with-actions">
        <h2>Recent DNS queries</h2>
        <form
          className="search-form"
          onSubmit={(event) => {
            event.preventDefault();
            void refresh(search);
          }}
        >
          <input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Search domain or client IP" />
          <button type="submit">Search</button>
        </form>
      </div>

      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Domain</th>
              <th>Client IP</th>
              <th>Type</th>
              <th>Action</th>
              <th>Source</th>
              <th>Timestamp</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {queries.map((query) => (
              <tr key={`${query.id ?? query.timestamp}-${query.domain}-${query.client_ip}`}>
                <td>
                  <span className="domain-cell">
                    <DomainFavicon domain={query.domain} />
                    {query.domain}
                  </span>
                </td>
                <td>{query.client_ip}</td>
                <td>{query.query_type}</td>
                <td>
                  <StatusBadge value={query.action} />
                </td>
                <td>{query.source}</td>
                <td>{new Date(query.timestamp).toLocaleString()}</td>
                <td className="row-actions">
                  <button type="button" onClick={() => void addRule(query.domain, "block")} disabled={busyDomain === query.domain}>
                    Block
                  </button>
                  <button type="button" className="secondary" onClick={() => void addRule(query.domain, "allow")} disabled={busyDomain === query.domain}>
                    Allow
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}
