import { useEffect, useMemo, useRef, useState } from "react";
import {
  api,
  type Blocklist,
  type DNSRecord,
  type DashboardSummary,
  type DeviceSummary,
  type Protection,
  type NotificationsResponse,
  type AuthStatus,
  type Setting
} from "./api/client";
import { DomainDrawer } from "./components/DomainDrawer";
import { AuthLoading, AuthScreen } from "./components/AuthScreen";
import { Onboarding } from "./components/Onboarding";
import { GlobalSearch } from "./components/GlobalSearch";
import { Layout } from "./components/Layout";
import { NotificationDrawer } from "./components/NotificationDrawer";
import { Dashboard } from "./pages/Dashboard";
import { Blocklists } from "./pages/Blocklists";
import { Devices } from "./pages/Devices";
import { LocalDns } from "./pages/LocalDns";
import { QueryLog } from "./pages/QueryLog";
import { ProtectionPage } from "./pages/Protection";
import { Settings } from "./pages/Settings";
import { Upstreams } from "./pages/Upstreams";

export type Page = "dashboard" | "queries" | "devices" | "records" | "upstreams" | "protection" | "blocklists" | "settings";

const pagePaths: Record<Page, string> = {
  dashboard: "/",
  queries: "/activity",
  devices: "/devices",
  records: "/local-dns",
  upstreams: "/upstreams",
  protection: "/protection",
  blocklists: "/blocklists",
  settings: "/settings"
};

const pageLabels: Record<Page, string> = {
  dashboard: "Dashboard",
  queries: "Activity",
  devices: "Devices",
  records: "Local DNS",
  upstreams: "Upstreams",
  protection: "Protection",
  blocklists: "Blocklists",
  settings: "Settings"
};

type AppRoute = {
  page: Page;
  clientIP: string | null;
  domain: string | null;
  canonicalPath: string;
};

export function App() {
  const [auth, setAuth] = useState<AuthStatus | null>(null);
  const [authError, setAuthError] = useState("");

  useEffect(() => {
    let active = true;
    api.authStatus()
      .then((status) => { if (active) setAuth(status); })
      .catch((caught) => { if (active) setAuthError(caught instanceof Error ? caught.message : "Faro API is unavailable."); });
    function unauthorized() {
      setAuth((current) => current ? { ...current, authenticated: false, username: undefined } : current);
    }
    window.addEventListener("faro:unauthorized", unauthorized);
    return () => {
      active = false;
      window.removeEventListener("faro:unauthorized", unauthorized);
    };
  }, []);

  async function authenticate(username: string, password: string) {
    setAuthError("");
    try {
      auth?.configured ? await api.login(username, password) : await api.setupAuth(username, password);
      setAuth(await api.authStatus());
    } catch (caught) {
      setAuthError(caught instanceof Error ? caught.message : "Authentication failed.");
    }
  }

  if (!auth) return authError ? <AuthScreen mode="login" onSubmit={authenticate} error={authError} /> : <AuthLoading />;
  if (!auth.configured) return <AuthScreen mode="setup" onSubmit={authenticate} error={authError} />;
  if (!auth.authenticated) return <AuthScreen mode="login" onSubmit={authenticate} error={authError} />;
  if (!auth.onboarding_complete) return <Onboarding username={auth.username || "admin"} onComplete={() => setAuth({ ...auth, onboarding_complete: true })} />;

  return <AuthenticatedApp username={auth.username || "admin"} onSignedOut={() => setAuth({ configured: true, authenticated: false, onboarding_complete: true })} />;
}

