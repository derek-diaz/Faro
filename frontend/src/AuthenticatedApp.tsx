import "./components/navigation.css";
import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import {
  api,
  type DashboardSummary,
  type NotificationsResponse,
  type VersionCheck
} from "./api/client";
import { FaviconEnabledContext } from "./components/DomainFavicon";
import { Button } from "./components/ui/button";
import type { CommandResult, SearchSession } from "./components/GlobalSearch";
import { Layout } from "./components/Layout";
import { Dashboard } from "./pages/Dashboard";
import { Devices } from "./pages/Devices";
import { dashboardStatus } from "./utils/dashboardStatus";
import { trafficActivityPath } from "./utils/trafficHours";
import { usePageCollection } from "./utils/usePageCollection";
import { useProtectionSession } from "./utils/useProtectionSession";
import type { ThemeMode } from "./theme";

const Blocklists = lazy(() => import("./pages/Blocklists").then(m => ({ default: m.Blocklists })));
const LocalDns = lazy(() => import("./pages/LocalDns").then(m => ({ default: m.LocalDns })));
const QueryLog = lazy(() => import("./pages/QueryLog").then(m => ({ default: m.QueryLog })));
const ProtectionPage = lazy(() => import("./pages/Protection").then(m => ({ default: m.ProtectionPage })));
const Settings = lazy(() => import("./pages/Settings").then(m => ({ default: m.Settings })));
const Upstreams = lazy(() => import("./pages/Upstreams").then(m => ({ default: m.Upstreams })));
const DomainDrawer = lazy(() => import("./components/DomainDrawer").then(m => ({ default: m.DomainDrawer })));
const GlobalSearch = lazy(() => import("./components/GlobalSearch").then(m => ({ default: m.GlobalSearch })));
const NotificationDrawer = lazy(() => import("./components/NotificationDrawer").then(m => ({ default: m.NotificationDrawer })));

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
  dashboard: "Overview",
  queries: "Activity",
  devices: "Devices",
  records: "Local names",
  upstreams: "DNS providers",
  protection: "Protection",
  blocklists: "Blocklists",
  settings: "Settings"
};

const DISMISSED_RELEASE_STORAGE_KEY = "faro-dismissed-release-version";

function readDismissedReleaseVersion() {
  try {
    return window.localStorage.getItem(DISMISSED_RELEASE_STORAGE_KEY);
  } catch {
    return null;
  }
}

function persistDismissedReleaseVersion(version: string) {
  try {
    window.localStorage.setItem(DISMISSED_RELEASE_STORAGE_KEY, version);
  } catch {
    // Dismissal still applies for the current session when storage is unavailable.
  }
}

type AppRoute = {
  page: Page;
  clientIP: string | null;
  domain: string | null;
  canonicalPath: string;
};

type AuthenticatedAppProps = Readonly<{
  username: string;
  onSignedOut: () => void;
  themeMode: ThemeMode;
  onThemeModeChange: (mode: ThemeMode) => void;
}>;

