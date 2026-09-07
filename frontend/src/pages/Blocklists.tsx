import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { DialogSurface } from "@/components/DialogSurface";
import { NativeSelect } from "@/components/ui/native-select";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Checkbox } from "@/components/ui/checkbox";
import { Button } from "@/components/ui/button";
import { AlertCircle, AlertTriangle, CheckCircle2, Database, Download, Eye, Info, ListFilter, Network, Plus, RefreshCw, Search, ShieldCheck, Sparkles, Trash2, X } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { useMemo, useRef, useState, type ReactNode, type SubmitEvent } from "react";
import { api, type Blocklist } from "@/api/client";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { EmptyState } from "@/components/EmptyState";
import { blocklistCatalog, blocklistCategories, type BlocklistCategory, type CatalogBlocklist } from "@/data/blocklists";
import { errorMessage, formatNumber, normalizeURL } from "@/utils/formatting";

type BlocklistsProps = {
  readonly blocklists: Blocklist[];
  readonly refresh: () => Promise<void>;
};

type View = "installed" | "available";
type Notice = { tone: "success" | "error"; text: string } | null;
type Installation = { id: string; name: string; stage: "Adding source" | "Downloading and checking domains" | "Updating Faro's library" };

const categoryIcons: Record<BlocklistCategory, LucideIcon> = {
  Everyday: ShieldCheck,
  Security: AlertTriangle,
  Privacy: Eye,
  Content: ListFilter,
  Network,
};

