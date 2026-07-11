import { AlertCircle, Ban, Check, CheckCircle2, Database, Download, Plus, RefreshCw, Search, ShieldCheck, Trash2, X } from "lucide-react";
import { useMemo, useState, type FormEvent, type ReactNode } from "react";
import { api, type Blocklist, type DomainEntry } from "../api/client";
import { EmptyState } from "../components/EmptyState";

type BlocklistsProps = {
  blocklists: Blocklist[];
  manualBlocks: DomainEntry[];
  refresh: () => Promise<void>;
};

type View = "installed" | "available";
type Notice = { tone: "success" | "error"; text: string } | null;

export function Blocklists({ blocklists, manualBlocks, refresh }: BlocklistsProps) {
  const [view, setView] = useState<View>("installed");
  const [form, setForm] = useState({ name: "", url: "", enabled: true });
  const [showCustom, setShowCustom] = useState(false);
  const [busy, setBusy] = useState<number | null>(null);
  const [installing, setInstalling] = useState<string | null>(null);
  const [updatingAll, setUpdatingAll] = useState(false);
  const [catalogQuery, setCatalogQuery] = useState("");
  const [catalogCategory, setCatalogCategory] = useState("All");
  const [notice, setNotice] = useState<Notice>(null);
  const [manualDomain, setManualDomain] = useState("");
  const [manualBusy, setManualBusy] = useState<string | null>(null);

  const enabledCount = blocklists.filter((blocklist) => blocklist.enabled).length;
  const entryCount = blocklists.reduce((total, blocklist) => total + (blocklist.entry_count ?? 0), 0);
  const needsUpdate = blocklists.filter((blocklist) => isStale(blocklist.last_refreshed_at)).length;
  const available = useMemo(() => blocklistCatalog.filter((item) => !isInstalled(item, blocklists)), [blocklists]);
  const filteredCatalog = available.filter((item) => {
    const query = catalogQuery.trim().toLowerCase();
    const matchesQuery = !query || `${item.name} ${item.provider} ${item.description} ${item.tags.join(" ")}`.toLowerCase().includes(query);
    return matchesQuery && (catalogCategory === "All" || item.category === catalogCategory);
  });

  async function add(event: FormEvent) {
    event.preventDefault();
    setInstalling("custom");
    setNotice(null);
    let created = false;
    try {
      const result = await api.createBlocklist(form);
      created = true;
      await api.refreshBlocklist(result.id);
      setForm({ name: "", url: "", enabled: true });
      setShowCustom(false);
      setView("installed");
      setNotice({ tone: "success", text: `${form.name} was installed and updated.` });
    } catch (caught) {
      setNotice({ tone: "error", text: created ? "The list was added, but its first update failed. Try updating it from Installed." : errorMessage(caught, "Could not add the blocklist.") });
    } finally {
      await refresh();
      setInstalling(null);
    }
  }

  async function toggle(blocklist: Blocklist) {
    setBusy(blocklist.id);
    setNotice(null);
    try {
      await api.updateBlocklist({ ...blocklist, enabled: !blocklist.enabled });
      await refresh();
    } catch (caught) {
      setNotice({ tone: "error", text: errorMessage(caught, "Could not change the blocklist status.") });
    } finally {
      setBusy(null);
    }
  }

  async function refreshList(blocklist: Blocklist) {
    setBusy(blocklist.id);
    setNotice(null);
    try {
      const result = await api.refreshBlocklist(blocklist.id);
      await refresh();
      setNotice({ tone: "success", text: `${blocklist.name} updated with ${formatNumber(result.entry_count)} entries.` });
    } catch (caught) {
      setNotice({ tone: "error", text: errorMessage(caught, `Could not update ${blocklist.name}.`) });
    } finally {
      setBusy(null);
    }
  }

  async function refreshAll() {
    setUpdatingAll(true);
    setNotice(null);
    try {
      const result = await api.refreshBlocklists();
      await refresh();
      setNotice({ tone: "success", text: `Updated ${result.updated} enabled list${result.updated === 1 ? "" : "s"} with ${formatNumber(result.entry_count)} entries.` });
    } catch (caught) {
      setNotice({ tone: "error", text: errorMessage(caught, "Could not update all blocklists.") });
    } finally {
      setUpdatingAll(false);
    }
  }

  async function installCatalog(item: CatalogBlocklist) {
    setInstalling(item.id);
    setNotice(null);
    let created = false;
    try {
      const result = await api.createBlocklist({ name: item.name, url: item.url, enabled: true });
      created = true;
      await api.refreshBlocklist(result.id);
      await refresh();
      setNotice({ tone: "success", text: `${item.name} was installed and is now active.` });
    } catch (caught) {
      await refresh();
      setNotice({ tone: "error", text: created ? `${item.name} was added, but its first update failed.` : errorMessage(caught, `Could not install ${item.name}.`) });
    } finally {
      setInstalling(null);
    }
  }

  async function remove(blocklist: Blocklist) {
    if (!window.confirm(`Remove ${blocklist.name}? Its downloaded entries will also be deleted.`)) return;
    setBusy(blocklist.id);
    setNotice(null);
    try {
      await api.deleteBlocklist(blocklist.id);
      await refresh();
      setNotice({ tone: "success", text: `${blocklist.name} was removed.` });
    } catch (caught) {
      setNotice({ tone: "error", text: errorMessage(caught, `Could not remove ${blocklist.name}.`) });
    } finally {
      setBusy(null);
    }
  }

  async function addManualBlock(event: FormEvent) {
    event.preventDefault();
    setManualBusy("add");
    setNotice(null);
    try {
      await api.addBlock(manualDomain);
      setManualDomain("");
      await refresh();
      setNotice({ tone: "success", text: "Manual domain block added." });
    } catch (caught) {
      setNotice({ tone: "error", text: errorMessage(caught, "Could not block the domain.") });
    } finally {
      setManualBusy(null);
    }
  }

  async function removeManualBlock(entry: DomainEntry) {
    setManualBusy(String(entry.id));
    setNotice(null);
    try {
      await api.deleteBlock(entry.id);
      await refresh();
      setNotice({ tone: "success", text: `${entry.domain} is no longer manually blocked.` });
    } catch (caught) {
      setNotice({ tone: "error", text: errorMessage(caught, `Could not remove ${entry.domain}.`) });
    } finally {
      setManualBusy(null);
    }
  }

  return (
    <div className="blocklists-page">
      <div className="blocklist-page-toolbar">
        <div className="blocklist-view-tabs" role="tablist" aria-label="Blocklist views">
          <button type="button" role="tab" aria-selected={view === "installed"} className={view === "installed" ? "active" : ""} onClick={() => setView("installed")}>Installed <span>{blocklists.length}</span></button>
          <button type="button" role="tab" aria-selected={view === "available"} className={view === "available" ? "active" : ""} onClick={() => setView("available")}>Available <span>{available.length}</span></button>
        </div>
        <div className="blocklist-primary-actions">
          {view === "installed" && <button type="button" className="secondary" disabled={updatingAll || enabledCount === 0} onClick={() => void refreshAll()}><RefreshCw className={updatingAll ? "spinning" : ""} size={16} /><span>{updatingAll ? "Updating" : "Update all"}</span></button>}
          <button type="button" onClick={() => setShowCustom(true)}><Plus size={16} /><span>Add custom</span></button>
        </div>
      </div>

      {notice && <div className={`blocklist-notice ${notice.tone}`} role="status">{notice.tone === "success" ? <CheckCircle2 size={17} /> : <AlertCircle size={17} />}<span>{notice.text}</span><button type="button" className="icon-button" aria-label="Dismiss message" onClick={() => setNotice(null)}><X size={15} /></button></div>}

      {view === "installed" ? (
        <>
          <section className="blocklist-summary-strip" aria-label="Installed blocklist summary">
            <SummaryItem icon={<ShieldCheck size={17} />} label="Enabled lists" value={String(enabledCount)} />
            <SummaryItem icon={<Database size={17} />} label="List entries" value={formatNumber(entryCount)} />
            <SummaryItem icon={<RefreshCw size={17} />} label="Need update" value={String(needsUpdate)} tone={needsUpdate > 0 ? "warning" : "healthy"} />
            <SummaryItem icon={<Check size={17} />} label="DNS filtering" value={enabledCount > 0 || manualBlocks.length > 0 ? "Active" : "Off"} tone={enabledCount > 0 || manualBlocks.length > 0 ? "healthy" : "warning"} />
          </section>

          <section className="panel installed-blocklists-panel">
            <div className="installed-list-heading">
              <div><h2>Installed lists</h2><p>Enable, update, or remove lists currently used by Faro.</p></div>
              <span>{enabledCount} of {blocklists.length} enabled</span>
            </div>
            {blocklists.length === 0 ? (
              <EmptyState title="No blocklists installed" body="Browse available lists or add a custom URL to start filtering domains." action={<button type="button" onClick={() => setView("available")}>Browse available lists</button>} />
            ) : (
              <div className="installed-blocklist-table">
                <div className="installed-blocklist-columns" aria-hidden="true"><span>List</span><span>Entries</span><span>Last updated</span><span>Status</span><span>Actions</span></div>
                {blocklists.map((blocklist) => (
                  <article className="installed-blocklist-row" key={blocklist.id}>
                    <div className="installed-list-identity"><span className="blocklist-source-mark">{sourceInitial(blocklist)}</span><div><strong>{blocklist.name}</strong><span>{sourceLabel(blocklist.url)}</span></div></div>
                    <div className="installed-list-metric"><strong>{formatNumber(blocklist.entry_count ?? 0)}</strong><span>entries</span></div>
                    <div className="installed-list-updated"><strong>{formatUpdated(blocklist.last_refreshed_at)}</strong><span>{isStale(blocklist.last_refreshed_at) ? "Update recommended" : "Current"}</span></div>
                    <label className="blocklist-switch" title={blocklist.enabled ? "Disable list" : "Enable list"}>
                      <input type="checkbox" checked={blocklist.enabled} disabled={busy === blocklist.id} onChange={() => void toggle(blocklist)} aria-label={`${blocklist.enabled ? "Disable" : "Enable"} ${blocklist.name}`} />
                      <span aria-hidden="true" />
                      <em>{blocklist.enabled ? "Enabled" : "Paused"}</em>
                    </label>
                    <div className="installed-list-actions">
                      <button type="button" className="icon-button" title="Update now" aria-label={`Update ${blocklist.name}`} disabled={busy === blocklist.id} onClick={() => void refreshList(blocklist)}><RefreshCw className={busy === blocklist.id ? "spinning" : ""} size={16} /></button>
                      <button type="button" className="icon-button danger-icon" title="Remove" aria-label={`Remove ${blocklist.name}`} disabled={busy === blocklist.id} onClick={() => void remove(blocklist)}><Trash2 size={16} /></button>
                    </div>
                  </article>
                ))}
              </div>
            )}
          </section>

          <section className="panel manual-blocks-panel">
            <div className="manual-blocks-heading">
              <div><h2>Manual domain blocks</h2><p>Block individual domains without creating or installing a list.</p></div>
              <form className="manual-block-form" onSubmit={(event) => void addManualBlock(event)}>
                <input required value={manualDomain} onChange={(event) => setManualDomain(event.target.value)} placeholder="example.com" aria-label="Domain to block manually" />
                <button type="submit" disabled={manualBusy !== null || !manualDomain.trim()}><Ban size={16} /><span>Block domain</span></button>
              </form>
            </div>
            {manualBlocks.length === 0 ? (
              <div className="compact-empty manual-blocks-empty"><strong>No manual blocks</strong><span>Installed blocklists are still active. Add a domain here only when you need a specific override.</span></div>
            ) : (
              <div className="manual-block-list">
                {manualBlocks.map((entry) => (
                  <div className="manual-block-row" key={entry.id}>
                    <span className="manual-block-mark"><Ban size={15} /></span>
                    <strong>{entry.domain}</strong>
                    <span>{formatEntryDate(entry.created_at)}</span>
                    <button type="button" className="icon-button danger-icon" title="Remove manual block" aria-label={`Remove manual block for ${entry.domain}`} disabled={manualBusy !== null} onClick={() => void removeManualBlock(entry)}><Trash2 size={16} /></button>
                  </div>
                ))}
              </div>
            )}
          </section>
        </>
      ) : (
        <section className="panel blocklist-catalog-panel">
          <div className="catalog-heading">
            <div><h2>Available blocklists</h2><p>Choose a protection level that fits your network. Installed lists are hidden here.</p></div>
            <div className="catalog-filters">
              <label className="catalog-search"><Search size={16} /><input value={catalogQuery} onChange={(event) => setCatalogQuery(event.target.value)} placeholder="Search lists" aria-label="Search available blocklists" /></label>
              <select value={catalogCategory} onChange={(event) => setCatalogCategory(event.target.value)} aria-label="Filter blocklists by category">
                {catalogCategories.map((category) => <option key={category} value={category}>{category === "All" ? "All categories" : category}</option>)}
              </select>
            </div>
          </div>
          {filteredCatalog.length === 0 ? (
            <div className="compact-empty"><strong>No matching lists</strong><span>Try another search or category.</span></div>
          ) : (
            <div className="blocklist-catalog-grid">
              {filteredCatalog.map((item) => (
                <article className="catalog-blocklist-card" key={item.id}>
                  <div className="catalog-card-top"><span className="category-badge">{item.category}</span>{item.recommended && <span className="catalog-recommended"><CheckCircle2 size={13} /> Recommended</span>}</div>
                  <div><h3>{item.name}</h3><span className="catalog-provider">{item.provider} · {item.intensity}</span></div>
                  <p>{item.description}</p>
                  <div className="catalog-tags">{item.tags.map((tag) => <span key={tag}>{tag}</span>)}</div>
                  <button type="button" className="secondary catalog-install-button" disabled={installing === item.id} onClick={() => void installCatalog(item)}><Download size={16} /><span>{installing === item.id ? "Installing and updating" : "Install"}</span></button>
                </article>
              ))}
            </div>
          )}
        </section>
      )}

      {showCustom && (
        <div className="blocklist-modal-backdrop" role="presentation">
          <section className="blocklist-modal" role="dialog" aria-modal="true" aria-labelledby="custom-blocklist-title">
            <header><div><h2 id="custom-blocklist-title">Add custom blocklist</h2><p>Use a public hosts file or plain domain list URL.</p></div><button type="button" className="icon-button" aria-label="Close custom blocklist form" onClick={() => setShowCustom(false)}><X size={18} /></button></header>
            <form className="stack-form" onSubmit={(event) => void add(event)}>
              <label>Name<input required value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} placeholder="My blocklist" /></label>
              <label>URL<input required type="url" value={form.url} onChange={(event) => setForm({ ...form, url: event.target.value })} placeholder="https://example.com/hosts.txt" /></label>
              <label className="checkbox-row"><input type="checkbox" checked={form.enabled} onChange={(event) => setForm({ ...form, enabled: event.target.checked })} />Enable after installation</label>
              <div className="blocklist-modal-actions"><button type="button" className="secondary" onClick={() => setShowCustom(false)}>Cancel</button><button type="submit" disabled={installing === "custom"}><Plus size={16} /><span>{installing === "custom" ? "Adding and updating" : "Add blocklist"}</span></button></div>
            </form>
          </section>
        </div>
      )}
    </div>
  );
}

