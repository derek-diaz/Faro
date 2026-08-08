import { AlertTriangle, Cable, Check, CheckCircle2, Clock3, Code2, Cpu, Database, Download, Eye, EyeOff, FileArchive, Gauge, Globe2, HardDrive, Image, KeyRound, LockKeyhole, Network, RefreshCw, RotateCw, Save, Server, ShieldCheck, Trash2, Upload, UserRound } from "lucide-react";
import { useEffect, useMemo, useState, type ReactNode, type SubmitEvent } from "react";
import { api, type CoreDNSDiagnosticFile, type CoreDNSDiagnostics, type MaintenanceStatus, type PruneResult, type Setting } from "../api/client";
import { LoadingState } from "../components/LoadingState";
import { RedundancySettings } from "../components/RedundancySettings";
import { UnifiIntegration } from "../components/UnifiIntegration";
import { formatNumber } from "../utils/formatting";

type SettingsProps = {
  readonly settings: Setting[];
  readonly refresh: () => Promise<void>;
  readonly onManageUpstreams: () => void;
};

type SettingsTab = "general" | "redundancy" | "integrations" | "data" | "advanced" | "account";
type ActionState = "idle" | "working" | "done" | "error";

export function Settings({ settings, refresh, onManageUpstreams }: SettingsProps) {
  const [tab, setTab] = useState<SettingsTab>("general");
  const [form, setForm] = useState<Record<string, string>>({});
  const [actionState, setActionState] = useState<ActionState>("idle");
  const [message, setMessage] = useState("");
  const [maintenance, setMaintenance] = useState<MaintenanceStatus | null>(null);
  const [maintenanceLoading, setMaintenanceLoading] = useState(true);
  const [pruneDays, setPruneDays] = useState(30);
  const [compact, setCompact] = useState(true);
  const [pruneResult, setPruneResult] = useState<PruneResult | null>(null);
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [passwordVisible, setPasswordVisible] = useState(false);
  const [advancedNetworkOpen, setAdvancedNetworkOpen] = useState(false);

  useEffect(() => {
    const values = Object.fromEntries(settings.map((setting) => [setting.key, setting.value]));
    setForm(values);
    const days = Number(values.retention_days || 30);
    if (Number.isFinite(days)) setPruneDays(days);
  }, [settings]);

  useEffect(() => {
    void loadMaintenance();
  }, []);

  const upstreams = useMemo(
    () => (form.upstream_dns ?? "").split(",").map((value) => value.trim()).filter(Boolean),
    [form.upstream_dns]
  );

  async function loadMaintenance() {
    setMaintenanceLoading(true);
    try {
      setMaintenance(await api.maintenance());
    } finally {
      setMaintenanceLoading(false);
    }
  }

  async function saveGeneral(event: SubmitEvent) {
    event.preventDefault();
    await runAction(async () => {
      await api.updateSettings({
        local_domain_suffix: form.local_domain_suffix || "home",
        faro_lan_ip: form.faro_lan_ip || "",
        dns_cache_enabled: form.dns_cache_enabled || "true",
        dns_cache_ttl: form.dns_cache_ttl || "300",
        allowed_client_cidrs: form.allowed_client_cidrs || "127.0.0.0/8,10.0.0.0/8,100.64.0.0/10,172.16.0.0/12,192.168.0.0/16,::1/128,fc00::/7,fe80::/10",
        favicon_fetching_enabled: form.favicon_fetching_enabled || "false"
      });
      await refresh();
      return "Settings saved. DNS configuration reloaded safely.";
    });
  }

  async function saveRetention() {
    const days = Number(form.retention_days || 30);
    if (!Number.isInteger(days) || days < 1 || days > 3650) {
      setActionState("error");
      setMessage("Retention must be between 1 and 3650 days.");
      return;
    }
    await runAction(async () => {
      await api.updateSettings({ retention_days: String(days) });
      setPruneDays(days);
      await refresh();
      await loadMaintenance();
      return `Automatic retention set to ${days} days.`;
    });
  }

  async function reloadDNS() {
    await runAction(async () => {
      await api.reload();
      return "DNS engine reloaded successfully.";
    });
  }

  async function pruneNow() {
    if (!Number.isInteger(pruneDays) || pruneDays < 1 || pruneDays > 3650) {
      setActionState("error");
      setMessage("Prune age must be between 1 and 3650 days.");
      return;
    }
    await runAction(async () => {
      const result = await api.prune(pruneDays, compact);
      setPruneResult(result);
      await loadMaintenance();
      return `Removed ${formatNumber(result.queries_deleted)} queries and ${formatNumber(result.events_deleted)} system events.`;
    });
  }

  async function changePassword(event: SubmitEvent) {
    event.preventDefault();
    if (newPassword.length < 8) {
      setActionState("error");
      setMessage("The new password must be at least 8 characters.");
      return;
    }
    if (newPassword !== confirmPassword) {
      setActionState("error");
      setMessage("The new passwords do not match.");
      return;
    }
    await runAction(async () => {
      await api.changePassword(currentPassword, newPassword);
      setCurrentPassword("");
      setNewPassword("");
      setConfirmPassword("");
      return "Password changed. Other signed-in sessions were closed.";
    });
  }

  async function runAction(action: () => Promise<string>) {
    setActionState("working");
    setMessage("");
    try {
      setMessage(await action());
      setActionState("done");
    } catch (error_) {
      setActionState("error");
      setMessage(error_ instanceof Error ? error_.message : "The operation failed.");
    }
  }

  return <SettingsWorkspace
    tab={tab}
    setTab={setTab}
    message={message}
    actionState={actionState}
    form={form}
    setForm={setForm}
    upstreams={upstreams}
    onManageUpstreams={onManageUpstreams}
    reloadDNS={() => void reloadDNS()}
    saveGeneral={saveGeneral}
    advancedNetworkOpen={advancedNetworkOpen}
    setAdvancedNetworkOpen={setAdvancedNetworkOpen}
    maintenance={maintenance}
    maintenanceLoading={maintenanceLoading}
    saveRetention={() => void saveRetention()}
    pruneDays={pruneDays}
    setPruneDays={setPruneDays}
    compact={compact}
    setCompact={setCompact}
    prune={() => void pruneNow()}
    refreshMaintenance={() => void loadMaintenance()}
    pruneResult={pruneResult}
    currentPassword={currentPassword}
    newPassword={newPassword}
    confirmPassword={confirmPassword}
    setCurrentPassword={setCurrentPassword}
    setNewPassword={setNewPassword}
    setConfirmPassword={setConfirmPassword}
    passwordVisible={passwordVisible}
    setPasswordVisible={setPasswordVisible}
    changePassword={changePassword}
    refresh={refresh}
  />;
}