export function Blocklists({ blocklists, refresh }: BlocklistsProps) {
  const [view, setView] = useState<View>("installed");
  const [form, setForm] = useState({ name: "", url: "", enabled: true });
  const [showCustom, setShowCustom] = useState(false);
  const [busy, setBusy] = useState<number | null>(null);
  const [installing, setInstalling] = useState<Installation | null>(null);
  const installationLock = useRef(false);
  const [updatingAll, setUpdatingAll] = useState(false);
  const [catalogQuery, setCatalogQuery] = useState("");
  const [catalogCategory, setCatalogCategory] = useState<"All" | BlocklistCategory>("All");
  const [catalogProvider, setCatalogProvider] = useState("All");
  const [catalogCompatibility, setCatalogCompatibility] = useState<"All" | CatalogBlocklist["compatibility"]>("All");
  const [notice, setNotice] = useState<Notice>(null);
  const [pendingRemoval, setPendingRemoval] = useState<Blocklist | null>(null);

  const enabledCount = blocklists.filter((blocklist) => blocklist.enabled).length;
  const entryCount = blocklists.reduce((total, blocklist) => total + (blocklist.entry_count ?? 0), 0);
  const needsUpdate = blocklists.filter((blocklist) => isStale(blocklist.last_refreshed_at)).length;
  const available = useMemo(() => blocklistCatalog.filter((item) => !isInstalled(item, blocklists)), [blocklists]);
  const catalogProviders = useMemo(() => Array.from(new Set(blocklistCatalog.map((item) => item.provider))).sort((left, right) => left.localeCompare(right)), []);
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
  const isInstalling = installing !== null;

  async function add(event: SubmitEvent) {
    event.preventDefault();
    if (installationLock.current) return;
    installationLock.current = true;
    const installingName = form.name.trim() || "Custom blocklist";
    setInstalling({ id: "custom", name: installingName, stage: "Adding source" });
    setNotice(null);
    let created = false;
    try {
      const result = await api.createBlocklist({ ...form, assign_to_default: false });
      created = true;
      setInstalling({ id: "custom", name: installingName, stage: "Downloading and checking domains" });
      await api.refreshBlocklist(result.id);
      setInstalling({ id: "custom", name: installingName, stage: "Updating Faro's library" });
      setForm({ name: "", url: "", enabled: true });
      setShowCustom(false);
      setView("installed");
      setNotice({ tone: "success", text: `${form.name} was installed. Choose it from a Protection setup when you want to use it.` });
    } catch (error_) {
      setNotice({ tone: "error", text: created ? "The list was added, but its first update failed. Try updating it from Installed." : errorMessage(error_, "Could not add the blocklist.") });
    } finally {
      try {
        await refresh();
      } finally {
        setInstalling(null);
        installationLock.current = false;
      }
    }
  }

  async function toggle(blocklist: Blocklist) {
    setBusy(blocklist.id);
    setNotice(null);
    try {
      await api.updateBlocklist({ ...blocklist, enabled: !blocklist.enabled });
      await refresh();
    } catch (error_) {
      setNotice({ tone: "error", text: errorMessage(error_, "Could not change the blocklist status.") });
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
    } catch (error_) {
      setNotice({ tone: "error", text: errorMessage(error_, `Could not update ${blocklist.name}.`) });
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
    } catch (error_) {
      setNotice({ tone: "error", text: errorMessage(error_, "Could not update all blocklists.") });
    } finally {
      setUpdatingAll(false);
    }
  }

  async function installCatalog(item: CatalogBlocklist) {
    if (installationLock.current) return;
    installationLock.current = true;
    setInstalling({ id: item.id, name: item.name, stage: "Adding source" });
    setNotice(null);
    let created = false;
    try {
      const result = await api.createBlocklist({ name: item.name, url: item.url, enabled: true, assign_to_default: false });
      created = true;
      setInstalling({ id: item.id, name: item.name, stage: "Downloading and checking domains" });
      await api.refreshBlocklist(result.id);
      setInstalling({ id: item.id, name: item.name, stage: "Updating Faro's library" });
      await refresh();
      setNotice({ tone: "success", text: `${item.name} was installed. Choose it from a Protection setup when you want to use it.` });
    } catch (error_) {
      await refresh();
      setNotice({ tone: "error", text: created ? `${item.name} was added, but its first update failed.` : errorMessage(error_, `Could not install ${item.name}.`) });
    } finally {
      setInstalling(null);
      installationLock.current = false;
    }
  }

  async function remove(blocklist: Blocklist) {
    setBusy(blocklist.id);
    setNotice(null);
    try {
      await api.deleteBlocklist(blocklist.id);
      await refresh();
      setNotice({ tone: "success", text: `${blocklist.name} was removed.` });
    } catch (error_) {
      setNotice({ tone: "error", text: errorMessage(error_, `Could not remove ${blocklist.name}.`) });
    } finally {
      setBusy(null);
      setPendingRemoval(null);
    }
  }

  return (
    <Tabs value={view} onValueChange={(value) => setView(value as View)} className="blocklists-page">
      <div className="blocklist-page-toolbar">
        <TabsList className="blocklist-view-tabs"  aria-label="Blocklist views">
          <TabsTrigger value="installed"     >Installed <span>{blocklists.length}</span></TabsTrigger>
          <TabsTrigger value="available"     >Available <span>{available.length}</span></TabsTrigger>
        </TabsList>
        <div className="blocklist-primary-actions">
          {view === "installed" && <Button variant="outline" type="button" className="secondary" disabled={updatingAll || isInstalling || enabledCount === 0} onClick={() => void refreshAll()}><RefreshCw className={updatingAll ? "spinning" : ""} size={16} /><span>{updatingAll ? "Updating" : "Update all"}</span></Button>}
          <Button variant="default" type="button" disabled={isInstalling} onClick={() => setShowCustom(true)}><Plus size={16} /><span>Add custom</span></Button>
        </div>
      </div>

      {notice && <BlocklistNotice notice={notice} onDismiss={() => setNotice(null)} />}
      {installing && <InstallationProgress installation={installing} />}
      <TabsContent value={view}><BlocklistsContent
        view={view}
        blocklists={blocklists}
        enabledCount={enabledCount}
        entryCount={entryCount}
        needsUpdate={needsUpdate}
        catalogCandidates={catalogCandidates}
        filteredCatalog={filteredCatalog}
        visibleCategories={visibleCategories}
        catalogQuery={catalogQuery}
        catalogCategory={catalogCategory}
        catalogProvider={catalogProvider}
        catalogCompatibility={catalogCompatibility}
        catalogProviders={catalogProviders}
        hasCatalogFilters={hasCatalogFilters}
        installing={installing}
        isInstalling={isInstalling}
        busy={busy}
        setView={setView}
        setCatalogQuery={setCatalogQuery}
        setCatalogCategory={setCatalogCategory}
        setCatalogProvider={setCatalogProvider}
        setCatalogCompatibility={setCatalogCompatibility}
        onToggle={toggle}
        onRefreshList={refreshList}
        onRequestRemoval={(blocklist) => { setNotice(null); setPendingRemoval(blocklist); }}
        onInstall={installCatalog}
      /></TabsContent>
      {showCustom && <CustomBlocklistDialog notice={notice} onDismissNotice={() => setNotice(null)} form={form} setForm={setForm} installing={installing} isInstalling={isInstalling} onSubmit={add} onClose={() => setShowCustom(false)} />}
      {pendingRemoval && <BlocklistRemovalDialog blocklist={pendingRemoval} busy={busy === pendingRemoval.id} onCancel={() => setPendingRemoval(null)} onConfirm={() => void remove(pendingRemoval)} />}
    </Tabs>
  );
}

