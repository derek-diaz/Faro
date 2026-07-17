import { AlertTriangle, Check, CheckCircle2, Clock3, Cpu, Database, Download, Eye, EyeOff, FileArchive, Gauge, Globe2, HardDrive, Image, KeyRound, LockKeyhole, Network, RefreshCw, RotateCw, Save, Server, ShieldCheck, Trash2, Upload, UserRound } from "lucide-react";
import { useEffect, useMemo, useState, type FormEvent, type ReactNode } from "react";
import { api, type MaintenanceStatus, type PruneResult, type Setting } from "../api/client";

type SettingsProps = {
  settings: Setting[];
  refresh: () => Promise<void>;
  onManageUpstreams: () => void;
};

type SettingsTab = "general" | "data" | "account";
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

  async function saveGeneral(event: FormEvent) {
    event.preventDefault();
    await runAction(async () => {
      await api.updateSettings({
        local_domain_suffix: form.local_domain_suffix || "home",
        faro_lan_ip: form.faro_lan_ip || "",
        dns_cache_enabled: form.dns_cache_enabled || "true",
        dns_cache_ttl: form.dns_cache_ttl || "300",
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

  async function changePassword(event: FormEvent) {
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
    } catch (caught) {
      setActionState("error");
      setMessage(caught instanceof Error ? caught.message : "The operation failed.");
    }
  }

  return (
    <div className="settings-workspace">
      <div className="settings-tabs" role="tablist" aria-label="Settings sections">
        <button type="button" role="tab" aria-selected={tab === "general"} className={tab === "general" ? "active" : ""} onClick={() => setTab("general")}>
          <Gauge size={16} /> DNS & interface
        </button>
        <button type="button" role="tab" aria-selected={tab === "data"} className={tab === "data" ? "active" : ""} onClick={() => setTab("data")}>
          <Database size={16} /> Health & data
        </button>
        <button type="button" role="tab" aria-selected={tab === "account"} className={tab === "account" ? "active" : ""} onClick={() => setTab("account")}>
          <UserRound size={16} /> Account
        </button>
      </div>

      {message && <div className={`settings-feedback ${actionState === "error" ? "error" : "success"}`}><CheckCircle2 size={16} /><span>{message}</span></div>}

      {tab === "general" ? (
        <div className="settings-general-grid">
          <section className="panel settings-compact-panel">
            <div className="panel-title with-actions">
              <div><h2>DNS & resolution</h2><p>Core behavior for local and public lookups.</p></div>
              <button type="button" className="secondary icon-text-button" onClick={() => void reloadDNS()} disabled={actionState === "working"}>
                <RotateCw size={16} /><span>Reload DNS</span>
              </button>
            </div>

            <form className="settings-rows" onSubmit={(event) => void saveGeneral(event)}>
              <SettingRow icon={<Server size={19} />} title="Upstream providers" description={`${upstreams.length} selected: ${upstreams.join(", ") || "none"}`}>
                <button type="button" className="secondary" onClick={onManageUpstreams}>Manage</button>
              </SettingRow>

              <SettingRow icon={<Globe2 size={19} />} title="Local domain suffix" description="Default suffix suggested for local records.">
                <input className="settings-short-input" value={form.local_domain_suffix ?? ""} onChange={(event) => setForm({ ...form, local_domain_suffix: event.target.value })} placeholder="home" />
              </SettingRow>

              <SettingRow icon={<Network size={19} />} title="Faro LAN address" description="Fixed address distributed to devices as their DNS server.">
                <input className="settings-short-input" value={form.faro_lan_ip ?? ""} onChange={(event) => setForm({ ...form, faro_lan_ip: event.target.value })} placeholder="192.168.1.20" required />
              </SettingRow>

              <SettingRow icon={<Gauge size={19} />} title="DNS response cache" description="Keep repeated answers local to reduce latency and upstream traffic.">
                <div className="settings-inline-controls">
                  <label className="compact-toggle"><input type="checkbox" checked={form.dns_cache_enabled !== "false"} onChange={(event) => setForm({ ...form, dns_cache_enabled: String(event.target.checked) })} /><span>Enabled</span></label>
                  <label className="unit-input"><input type="number" min="30" max="3600" step="30" disabled={form.dns_cache_enabled === "false"} value={form.dns_cache_ttl ?? "300"} onChange={(event) => setForm({ ...form, dns_cache_ttl: event.target.value })} /><span>sec max</span></label>
                </div>
              </SettingRow>

              <SettingRow icon={<Image size={19} />} title="Domain favicons" description="Fetch and cache icons for public domains; local names retain initials.">
                <label className="compact-toggle"><input type="checkbox" checked={form.favicon_fetching_enabled === "true"} onChange={(event) => setForm({ ...form, favicon_fetching_enabled: String(event.target.checked) })} /><span>Enabled</span></label>
              </SettingRow>

              <div className="settings-save-row">
                <button type="submit" className="icon-text-button" disabled={actionState === "working"}><Save size={16} /><span>{actionState === "working" ? "Saving" : "Save changes"}</span></button>
              </div>
            </form>
          </section>

          <aside className="settings-overview-column">
            <section className="panel settings-summary">
              <div className="panel-title"><h2>Current configuration</h2></div>
              <div className="settings-summary-list">
                <SummaryRow label="Upstreams" value={upstreams.join(", ") || "Not configured"} />
                <SummaryRow label="Local suffix" value={form.local_domain_suffix || "home"} />
                <SummaryRow label="LAN address" value={form.faro_lan_ip || "Not configured"} />
                <SummaryRow label="Cache" value={form.dns_cache_enabled !== "false" ? `${form.dns_cache_ttl || "300"} seconds` : "Disabled"} />
                <SummaryRow label="Favicons" value={form.favicon_fetching_enabled === "true" ? "Enabled" : "Disabled"} />
              </div>
            </section>
            <section className="panel settings-note">
              <CheckCircle2 size={19} />
              <div><strong>Safe configuration updates</strong><span>Faro validates generated DNS files before replacing the active configuration.</span></div>
            </section>
          </aside>
        </div>
      ) : tab === "data" ? (
        <DataAndHealth
          maintenance={maintenance}
          loading={maintenanceLoading}
          retentionDays={form.retention_days || "30"}
          setRetentionDays={(value) => setForm({ ...form, retention_days: value })}
          saveRetention={() => void saveRetention()}
          pruneDays={pruneDays}
          setPruneDays={setPruneDays}
          compact={compact}
          setCompact={setCompact}
          prune={() => void pruneNow()}
          refresh={() => void loadMaintenance()}
          busy={actionState === "working"}
          result={pruneResult}
        />
      ) : (
        <section className="panel account-password-panel">
          <div className="panel-title"><div><h2>Change password</h2><p>Enter your current password, then choose a new one.</p></div></div>
          <form className="account-password-form" onSubmit={(event) => void changePassword(event)}>
            <PasswordField label="Current password" value={currentPassword} setValue={setCurrentPassword} visible={passwordVisible} autoComplete="current-password" />
            <PasswordField label="New password" value={newPassword} setValue={setNewPassword} visible={passwordVisible} autoComplete="new-password" />
            <PasswordField label="Confirm new password" value={confirmPassword} setValue={setConfirmPassword} visible={passwordVisible} autoComplete="new-password" />
            <div className="account-password-footer">
              <div className="password-requirements" aria-label="Password requirements">
                <span className={newPassword.length >= 8 ? "met" : ""}><Check size={13} /> 8 or more characters</span>
                <span className={confirmPassword.length > 0 && newPassword === confirmPassword ? "met" : ""}><Check size={13} /> Passwords match</span>
              </div>
              <button type="button" className="secondary password-visibility" onClick={() => setPasswordVisible((visible) => !visible)}>{passwordVisible ? <EyeOff size={15} /> : <Eye size={15} />}<span>{passwordVisible ? "Hide" : "Show"}</span></button>
            </div>
            <div className="settings-save-row account-password-action"><span>Other sessions will be signed out.</span><button type="submit" className="icon-text-button" disabled={actionState === "working" || !currentPassword || !newPassword || !confirmPassword}><KeyRound size={16} /><span>{actionState === "working" ? "Changing password" : "Change password"}</span></button></div>
          </form>
        </section>
      )}
    </div>
  );
}

function PasswordField({ label, value, setValue, visible, autoComplete }: { label: string; value: string; setValue: (value: string) => void; visible: boolean; autoComplete: string }) {
  return <label className="account-password-field"><span>{label}</span><div><LockKeyhole size={17} /><input type={visible ? "text" : "password"} value={value} onChange={(event) => setValue(event.target.value)} autoComplete={autoComplete} required /></div></label>;
}

function DataAndHealth({ maintenance, loading, retentionDays, setRetentionDays, saveRetention, pruneDays, setPruneDays, compact, setCompact, prune, refresh, busy, result }: {
  maintenance: MaintenanceStatus | null;
  loading: boolean;
  retentionDays: string;
  setRetentionDays: (value: string) => void;
  saveRetention: () => void;
  pruneDays: number;
  setPruneDays: (value: number) => void;
  compact: boolean;
  setCompact: (value: boolean) => void;
  prune: () => void;
  refresh: () => void;
  busy: boolean;
  result: PruneResult | null;
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
    } catch (caught) {
      setBackupMessage({ tone: "error", text: caught instanceof Error ? caught.message : "The backup could not be created." });
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
    } catch (caught) {
      setBackupMessage({ tone: "error", text: caught instanceof Error ? caught.message : "The backup could not be restored." });
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
        {backupMessage && <div className={`backup-message ${backupMessage.tone}`} role="status">{backupMessage.tone === "error" ? <AlertTriangle size={16} /> : <CheckCircle2 size={16} />}<span>{backupMessage.text}</span></div>}
        <div className="backup-actions-grid">
          <div className="backup-action-card">
            <div className="backup-card-heading"><FileArchive size={19} /><div><strong>Create encrypted backup</strong><span>Choose a unique passphrase. It is required to restore the file.</span></div></div>
            <label><span>Backup passphrase</span><input type="password" minLength={12} autoComplete="new-password" value={backupPassphrase} onChange={(event) => setBackupPassphrase(event.target.value)} placeholder="At least 12 characters" /></label>
            <label><span>Confirm passphrase</span><input type="password" minLength={12} autoComplete="new-password" value={backupConfirmation} onChange={(event) => setBackupConfirmation(event.target.value)} /></label>
            <button type="button" className="secondary icon-text-button" onClick={() => void exportBackup()} disabled={busy || backupBusy !== null}><Download size={16} /><span>{backupBusy === "export" ? "Encrypting backup" : "Download backup"}</span></button>
          </div>

          <div className="backup-action-card restore-card">
            <div className="backup-card-heading"><Upload size={19} /><div><strong>Restore encrypted backup</strong><span>This replaces the live database and reloads DNS configuration.</span></div></div>
            <label><span>Backup file</span><input type="file" accept=".faro-backup,application/octet-stream" onChange={(event) => setRestoreFile(event.target.files?.[0] ?? null)} /></label>
            <label><span>Backup passphrase</span><input type="password" minLength={12} autoComplete="off" value={restorePassphrase} onChange={(event) => setRestorePassphrase(event.target.value)} /></label>
            <label className="restore-confirmation"><input type="checkbox" checked={restoreConfirmed} onChange={(event) => setRestoreConfirmed(event.target.checked)} /><span>I understand this replaces current data and signs out every session.</span></label>
            <button type="button" className="danger-outline icon-text-button" onClick={() => void restoreBackup()} disabled={busy || backupBusy !== null || !restoreConfirmed}><Upload size={16} /><span>{backupBusy === "restore" ? "Validating and restoring" : "Restore backup"}</span></button>
          </div>
        </div>
      </section>
    </div>
  );
}

function SettingRow({ icon, title, description, children }: { icon: ReactNode; title: string; description: string; children: ReactNode }) {
  return <div className="compact-setting-row"><span className="compact-setting-icon">{icon}</span><div className="compact-setting-copy"><strong>{title}</strong><span>{description}</span></div><div className="compact-setting-control">{children}</div></div>;
}

function HealthMetric({ icon, label, value, detail, tone = "default" }: { icon: ReactNode; label: string; value: string; detail: string; tone?: "default" | "healthy" }) {
  return <div className={`maintenance-metric ${tone}`}><span className="maintenance-metric-icon">{icon}</span><div><small>{label}</small><strong>{value}</strong><span>{detail}</span></div></div>;
}

function SummaryRow({ label, value }: { label: string; value: string }) {
  return <div className="summary-config-row"><span>{label}</span><strong>{value}</strong></div>;
}

function formatBytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  const amount = value / 1024 ** index;
  return `${amount >= 10 || index === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[index]}`;
}

function formatNumber(value: number) {
  return value.toLocaleString();
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