type SettingsWorkspaceProps = {
  readonly tab: SettingsTab;
  readonly setTab: (tab: SettingsTab) => void;
  readonly message: string;
  readonly actionState: ActionState;
  readonly form: Record<string, string>;
  readonly setForm: (form: Record<string, string>) => void;
  readonly upstreams: string[];
  readonly onManageUpstreams: () => void;
  readonly reloadDNS: () => void;
  readonly saveGeneral: (event: SubmitEvent) => Promise<void>;
  readonly advancedNetworkOpen: boolean;
  readonly setAdvancedNetworkOpen: (open: boolean) => void;
  readonly maintenance: MaintenanceStatus | null;
  readonly maintenanceLoading: boolean;
  readonly saveRetention: () => void;
  readonly pruneDays: number;
  readonly setPruneDays: (days: number) => void;
  readonly compact: boolean;
  readonly setCompact: (compact: boolean) => void;
  readonly prune: () => void;
  readonly refreshMaintenance: () => void;
  readonly pruneResult: PruneResult | null;
  readonly currentPassword: string;
  readonly newPassword: string;
  readonly confirmPassword: string;
  readonly setCurrentPassword: (value: string) => void;
  readonly setNewPassword: (value: string) => void;
  readonly setConfirmPassword: (value: string) => void;
  readonly passwordVisible: boolean;
  readonly setPasswordVisible: (visible: boolean) => void;
  readonly changePassword: (event: SubmitEvent) => Promise<void>;
  readonly refresh: () => Promise<void>;
};

function SettingsWorkspace(props: SettingsWorkspaceProps) {
  const { tab, setTab, message, actionState } = props;
  return <div className="settings-workspace"><div className="settings-tabs" role="tablist" aria-label="Settings sections"><button type="button" role="tab" aria-selected={tab === "general"} className={tab === "general" ? "active" : ""} onClick={() => setTab("general")}><Gauge size={16} /> DNS & interface</button><button type="button" role="tab" aria-selected={tab === "data"} className={tab === "data" ? "active" : ""} onClick={() => setTab("data")}><Database size={16} /> Health & data</button><button type="button" role="tab" aria-selected={tab === "advanced"} className={tab === "advanced" ? "active" : ""} onClick={() => setTab("advanced")}><Code2 size={16} /> Advanced</button><button type="button" role="tab" aria-selected={tab === "redundancy"} className={tab === "redundancy" ? "active" : ""} onClick={() => setTab("redundancy")}><Network size={16} /> Redundancy</button><button type="button" role="tab" aria-selected={tab === "integrations"} className={tab === "integrations" ? "active" : ""} onClick={() => setTab("integrations")}><Cable size={16} /> Integrations</button><button type="button" role="tab" aria-selected={tab === "account"} className={tab === "account" ? "active" : ""} onClick={() => setTab("account")}><UserRound size={16} /> Account</button></div>{message && <div className={`settings-feedback ${actionState === "error" ? "error" : "success"}`}><CheckCircle2 size={16} /><span>{message}</span></div>}<SettingsTabContent {...props} /></div>;
}

function SettingsTabContent(props: SettingsWorkspaceProps) {
  switch (props.tab) {
    case "general":
      return <GeneralSettings {...props} />;
    case "redundancy":
      return <RedundancySettings />;
    case "data":
      return <DataAndHealth maintenance={props.maintenance} loading={props.maintenanceLoading} retentionDays={props.form.retention_days || "30"} setRetentionDays={(value) => props.setForm({ ...props.form, retention_days: value })} saveRetention={props.saveRetention} pruneDays={props.pruneDays} setPruneDays={props.setPruneDays} compact={props.compact} setCompact={props.setCompact} prune={props.prune} refresh={props.refreshMaintenance} busy={props.actionState === "working"} result={props.pruneResult} />;
    case "advanced":
      return <AdvancedSettings />;
    case "integrations":
      return <UnifiIntegration onChanged={props.refresh} />;
    case "account":
      return <AccountSettings {...props} />;
  }
}