function AuthenticatedApp({ username, onSignedOut }: { username: string; onSignedOut: () => void }) {
  const [initialRoute] = useState(readRoute);
  const [page, setPageState] = useState<Page>(initialRoute.page);
  const [summary, setSummary] = useState<DashboardSummary | null>(null);
  const [devices, setDevices] = useState<DeviceSummary[]>([]);
  const [records, setRecords] = useState<DNSRecord[]>([]);
  const [blocklists, setBlocklists] = useState<Blocklist[]>([]);
  const [protections, setProtections] = useState<Protection[]>([]);
  const [settings, setSettings] = useState<Setting[]>([]);
  const [notifications, setNotifications] = useState<NotificationsResponse>({ attention_count: 0, unread_count: 0, items: [] });
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selectedDomain, setSelectedDomain] = useState<string | null>(initialRoute.domain);
  const [selectedClientIP, setSelectedClientIPState] = useState<string | null>(initialRoute.clientIP);
  const [searchOpen, setSearchOpen] = useState(false);
  const [notificationOpen, setNotificationOpen] = useState(false);
  const liveRefreshBusy = useRef(false);

  const apiState = useMemo(() => (loading ? "checking" : error ? "offline" : "online"), [loading, error]);

  async function loadAll() {
    setLoading(true);
    setError(null);
    try {
      const [nextSummary, nextDevices, nextRecords, nextBlocklists, nextProtections, nextSettings, nextNotifications] =
        await Promise.all([
        api.dashboard(),
        api.devices(),
        api.records(),
        api.blocklists(),
        api.protections(),
        api.settings(),
        api.notifications()
      ]);
      setSummary(nextSummary);
      setDevices(nextDevices);
      setRecords(nextRecords);
      setBlocklists(nextBlocklists);
      setProtections(nextProtections);
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
    if (`${window.location.pathname}${window.location.search}` !== initialRoute.canonicalPath) {
      window.history.replaceState({}, "", initialRoute.canonicalPath);
    }
    function onPopState() {
      const route = readRoute();
      setPageState(route.page);
      setSelectedClientIPState(route.clientIP);
      setSelectedDomain(route.domain);
      setSearchOpen(false);
      setNotificationOpen(false);
    }
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, [initialRoute]);

  useEffect(() => {
    const timer = window.setInterval(() => {
      void refreshLiveData();
    }, 10000);
    return () => window.clearInterval(timer);
  }, [page]);

  useEffect(() => {
    document.title = `${pageLabels[page]} | Faro`;
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
    if (liveRefreshBusy.current) return;
    liveRefreshBusy.current = true;
    try {
      const [nextSummary, nextNotifications] = await Promise.all([api.dashboard(), api.notifications()]);
      setSummary(nextSummary);
      setNotifications(nextNotifications);
      setError(null);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Failed to refresh live data");
    } finally {
      liveRefreshBusy.current = false;
    }
  }

  async function refreshDevices() {
    setDevices(await api.devices());
    setSummary(await api.dashboard());
  }

  async function signOut() {
    try {
      await api.logout();
    } finally {
      onSignedOut();
    }
  }

  function refreshNotifications() {
    void api.notifications().then(setNotifications).catch(() => undefined);
  }

  function markNotificationRead(id: string) {
    setNotifications((current) => {
      const item = current.items.find((event) => event.id === id);
      return {
        ...current,
        unread_count: item && !item.is_read ? Math.max(0, current.unread_count - 1) : current.unread_count,
        items: current.items.map((event) => event.id === id ? { ...event, is_read: true } : event)
      };
    });
    void api.markNotificationRead(id).then(refreshNotifications).catch(refreshNotifications);
  }

  function dismissNotification(id: string) {
    setNotifications((current) => {
      const item = current.items.find((event) => event.id === id);
      const needsAttention = item?.severity === "warning" || item?.severity === "critical";
      return {
        ...current,
        unread_count: item && !item.is_read ? Math.max(0, current.unread_count - 1) : current.unread_count,
        attention_count: needsAttention ? Math.max(0, current.attention_count - 1) : current.attention_count,
        items: current.items.filter((event) => event.id !== id)
      };
    });
    void api.dismissNotification(id).then(refreshNotifications).catch(refreshNotifications);
  }

  function markAllNotificationsRead() {
    setNotifications((current) => ({ ...current, unread_count: 0, items: current.items.map((event) => ({ ...event, is_read: true })) }));
    void api.markAllNotificationsRead().then(refreshNotifications).catch(refreshNotifications);
  }

  function navigateToPage(nextPage: Page) {
    setPageState(nextPage);
    setSelectedDomain(null);
    if (nextPage !== "devices") setSelectedClientIPState(null);
    const target = nextPage === "devices" && selectedClientIP
      ? `/devices/${encodeURIComponent(selectedClientIP)}`
      : pagePaths[nextPage];
    pushRoute(target);
    window.scrollTo({ top: 0 });
  }

  function selectDevice(clientIP: string | null, replace = false) {
    setPageState("devices");
    setSelectedClientIPState(clientIP);
    setSelectedDomain(null);
    const target = clientIP ? `/devices/${encodeURIComponent(clientIP)}` : pagePaths.devices;
    const invalidCurrentSelection = selectedClientIP !== null && !devices.some((device) => device.client_ip === selectedClientIP);
    const shouldReplace = replace || invalidCurrentSelection || (window.location.pathname === pagePaths.devices && clientIP !== null);
    pushRoute(target, shouldReplace);
  }

  function openDevice(clientIP: string) {
    selectDevice(clientIP, false);
    window.scrollTo({ top: 0 });
  }

  function openDomain(domain: string) {
    setSelectedDomain(domain);
    const url = new URL(window.location.href);
    url.searchParams.set("domain", domain);
    pushRoute(`${url.pathname}${url.search}`);
  }

  function closeDomain() {
    setSelectedDomain(null);
    const url = new URL(window.location.href);
    url.searchParams.delete("domain");
    pushRoute(`${url.pathname}${url.search}`, true);
  }

  const content = (() => {
    switch (page) {
      case "dashboard":
        return (
          <Dashboard
            summary={summary}
            settings={settings}
            loading={loading}
            onDomainSelect={openDomain}
            onViewActivity={() => navigateToPage("queries")}
            onViewDevices={() => navigateToPage("devices")}
            onManageUpstreams={() => navigateToPage("upstreams")}
          />
        );
      case "queries":
        return (
          <QueryLog
            onDomainSelect={openDomain}
            onDeviceSelect={openDevice}
          />
        );
      case "devices":
        return (
          <Devices
            devices={devices}
            protections={protections}
            refresh={refreshDevices}
            selectedClientIP={selectedClientIP}
            onSelectClient={selectDevice}
            onDomainSelect={openDomain}
          />
        );
      case "records":
        return <LocalDns records={records} settings={settings} refresh={loadAll} />;
      case "upstreams":
        return <Upstreams settings={settings} refresh={loadAll} />;
      case "protection":
        return <ProtectionPage protections={protections} blocklists={blocklists} devices={devices} refresh={loadAll} onManageBlocklists={() => navigateToPage("blocklists")} />;
      case "blocklists":
        return <Blocklists blocklists={blocklists} refresh={loadAll} />;
      case "settings":
        return <Settings settings={settings} refresh={loadAll} onManageUpstreams={() => navigateToPage("upstreams")} />;
    }
  })();

  return (
    <Layout
      page={page}
      setPage={navigateToPage}
      apiState={apiState}
      onOpenSearch={() => setSearchOpen(true)}
      notifications={notifications}
      onOpenNotifications={() => setNotificationOpen(true)}
      username={username}
      onSignOut={signOut}
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
      <DomainDrawer domain={selectedDomain} onClose={closeDomain} onChanged={loadAll} />
      <GlobalSearch
        open={searchOpen}
        onClose={() => setSearchOpen(false)}
        setPage={navigateToPage}
        onDomainSelect={openDomain}
        onDeviceSelect={openDevice}
      />
      <NotificationDrawer
        open={notificationOpen}
        notifications={notifications.items}
        attentionCount={notifications.attention_count}
        unreadCount={notifications.unread_count}
        onClose={() => setNotificationOpen(false)}
        onMarkRead={markNotificationRead}
        onDismiss={dismissNotification}
        onMarkAllRead={markAllNotificationsRead}
        onDomainSelect={openDomain}
        onDeviceSelect={openDevice}
        setPage={navigateToPage}
      />
    </Layout>
  );
}

function readRoute(): AppRoute {
  const pathname = normalizePath(window.location.pathname);
  const domain = new URLSearchParams(window.location.search).get("domain");
  const directPage = (Object.entries(pagePaths) as [Page, string][]).find(([, path]) => path === pathname)?.[0];
  if (directPage) return { page: directPage, clientIP: null, domain, canonicalPath: withDomain(pagePaths[directPage], domain) };

  if (pathname.startsWith("/devices/")) {
    const encodedClientIP = pathname.slice("/devices/".length);
    try {
      const clientIP = decodeURIComponent(encodedClientIP);
      if (clientIP.trim()) return { page: "devices", clientIP, domain, canonicalPath: withDomain(`/devices/${encodeURIComponent(clientIP)}`, domain) };
    } catch {
      // Invalid path segments fall through to the Devices index.
    }
    return { page: "devices", clientIP: null, domain, canonicalPath: withDomain(pagePaths.devices, domain) };
  }

  const legacyPages: Record<string, Page> = { "/query-log": "queries", "/records": "records", "/lists": "protection", "/rules": "protection", "/allowlist": "protection" };
  const legacyPage = legacyPages[pathname];
  if (legacyPage) return { page: legacyPage, clientIP: null, domain, canonicalPath: withDomain(pagePaths[legacyPage], domain) };
  return { page: "dashboard", clientIP: null, domain: null, canonicalPath: pagePaths.dashboard };
}

function normalizePath(pathname: string) {
  if (pathname === "/") return pathname;
  return pathname.replace(/\/+$/, "") || "/";
}

function withDomain(pathname: string, domain: string | null) {
  if (!domain) return pathname;
  const params = new URLSearchParams({ domain });
  return `${pathname}?${params.toString()}`;
}

function pushRoute(target: string, replace = false) {
  const current = `${window.location.pathname}${window.location.search}`;
  if (current === target) return;
  if (replace) window.history.replaceState({}, "", target);
  else window.history.pushState({}, "", target);
}
