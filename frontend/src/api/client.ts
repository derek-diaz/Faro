export type DNSRecord = {
  id: number;
  hostname: string;
  type: "A" | "AAAA";
  value: string;
  description: string;
  created_at?: string;
  updated_at?: string;
};

export type Blocklist = {
  id: number;
  name: string;
  url: string;
  enabled: boolean;
  entry_count: number;
  protection_count?: number;
  last_refreshed_at?: string | null;
  created_at?: string;
  updated_at?: string;
};

export type DomainEntry = {
  id: number;
  domain: string;
  created_at?: string;
};

export type Protection = {
  id: number;
  name: string;
  icon: ProtectionIconKey;
  is_default: boolean;
  blocklist_ids: number[];
  allow_entries: DomainEntry[];
  block_entries: DomainEntry[];
  device_ips: string[];
  created_at?: string;
  updated_at?: string;
};

export type ProtectionIconKey = "house" | "users" | "baby" | "guest" | "tv" | "gamepad" | "smartphone" | "laptop" | "briefcase" | "lightbulb" | "cpu" | "shield";

export type ProtectionInput = {
  name: string;
  icon: ProtectionIconKey;
  blocklist_ids: number[];
  allow_domains: string[];
  block_domains: string[];
  device_ips: string[];
};

export type DNSQuery = {
  id?: number;
  timestamp: string;
  client_ip: string;
  domain: string;
  query_type: string;
  action: "allowed" | "blocked";
  source: string;
  upstream?: string | null;
  latency_ms?: number | null;
  rcode?: string;
  decision_reason?: string;
  decision?: DNSDecision;
};

export type DecisionRule = {
  kind: "allowlist" | "manual_block" | "blocklist" | string;
  id: number;
  name: string;
};

export type DecisionLocalRecord = {
  id: number;
  type: string;
  value: string;
};

export type DNSDecision = {
  action?: "allowed" | "blocked";
  reason?: string;
  protection?: DecisionRule;
  allowlist?: DecisionRule;
  manual_block?: DecisionRule;
  blocklists?: DecisionRule[];
  local_record?: DecisionLocalRecord;
  confidence?: "observed" | "inferred" | "configuration_snapshot" | string;
  captured_at?: string;
  upstream?: string;
  response_code?: string;
};

export type DeviceSummary = {
  device_id: number;
  client_ip: string;
  addresses?: string[];
  address_history?: Array<{
    address: string;
    family: "ipv4" | "ipv6" | string;
    source: string;
    confidence: string;
    first_seen: string;
    last_seen: string;
  }>;
  identity_source?: string;
  name: string;
  display_name?: string;
  name_source?: "manual" | "local_dns" | "reverse_dns" | string;
  location?: string | null;
  notes?: string | null;
  device_type: string;
  device_icon?: string;
  type_category?: string;
  type_confidence?: "high" | "medium" | "unknown" | string;
  type_source?: "manual" | "automatic" | string;
  classification?: {
    source: "manual" | "automatic" | string;
    definition_id: string;
    predicted_type: string;
    category: string;
    icon: string;
    confidence: "high" | "medium" | "unknown" | string;
    score: number;
    catalog_version: string;
    evidence: Array<{
      kind: "hostname" | "domain" | "address" | "conflict" | string;
      value: string;
      description: string;
      weight: number;
    }>;
    evaluated_at: string;
  };
  total_queries_today: number;
  blocked_queries_today: number;
  block_percentage: number;
  top_domains: CountItem[];
  last_seen?: string | null;
  first_seen?: string | null;
  profile: string;
  protection?: string;
  protection_id: number;
  protection_icon: ProtectionIconKey;
  recent_activity?: DNSQuery[];
};

export type DeviceInventoryPage = {
  items: DeviceSummary[];
  page: number;
  page_size: number;
  total: number;
  total_pages: number;
  revision: string;
  summary: {
    observed: number;
    active_today: number;
    requests_today: number;
    blocked_today: number;
    most_active_name: string;
    most_active_requests: number;
  };
};

export type DeviceInventoryResult = {
  page: DeviceInventoryPage | null;
  etag: string;
  notModified: boolean;
};

export type ReplayBucket = {
  timestamp: string;
  total: number;
  blocked: number;
};

export type DeviceReplay = {
  client_ip: string;
  range: string;
  from: string;
  to: string;
  bucket_seconds: number;
  total_queries: number;
  blocked_queries: number;
  unique_domains: number;
  queries_per_minute: number;
  first_seen?: string | null;
  last_seen?: string | null;
  buckets: ReplayBucket[];
  top_domains: CountItem[];
  sources: CountItem[];
  events: DNSQuery[];
  truncated: boolean;
};