function GeneralSettings({ form, setForm, upstreams, onManageUpstreams, reloadDNS, saveGeneral, actionState, advancedNetworkOpen, setAdvancedNetworkOpen }: SettingsWorkspaceProps) {
  return <div className="settings-general-grid"><section className="panel settings-compact-panel"><div className="panel-title with-actions"><div><h2>DNS & resolution</h2><p>Core behavior for local and public lookups.</p></div><button type="button" className="secondary icon-text-button" onClick={reloadDNS} disabled={actionState === "working"}><RotateCw size={16} /><span>Reload DNS</span></button></div><form className="settings-rows" onSubmit={(event) => void saveGeneral(event)}><SettingRow icon={<Server size={19} />} title="Upstream providers" description={`${upstreams.length} selected: ${upstreams.join(", ") || "none"}`}><button type="button" className="secondary" onClick={onManageUpstreams}>Manage</button></SettingRow><SettingRow icon={<Globe2 size={19} />} title="Local domain suffix" description="Default suffix suggested for local records."><input className="settings-short-input" value={form.local_domain_suffix ?? ""} onChange={(event) => setForm({ ...form, local_domain_suffix: event.target.value })} placeholder="home" /></SettingRow><SettingRow icon={<Network size={19} />} title="Faro LAN address" description="Fixed address distributed to devices as their DNS server."><input className="settings-short-input" value={form.faro_lan_ip ?? ""} onChange={(event) => setForm({ ...form, faro_lan_ip: event.target.value })} placeholder="Faro LAN address" required /></SettingRow><SettingRow icon={<ShieldCheck size={19} />} title="Network access" description="Faro automatically accepts DNS requests from devices on home and private networks."><div className="settings-network-access"><div className="settings-network-access-summary"><span className="settings-access-badge"><Check size={13} /> Home networks</span><button type="button" className="secondary" aria-expanded={advancedNetworkOpen} onClick={() => setAdvancedNetworkOpen(!advancedNetworkOpen)}>{advancedNetworkOpen ? "Hide advanced" : "Advanced"}</button></div>{advancedNetworkOpen && <label className="settings-network-advanced"><span>Custom network ranges</span><textarea className="settings-cidr-input" rows={4} value={form.allowed_client_cidrs ?? ""} onChange={(event) => setForm({ ...form, allowed_client_cidrs: event.target.value })} aria-label="Custom allowed DNS client network ranges" /><small>Most people never need this. Only change these ranges if your network uses addresses outside standard home networks.</small></label>}</div></SettingRow><SettingRow icon={<Gauge size={19} />} title="DNS response cache" description="Keep repeated answers local to reduce latency and upstream traffic."><div className="settings-inline-controls"><label className="compact-toggle"><input type="checkbox" checked={form.dns_cache_enabled !== "false"} onChange={(event) => setForm({ ...form, dns_cache_enabled: String(event.target.checked) })} /><span>Enabled</span></label><label className="unit-input"><input type="number" min="30" max="3600" step="30" disabled={form.dns_cache_enabled === "false"} value={form.dns_cache_ttl ?? "300"} onChange={(event) => setForm({ ...form, dns_cache_ttl: event.target.value })} /><span>sec max</span></label></div></SettingRow><SettingRow icon={<Image size={19} />} title="Domain favicons" description="Fetch and cache icons for public domains; local names retain initials."><label className="compact-toggle"><input type="checkbox" checked={form.favicon_fetching_enabled === "true"} onChange={(event) => setForm({ ...form, favicon_fetching_enabled: String(event.target.checked) })} /><span>Enabled</span></label></SettingRow><div className="settings-save-row"><button type="submit" className="icon-text-button" disabled={actionState === "working"}><Save size={16} /><span>{actionState === "working" ? "Saving" : "Save changes"}</span></button></div></form></section><aside className="settings-overview-column"><section className="panel settings-summary"><div className="panel-title"><h2>Current configuration</h2></div><div className="settings-summary-list"><SummaryRow label="Upstreams" value={upstreams.join(", ") || "Not configured"} /><SummaryRow label="Local suffix" value={form.local_domain_suffix || "home"} /><SummaryRow label="LAN address" value={form.faro_lan_ip || "Not configured"} /><SummaryRow label="Cache" value={cacheSummary(form)} /><SummaryRow label="Favicons" value={form.favicon_fetching_enabled === "true" ? "Enabled" : "Disabled"} /></div></section></aside></div>;
}