function SummaryItem({ icon, label, value, tone = "" }: { icon: ReactNode; label: string; value: string; tone?: string }) {
  return <div className={`blocklist-summary-item ${tone}`}>{icon}<span>{label}</span><strong>{value}</strong></div>;
}

type CatalogBlocklist = {
  id: string;
  name: string;
  provider: string;
  description: string;
  category: "Balanced" | "Privacy" | "Strict" | "Security" | "Classic";
  intensity: string;
  tags: string[];
  url: string;
  recommended?: boolean;
};

const blocklistCatalog: CatalogBlocklist[] = [
  { id: "oisd-small", name: "OISD Small", provider: "OISD", description: "A conservative all-purpose list designed to minimize site breakage.", category: "Balanced", intensity: "Light", tags: ["Ads", "Tracking"], url: "https://small.oisd.nl/", recommended: true },
  { id: "oisd-big", name: "OISD Big", provider: "OISD", description: "Broader coverage for ads, trackers, malware, and telemetry.", category: "Privacy", intensity: "Strong", tags: ["Ads", "Malware", "Telemetry"], url: "https://big.oisd.nl/" },
  { id: "hagezi-light", name: "HaGeZi Light", provider: "HaGeZi", description: "Light protection for networks where compatibility is the priority.", category: "Balanced", intensity: "Light", tags: ["Ads", "Tracking"], url: "https://raw.githubusercontent.com/hagezi/dns-blocklists/main/hosts/light.txt" },
  { id: "hagezi-normal", name: "HaGeZi Normal", provider: "HaGeZi", description: "All-round privacy protection for most home networks.", category: "Privacy", intensity: "Balanced", tags: ["Ads", "Tracking", "Telemetry"], url: "https://raw.githubusercontent.com/hagezi/dns-blocklists/main/hosts/multi.txt", recommended: true },
  { id: "hagezi-pro", name: "HaGeZi Pro", provider: "HaGeZi", description: "Extended blocking with stronger privacy and badware coverage.", category: "Privacy", intensity: "Strong", tags: ["Ads", "Badware", "Scams"], url: "https://raw.githubusercontent.com/hagezi/dns-blocklists/main/hosts/pro.txt" },
  { id: "hagezi-ultimate", name: "HaGeZi Ultimate", provider: "HaGeZi", description: "Aggressive all-in-one filtering for users comfortable troubleshooting exceptions.", category: "Strict", intensity: "Maximum", tags: ["Ads", "Malware", "Scams"], url: "https://raw.githubusercontent.com/hagezi/dns-blocklists/main/hosts/ultimate.txt" },
  { id: "hagezi-tif", name: "HaGeZi Threat Intelligence", provider: "HaGeZi", description: "Focused threat intelligence feed intended to complement a general list.", category: "Security", intensity: "Supplemental", tags: ["Malware", "Phishing", "Threats"], url: "https://raw.githubusercontent.com/hagezi/dns-blocklists/main/hosts/tif.txt", recommended: true },
  { id: "stevenblack-unified", name: "StevenBlack Unified", provider: "StevenBlack", description: "Long-running consolidated hosts list for ads and malware.", category: "Classic", intensity: "Balanced", tags: ["Ads", "Malware"], url: "https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts" },
  { id: "stevenblack-social", name: "StevenBlack + Social", provider: "StevenBlack", description: "The unified list with social-network domains added.", category: "Strict", intensity: "Strong", tags: ["Ads", "Malware", "Social"], url: "https://raw.githubusercontent.com/StevenBlack/hosts/master/alternates/social/hosts" },
  { id: "adaway", name: "AdAway Hosts", provider: "AdAway", description: "A compact community-maintained list focused on mobile and web advertising.", category: "Classic", intensity: "Light", tags: ["Ads", "Mobile"], url: "https://adaway.org/hosts.txt" },
  { id: "onehosts-lite", name: "1Hosts Lite", provider: "1Hosts", description: "Lightweight ad and tracker blocking with a compatibility-first profile.", category: "Balanced", intensity: "Light", tags: ["Ads", "Tracking"], url: "https://o0.pages.dev/Lite/hosts.txt" },
  { id: "urlhaus", name: "URLhaus Malware", provider: "abuse.ch", description: "Active malware-distribution hostnames from the URLhaus project.", category: "Security", intensity: "Supplemental", tags: ["Malware", "Threats"], url: "https://urlhaus.abuse.ch/downloads/hostfile/" },
  { id: "phishing-army", name: "Phishing Army Extended", provider: "Phishing Army", description: "Focused protection against active phishing and credential-theft domains.", category: "Security", intensity: "Supplemental", tags: ["Phishing", "Scams"], url: "https://phishing.army/download/phishing_army_blocklist_extended.txt" }
];