function BlocklistNotice({ notice, onDismiss }: { readonly notice: Exclude<Notice, null>; readonly onDismiss: () => void }) {
  return <output className={`blocklist-notice ${notice.tone}`}>{notice.tone === "success" ? <CheckCircle2 size={17} /> : <AlertCircle size={17} />}<span>{notice.text}</span><Button variant="ghost" size="icon" type="button" className="icon-button" aria-label="Dismiss message" onClick={onDismiss}><X size={15} /></Button></output>;
}

function InstallationProgress({ installation }: { readonly installation: Installation }) {
  return <output className="blocklist-install-progress" aria-live="polite" aria-label={`Installing ${installation.name}`}><span className="blocklist-install-spinner"><RefreshCw className="spinning" size={18} /></span><div><strong>Installing {installation.name}</strong><span>{installation.stage}. Keep this page open; other installations will be available when this finishes.</span><i aria-hidden="true"><b /></i></div></output>;
}

function BlocklistsContent({ view, ...props }: {
  readonly view: View;
  readonly blocklists: Blocklist[];
  readonly enabledCount: number;
  readonly entryCount: number;
  readonly needsUpdate: number;
  readonly catalogCandidates: CatalogBlocklist[];
  readonly filteredCatalog: CatalogBlocklist[];
  readonly visibleCategories: typeof blocklistCategories;
  readonly catalogQuery: string;
  readonly catalogCategory: "All" | BlocklistCategory;
  readonly catalogProvider: string;
  readonly catalogCompatibility: "All" | CatalogBlocklist["compatibility"];
  readonly catalogProviders: string[];
  readonly hasCatalogFilters: boolean;
  readonly installing: Installation | null;
  readonly isInstalling: boolean;
  readonly busy: number | null;
  readonly setView: (view: View) => void;
  readonly setCatalogQuery: (value: string) => void;
  readonly setCatalogCategory: (value: "All" | BlocklistCategory) => void;
  readonly setCatalogProvider: (value: string) => void;
  readonly setCatalogCompatibility: (value: "All" | CatalogBlocklist["compatibility"]) => void;
  readonly onToggle: (blocklist: Blocklist) => Promise<void>;
  readonly onRefreshList: (blocklist: Blocklist) => Promise<void>;
  readonly onRequestRemoval: (blocklist: Blocklist) => void;
  readonly onInstall: (item: CatalogBlocklist) => Promise<void>;
}) {
  if (view === "installed") return <InstalledBlocklists {...props} />;
  return <BlocklistCatalog {...props} />;
}

function InstalledBlocklists({ blocklists, enabledCount, entryCount, needsUpdate, isInstalling, busy, setView, onToggle, onRefreshList, onRequestRemoval }: {
  readonly blocklists: Blocklist[];
  readonly enabledCount: number;
  readonly entryCount: number;
  readonly needsUpdate: number;
  readonly isInstalling: boolean;
  readonly busy: number | null;
  readonly setView: (view: View) => void;
  readonly onToggle: (blocklist: Blocklist) => Promise<void>;
  readonly onRefreshList: (blocklist: Blocklist) => Promise<void>;
  readonly onRequestRemoval: (blocklist: Blocklist) => void;
}) {
  return <>
    <section className="blocklist-summary-strip" aria-label="Installed blocklist summary">
      <SummaryItem icon={<ShieldCheck size={17} />} label="Enabled lists" value={String(enabledCount)} />
      <SummaryItem icon={<Database size={17} />} label="List entries" value={formatNumber(entryCount)} />
      <SummaryItem icon={<RefreshCw size={17} />} label="Need update" value={String(needsUpdate)} tone={needsUpdate > 0 ? "warning" : "healthy"} />
    </section>
    <section className="panel installed-blocklists-panel">
      <div className="installed-list-heading"><div><h2>Installed lists</h2><p>Install sources once, then choose where they are used under Protection.</p></div><span>{enabledCount} of {blocklists.length} enabled</span></div>
      {blocklists.length === 0 ? <EmptyState title="No blocklists installed" body="Browse available lists or add a custom URL to start filtering domains." action={<Button variant="default" type="button" onClick={() => setView("available")}>Browse available lists</Button>} /> : <InstalledBlocklistTable blocklists={blocklists} isInstalling={isInstalling} busy={busy} onToggle={onToggle} onRefreshList={onRefreshList} onRequestRemoval={onRequestRemoval} />}
    </section>
  </>;
}