export type DomainSummary = {
  domain: string;
  total_queries_today: number;
  blocked_queries_today: number;
  first_seen?: string | null;
  last_seen?: string | null;
  clients: CountItem[];
  query_types: CountItem[];
  status: "Allowed" | "Blocked" | "Mixed";
  recent_queries: DNSQuery[];
  recent_events: FaroEvent[];
};

export type NetworkSummary = {
  headline: string;
  messages: string[];
};

export type FaroEvent = {
  id: string;
  timestamp: string;
  type:
    | "dns.query"
    | "dns.blocked"
    | "device.first_seen"
    | "device.alias_updated"
    | "blocklist.installed"
    | "blocklist.updated"
    | "dns.reload"
    | "dns.reload_failed"
    | "upstream.changed"
    | string;
  severity: "info" | "success" | "warning" | "critical" | string;
  title: string;
  description: string;
  client_ip?: string | null;
  domain?: string | null;
  metadata: Record<string, unknown>;
  source: string;
  is_read?: boolean;
};

export type ActivityCounts = {
  all: number;
  dns: number;
  cache: number;
  upstream: number;
  blocked: number;
  system: number;
};

export type ActivityPage = {
  items: FaroEvent[];
  page: number;
  page_size: number;
  total: number;
  total_pages: number;
  counts: ActivityCounts;
};

export type HealthCard = {
  label: string;
  value: string;
  detail: string;
  status: "healthy" | "info" | "warning" | "critical" | string;
};

export type DashboardStory = {
  title: string;
  body: string;
  tone: "success" | "info" | "warning" | "critical" | string;
};

export type WhatsNewItem = {
  label: string;
  subtitle?: string | null;
};

export type WhatsNew = {
  devices: WhatsNewItem[];
  domains: WhatsNewItem[];
  blocklists: WhatsNewItem[];
  local_records: WhatsNewItem[];
};

export type DashboardSummary = {
  total_queries_today: number;
  blocked_queries_today: number;
  block_percentage: number;
  enabled_blocklists: number;
  blocklist_entries: number;
  cache: CacheSummary;
  network_summary: NetworkSummary;
  health_cards: HealthCard[];
  stories: DashboardStory[];
  whats_new: WhatsNew;
  sparklines: Record<string, number[]>;
  top_queried_domains: CountItem[];
  top_blocked_domains: CountItem[];
  top_clients: CountItem[];
  recent_activity: DNSQuery[];
  upstream_health: string;
  upstream_health_status: string;
  upstream_checked_at?: string;
  upstream_probes?: UpstreamProbe[];
  favicon_fetching_enabled: string;
};

export type CacheSummary = {
  enabled: boolean;
  metrics_available: boolean;
  entries: number;
  hits_since_restart: number;
  requests_since_restart: number;
  hit_rate_since_restart: number;
  hits_today: number;
  upstream_queries_today: number;
  hit_rate_today: number;
  average_cache_latency_ms: number;
  average_upstream_latency_ms: number;
};

export type CountItem = {
  label: string;
  count: number;
};

export type Setting = {
  key: string;
  value: string;
  updated_at?: string;
};

export type SearchItem = {
  label: string;
  subtitle?: string | null;
  count?: number;
};

export type SearchResults = {
  domains: SearchItem[];
  devices: SearchItem[];
  events: SearchItem[];
  local_records: SearchItem[];
  rules: SearchItem[];
  blocklists: SearchItem[];
};

export type NotificationsResponse = {
  attention_count: number;
  unread_count: number;
  items: FaroEvent[];
};

export type UpstreamProbe = {
  address: string;
  status: "online" | "unavailable";
  latency_ms: number | null;
  checked_at: string;
  error?: string;
};

export type UpstreamProbeResponse = {
  items: UpstreamProbe[];
};

export type EncryptedUpstreamEndpoint = {
  name: string;
  url: string;
  bootstrap_ips: string[];
};

export type UpstreamCatalogResponse = {
  encrypted_endpoints: EncryptedUpstreamEndpoint[];
};

export type MaintenanceStorage = {
  database_bytes: number;
  database_used_bytes: number;
  database_reclaimable_bytes: number;
  query_count: number;
  event_count: number;
  oldest_query?: string;
  oldest_event?: string;
  retention_days: number;
  retention_cutoff: string;
  last_pruned_at?: string;
  last_queries_deleted: number;
  last_events_deleted: number;
};