const catalogCategories = ["All", "Balanced", "Privacy", "Strict", "Security", "Classic"];

function isInstalled(item: CatalogBlocklist, installed: Blocklist[]) {
  const itemURL = normalizeURL(item.url);
  return installed.some((blocklist) => normalizeURL(blocklist.url) === itemURL || blocklist.name.toLowerCase() === item.name.toLowerCase());
}

function normalizeURL(value: string) {
  return value.trim().replace(/\/+$/, "").toLowerCase();
}

function isStale(value?: string | null) {
  if (!value) return true;
  return Date.now() - new Date(value).getTime() > 7 * 24 * 60 * 60 * 1000;
}

function formatUpdated(value?: string | null) {
  if (!value) return "Never";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "Unknown";
  const days = Math.floor((Date.now() - date.getTime()) / (24 * 60 * 60 * 1000));
  if (days <= 0) return `Today, ${date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`;
  if (days === 1) return "Yesterday";
  return `${days} days ago`;
}

function sourceLabel(url: string) {
  if (url.startsWith("file://")) return "Local file";
  try {
    return new URL(url).hostname.replace(/^www\./, "");
  } catch {
    return url;
  }
}

function sourceInitial(blocklist: Blocklist) {
  return blocklist.name.slice(0, 1).toUpperCase();
}

function formatNumber(value: number) {
  return new Intl.NumberFormat().format(value);
}

function formatEntryDate(value?: string) {
  if (!value) return "Unknown";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "Unknown" : date.toLocaleDateString([], { month: "short", day: "numeric", year: "numeric" });
}

function errorMessage(caught: unknown, fallback: string) {
  return caught instanceof Error && caught.message ? caught.message : fallback;
}