function AccountSettings({ currentPassword, newPassword, confirmPassword, setCurrentPassword, setNewPassword, setConfirmPassword, passwordVisible, setPasswordVisible, changePassword, actionState }: SettingsWorkspaceProps) {
  return <section className="panel account-password-panel"><div className="panel-title"><div><h2>Change password</h2><p>Enter your current password, then choose a new one.</p></div></div><form className="account-password-form" onSubmit={(event) => void changePassword(event)}><PasswordField label="Current password" value={currentPassword} setValue={setCurrentPassword} visible={passwordVisible} autoComplete="current-password" /><PasswordField label="New password" value={newPassword} setValue={setNewPassword} visible={passwordVisible} autoComplete="new-password" /><PasswordField label="Confirm new password" value={confirmPassword} setValue={setConfirmPassword} visible={passwordVisible} autoComplete="new-password" /><div className="account-password-footer"><div className="password-requirements" aria-label="Password requirements"><span className={newPassword.length >= 8 ? "met" : ""}><Check size={13} /> 8 or more characters</span><span className={confirmPassword.length > 0 && newPassword === confirmPassword ? "met" : ""}><Check size={13} /> Passwords match</span></div><button type="button" className="secondary password-visibility" onClick={() => setPasswordVisible(!passwordVisible)}>{passwordVisible ? <EyeOff size={15} /> : <Eye size={15} />}<span>{passwordVisible ? "Hide" : "Show"}</span></button></div><div className="settings-save-row account-password-action"><span>Other sessions will be signed out.</span><button type="submit" className="icon-text-button" disabled={actionState === "working" || !currentPassword || !newPassword || !confirmPassword}><KeyRound size={16} /><span>{actionState === "working" ? "Changing password" : "Change password"}</span></button></div></form></section>;
}

function cacheSummary(form: Record<string, string>) {
  if (form.dns_cache_enabled === "false") return "Disabled";
  return `${form.dns_cache_ttl || "300"} seconds`;
}

function PasswordField({ label, value, setValue, visible, autoComplete }: { readonly label: string; readonly value: string; readonly setValue: (value: string) => void; readonly visible: boolean; readonly autoComplete: string }) {
  return <label className="account-password-field"><span>{label}</span><div><LockKeyhole size={17} /><input type={visible ? "text" : "password"} value={value} onChange={(event) => setValue(event.target.value)} autoComplete={autoComplete} required /></div></label>;
}

