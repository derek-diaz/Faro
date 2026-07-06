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

export type DashboardSummary = {
  total_queries_today: number;
  blocked_queries_today: number;
  block_percentage: number;
  enabled_blocklists: number;
  blocklist_entries: number;
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
