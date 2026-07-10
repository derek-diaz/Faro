import { useEffect, useMemo, useState } from "react";
import {
  api,
  type Blocklist,
  type DNSQuery,
  type DNSRecord,
  type DashboardSummary,
  type DeviceSummary,
  type DomainEntry,
  type FaroEvent,
  type NotificationsResponse,
  type Setting
} from "./api/client";
import { DomainDrawer } from "./components/DomainDrawer";
import { GlobalSearch } from "./components/GlobalSearch";
import { Layout } from "./components/Layout";
import { NotificationDrawer } from "./components/NotificationDrawer";
import { Blocklists } from "./pages/Blocklists";
import { Dashboard } from "./pages/Dashboard";
import { Devices } from "./pages/Devices";
import { Lists } from "./pages/Lists";
import { LocalDns } from "./pages/LocalDns";
import { QueryLog } from "./pages/QueryLog";
import { Settings } from "./pages/Settings";
import { Upstreams } from "./pages/Upstreams";

export type Page = "dashboard" | "queries" | "devices" | "records" | "upstreams" | "blocklists" | "lists" | "settings";

export function App() {
  const [page, setPage] = useState<Page>("dashboard");
  const [summary, setSummary] = useState<DashboardSummary | null>(null);
  const [queries, setQueries] = useState<DNSQuery[]>([]);
  const [events, setEvents] = useState<FaroEvent[]>([]);
  const [devices, setDevices] = useState<DeviceSummary[]>([]);
  const [records, setRecords] = useState<DNSRecord[]>([]);
  const [blocklists, setBlocklists] = useState<Blocklist[]>([]);
  const [allowlist, setAllowlist] = useState<DomainEntry[]>([]);
  const [manualBlocks, setManualBlocks] = useState<DomainEntry[]>([]);
  const [settings, setSettings] = useState<Setting[]>([]);
  const [notifications, setNotifications] = useState<NotificationsResponse>({ unread_count: 0, items: [] });
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selectedDomain, setSelectedDomain] = useState<string | null>(null);
  const [selectedClientIP, setSelectedClientIP] = useState<string | null>(null);
  const [searchOpen, setSearchOpen] = useState(false);
  const [notificationOpen, setNotificationOpen] = useState(false);

  const apiState = useMemo(() => (loading ? "checking" : error ? "offline" : "online"), [loading, error]);

  async function loadAll() {
    setLoading(true);
    setError(null);
    try {
      const [nextSummary, nextQueries, nextEvents, nextDevices, nextRecords, nextBlocklists, nextAllowlist, nextBlocks, nextSettings, nextNotifications] =
        await Promise.all([
        api.dashboard(),
        api.queries(),
        api.events(),
        api.devices(),
        api.records(),
        api.blocklists(),
        api.allowlist(),
        api.blockDomains(),
        api.settings(),
        api.notifications()
      ]);
      setSummary(nextSummary);
      setQueries(nextQueries);
      setEvents(nextEvents);
      setDevices(nextDevices);
      setRecords(nextRecords);
      setBlocklists(nextBlocklists);
      setAllowlist(nextAllowlist);
      setManualBlocks(nextBlocks);
      setSettings(nextSettings);
      setNotifications(nextNotifications);
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

  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setSearchOpen(true);
      }
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  async function refreshLiveData() {
    try {
      setSummary(await api.dashboard());
      if (page === "queries") {
        setEvents(await api.events());
      }
      if (page === "devices") {
        setDevices(await api.devices());
      }
      setNotifications(await api.notifications());
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Failed to refresh live data");
    }
  }

  async function refreshQueries(search = "") {
    setQueries(await api.queries(search));
    setEvents(await api.events(search));
    setSummary(await api.dashboard());
    setNotifications(await api.notifications());
  }

  async function refreshDevices() {
    setDevices(await api.devices());
    setSummary(await api.dashboard());
  }

  async function reloadCoreDNS() {
    await api.reload();
  }

  const content = (() => {
    switch (page) {
      case "dashboard":
        return (
          <Dashboard
            summary={summary}
            settings={settings}
            loading={loading}
            onDomainSelect={setSelectedDomain}
            onViewActivity={() => setPage("queries")}
            onViewDevices={() => setPage("devices")}
          />
        );
      case "queries":
        return (
          <QueryLog
            events={events}
            refresh={refreshQueries}
            onDomainSelect={setSelectedDomain}
            onDeviceSelect={(clientIP) => {
              setSelectedClientIP(clientIP);
              setPage("devices");
            }}
          />
        );
      case "devices":
        return (
          <Devices
            devices={devices}
            refresh={refreshDevices}
            selectedClientIP={selectedClientIP}
            onSelectClient={setSelectedClientIP}
            onDomainSelect={setSelectedDomain}
          />
        );
      case "records":
        return <LocalDns records={records} refresh={loadAll} />;
      case "upstreams":
        return <Upstreams settings={settings} refresh={loadAll} />;
      case "blocklists":
        return <Blocklists blocklists={blocklists} refresh={loadAll} />;
      case "lists":
        return <Lists allowlist={allowlist} blocklist={manualBlocks} refresh={loadAll} />;
      case "settings":
        return <Settings settings={settings} refresh={loadAll} onManageUpstreams={() => setPage("upstreams")} />;
    }
  })();

  return (
    <Layout
      page={page}
      setPage={setPage}
      apiState={apiState}
      onReload={reloadCoreDNS}
      onOpenSearch={() => setSearchOpen(true)}
      notifications={notifications}
      onOpenNotifications={() => setNotificationOpen(true)}
    >
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
      <DomainDrawer domain={selectedDomain} onClose={() => setSelectedDomain(null)} onChanged={loadAll} />
      <GlobalSearch
        open={searchOpen}
        onClose={() => setSearchOpen(false)}
        setPage={setPage}
        onDomainSelect={setSelectedDomain}
        onDeviceSelect={(clientIP) => {
          setSelectedClientIP(clientIP);
          setPage("devices");
        }}
      />
      <NotificationDrawer
        open={notificationOpen}
        notifications={notifications.items}
        onClose={() => setNotificationOpen(false)}
        onDomainSelect={setSelectedDomain}
        onDeviceSelect={(clientIP) => {
          setSelectedClientIP(clientIP);
          setPage("devices");
        }}
      />
    </Layout>
  );
}