function DataAndHealth({ maintenance, loading, retentionDays, setRetentionDays, saveRetention, pruneDays, setPruneDays, compact, setCompact, prune, refresh, busy, result }: {
  readonly maintenance: MaintenanceStatus | null;
  readonly loading: boolean;
  readonly retentionDays: string;
  readonly setRetentionDays: (value: string) => void;
  readonly saveRetention: () => void;
  readonly pruneDays: number;
  readonly setPruneDays: (value: number) => void;
  readonly compact: boolean;
  readonly setCompact: (value: boolean) => void;
  readonly prune: () => void;
  readonly refresh: () => void;
  readonly busy: boolean;
  readonly result: PruneResult | null;
}) {
  const storage = maintenance?.storage;
  const [backupPassphrase, setBackupPassphrase] = useState("");
  const [backupConfirmation, setBackupConfirmation] = useState("");
  const [restorePassphrase, setRestorePassphrase] = useState("");
  const [restoreFile, setRestoreFile] = useState<File | null>(null);
  const [restoreConfirmed, setRestoreConfirmed] = useState(false);
  const [backupBusy, setBackupBusy] = useState<"export" | "restore" | null>(null);
  const [backupMessage, setBackupMessage] = useState<{ tone: "success" | "error"; text: string } | null>(null);

  async function exportBackup() {
    if (backupPassphrase.length < 12) {
      setBackupMessage({ tone: "error", text: "Use a backup passphrase with at least 12 characters." });
      return;
    }
    if (backupPassphrase !== backupConfirmation) {
      setBackupMessage({ tone: "error", text: "The backup passphrases do not match." });
      return;
    }
    setBackupBusy("export");
    setBackupMessage(null);
    try {
      const { blob, filename } = await api.exportBackup(backupPassphrase);
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = filename;
      document.body.appendChild(link);
      link.click();
      link.remove();
      window.setTimeout(() => URL.revokeObjectURL(url), 0);
      setBackupPassphrase("");
      setBackupConfirmation("");
      setBackupMessage({ tone: "success", text: "Encrypted backup downloaded. Keep its passphrase somewhere safe—Faro cannot recover it." });
    } catch (error_) {
      setBackupMessage({ tone: "error", text: error_ instanceof Error ? error_.message : "The backup could not be created." });
    } finally {
      setBackupBusy(null);
    }
  }

  async function restoreBackup() {
    if (!restoreFile) {
      setBackupMessage({ tone: "error", text: "Select a .faro-backup file to restore." });
      return;
    }
    if (restorePassphrase.length < 12) {
      setBackupMessage({ tone: "error", text: "Enter the backup passphrase." });
      return;
    }
    if (!restoreConfirmed) {
      setBackupMessage({ tone: "error", text: "Confirm that the current Faro database will be replaced." });
      return;
    }
    setBackupBusy("restore");
    setBackupMessage(null);
    try {
      const restored = await api.restoreBackup(restoreFile, restorePassphrase);
      setBackupMessage({ tone: restored.warning ? "error" : "success", text: restored.warning || "Backup restored. Sign in with the account credentials stored in that backup." });
      window.setTimeout(() => window.dispatchEvent(new Event("faro:unauthorized")), 1800);
    } catch (error_) {
      setBackupMessage({ tone: "error", text: error_ instanceof Error ? error_.message : "The backup could not be restored." });
      setBackupBusy(null);
    }
  }

  return (
    <div className="maintenance-layout">
      <section className="panel maintenance-health-panel">
        <div className="panel-title with-actions">
          <div><h2>Application health</h2><p>{maintenance ? `Faro has been running for ${formatDuration(maintenance.uptime_seconds)}.` : "Live resource use and local data footprint."}</p></div>
          <button type="button" className="secondary icon-button" title="Refresh health" aria-label="Refresh health" onClick={refresh} disabled={loading}><RefreshCw size={16} /></button>
        </div>
        <div className="maintenance-metrics">
          <HealthMetric icon={<Cpu size={18} />} label="App memory" value={maintenance ? formatBytes(maintenance.process_memory_bytes) : "—"} detail="Current Go heap" />
          <HealthMetric icon={<HardDrive size={18} />} label="Database storage" value={storage ? formatBytes(storage.database_bytes) : "—"} detail={storage ? `${formatBytes(storage.database_reclaimable_bytes)} reclaimable` : "SQLite file allocation"} />
          <HealthMetric icon={<Database size={18} />} label="Activity records" value={storage ? formatNumber(storage.query_count + storage.event_count) : "—"} detail={storage ? `${formatNumber(storage.query_count)} DNS · ${formatNumber(storage.event_count)} system` : "Queries and system events"} />
        </div>
        {storage?.activity_storage && storage.activity_storage.status !== "healthy" && <div className="activity-storage-status" role="status"><AlertTriangle size={17} /><div><strong>Activity storage: {storage.activity_storage.status === "paused" ? "Paused" : "Unavailable"}</strong><span>Reason: {storage.activity_storage.reason || "Database write failed"}. DNS resolution continues normally.</span></div></div>}
      </section>

      <div className="maintenance-columns">
        <section className="panel retention-panel">
          <div className="maintenance-section-heading"><Clock3 size={19} /><div><h2>Automatic retention</h2><p>Faro removes expired DNS queries and system events on startup and every six hours.</p></div></div>
          <label className="retention-days-field"><span>Keep logs for</span><div><input type="number" min="1" max="3650" value={retentionDays} onChange={(event) => setRetentionDays(event.target.value)} /><span>days</span></div></label>
          <button type="button" className="secondary icon-text-button" onClick={saveRetention} disabled={busy}><Save size={16} /><span>Save retention</span></button>
          <div className="retention-status">
            <SummaryRow label="Oldest DNS query" value={formatTimestamp(storage?.oldest_query)} />
            <SummaryRow label="Last automatic cleanup" value={formatTimestamp(storage?.last_pruned_at)} />
            <SummaryRow label="Last cleanup removed" value={storage ? `${formatNumber(storage.last_queries_deleted)} queries · ${formatNumber(storage.last_events_deleted)} events` : "—"} />
          </div>
        </section>

        <section className="panel prune-panel">
          <div className="maintenance-section-heading"><Trash2 size={19} /><div><h2>Prune database now</h2><p>Delete data older than the selected age. This does not change the automatic retention policy.</p></div></div>
          <div className="prune-controls">
            <label className="retention-days-field"><span>Delete logs older than</span><div><input type="number" min="1" max="3650" value={pruneDays} onChange={(event) => setPruneDays(Number(event.target.value))} /><span>days</span></div></label>
            <label className="compact-toggle prune-compact"><input type="checkbox" checked={compact} onChange={(event) => setCompact(event.target.checked)} /><span>Compact SQLite afterward to reclaim disk space</span></label>
          </div>
          {result && <div className="prune-result"><strong>{formatNumber(result.queries_deleted + result.events_deleted)} rows removed</strong><span>{result.compacted ? `${formatBytes(result.reclaimed_bytes)} returned to disk.` : "Free pages retained for SQLite reuse."}</span></div>}
          <div className="prune-action-footer">
            <span>Only DNS query history and system events are removed.</span>
            <button type="button" className="danger-outline icon-text-button" onClick={prune} disabled={busy}><Trash2 size={16} /><span>{busy ? "Pruning" : "Prune now"}</span></button>
          </div>
        </section>
      </div>

      <section className="panel backup-panel">
        <div className="maintenance-section-heading"><ShieldCheck size={20} /><div><h2>Encrypted backup & restore</h2><p>Download a portable copy of Faro's configuration, account, rules, records, and database history.</p></div></div>
        <div className="backup-security-note"><LockKeyhole size={16} /><span>Backups use Argon2id and AES-256-GCM. Active login sessions, cached favicon files, and the raw query-log buffer are excluded.</span></div>
        {backupMessage && <output className={`backup-message ${backupMessage.tone}`}>{backupMessage.tone === "error" ? <AlertTriangle size={16} /> : <CheckCircle2 size={16} />}<span>{backupMessage.text}</span></output>}
        <div className="backup-actions-grid">
          <div className="backup-action-card">
            <div className="backup-card-heading"><FileArchive size={19} /><div><strong>Create encrypted backup</strong><span>Choose a unique passphrase. It is required to restore the file.</span></div></div>
            <label><span>Backup passphrase</span><input type="password" minLength={12} autoComplete="new-password" value={backupPassphrase} onChange={(event) => setBackupPassphrase(event.target.value)} placeholder="At least 12 characters" /></label>
            <label><span>Confirm passphrase</span><input type="password" minLength={12} autoComplete="new-password" value={backupConfirmation} onChange={(event) => setBackupConfirmation(event.target.value)} /></label>
            <button type="button" className="secondary icon-text-button" onClick={() => void exportBackup()} disabled={busy || backupBusy !== null}><Download size={16} /><span>{backupBusy === "export" ? "Encrypting backup" : "Download backup"}</span></button>
          </div>

          <div className="backup-action-card restore-card">
            <div className="backup-card-heading"><Upload size={19} /><div><strong>Restore encrypted backup</strong><span>This replaces the existing Faro configuration, records, rules, and query history, then reloads DNS.</span></div></div>
            <label><span>Backup file</span><input type="file" accept=".faro-backup,application/octet-stream" onChange={(event) => setRestoreFile(event.target.files?.[0] ?? null)} /></label>
            <label><span>Backup passphrase</span><input type="password" minLength={12} autoComplete="off" value={restorePassphrase} onChange={(event) => setRestorePassphrase(event.target.value)} /></label>
            <div className="restore-impact-note">
              <strong>Before you restore</strong>
              <ul>
                <li>Existing backup-covered state will be replaced.</li>
                <li>UniFi credentials and replica relationships are not included; any local ones stay unchanged.</li>
                <li>All active sessions will be invalidated, so you will sign in again.</li>
                <li>The backup passphrase cannot be recovered.</li>
              </ul>
            </div>
            <label className="restore-confirmation"><input type="checkbox" checked={restoreConfirmed} onChange={(event) => setRestoreConfirmed(event.target.checked)} /><span>I understand this replaces the backup-covered state and signs out every session.</span></label>
            <button type="button" className="danger-outline icon-text-button" onClick={() => void restoreBackup()} disabled={busy || backupBusy !== null || !restoreConfirmed}><Upload size={16} /><span>{backupBusy === "restore" ? "Validating and restoring" : "Restore backup"}</span></button>
          </div>
        </div>
      </section>

    </div>
  );
}

