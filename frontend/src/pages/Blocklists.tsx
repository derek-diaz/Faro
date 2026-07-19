import { AlertCircle, Check, CheckCircle2, Database, Download, Filter, Info, Plus, RefreshCw, Search, ShieldCheck, Sparkles, Trash2, X } from "lucide-react";
import { useMemo, useState, type FormEvent, type ReactNode } from "react";
import { api, type Blocklist } from "../api/client";
import { EmptyState } from "../components/EmptyState";
import { blocklistCatalog, blocklistCategories, type BlocklistCategory, type CatalogBlocklist } from "../data/blocklists";

type BlocklistsProps = {
  blocklists: Blocklist[];
  refresh: () => Promise<void>;
};

type View = "installed" | "available";
type Notice = { tone: "success" | "error"; text: string } | null;

export function Blocklists({ blocklists, refresh }: BlocklistsProps) {
  const [view, setView] = useState<View>("installed");
  const [form, setForm] = useState({ name: "", url: "", enabled: true });
  const [showCustom, setShowCustom] = useState(false);
  const [busy, setBusy] = useState<number | null>(null);
  const [installing, setInstalling] = useState<string | null>(null);
  const [updatingAll, setUpdatingAll] = useState(false);
  const [catalogQuery, setCatalogQuery] = useState("");
  const [catalogCategory, setCatalogCategory] = useState<"All" | BlocklistCategory>("All");
  const [catalogProvider, setCatalogProvider] = useState("All");
  const [catalogCompatibility, setCatalogCompatibility] = useState<"All" | CatalogBlocklist["compatibility"]>("All");
  const [notice, setNotice] = useState<Notice>(null);

  const enabledCount = blocklists.filter((blocklist) => blocklist.enabled).length;
  const entryCount = blocklists.reduce((total, blocklist) => total + (blocklist.entry_count ?? 0), 0);
  const needsUpdate = blocklists.filter((blocklist) => isStale(blocklist.last_refreshed_at)).length;
  const available = useMemo(() => blocklistCatalog.filter((item) => !isInstalled(item, blocklists)), [blocklists]);
  const catalogProviders = useMemo(() => Array.from(new Set(blocklistCatalog.map((item) => item.provider))).sort(), []);
  const catalogCandidates = available.filter((item) => {
    const query = catalogQuery.trim().toLowerCase();
    const matchesQuery = !query || `${item.name} ${item.provider} ${item.description} ${item.bestFor} ${item.tags.join(" ")}`.toLowerCase().includes(query);
    const matchesProvider = catalogProvider === "All" || item.provider === catalogProvider;
    const matchesCompatibility = catalogCompatibility === "All" || item.compatibility === catalogCompatibility;
    return matchesQuery && matchesProvider && matchesCompatibility;
  });
  const filteredCatalog = catalogCandidates.filter((item) => catalogCategory === "All" || item.category === catalogCategory);
  const visibleCategories = blocklistCategories.filter((category) => filteredCatalog.some((item) => item.category === category.id));
  const hasCatalogFilters = Boolean(catalogQuery || catalogCategory !== "All" || catalogProvider !== "All" || catalogCompatibility !== "All");

  async function add(event: FormEvent) {
    event.preventDefault();
    setInstalling("custom");
    setNotice(null);
    let created = false;
    try {
      const result = await api.createBlocklist({ ...form, assign_to_default: false });
      created = true;
      await api.refreshBlocklist(result.id);
      setForm({ name: "", url: "", enabled: true });
      setShowCustom(false);
      setView("installed");
      setNotice({ tone: "success", text: `${form.name} was installed. Choose it from a Protection setup when you want to use it.` });
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
      const result = await api.createBlocklist({ name: item.name, url: item.url, enabled: true, assign_to_default: false });
      created = true;
      await api.refreshBlocklist(result.id);
      await refresh();
      setNotice({ tone: "success", text: `${item.name} was installed. Choose it from a Protection setup when you want to use it.` });
    } catch (caught) {
      await refresh();
      setNotice({ tone: "error", text: created ? `${item.name} was added, but its first update failed.` : errorMessage(caught, `Could not install ${item.name}.`) });
    } finally {
      setInstalling(null);
    }
  }

  async function remove(blocklist: Blocklist) {
    const usage = blocklist.protection_count ?? 0;
    const impact = usage > 0 ? ` It is used by ${usage} protection setup${usage === 1 ? "" : "s"} and will be removed from ${usage === 1 ? "it" : "them"}.` : "";
    if (!window.confirm(`Remove ${blocklist.name}? Its downloaded entries will also be deleted.${impact}`)) return;
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
            <SummaryItem icon={<Check size={17} />} label="Source library" value={enabledCount > 0 ? "Ready" : "Empty"} tone={enabledCount > 0 ? "healthy" : "warning"} />
          </section>

          <section className="panel installed-blocklists-panel">
            <div className="installed-list-heading">
              <div><h2>Installed lists</h2><p>Install sources once, then choose where they are used under Protection.</p></div>
              <span>{enabledCount} of {blocklists.length} enabled</span>
            </div>
            {blocklists.length === 0 ? (
              <EmptyState title="No blocklists installed" body="Browse available lists or add a custom URL to start filtering domains." action={<button type="button" onClick={() => setView("available")}>Browse available lists</button>} />
            ) : (
              <div className="installed-blocklist-table">
                <div className="installed-blocklist-columns" aria-hidden="true"><span>List</span><span>Entries</span><span>Last updated</span><span>Status</span><span>Actions</span></div>
                {blocklists.map((blocklist) => (
                  <article className="installed-blocklist-row" key={blocklist.id}>
                    <div className="installed-list-identity"><span className="blocklist-source-mark">{sourceInitial(blocklist)}</span><div><strong>{blocklist.name}</strong><span>{sourceLabel(blocklist.url)} · {usageLabel(blocklist.protection_count)}</span></div></div>
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
        </>
      ) : (
        <section className="panel blocklist-catalog-panel">
          <div className="catalog-heading">
            <div><h2>Find a blocklist</h2><p>Choose by what you want to protect, not by technical list names.</p></div>
            <label className="catalog-search"><Search size={16} /><input value={catalogQuery} onChange={(event) => setCatalogQuery(event.target.value)} placeholder="Search by need, provider, or list" aria-label="Search available blocklists" /></label>
          </div>
          <div className="catalog-guide">
            <span className="catalog-guide-icon"><Sparkles size={18} /></span>
            <div><strong>Start with one everyday list</strong><span>For most homes, that is enough. Add a Security, Privacy, or Content list only when you have a specific need.</span></div>
          </div>
          <div className="catalog-refine-row">
            <span><Filter size={14} /> Refine</span>
            <label>Category<select value={catalogCategory} onChange={(event) => setCatalogCategory(event.target.value as "All" | BlocklistCategory)} aria-label="Filter blocklists by category"><option value="All">All categories ({catalogCandidates.length})</option>{blocklistCategories.map((category) => <option key={category.id} value={category.id}>{category.label} ({catalogCandidates.filter((item) => item.category === category.id).length})</option>)}</select></label>
            <label>Provider<select value={catalogProvider} onChange={(event) => setCatalogProvider(event.target.value)} aria-label="Filter blocklists by provider"><option value="All">All providers</option>{catalogProviders.map((provider) => <option key={provider} value={provider}>{provider}</option>)}</select></label>
            <label>Compatibility<select value={catalogCompatibility} onChange={(event) => setCatalogCompatibility(event.target.value as "All" | CatalogBlocklist["compatibility"])} aria-label="Filter blocklists by compatibility"><option value="All">Any compatibility</option><option value="Easy">Easy to use</option><option value="Balanced">Balanced</option><option value="Advanced">Advanced</option></select></label>
            {hasCatalogFilters && <button type="button" className="text-action" onClick={() => { setCatalogQuery(""); setCatalogCategory("All"); setCatalogProvider("All"); setCatalogCompatibility("All"); }}>Clear filters</button>}
          </div>
          {filteredCatalog.length === 0 ? (
            <div className="compact-empty"><strong>No matching lists</strong><span>Try another search, category, or provider.</span>{hasCatalogFilters && <button type="button" className="secondary" onClick={() => { setCatalogQuery(""); setCatalogCategory("All"); setCatalogProvider("All"); setCatalogCompatibility("All"); }}>Clear filters</button>}</div>
          ) : (
            <div className="blocklist-catalog-sections">
              {visibleCategories.map((category) => (
                <section className="catalog-category-section" key={category.id}>
                  <header><div><h3>{category.label}</h3><p>{category.description}</p></div><span>{filteredCatalog.filter((item) => item.category === category.id).length} available</span></header>
                  <div className="blocklist-catalog-grid">
                    {filteredCatalog.filter((item) => item.category === category.id).map((item) => (
                      <article className={`catalog-blocklist-card ${item.recommended ? "recommended" : ""}`} key={item.id}>
                        <div className="catalog-card-top"><span className={`compatibility-badge ${item.compatibility.toLowerCase()}`}>{item.compatibility === "Easy" ? "Easy to use" : item.compatibility}</span>{item.recommended && <span className="catalog-recommended"><CheckCircle2 size={13} /> Recommended</span>}</div>
                        <div><h4>{item.name}</h4><span className="catalog-provider">By {item.provider}</span></div>
                        <p>{item.description}</p>
                        <div className="catalog-best-for"><strong>Best for</strong><span>{item.bestFor}</span></div>
                        {item.caution && <div className="catalog-caution"><Info size={13} /><span>{item.caution}</span></div>}
                        <div className="catalog-card-footer">
                          <div className="catalog-tags">{item.tags.map((tag) => <span key={tag}>{tag}</span>)}</div>
                          <button type="button" className="secondary catalog-install-button" disabled={installing === item.id} onClick={() => void installCatalog(item)}><Download size={15} /><span>{installing === item.id ? "Installing" : "Install"}</span></button>
                        </div>
                      </article>
                    ))}
                  </div>
                </section>
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

function usageLabel(count = 0) {
  if (count === 0) return "Not used by a protection setup";
  return `Used by ${count} protection setup${count === 1 ? "" : "s"}`;
}

function formatNumber(value: number) {
  return new Intl.NumberFormat().format(value);
}

function errorMessage(caught: unknown, fallback: string) {
  return caught instanceof Error && caught.message ? caught.message : fallback;
}
