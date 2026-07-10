export type DNSRecord = {
  id: number;
  hostname: string;
  type: "A" | "AAAA" | "CNAME";
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
  last_refreshed_at?: string | null;
};

export type DomainEntry = {
  id: number;
  domain: string;
  created_at?: string;
};

export type DNSQuery = {
  id?: number;
  timestamp: string;
  client_ip: string;
  domain: string;
  query_type: string;
  action: "allowed" | "blocked";
  source: string;
  latency_ms?: number | null;
};

export type DeviceSummary = {
  client_ip: string;
  name: string;
  location?: string | null;
  notes?: string | null;
  device_type: string;
  total_queries_today: number;
  blocked_queries_today: number;
  block_percentage: number;
  top_domains: CountItem[];
  last_seen?: string | null;
  first_seen?: string | null;
  profile: string;
  recent_activity?: DNSQuery[];
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
  favicon_fetching_enabled: string;
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
  unread_count: number;
  items: FaroEvent[];
};

const API_BASE = import.meta.env.VITE_API_BASE_URL ?? "";

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${API_BASE}${path}`, {
    headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) },
    ...init
  });
  if (!response.ok) {
    const body = await response.json().catch(() => ({ error: response.statusText }));
    throw new Error(body.error ?? response.statusText);
  }
  return response.json() as Promise<T>;
}

export const api = {
  dashboard: () => request<DashboardSummary>("/api/dashboard"),
  queries: (search = "") => request<DNSQuery[]>(`/api/queries?search=${encodeURIComponent(search)}`),
  events: (search = "") => request<FaroEvent[]>(`/api/events?search=${encodeURIComponent(search)}`),
  notifications: () => request<NotificationsResponse>("/api/notifications"),
  devices: () => request<DeviceSummary[]>("/api/devices"),
  device: (clientIP: string) => request<DeviceSummary>(`/api/devices/${encodeURIComponent(clientIP)}`),
  updateDeviceAlias: (clientIP: string, alias: { name: string; location?: string; notes?: string }) =>
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
  createBlocklist: (blocklist: { name: string; url: string; enabled: boolean }) =>
    request<{ id: number }>("/api/blocklists", { method: "POST", body: JSON.stringify(blocklist) }),
  updateBlocklist: (blocklist: Blocklist) =>
    request<{ ok: boolean }>(`/api/blocklists/${blocklist.id}`, { method: "PUT", body: JSON.stringify(blocklist) }),
  refreshBlocklist: (id: number) => request<{ entry_count: number }>(`/api/blocklists/${id}/refresh`, { method: "POST" }),
  deleteBlocklist: (id: number) => request<{ ok: boolean }>(`/api/blocklists/${id}`, { method: "DELETE" }),
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
  reload: () => request<{ ok: boolean }>("/api/reload", { method: "POST" })
};