export default function AuthenticatedApp({ username, onSignedOut, themeMode, onThemeModeChange }: AuthenticatedAppProps) {
  const [versionInfo, setVersionInfo] = useState<VersionCheck | null>(null);
  const [dismissedReleaseVersion, setDismissedReleaseVersion] = useState<string | null>(() => readDismissedReleaseVersion());
  const initialRoute = useMemo(readRoute, []);
  const [page, setPage] = useState<Page>(initialRoute.page);
  const [summary, setSummary] = useState<DashboardSummary | null>(null);
  const [dashboardStale, setDashboardStale] = useState(false);
  const dashboardRequestID = useRef(0);
  const [notifications, setNotifications] = useState<NotificationsResponse>({ attention_count: 0, unread_count: 0, items: [] });
  const [loading, setLoading] = useState(true);
  const [dashboardLoading, setDashboardLoading] = useState(true);
  const [liveError, setError] = useState<string | null>(null);
  const [selectedDomain, setSelectedDomain] = useState<string | null>(initialRoute.domain);
  const [selectedDeviceRef, setSelectedDeviceRef] = useState<string | null>(initialRoute.clientIP);
  const records = usePageCollection(api.records, page === "records");
  const blocklists = usePageCollection(api.blocklists, page === "blocklists" || page === "protection");
  const protections = usePageCollection(api.protections, page === "protection" || (page === "devices" && !!selectedDeviceRef));
  const devices = usePageCollection(api.devices, page === "protection");
  const settingsCollection = usePageCollection(api.settings, true);
  const settings = settingsCollection.data ?? [];
  const error = liveError || records.error || blocklists.error || protections.error || devices.error || settingsCollection.error;
  const [searchSession, setSearchSession] = useState<SearchSession | null>(null);
  const [routeQuery, setRouteQuery] = useState(window.location.search);
  const params = useMemo(() => new URLSearchParams(routeQuery), [routeQuery]);
  const protectionSession = useProtectionSession();
  const ruleKind = params.has("allow") ? "allow" : "block";
  const ruleDomain = params.get(ruleKind);

  const [notificationOpen, setNotificationOpen] = useState(false);
  const liveRefreshBusy = useRef(false);

  let apiState: "checking" | "offline" | "online" = "online";
  if (loading) apiState = "checking";
  else if (error) apiState = "offline";

  async function refreshDashboard() {
    const requestID = ++dashboardRequestID.current;
    try {
      const next = await api.dashboard();
      if (requestID === dashboardRequestID.current) {
        setSummary(next);
        setDashboardStale(false);
      }
    } catch (failure) {
      if (requestID === dashboardRequestID.current) setDashboardStale(true);
      throw failure;
    }
  }

  async function loadNotifications() {
    setLoading(true);
    try {
      setNotifications(await api.notifications());
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : "Failed to reach Faro API");
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void loadNotifications();
  }, []);

  useEffect(() => {
    let active = true;
    const checkVersion = () => {
      void api.versionCheck().then((nextVersion) => {
        if (active) setVersionInfo(nextVersion);
      }).catch(() => undefined);
    };
    checkVersion();
    const timer = window.setInterval(checkVersion, 6 * 60 * 60 * 1000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, []);

  useEffect(() => {
    if (`${window.location.pathname}${window.location.search}` !== initialRoute.canonicalPath) {
      window.history.replaceState({}, "", initialRoute.canonicalPath);
    }
    function onPopState() {
      const route = readRoute();
      setPage(route.page);
      setSelectedDeviceRef(route.clientIP);
      setSelectedDomain(route.domain);
      setRouteQuery(window.location.search);
      setSearchSession(window.history.state?.faroSearch ?? null);
      setNotificationOpen(false);
    }
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, [initialRoute]);

  useEffect(() => {
    const refreshIfVisible = () => {
      if (document.visibilityState === "visible") void refreshLiveData();
    };
    const timer = window.setInterval(refreshIfVisible, 30000);
    document.addEventListener("visibilitychange", refreshIfVisible);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", refreshIfVisible);
    };
  }, [page]);

  useEffect(() => {
    let active = true;
    if (page === "dashboard") {
      setDashboardLoading(true);
      void refreshDashboard()
        .catch((failure: unknown) => { if (active) setError(failure instanceof Error ? failure.message : "Could not load dashboard."); })
        .finally(() => { if (active) setDashboardLoading(false); });
    }
    return () => { active = false; };
  }, [page]);

  useEffect(() => {
    if (!summary?.cache.metrics_pending || page !== "dashboard") return;
    const timer = window.setTimeout(() => {
      void refreshDashboard().catch(() => undefined);
    }, 2500);
    return () => window.clearTimeout(timer);
  }, [summary, page]);

  useEffect(() => {
    document.title = `${pageLabels[page]} | Faro`;
  }, [page]);

  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setSearchSession((current) => current ?? { query: "", index: 0 });
      }
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  async function refreshLiveData() {
    if (liveRefreshBusy.current) return;
    liveRefreshBusy.current = true;
    try {
      await Promise.all([
        ...(page === "dashboard" ? [refreshDashboard()] : []),
        api.notifications().then(setNotifications)
      ]);
      setError(null);
    } catch (error_) {
      setError(error_ instanceof Error ? error_.message : "Failed to refresh live data");
    } finally {
      liveRefreshBusy.current = false;
    }
  }

  async function refreshRecords() {
    await Promise.all([records.invalidate(), devices.invalidate()]);
  }

  async function refreshDevices() {
    await Promise.all([devices.invalidate(), protections.invalidate()]);
  }

  async function refreshProtectionData() {
    await Promise.all([protections.invalidate(), blocklists.invalidate(), devices.invalidate()]);
  }

  async function refreshBlocklists() {
    await Promise.allSettled([blocklists.invalidate(), protections.invalidate()]);
    refreshNotifications();
  }

  async function refreshSettings() {
    await Promise.all([settingsCollection.refresh(), devices.invalidate()]);
    refreshNotifications();
  }

  async function refreshRules() {
    await Promise.all([protections.invalidate().catch(() => undefined), refreshLiveData()]);
  }

  function retry() {
    for (const collection of [settingsCollection, devices, protections, blocklists, records]) {
      if (collection.error) void collection.refresh().catch(() => undefined);
    }
    void refreshLiveData();
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

  function dismissReleaseUpdate() {
    const version = versionInfo?.latest?.version;
    if (!version) return;
    setDismissedReleaseVersion(version);
    persistDismissedReleaseVersion(version);
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

  function navigate(target: string, replace = false) {
    pushRoute(target, replace);
    const route = readRoute();
    setPage(route.page);
    setSelectedDeviceRef(route.clientIP);
    setSelectedDomain(route.domain);
    setRouteQuery(window.location.search);
    window.scrollTo({ top: 0 });
  }

  function navigateToPage(nextPage: Page) {
    if (nextPage === "protection" && page === "blocklists" && protectionSession.returnToBlocklistsFor.current !== null) {
      navigate(`/protection?profile=${protectionSession.returnToBlocklistsFor.current}`);
    } else if (nextPage === "devices" && selectedDeviceRef) {
      navigate(`/devices/${encodeURIComponent(selectedDeviceRef)}`);
    } else {
      navigate(pagePaths[nextPage]);
    }
  }

  function selectSearchResult(item: CommandResult, session: SearchSession) {
    window.history.replaceState({ ...window.history.state, faroSearch: session }, "");
    setSearchSession(null);
    const destinations = { events: "/activity", local_records: "/local-dns", blocklists: "/blocklists" };
    switch (item.group) {
      case "domains": {
        const url = new URL(window.location.href);
        url.searchParams.set("domain", item.label);
        navigate(url.pathname + url.search + url.hash);
        break;
      }
      case "devices":
        navigate(`/devices/${encodeURIComponent(item.id ? "id:" + item.id : item.label)}`);
        break;
      case "rules": {
        protectionSession.returnToBlocklistsFor.current = null;
        const match = new URLSearchParams();
        if (item.id) match.set("profile", String(item.id));
        if (item.kind) match.set(item.kind, item.label);
        navigate(`/protection?${match}`);
        break;
      }
      default: {
        const field = { events: "event", local_records: "record", blocklists: "list" }[item.group];
        navigate(destinations[item.group] + (item.id ? `?${field}=${item.id}` : ""));
      }
    }
  }

  function closeSearch() {
    setSearchSession(null);
    if (window.history.state?.faroSearch) {
      const { faroSearch: _previous, ...state } = window.history.state;
      window.history.replaceState(state, "");
    }
  }

  const deviceTask = params.get("task");
  const deviceView = deviceTask === "troubleshoot" || deviceTask === "replay" ? deviceTask : "overview";

  function devicePath(reference: string | null, view = "overview") {
    const path = reference ? `/devices/${encodeURIComponent(reference)}` : pagePaths.devices;
    return view === "overview" ? path : `${path}?task=${view}`;
  }

  function selectDevice(reference: string | null, replace = false) {
    navigate(devicePath(reference, deviceView), replace);
  }

  function openDevice(reference: string) {
    navigate(devicePath(reference));
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

  const networkStatus = dashboardStatus(summary, settings.find((setting) => setting.key === "faro_lan_ip")?.value ?? "", dashboardStale);
  const loadingPage = <p role="status">Loading {pageLabels[page]}…</p>;
  const content = (() => {
    switch (page) {
      case "dashboard":
        return (
          <Dashboard
            summary={summary}
            status={networkStatus}
            settings={settings}
            loading={dashboardLoading && !summary}
            onDomainSelect={openDomain}
            onDeviceSelect={openDevice}
            onViewActivity={() => navigateToPage("queries")}
            onViewHour={(from, to) => navigate(trafficActivityPath(from, to))}
            onConnectionSettings={() => navigate("/settings#general")}
            onViewDnsHealth={() => navigate("/settings#data")}
            onConnected={() => void refreshLiveData()}
            onTroubleshoot={() => navigate(devicePath(null, "troubleshoot"))}
            onViewServerHealth={() => navigate("/settings#redundancy")}
            onViewDevices={() => navigateToPage("devices")}
            onViewBlocklists={(id) => navigate(id ? `/blocklists?list=${id}` : pagePaths.blocklists)}
            onViewLocalDns={(id) => navigate(id ? `/local-dns?record=${id}` : pagePaths.records)}
            onManageUpstreams={() => navigateToPage("upstreams")}
          />
        );
      case "queries":
        return (
          <QueryLog
            query={routeQuery}
            onRulesChanged={refreshRules}
            onViewActivity={() => navigateToPage("queries")}
            eventID={positiveID(params.get("event"))}
            onDomainSelect={openDomain}
            onDeviceSelect={openDevice}
          />
        );
      case "devices":
        return (
          <Devices
            protections={protections.data ?? []}
            refresh={refreshDevices}
            view={deviceView}
            onViewChange={(view) => navigate(devicePath(selectedDeviceRef, view))}
            selectedDeviceRef={selectedDeviceRef}
            onSelectDevice={selectDevice}
            onDomainSelect={openDomain}
          />
        );
      case "records":
        if (!records.data || !settingsCollection.data) return error ? null : loadingPage;
        return <LocalDns recordID={positiveID(params.get("record"))} records={records.data} settings={settings} refresh={refreshRecords} />;
      case "upstreams":
        if (!settingsCollection.data) return error ? null : loadingPage;
        return <Upstreams settings={settings} refresh={settingsCollection.refresh} />;
      case "protection":
        if (!protections.data || !blocklists.data || !devices.data) return error ? null : loadingPage;
        return <ProtectionPage
          protections={protections.data}
          blocklists={blocklists.data}
          devices={devices.data}
          session={protectionSession}
          selectedID={positiveID(params.get("profile")) ?? null}
          onSelectSetup={(id, replace) => navigate(id === null ? "/protection" : `/protection?profile=${id}`, replace)}
          exception={ruleDomain ? { domain: ruleDomain, kind: ruleKind } : null}
          refresh={refreshProtectionData}
          onManageBlocklists={() => navigateToPage("blocklists")}
        />;
      case "blocklists":
        if (!blocklists.data) return error ? null : loadingPage;
        return <Blocklists key={params.get("list")} listID={positiveID(params.get("list"))} blocklists={blocklists.data} refresh={refreshBlocklists} />;
      case "settings":
        if (!settingsCollection.data) return error ? null : loadingPage;
        return <Settings onManageLocalNames={() => navigateToPage("records")} settings={settings} refresh={refreshSettings} onManageUpstreams={() => navigateToPage("upstreams")} />;
    }
  })();

  const releaseUpdate = versionInfo?.latest ?? null;
  const showReleaseUpdate = releaseUpdate !== null && releaseUpdate.version !== dismissedReleaseVersion;

  const layout = (
    <Layout
      overviewStatus={networkStatus}
      page={page}
      setPage={navigateToPage}
      themeMode={themeMode}
      onThemeModeChange={onThemeModeChange}
      apiState={apiState}
      onOpenSearch={() => setSearchSession((current) => current ?? { query: "", index: 0 })}
      notifications={notifications}
      onOpenNotifications={() => setNotificationOpen(true)}
      username={username}
      onSignOut={signOut}
      appVersion={versionInfo}
      releaseUpdate={releaseUpdate}
      showReleaseUpdate={showReleaseUpdate}
      onDismissReleaseUpdate={dismissReleaseUpdate}
    >
      {error && (
        <div className="error-banner">
          <strong>Faro API is not reachable.</strong>
          <span>{error}</span>
          <Button variant="outline" className="secondary" type="button" onClick={retry}>
            Retry
          </Button>
        </div>
      )}
      <Suspense key={page} fallback={loadingPage}>{content}</Suspense>
      <Suspense fallback={null}>
        {selectedDomain && <DomainDrawer key={selectedDomain} domain={selectedDomain} onClose={closeDomain} onChanged={refreshRules} />}
        {searchSession && <GlobalSearch session={searchSession} onClose={closeSearch} onSelect={selectSearchResult} />}
        {notificationOpen && <NotificationDrawer
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
        />}
      </Suspense>
    </Layout>
  );

  return (
    <FaviconEnabledContext.Provider value={settings.some((setting) => setting.key === "favicon_fetching_enabled" && setting.value === "true")}>
      {layout}
    </FaviconEnabledContext.Provider>
  );
}

function readRoute(): AppRoute {
  const pathname = normalizePath(window.location.pathname);
  const domain = new URLSearchParams(window.location.search).get("domain");
  const directPage = (Object.entries(pagePaths) as [Page, string][]).find(([, path]) => path === pathname)?.[0];
  if (directPage) return { page: directPage, clientIP: null, domain, canonicalPath: pagePaths[directPage] + window.location.search };

  if (pathname.startsWith("/devices/")) {
    const encodedClientIP = pathname.slice("/devices/".length);
    try {
      const clientIP = decodeURIComponent(encodedClientIP);
      if (clientIP.trim()) return { page: "devices", clientIP, domain, canonicalPath: `/devices/${encodeURIComponent(clientIP)}` + window.location.search };
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
  let normalized = pathname;
  while (normalized.length > 1 && normalized.endsWith("/")) {
    normalized = normalized.slice(0, -1);
  }
  return normalized || "/";
}

function withDomain(pathname: string, domain: string | null) {
  if (!domain) return pathname;
  const params = new URLSearchParams({ domain });
  return `${pathname}?${params.toString()}`;
}

function positiveID(value: string | null) {
  const id = Number(value);
  return Number.isSafeInteger(id) && id > 0 ? id : undefined;
}

function pushRoute(target: string, replace = false) {
  const current = window.location.pathname + window.location.search + window.location.hash;
  if (current === target && !window.history.state?.faroSearch) return;
  const oldHash = window.location.hash;
  if (replace) window.history.replaceState({}, "", target);
  else window.history.pushState({}, "", target);
  if (oldHash !== window.location.hash) window.dispatchEvent(new Event("hashchange"));
}