export type MaintenanceStatus = {
  status: "healthy" | "degraded" | string;
  process_memory_bytes: number;
  uptime_seconds: number;
  storage: MaintenanceStorage;
};

export type PruneResult = {
  queries_deleted: number;
  events_deleted: number;
  before_bytes: number;
  after_bytes: number;
  reclaimed_bytes: number;
  retention_days: number;
  cutoff: string;
  compacted: boolean;
  completed_at: string;
};

export type BackupRestoreResult = {
  ok: boolean;
  restored_at: string;
  backup_created: string;
  dns_reloaded: boolean;
  warning?: string;
  requires_login: boolean;
};

export type UnifiSite = {
  id: string;
  internalReference?: string;
  name: string;
};

export type UnifiCertificate = {
  fingerprint_sha256: string;
  subject: string;
  issuer: string;
  expires_at: string;
};

export type UnifiConnectionTest = {
  ok: boolean;
  sites: UnifiSite[];
  requires_certificate_trust: boolean;
  certificate?: UnifiCertificate;
};

export type UnifiStatus = {
  configured: boolean;
  enabled: boolean;
  base_url: string;
  site_id: string;
  site_name: string;
  api_key_configured: boolean;
  tls_mode: "verified" | "pinned" | string;
  tls_fingerprint?: string;
  last_sync_at?: string;
  last_error?: string;
  synced_devices: number;
};

export type UnifiSyncResult = {
  synced_devices: number;
  skipped: number;
  completed_at: string;
};

export type AuthStatus = {
  configured: boolean;
  authenticated: boolean;
  onboarding_complete: boolean;
  username?: string;
};

export type RedundancyRole = "standalone" | "controller" | "replica";

export type RedundancyPublicStatus = {
  role: RedundancyRole;
  home_id?: string;
  node_id: string;
  node_name: string;
  controller_url?: string;
  config_revision: number;
  last_sync_at?: string;
  last_error?: string;
};

export type RedundancyNode = {
  node_id: string;
  name: string;
  lan_address?: string;
  role: "controller" | "replica";
  online: boolean;
  config_revision: number;
  last_seen_at?: string;
  last_sync_at?: string;
  last_error?: string;
};

export type RedundancyStatus = RedundancyPublicStatus & {
  healthy: boolean;
  controller_name?: string;
  lan_address?: string;
  nodes: RedundancyNode[];
};

export type PairingCode = {
  code: string;
  expires_at: string;
};