function InstalledBlocklistTable({ blocklists, isInstalling, busy, onToggle, onRefreshList, onRequestRemoval }: {
  readonly blocklists: Blocklist[];
  readonly isInstalling: boolean;
  readonly busy: number | null;
  readonly onToggle: (blocklist: Blocklist) => Promise<void>;
  readonly onRefreshList: (blocklist: Blocklist) => Promise<void>;
  readonly onRequestRemoval: (blocklist: Blocklist) => void;
}) {
  return (
    <div className="installed-blocklist-table">
      <div className="installed-blocklist-columns" aria-hidden="true"><span>List</span><span>Entries</span><span>Last updated</span><span>Status</span><span className="installed-actions-label">Actions</span></div>
      {blocklists.map((blocklist) => <article className="installed-blocklist-row" key={blocklist.id} aria-label={blocklist.name}>
        <div className="installed-list-identity">
          <span className="blocklist-source-mark" aria-hidden="true">{sourceInitial(blocklist)}</span>
          <div><strong>{blocklist.name}</strong><span>{sourceLabel(blocklist.url)} · {usageLabel(blocklist.protection_count)}</span></div>
        </div>
        <div className="installed-list-metric"><strong>{formatNumber(blocklist.entry_count ?? 0)}</strong><span>entries</span></div>
        <div className="installed-list-updated"><strong>{formatUpdated(blocklist.last_refreshed_at)}</strong><span className={isStale(blocklist.last_refreshed_at) ? "stale" : undefined}>{isStale(blocklist.last_refreshed_at) ? "Update recommended" : "Current"}</span></div>
        <label className="installed-list-status">
          <Switch checked={blocklist.enabled} disabled={isInstalling || busy === blocklist.id} onCheckedChange={() => void onToggle(blocklist)} aria-label={`${blocklist.enabled ? "Disable" : "Enable"} ${blocklist.name}`} />
          <span>{blocklist.enabled ? "Enabled" : "Paused"}</span>
        </label>
        <div className="installed-list-actions">
          <Button variant="ghost" size="icon" type="button" title="Update now" aria-label={`Update ${blocklist.name}`} disabled={isInstalling || busy === blocklist.id} onClick={() => void onRefreshList(blocklist)}><RefreshCw className={busy === blocklist.id ? "spinning" : ""} size={16} /></Button>
          <Button variant="ghost" size="icon" type="button" className="blocklist-remove-button" title="Remove" aria-label={`Remove ${blocklist.name}`} disabled={isInstalling || busy === blocklist.id} onClick={() => onRequestRemoval(blocklist)}><Trash2 size={16} /></Button>
        </div>
      </article>)}
    </div>
  );
}