function AdvancedSettings() {
  return <div className="settings-advanced-layout"><CoreDNSDiagnosticsPanel /></div>;
}

function CoreDNSDiagnosticsPanel() {
  const [loading, setLoading] = useState(true);
  const [diagnostics, setDiagnostics] = useState<CoreDNSDiagnostics | null>(null);
  const [error, setError] = useState("");
  const [expandedFile, setExpandedFile] = useState<string | null>(null);
  const [comparisonFile, setComparisonFile] = useState<string | null>(null);

  useEffect(() => {
    void refreshDiagnostics();
  }, []);

  useEffect(() => {
    if (!diagnostics) return;
    const mismatch = diagnostics.status === "drifted" ? diagnostics.files.find((file) => file.referenced && !file.matches) : undefined;
    if (mismatch) {
      setExpandedFile(mismatch.name);
      setComparisonFile(mismatch.name);
      return;
    }
    setExpandedFile(null);
    setComparisonFile(null);
  }, [diagnostics]);

  useEffect(() => {
    if (!expandedFile) return;
    document.getElementById(diagnosticFileId(expandedFile))?.scrollIntoView({ behavior: "smooth", block: "nearest" });
  }, [expandedFile]);

  async function refreshDiagnostics() {
    setLoading(true);
    setError("");
    try {
      setDiagnostics(await api.corednsDiagnostics());
    } catch (error_) {
      setError(error_ instanceof Error ? error_.message : "CoreDNS diagnostics could not be loaded.");
    } finally {
      setLoading(false);
    }
  }

  function viewActiveCorefile() {
    const corefile = diagnostics?.files.find((file) => file.name === "Corefile");
    if (!corefile) return;
    setExpandedFile(corefile.name);
    setComparisonFile(null);
  }

  function viewDifferences() {
    const mismatch = diagnostics?.files.find((file) => file.referenced && !file.matches);
    if (!mismatch) return;
    setExpandedFile(mismatch.name);
    setComparisonFile(mismatch.name);
  }

  const corefile = diagnostics?.files.find((file) => file.name === "Corefile");
  const activeFiles = diagnostics?.files.filter((file) => file.referenced) ?? [];

  return (
    <div className="advanced-diagnostics-page">
      <section className="panel configuration-panel">
        <div className="maintenance-section-heading"><ShieldCheck size={20} /><div><h2>CoreDNS configuration</h2><p>Faro manages CoreDNS automatically. Inspect the configuration and supporting files currently used by the DNS server.</p></div></div>
        {loading ? <LoadingState title="Inspecting DNS state" description="Comparing accepted CoreDNS files with Faro’s generated candidate…" /> : <>
          {error && <DiagnosticsRequestError message={error} onRetry={() => void refreshDiagnostics()} />}
          {diagnostics && <ConfigurationHealth diagnostics={diagnostics} corefile={corefile} loading={loading} onRefresh={() => void refreshDiagnostics()} onViewCorefile={viewActiveCorefile} onViewDifferences={viewDifferences} />}
        </>}
      </section>

      {!loading && diagnostics && <>
        <section className="panel generated-files-panel">
          <div className="generated-files-heading"><div className="maintenance-section-heading"><Code2 size={19} /><div><h2>Active configuration files</h2><p>Files currently used by Faro’s DNS server.</p></div></div></div>
          <div className="diagnostics-safety-note"><ShieldCheck size={15} /><span>Read-only inspection. These files may contain local hostnames and network details.</span></div>
          <div className="generated-files-list">
            {activeFiles.map((file) => <GeneratedDNSFileRow key={file.name} file={file} expanded={expandedFile === file.name} comparing={comparisonFile === file.name} onToggle={() => setExpandedFile((current) => current === file.name ? null : file.name)} onCompare={() => setComparisonFile((current) => current === file.name ? null : file.name)} />)}
            {activeFiles.length === 0 && <div className="diagnostics-empty">No CoreDNS-referenced files are present yet.</div>}
          </div>
        </section>

      </>}
    </div>
  );
}