const API_BASE = import.meta.env.VITE_API_BASE_URL ?? "";
const BROWSER_TIMEZONE = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${API_BASE}${path}`, {
    headers: { "Content-Type": "application/json", "X-Faro-Timezone": BROWSER_TIMEZONE, ...(init?.headers ?? {}) },
    ...init
  });
  if (response.status === 401) {
    window.dispatchEvent(new Event("faro:unauthorized"));
  }
  if (!response.ok) {
    const body = await response.json().catch(() => ({ error: response.statusText }));
    throw new Error(body.error ?? response.statusText);
  }
  return response.json() as Promise<T>;
}

async function deviceInventoryRequest(
  options: { page: number; pageSize: number; search: string; sort: string; direction: string; activeToday: boolean },
  etag = "",
  signal?: AbortSignal
): Promise<DeviceInventoryResult> {
  const query = new URLSearchParams({
    format: "page",
    page: String(options.page),
    page_size: String(options.pageSize),
    search: options.search,
    sort: options.sort,
    direction: options.direction,
    active_today: String(options.activeToday)
  });
  const response = await fetch(`${API_BASE}/api/devices?${query}`, {
    signal,
    headers: {
      "Content-Type": "application/json",
      "X-Faro-Timezone": BROWSER_TIMEZONE,
      ...(etag ? { "If-None-Match": etag } : {})
    }
  });
  if (response.status === 401) window.dispatchEvent(new Event("faro:unauthorized"));
  if (response.status === 304) {
    return { page: null, etag: response.headers.get("ETag") ?? etag, notModified: true };
  }
  if (!response.ok) {
    const body = await response.json().catch(() => ({ error: response.statusText }));
    throw new Error(body.error ?? response.statusText);
  }
  return {
    page: await response.json() as DeviceInventoryPage,
    etag: response.headers.get("ETag") ?? "",
    notModified: false
  };
}

async function backupRequest(path: string, init: RequestInit): Promise<Response> {
  const response = await fetch(`${API_BASE}${path}`, {
    ...init,
    headers: { "X-Faro-Timezone": BROWSER_TIMEZONE, ...(init.headers ?? {}) }
  });
  if (response.status === 401) window.dispatchEvent(new Event("faro:unauthorized"));
  if (!response.ok) {
    const body = await response.json().catch(() => ({ error: response.statusText }));
    throw new Error(body.error ?? response.statusText);
  }
  return response;
}

export const api = {
  authStatus: () => request<AuthStatus>("/api/auth/status"),
  setupAuth: (username: string, password: string) => request<{ ok: boolean; username: string }>("/api/auth/setup", { method: "POST", body: JSON.stringify({ username, password }) }),
  login: (username: string, password: string) => request<{ ok: boolean; username: string }>("/api/auth/login", { method: "POST", body: JSON.stringify({ username, password }) }),
  logout: () => request<{ ok: boolean }>("/api/auth/logout", { method: "POST" }),
  changePassword: (currentPassword: string, newPassword: string) =>
    request<{ ok: boolean }>("/api/auth/password", { method: "POST", body: JSON.stringify({ current_password: currentPassword, new_password: newPassword }) }),
  redundancyPublic: () => request<RedundancyPublicStatus>("/api/redundancy/public"),
  redundancyStatus: () => request<RedundancyStatus>("/api/redundancy"),
  leaveRedundancy: () =>
    request<{ status: RedundancyPublicStatus }>("/api/redundancy", { method: "DELETE" }),
  startRedundancyPairing: (nodeName: string) =>
    request<PairingCode>("/api/redundancy/pairing", { method: "POST", body: JSON.stringify({ node_name: nodeName }) }),
  joinRedundancy: (input: { controller_url: string; pairing_code: string; node_name: string; lan_address: string }) =>
    request<{ status: RedundancyPublicStatus }>("/api/redundancy/join", { method: "POST", body: JSON.stringify(input) }),
  removeRedundancyNode: (nodeID: string) =>
    request<{ ok: boolean }>(`/api/redundancy/nodes/${encodeURIComponent(nodeID)}`, { method: "DELETE" }),
  dashboard: () => request<DashboardSummary>("/api/dashboard"),
  queries: (search = "") => request<DNSQuery[]>(`/api/queries?search=${encodeURIComponent(search)}`),
  events: (search = "", scope = "all", page = 1, pageSize = 50) =>
    request<ActivityPage>(`/api/events?search=${encodeURIComponent(search)}&scope=${encodeURIComponent(scope)}&page=${page}&page_size=${pageSize}`),
  notifications: () => request<NotificationsResponse>("/api/notifications"),
  markNotificationRead: (id: string) => request<{ ok: boolean }>(`/api/notifications/${encodeURIComponent(id)}/read`, { method: "PUT" }),
  dismissNotification: (id: string) => request<{ ok: boolean }>(`/api/notifications/${encodeURIComponent(id)}`, { method: "DELETE" }),
  markAllNotificationsRead: () => request<{ ok: boolean }>("/api/notifications/read-all", { method: "POST" }),
  upstreamCatalog: () => request<UpstreamCatalogResponse>("/api/upstreams/catalog"),
  probeUpstreams: (addresses: string[], transport: "encrypted" | "standard" = "standard") =>
    request<UpstreamProbeResponse>("/api/upstreams/probe", { method: "POST", body: JSON.stringify({ addresses, transport }) }),
  devices: () => request<DeviceSummary[]>("/api/devices"),
  deviceInventory: deviceInventoryRequest,
  device: (clientIP: string) => request<DeviceSummary>(`/api/devices/${encodeURIComponent(clientIP)}`),
  deviceReplay: (clientIP: string, range = "7d") => request<DeviceReplay>(`/api/devices/${encodeURIComponent(clientIP)}/replay?range=${encodeURIComponent(range)}`),
  updateDeviceAlias: (clientIP: string, alias: { name: string; location?: string; notes?: string; device_type?: string }) =>
    request<{ ok: boolean }>(`/api/devices/${encodeURIComponent(clientIP)}/alias`, { method: "PUT", body: JSON.stringify(alias) }),
  domainSummary: (domain: string) => request<DomainSummary>(`/api/domains/${encodeURIComponent(domain)}/summary`),
  search: (q: string) => request<SearchResults>(`/api/search?q=${encodeURIComponent(q)}`),
  records: () => request<DNSRecord[]>("/api/dns-records"),
  createRecord: (record: Omit<DNSRecord, "id">) =>
    request<{ id: number }>("/api/dns-records", { method: "POST", body: JSON.stringify(record) }),
  updateRecord: (record: DNSRecord) =>
    request<{ ok: boolean }>(`/api/dns-records/${record.id}`, { method: "PUT", body: JSON.stringify(record) }),
  deleteRecord: (id: number) => request<{ ok: boolean }>(`/api/dns-records/${id}`, { method: "DELETE" }),
  blocklists: () => request<Blocklist[]>("/api/blocklists"),
  createBlocklist: (blocklist: { name: string; url: string; enabled: boolean; assign_to_default?: boolean }) =>
    request<{ id: number }>("/api/blocklists", { method: "POST", body: JSON.stringify(blocklist) }),
  updateBlocklist: (blocklist: Blocklist) =>
    request<{ ok: boolean }>(`/api/blocklists/${blocklist.id}`, { method: "PUT", body: JSON.stringify(blocklist) }),
  refreshBlocklist: (id: number) => request<{ entry_count: number }>(`/api/blocklists/${id}/refresh`, { method: "POST" }),
  refreshBlocklists: () => request<{ updated: number; entry_count: number }>("/api/blocklists/refresh", { method: "POST" }),
  deleteBlocklist: (id: number) => request<{ ok: boolean }>(`/api/blocklists/${id}`, { method: "DELETE" }),
  protections: () => request<Protection[]>("/api/protections"),
  createProtection: (protection: ProtectionInput) =>
    request<{ id: number }>("/api/protections", { method: "POST", body: JSON.stringify(protection) }),
  updateProtection: (id: number, protection: ProtectionInput) =>
    request<{ ok: boolean }>(`/api/protections/${id}`, { method: "PUT", body: JSON.stringify(protection) }),
  deleteProtection: (id: number) => request<{ ok: boolean }>(`/api/protections/${id}`, { method: "DELETE" }),
  assignDeviceProtection: (clientIP: string, protectionID: number) =>
    request<{ ok: boolean }>(`/api/devices/${encodeURIComponent(clientIP)}/protection`, { method: "PUT", body: JSON.stringify({ protection_id: protectionID }) }),
  allowlist: () => request<DomainEntry[]>("/api/allowlist"),
  blockDomains: () => request<DomainEntry[]>("/api/blocklist-domains"),
  addAllow: (domain: string) => request<{ id: number }>("/api/allowlist", { method: "POST", body: JSON.stringify({ domain }) }),
  addBlock: (domain: string) =>
    request<{ id: number }>("/api/blocklist-domains", { method: "POST", body: JSON.stringify({ domain }) }),
  deleteAllow: (id: number) => request<{ ok: boolean }>(`/api/allowlist/${id}`, { method: "DELETE" }),
  deleteBlock: (id: number) => request<{ ok: boolean }>(`/api/blocklist-domains/${id}`, { method: "DELETE" }),
  settings: () => request<Setting[]>("/api/settings"),
  updateSettings: (settings: Record<string, string>) =>
    request<{ ok: boolean }>("/api/settings", { method: "PUT", body: JSON.stringify(settings) }),
  unifiStatus: () => request<UnifiStatus>("/api/integrations/unifi"),
  testUnifi: (input: { base_url: string; api_key: string; tls_fingerprint?: string }) =>
    request<UnifiConnectionTest>("/api/integrations/unifi/test", { method: "POST", body: JSON.stringify(input) }),
  configureUnifi: (input: { base_url: string; api_key: string; site_id: string; tls_fingerprint?: string }) =>
    request<UnifiStatus>("/api/integrations/unifi", { method: "PUT", body: JSON.stringify(input) }),
  syncUnifi: () => request<UnifiSyncResult>("/api/integrations/unifi/sync", { method: "POST" }),
  disconnectUnifi: () => request<{ ok: boolean }>("/api/integrations/unifi", { method: "DELETE" }),
  maintenance: () => request<MaintenanceStatus>("/api/maintenance"),
  prune: (retentionDays: number, compact: boolean) =>
    request<PruneResult>("/api/maintenance", { method: "POST", body: JSON.stringify({ retention_days: retentionDays, compact }) }),
  exportBackup: async (passphrase: string) => {
    const response = await backupRequest("/api/backups", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ passphrase })
    });
    const disposition = response.headers.get("Content-Disposition") ?? "";
    const filename = disposition.match(/filename="([^"]+)"/)?.[1] ?? "faro-backup.faro-backup";
    return { blob: await response.blob(), filename };
  },
  restoreBackup: async (file: File, passphrase: string) => {
    const body = new FormData();
    body.append("passphrase", passphrase);
    body.append("backup", file);
    const response = await backupRequest("/api/backups/restore", { method: "POST", body });
    return response.json() as Promise<BackupRestoreResult>;
  },
  reload: () => request<{ ok: boolean }>("/api/reload", { method: "POST" })
};