function BlocklistCatalog({ catalogCandidates, filteredCatalog, visibleCategories, catalogQuery, catalogCategory, catalogProvider, catalogCompatibility, catalogProviders, hasCatalogFilters, installing, isInstalling, setCatalogQuery, setCatalogCategory, setCatalogProvider, setCatalogCompatibility, onInstall }: {
  readonly catalogCandidates: CatalogBlocklist[];
  readonly filteredCatalog: CatalogBlocklist[];
  readonly visibleCategories: typeof blocklistCategories;
  readonly catalogQuery: string;
  readonly catalogCategory: "All" | BlocklistCategory;
  readonly catalogProvider: string;
  readonly catalogCompatibility: "All" | CatalogBlocklist["compatibility"];
  readonly catalogProviders: string[];
  readonly hasCatalogFilters: boolean;
  readonly installing: Installation | null;
  readonly isInstalling: boolean;
  readonly setCatalogQuery: (value: string) => void;
  readonly setCatalogCategory: (value: "All" | BlocklistCategory) => void;
  readonly setCatalogProvider: (value: string) => void;
  readonly setCatalogCompatibility: (value: "All" | CatalogBlocklist["compatibility"]) => void;
  readonly onInstall: (item: CatalogBlocklist) => Promise<void>;
}) {
  const clearFilters = () => { setCatalogQuery(""); setCatalogCategory("All"); setCatalogProvider("All"); setCatalogCompatibility("All"); };
  return <section className="blocklist-catalog-panel"><div className="catalog-heading"><div><h2>Find a blocklist</h2><p>Browse filtering sources by category, provider, and compatibility.</p></div><label className="catalog-search"><Search size={16} /><Input variant="embedded" value={catalogQuery} onChange={(event) => setCatalogQuery(event.target.value)} placeholder="Search by need, provider, or list" aria-label="Search available blocklists" /></label></div><div className="catalog-guide"><span className="catalog-guide-icon"><Sparkles size={18} /></span><div><strong>Start with one everyday list</strong><span>Add specialized lists only when you need extra coverage.</span></div></div><div className="catalog-refine-row"><label>Category<NativeSelect value={catalogCategory} onChange={(event) => setCatalogCategory(event.target.value as "All" | BlocklistCategory)} aria-label="Filter blocklists by category"><option value="All">All categories ({catalogCandidates.length})</option>{blocklistCategories.map((category) => <option key={category.id} value={category.id}>{category.label} ({catalogCandidates.filter((item) => item.category === category.id).length})</option>)}</NativeSelect></label><label>Provider<NativeSelect value={catalogProvider} onChange={(event) => setCatalogProvider(event.target.value)} aria-label="Filter blocklists by provider"><option value="All">All providers</option>{catalogProviders.map((provider) => <option key={provider} value={provider}>{provider}</option>)}</NativeSelect></label><label>Compatibility<NativeSelect value={catalogCompatibility} onChange={(event) => setCatalogCompatibility(event.target.value as "All" | CatalogBlocklist["compatibility"])} aria-label="Filter blocklists by compatibility"><option value="All">Any compatibility</option><option value="Easy">Easy to use</option><option value="Balanced">Balanced</option><option value="Advanced">Advanced</option></NativeSelect></label>{hasCatalogFilters && <Button variant="link" type="button" className="text-action" onClick={clearFilters}>Clear filters</Button>}</div>{filteredCatalog.length === 0 ? <div className="compact-empty"><strong>No matching lists</strong><span>Try another search, category, or provider.</span>{hasCatalogFilters && <Button variant="outline" type="button" className="secondary" onClick={clearFilters}>Clear filters</Button>}</div> : <div className="blocklist-catalog-sections">{visibleCategories.map((category) => <CatalogCategory key={category.id} category={category} items={filteredCatalog.filter((item) => item.category === category.id)} installing={installing} isInstalling={isInstalling} onInstall={onInstall} />)}</div>}</section>;
}

function CatalogCategory({ category, items, installing, isInstalling, onInstall }: { readonly category: typeof blocklistCategories[number]; readonly items: CatalogBlocklist[]; readonly installing: Installation | null; readonly isInstalling: boolean; readonly onInstall: (item: CatalogBlocklist) => Promise<void> }) {
  const CategoryIcon = categoryIcons[category.id];
  return <section className="catalog-category-section"><header><div className="catalog-category-icon" aria-hidden="true"><CategoryIcon size={18} /></div><div className="catalog-category-copy"><div className="catalog-category-title-row"><h3>{category.label}</h3><span className="catalog-category-count">{items.length} available</span></div><p>{category.description}</p></div></header><div className="blocklist-catalog-grid">{items.map((item) => <CatalogCard key={item.id} item={item} installing={installing} isInstalling={isInstalling} onInstall={onInstall} />)}</div></section>;
}

function CatalogCard({ item, installing, isInstalling, onInstall }: { readonly item: CatalogBlocklist; readonly installing: Installation | null; readonly isInstalling: boolean; readonly onInstall: (item: CatalogBlocklist) => Promise<void> }) {
  const itemInstalling = installing?.id === item.id;
  return (
    <article className={`catalog-blocklist-card ${item.recommended ? "recommended" : ""} ${itemInstalling ? "installing" : ""}`} aria-busy={itemInstalling} aria-label={item.name}>
      <div className="catalog-card-heading"><div><h4>{item.name}</h4><span className="catalog-provider">{item.provider}</span></div>{item.recommended && <span className="catalog-recommended"><CheckCircle2 size={13} />Recommended</span>}</div>
      <div className="catalog-card-top"><span className={`compatibility-badge ${item.compatibility.toLowerCase()}`}>{item.compatibility === "Easy" ? "Easy to use" : item.compatibility}</span><div className="catalog-tags">{item.tags.map((tag) => <span key={tag}>{tag}</span>)}</div></div>
      <p>{item.description}</p>
      <div className="catalog-best-for"><strong>Best for</strong><span>{item.bestFor}</span></div>
      {item.caution && <div className="catalog-caution"><Info size={14} /><span>{item.caution}</span></div>}
      <div className="catalog-card-footer"><Button variant="outline" type="button" className="catalog-install-button" disabled={isInstalling} onClick={() => void onInstall(item)}>{itemInstalling ? <RefreshCw className="spinning" size={15} /> : <Download size={15} />}<span>{itemInstalling ? "Installing…" : "Install"}</span></Button></div>
    </article>
  );
}