function DiagnosticsRequestError({ message, onRetry }: { readonly message: string; readonly onRetry: () => void }) {
  return <div className="diagnostics-request-error" role="alert"><AlertTriangle size={17} /><div><strong>CoreDNS diagnostics are unavailable</strong><span>{message}</span></div><button type="button" className="secondary" onClick={onRetry}>Try again</button></div>;
}

function ConfigurationHealth({ diagnostics, corefile, loading, onRefresh, onViewCorefile, onViewDifferences }: { readonly diagnostics: CoreDNSDiagnostics; readonly corefile?: CoreDNSDiagnosticFile; readonly loading: boolean; readonly onRefresh: () => void; readonly onViewCorefile: () => void; readonly onViewDifferences: () => void }) {
  const health = configurationHealthCopy(diagnostics);
  const hasMismatch = diagnostics.status === "drifted" && diagnostics.files.some((file) => file.referenced && !file.matches);
  return <div className={`configuration-health ${health.tone}`}><span className="configuration-health-icon">{health.tone === "healthy" ? <CheckCircle2 size={21} /> : <AlertTriangle size={20} />}</span><div className="configuration-health-copy"><strong>{health.title}</strong><span>{health.description}</span></div><div className="configuration-health-actions"><button type="button" className="secondary icon-text-button" onClick={onRefresh} disabled={loading}><RefreshCw size={15} /><span>Refresh</span></button><button type="button" className="secondary" onClick={hasMismatch ? onViewDifferences : onViewCorefile} disabled={hasMismatch ? false : !corefile}>{hasMismatch ? "View differences" : "View active Corefile"}</button></div></div>;
}

function configurationHealthCopy(diagnostics: CoreDNSDiagnostics): { readonly title: string; readonly description: string; readonly tone: "healthy" | "warning" | "error" } {
  switch (diagnostics.status) {
    case "healthy":
      return { title: "Configuration healthy", description: "CoreDNS is running the latest configuration generated by Faro.", tone: "healthy" };
    case "drifted":
      return { title: "Configuration mismatch", description: "One or more active files differ from Faro’s generated configuration. Review the differences below.", tone: "warning" };
    case "not_initialized":
      return { title: "Configuration not initialized", description: "Faro has not accepted a CoreDNS configuration yet.", tone: "warning" };
    case "generator_error":
      return { title: "Configuration generation failed", description: diagnostics.error ? `${diagnostics.error} Faro is keeping the last accepted DNS files.` : "Faro could not generate a current candidate. The last accepted DNS files remain in use.", tone: "error" };
    default:
      return { title: "Configuration unavailable", description: "Faro could not read the current CoreDNS state.", tone: "error" };
  }
}

function GeneratedDNSFileRow({ file, expanded, comparing, onToggle, onCompare }: { readonly file: CoreDNSDiagnosticFile; readonly expanded: boolean; readonly comparing: boolean; readonly onToggle: () => void; readonly onCompare: () => void }) {
  return <div id={diagnosticFileId(file.name)} className={`generated-file-item ${expanded ? "expanded" : ""} ${file.matches ? "current" : "different"}`}><button type="button" className="generated-file-row" aria-expanded={expanded} onClick={onToggle}><span className="generated-file-name"><Code2 size={15} /><strong>{file.name}</strong></span><span className={`generated-file-state ${file.matches ? "current" : "different"}`}>{diagnosticFileState(file)}</span><span className="generated-file-chevron" aria-hidden="true">{expanded ? "−" : "+"}</span></button>{expanded && <div className="generated-file-inspector"><div className="generated-file-inspector-meta"><span>Active file · {formatBytes(file.active_bytes)}</span><span>Read only</span></div><pre>{file.active || "File not present."}</pre>{file.active_truncated && <small>Preview truncated at 1 MiB; the byte count and hash cover the full file.</small>}<div className="generated-file-inspector-footer"><span className={file.matches ? "" : "different"}>{file.matches ? "Active file matches Faro’s generated output." : "Active file differs from Faro’s generated output."}</span><button type="button" className="secondary" onClick={onCompare}>{comparing ? "Hide comparison" : "Compare configurations"}</button></div>{comparing && <CoreDNSComparison file={file} />}</div>}</div>;
}

