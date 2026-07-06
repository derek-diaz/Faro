import { useEffect, useMemo, useState } from "react";
import { api, type Blocklist, type DNSQuery, type DNSRecord, type DashboardSummary, type DomainEntry, type Setting } from "./api/client";
import { Layout } from "./components/Layout";
import { Blocklists } from "./pages/Blocklists";
import { Dashboard } from "./pages/Dashboard";
import { Lists } from "./pages/Lists";
import { LocalDns } from "./pages/LocalDns";
import { QueryLog } from "./pages/QueryLog";
import { Settings } from "./pages/Settings";

export type Page = "dashboard" | "queries" | "records" | "blocklists" | "lists" | "settings";

export function App() {
  const [page, setPage] = useState<Page>("dashboard");
  const [summary, setSummary] = useState<DashboardSummary | null>(null);
  const [queries, setQueries] = useState<DNSQuery[]>([]);
  const [records, setRecords] = useState<DNSRecord[]>([]);
  const [blocklists, setBlocklists] = useState<Blocklist[]>([]);
  const [allowlist, setAllowlist] = useState<DomainEntry[]>([]);
  const [manualBlocks, setManualBlocks] = useState<DomainEntry[]>([]);
  const [settings, setSettings] = useState<Setting[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const apiState = useMemo(() => (loading ? "checking" : error ? "offline" : "online"), [loading, error]);

  async function loadAll() {
    setLoading(true);
    setError(null);
    try {
      const [nextSummary, nextQueries, nextRecords, nextBlocklists, nextAllowlist, nextBlocks, nextSettings] = await Promise.all([
        api.dashboard(),
        api.queries(),
        api.records(),
        api.blocklists(),
        api.allowlist(),
        api.blockDomains(),
        api.settings()
      ]);
      setSummary(nextSummary);
      setQueries(nextQueries);
      setRecords(nextRecords);
      setBlocklists(nextBlocklists);
      setAllowlist(nextAllowlist);
      setManualBlocks(nextBlocks);
      setSettings(nextSettings);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Failed to reach Faro API");
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void loadAll();
  }, []);

  useEffect(() => {
    const timer = window.setInterval(() => {
      void refreshLiveData();
    }, 5000);
    return () => window.clearInterval(timer);
  }, [page]);

  async function refreshLiveData() {
    try {
      setSummary(await api.dashboard());
      if (page === "queries") {
        setQueries(await api.queries());
      }
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Failed to refresh live data");
    }
  }

  async function refreshQueries(search = "") {
    setQueries(await api.queries(search));
    setSummary(await api.dashboard());
  }

  async function reloadCoreDNS() {
    await api.reload();
  }

  const content = (() => {
    switch (page) {
      case "dashboard":
        return <Dashboard summary={summary} blocklists={blocklists} settings={settings} loading={loading} />;
      case "queries":
        return <QueryLog queries={queries} refresh={refreshQueries} />;
      case "records":
        return <LocalDns records={records} refresh={loadAll} />;
      case "blocklists":
        return <Blocklists blocklists={blocklists} refresh={loadAll} />;
      case "lists":
        return <Lists allowlist={allowlist} blocklist={manualBlocks} refresh={loadAll} />;
      case "settings":
        return <Settings settings={settings} refresh={loadAll} />;
    }
  })();

  return (
    <Layout page={page} setPage={setPage} apiState={apiState} onReload={reloadCoreDNS}>
      {error && (
        <div className="error-banner">
          <strong>Faro API is not reachable.</strong>
          <span>{error}</span>
          <button type="button" onClick={() => void loadAll()}>
            Retry
          </button>
        </div>
      )}
      {content}
    </Layout>
  );
}
