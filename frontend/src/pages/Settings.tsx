import { CheckCircle2, Database, Globe2, Image, RotateCw, Save, Server, ShieldAlert } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { api, type Setting } from "../api/client";

type SettingsProps = {
  settings: Setting[];
  refresh: () => Promise<void>;
  onManageUpstreams: () => void;
};

export function Settings({ settings, refresh, onManageUpstreams }: SettingsProps) {
  const [form, setForm] = useState<Record<string, string>>({});
  const [saveState, setSaveState] = useState<"idle" | "saving" | "saved" | "error">("idle");
  const [reloadState, setReloadState] = useState<"idle" | "reloading" | "reloaded" | "error">("idle");
  const [message, setMessage] = useState("");

  useEffect(() => {
    setForm(Object.fromEntries(settings.map((setting) => [setting.key, setting.value])));
  }, [settings]);

  const upstreams = useMemo(
    () =>
      (form.upstream_dns ?? "")
        .split(",")
        .map((value) => value.trim())
        .filter(Boolean),
    [form.upstream_dns]
  );

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setSaveState("saving");
    setMessage("");
    try {
      await api.updateSettings(form);
      await refresh();
      setSaveState("saved");
      setMessage("Settings saved and CoreDNS reload requested.");
    } catch (caught) {
      setSaveState("error");
      setMessage(caught instanceof Error ? caught.message : "Failed to save settings.");
    }
  }

  async function reloadCoreDNS() {
    setReloadState("reloading");
    setMessage("");
    try {
      await api.reload();
      setReloadState("reloaded");
      setMessage("CoreDNS reload completed.");
    } catch (caught) {
      setReloadState("error");
      setMessage(caught instanceof Error ? caught.message : "CoreDNS reload failed.");
    }
  }

  return (
    <div className="settings-layout">
      <section className="panel settings-panel">
        <div className="panel-title with-actions">
          <div>
            <h2>DNS behavior</h2>
            <p>These settings regenerate the CoreDNS config through Faro's safe reload path.</p>
          </div>
          <button type="button" className="secondary icon-text-button" onClick={() => void reloadCoreDNS()} disabled={reloadState === "reloading"}>
            <RotateCw size={16} />
            <span>{reloadState === "reloading" ? "Reloading" : "Safe reload"}</span>
          </button>
        </div>

        <form className="settings-form" onSubmit={(event) => void submit(event)}>
          <div className="settings-card">
            <div className="settings-card-icon">
              <Server size={22} />
            </div>
            <div className="setting-copy">
              <strong>Upstream DNS servers</strong>
              <span>{upstreams.length} servers configured: {upstreams.join(", ") || "none"}</span>
              <button type="button" className="secondary settings-link-button" onClick={onManageUpstreams}>Manage upstream providers</button>
            </div>
          </div>

          <div className="settings-card">
            <div className="settings-card-icon">
              <Globe2 size={22} />
            </div>
            <label>
              Local domain suffix
              <input
                value={form.local_domain_suffix ?? ""}
                onChange={(event) => setForm({ ...form, local_domain_suffix: event.target.value })}
                placeholder="home"
              />
              <span>Used as the friendly default suffix for local records.</span>
            </label>
          </div>

          <div className="settings-card">
            <div className="settings-card-icon">
              <Database size={22} />
            </div>
            <label>
              Query log retention days
              <input
                inputMode="numeric"
                value={form.retention_days ?? ""}
                onChange={(event) => setForm({ ...form, retention_days: event.target.value })}
                placeholder="30"
              />
              <span>The setting is stored now; scheduled pruning is a follow-up task.</span>
            </label>
          </div>

          <div className="settings-card favicon-settings-card">
            <div className="settings-card-icon">
              <Image size={22} />
            </div>
            <div className="setting-copy">
              <strong>Domain favicons</strong>
              <span>When enabled, Faro fetches and caches `/favicon.ico` for public-looking domains. Local domains such as `.home`, `.lan`, and `.local` keep placeholder initials.</span>
              <label className="checkbox-row">
                <input
                  type="checkbox"
                  checked={form.favicon_fetching_enabled === "true"}
                  onChange={(event) => setForm({ ...form, favicon_fetching_enabled: String(event.target.checked) })}
                />
                Enable favicon fetching
              </label>
            </div>
          </div>

          <div className="settings-actions">
            <button type="submit" className="icon-text-button" disabled={saveState === "saving"}>
              <Save size={16} />
              <span>{saveState === "saving" ? "Saving" : "Save settings"}</span>
            </button>
            {message && <span className={`settings-message ${saveState === "error" || reloadState === "error" ? "error" : "ok"}`}>{message}</span>}
          </div>
        </form>
      </section>

      <aside className="settings-side">
        <section className="panel settings-summary">
          <div className="panel-title">
            <h2>Current config</h2>
          </div>
          <div className="settings-summary-list">
            <SummaryRow label="Upstreams" value={upstreams.length > 0 ? upstreams.join(", ") : "Not set"} />
            <SummaryRow label="Local suffix" value={form.local_domain_suffix || "home"} />
            <SummaryRow label="Retention" value={`${form.retention_days || "30"} days`} />
            <SummaryRow label="Favicon fetcher" value={form.favicon_fetching_enabled === "true" ? "Enabled" : "Disabled"} />
          </div>
        </section>

        <section className="panel settings-summary">
          <div className="settings-warning">
            <ShieldAlert size={20} />
            <div>
              <strong>Favicon status</strong>
              <span>Public-domain icons are fetched only when enabled. Local-only names always use placeholders.</span>
            </div>
          </div>
          <div className="settings-warning ok">
            <CheckCircle2 size={20} />
            <div>
              <strong>Safe reload path</strong>
              <span>Saving DNS settings writes generated files and requests CoreDNS reload.</span>
            </div>
          </div>
        </section>
      </aside>
    </div>
  );
}

function SummaryRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="summary-config-row">
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}