function diagnosticFileState(file: CoreDNSDiagnosticFile) {
  if (!file.active_hash) return "Not active";
  if (!file.generated_hash) return "Not generated";
  return file.matches ? "Current" : "Differs";
}

function CoreDNSComparison({ file }: { readonly file: CoreDNSDiagnosticFile }) {
  const diff = createLineDiff(file.active, file.generated);
  return <div className="diagnostics-comparison"><div className="diagnostics-comparison-heading"><strong>Accepted vs generated</strong><span>{diff ? "Line-level comparison" : "Large file comparison"}</span></div>{diff ? <><div className="diagnostics-diff-legend"><span className="removed">Removed from active</span><span className="added">Added in generated</span></div><pre className="diagnostics-line-diff">{diff.map((line, index) => <span key={`${line.kind}-${index}`} className={`diagnostics-diff-line ${line.kind}`}><b>{line.kind === "removed" ? "−" : line.kind === "added" ? "+" : " "}</b><i>{line.oldLine ?? ""}/{line.newLine ?? ""}</i>{line.text || " "}</span>)}</pre></> : <div className="diagnostics-code-grid"><div><h3>Accepted active</h3><pre>{file.active || "File not present."}</pre></div><div><h3>Faro generated</h3><pre>{file.generated || "File not generated."}</pre></div></div>}</div>;
}

type LineDiff = { readonly kind: "same" | "added" | "removed"; readonly text: string; readonly oldLine: number | null; readonly newLine: number | null };

function createLineDiff(active: string, generated: string): LineDiff[] | null {
  const oldLines = active.split(/\r?\n/);
  const newLines = generated.split(/\r?\n/);
  if (oldLines.length > 600 || newLines.length > 600) return null;
  const table = Array.from({ length: oldLines.length + 1 }, () => new Uint16Array(newLines.length + 1));
  for (let oldIndex = oldLines.length - 1; oldIndex >= 0; oldIndex -= 1) {
    for (let newIndex = newLines.length - 1; newIndex >= 0; newIndex -= 1) {
      table[oldIndex][newIndex] = oldLines[oldIndex] === newLines[newIndex] ? table[oldIndex + 1][newIndex + 1] + 1 : Math.max(table[oldIndex + 1][newIndex], table[oldIndex][newIndex + 1]);
    }
  }
  const result: LineDiff[] = [];
  let oldIndex = 0;
  let newIndex = 0;
  while (oldIndex < oldLines.length || newIndex < newLines.length) {
    if (oldIndex < oldLines.length && newIndex < newLines.length && oldLines[oldIndex] === newLines[newIndex]) {
      result.push({ kind: "same", text: oldLines[oldIndex], oldLine: oldIndex + 1, newLine: newIndex + 1 });
      oldIndex += 1;
      newIndex += 1;
    } else if (newIndex >= newLines.length || (oldIndex < oldLines.length && table[oldIndex + 1][newIndex] >= table[oldIndex][newIndex + 1])) {
      result.push({ kind: "removed", text: oldLines[oldIndex], oldLine: oldIndex + 1, newLine: null });
      oldIndex += 1;
    } else {
      result.push({ kind: "added", text: newLines[newIndex], oldLine: null, newLine: newIndex + 1 });
      newIndex += 1;
    }
  }
  return result;
}

function diagnosticFileId(name: string) {
  return `coredns-file-${name.replace(/[^a-zA-Z0-9_-]/g, "-")}`;
}

function shortHash(value?: string) {
  if (!value) return "—";
  return value.length > 12 ? `${value.slice(0, 12)}…` : value;
}

function SettingRow({ icon, title, description, children }: { readonly icon: ReactNode; readonly title: string; readonly description: string; readonly children: ReactNode }) {
  return <div className="compact-setting-row"><span className="compact-setting-icon">{icon}</span><div className="compact-setting-copy"><strong>{title}</strong><span>{description}</span></div><div className="compact-setting-control">{children}</div></div>;
}

function HealthMetric({ icon, label, value, detail, tone = "default" }: { readonly icon: ReactNode; readonly label: string; readonly value: string; readonly detail: string; readonly tone?: "default" | "healthy" }) {
  return <div className={`maintenance-metric ${tone}`}><span className="maintenance-metric-icon">{icon}</span><div><small>{label}</small><strong>{value}</strong><span>{detail}</span></div></div>;
}

function SummaryRow({ label, value }: { readonly label: string; readonly value: string }) {
  return <div className="summary-config-row"><span>{label}</span><strong>{value}</strong></div>;
}

function formatBytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  const amount = value / 1024 ** index;
  return `${amount >= 10 || index === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[index]}`;
}

function formatTimestamp(value?: string) {
  if (!value) return "Not yet";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}

function formatDuration(seconds: number) {
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`;
  return `${Math.floor(seconds / 86400)}d ${Math.floor((seconds % 86400) / 3600)}h`;
}