type CustomBlocklistForm = { readonly name: string; readonly url: string; readonly enabled: boolean };

function CustomBlocklistDialog({ notice, onDismissNotice, form, setForm, installing, isInstalling, onSubmit, onClose }: { readonly notice: Notice; readonly onDismissNotice: () => void; readonly form: CustomBlocklistForm; readonly setForm: (form: CustomBlocklistForm) => void; readonly installing: Installation | null; readonly isInstalling: boolean; readonly onSubmit: (event: SubmitEvent) => Promise<void>; readonly onClose: () => void }) {
  const customInstalling = installing?.id === "custom";
  return <DialogSurface className="blocklist-modal-backdrop" aria-labelledby="custom-blocklist-title" onClose={onClose} busy={customInstalling}><section className="blocklist-modal"><header><div><h2 id="custom-blocklist-title">Add custom blocklist</h2><p>Use a public hosts file or plain domain list URL.</p></div><Button variant="ghost" size="icon" type="button" className="icon-button" aria-label="Close custom blocklist form" disabled={customInstalling} onClick={onClose}><X size={18} /></Button></header>{notice?.tone === "error" && <BlocklistNotice notice={notice} onDismiss={onDismissNotice} />}<form className="stack-form" onSubmit={(event) => void onSubmit(event)}><label>Name<Input required value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} placeholder="My blocklist" /></label><label>URL<Input required type="url" value={form.url} onChange={(event) => setForm({ ...form, url: event.target.value })} placeholder="https://example.com/hosts.txt" /></label><label className="checkbox-row"><Checkbox  checked={form.enabled} onCheckedChange={(checked) => setForm({ ...form, enabled: checked })} />Enable after installation</label><div className="blocklist-modal-actions"><Button variant="outline" type="button" className="secondary" disabled={customInstalling} onClick={onClose}>Cancel</Button><Button variant="default" type="submit" disabled={isInstalling}>{customInstalling ? <RefreshCw className="spinning" size={16} /> : <Plus size={16} />}<span>{customInstalling ? "Installing…" : "Add blocklist"}</span></Button></div></form></section></DialogSurface>;
}

function BlocklistRemovalDialog({ blocklist, busy, onCancel, onConfirm }: { readonly blocklist: Blocklist; readonly busy: boolean; readonly onCancel: () => void; readonly onConfirm: () => void }) {
  const usedBy = blocklist.protection_count ?? 0;
  const setupLabel = usedBy === 1 ? "that setup" : "those setups";
  const possessive = usedBy === 1 ? "Its" : "Their";
  const detail = usedBy > 0 ? <div className="confirm-dialog-impact warning"><AlertTriangle size={18} /><span><strong>Used by {usedBy} protection setup{usedBy === 1 ? "" : "s"}</strong><small>The list will be removed from {setupLabel}. {possessive} devices and other settings will remain.</small></span></div> : <div className="confirm-dialog-impact"><ShieldCheck size={18} /><span><strong>No protection setups use this list</strong><small>Removing it will not change protection for any device.</small></span></div>;
  return <ConfirmDialog title={`Remove ${blocklist.name}?`} body={`This will permanently delete ${formatNumber(blocklist.entry_count ?? 0)} downloaded domain ${blocklist.entry_count === 1 ? "entry" : "entries"} from Faro.`} confirmLabel="Remove blocklist" busyLabel="Removing blocklist…" busy={busy} onCancel={onCancel} onConfirm={onConfirm} detail={detail} />;
}

function SummaryItem({ icon, label, value, tone = "" }: { readonly icon: ReactNode; readonly label: string; readonly value: string; readonly tone?: string }) {
  return <div className={`blocklist-summary-item ${tone}`}>{icon}<span>{label}</span><strong>{value}</strong></div>;
}

function isInstalled(item: CatalogBlocklist, installed: Blocklist[]) {
  const itemURL = normalizeURL(item.url);
  return installed.some((blocklist) => normalizeURL(blocklist.url) === itemURL || blocklist.name.toLowerCase() === item.name.toLowerCase());
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
