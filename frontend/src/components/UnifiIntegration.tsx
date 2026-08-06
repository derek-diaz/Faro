import { AlertTriangle, Check, CheckCircle2, KeyRound, Link2, LoaderCircle, LockKeyhole, RefreshCw, Router, ShieldCheck, Unplug, Wifi } from "lucide-react";
import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { api, type UnifiCertificate, type UnifiSite, type UnifiStatus } from "../api/client";

type Phase = "idle" | "testing" | "connecting" | "syncing" | "disconnecting";

export function UnifiIntegration({ onChanged }: { onChanged: () => Promise<void> }) {
  const [status, setStatus] = useState<UnifiStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [phase, setPhase] = useState<Phase>("idle");
  const [baseURL, setBaseURL] = useState("");
  const [apiKey, setAPIKey] = useState("");
  const [sites, setSites] = useState<UnifiSite[]>([]);
  const [siteID, setSiteID] = useState("");
  const [certificate, setCertificate] = useState<UnifiCertificate | null>(null);
  const [certificateTrusted, setCertificateTrusted] = useState(false);
  const [fingerprint, setFingerprint] = useState("");
  const [message, setMessage] = useState<{ tone: "success" | "error"; text: string } | null>(null);
  const [confirmDisconnect, setConfirmDisconnect] = useState(false);

  useEffect(() => {
    void loadStatus();
  }, []);

  async function loadStatus() {
    setLoading(true);
    try {
      const next = await api.unifiStatus();
      setStatus(next);
      if (next.base_url) setBaseURL(next.base_url);
    } catch (error_) {
      showError(error_);
    } finally {
      setLoading(false);
    }
  }

  async function testConnection(event?: FormEvent, trustedFingerprint = fingerprint) {
    event?.preventDefault();
    setPhase("testing");
    setMessage(null);
    try {
      const result = await api.testUnifi({
        base_url: baseURL,
        api_key: apiKey,
        tls_fingerprint: trustedFingerprint || undefined
      });
      if (result.requires_certificate_trust && result.certificate) {
        setCertificate(result.certificate);
        setFingerprint("");
        setCertificateTrusted(false);
        setSites([]);
        return;
      }
      setCertificate(null);
      setSites(result.sites);
      setSiteID((current) => result.sites.some((site) => site.id === current) ? current : result.sites[0]?.id ?? "");
      setMessage({
        tone: "success",
        text: result.sites.length === 1 ? "Console verified. Select Connect to finish." : `Console verified. Choose one of ${result.sites.length} sites.`
      });
    } catch (error_) {
      showError(error_);
    } finally {
      setPhase("idle");
    }
  }

  async function trustAndRetry() {
    if (!certificate || !certificateTrusted) return;
    const trustedFingerprint = certificate.fingerprint_sha256;
    setFingerprint(trustedFingerprint);
    await testConnection(undefined, trustedFingerprint);
  }

  async function connect() {
    if (!siteID) return;
    setPhase("connecting");
    setMessage(null);
    try {
      const next = await api.configureUnifi({
        base_url: baseURL,
        api_key: apiKey,
        site_id: siteID,
        tls_fingerprint: fingerprint || undefined
      });
      setStatus(next);
      setAPIKey("");
      setSites([]);
      setCertificate(null);
      setMessage({
        tone: next.last_error ? "error" : "success",
        text: next.last_error || `Connected to ${next.site_name || "UniFi"} and synchronized ${next.synced_devices} devices.`
      });
      await onChanged();
    } catch (error_) {
      showError(error_);
    } finally {
      setPhase("idle");
    }
  }

  async function syncNow() {
    setPhase("syncing");
    setMessage(null);
    try {
      const result = await api.syncUnifi();
      await loadStatus();
      await onChanged();
      setMessage({
        tone: result.skipped > 0 ? "error" : "success",
        text: result.skipped > 0
          ? `Synchronized ${result.synced_devices} devices; ${result.skipped} clients lacked a usable MAC address or IP.`
          : `Synchronized ${result.synced_devices} connected devices.`
      });
    } catch (error_) {
      showError(error_);
      await loadStatus();
    } finally {
      setPhase("idle");
    }
  }

  async function disconnect() {
    setPhase("disconnecting");
    setMessage(null);
    try {
      await api.disconnectUnifi();
      setStatus(null);
      setBaseURL("");
      setAPIKey("");
      setFingerprint("");
      setSites([]);
      setConfirmDisconnect(false);
      setMessage({ tone: "success", text: "UniFi disconnected. Existing Faro devices, history, and protection assignments were kept." });
      await onChanged();
    } catch (error_) {
      showError(error_);
    } finally {
      setPhase("idle");
    }
  }

  function showError(error_: unknown) {
    setMessage({ tone: "error", text: error_ instanceof Error ? error_.message : "The UniFi operation failed." });
  }

  if (loading) {
    return <section className="panel integration-panel integration-loading"><LoaderCircle className="spin" size={22} /><span>Loading integrations</span></section>;
  }

  const connected = status?.configured && status.enabled;
  return (
    <div className="integration-workspace">
      <section className="panel integration-panel">
        <header className="integration-heading">
          <span className="integration-logo"><Router size={25} /></span>
          <div>
            <small>LOCAL NETWORK INTEGRATION</small>
            <h2>UniFi Network</h2>
            <p>Give Faro stable device names and addresses without making Faro your router or DHCP server.</p>
          </div>
          <span className={`integration-state ${connected ? "connected" : ""}`}>
            {connected ? <CheckCircle2 size={15} /> : <Link2 size={15} />}
            {connected ? "Connected" : "Not connected"}
          </span>
        </header>

        {message && <div className={`integration-feedback ${message.tone}`} role="status">{message.tone === "error" ? <AlertTriangle size={17} /> : <CheckCircle2 size={17} />}<span>{message.text}</span></div>}

        {connected && status ? (
          <div className="integration-connected">
            <div className="integration-facts">
              <IntegrationFact icon={<Wifi size={18} />} label="Site" value={status.site_name || status.site_id} />
              <IntegrationFact icon={<Router size={18} />} label="Console" value={status.base_url} />
              <IntegrationFact icon={<Check size={18} />} label="Devices synchronized" value={status.synced_devices.toLocaleString()} />
              <IntegrationFact icon={<RefreshCw size={18} />} label="Last synchronized" value={formatTimestamp(status.last_sync_at)} />
            </div>

            {status.last_error && <div className="integration-sync-error"><AlertTriangle size={18} /><div><strong>Synchronization needs attention</strong><span>{status.last_error}</span></div></div>}

            <div className="integration-security-summary">
              <ShieldCheck size={18} />
              <div>
                <strong>{status.tls_mode === "pinned" ? "Trusted local certificate" : "Verified HTTPS connection"}</strong>
                <span>{status.tls_mode === "pinned" ? `Pinned fingerprint ${status.tls_fingerprint}` : "The console certificate is verified for every connection."}</span>
              </div>
            </div>

            <footer className="integration-actions">
              {confirmDisconnect ? (
                <div className="integration-disconnect-confirm">
                  <span>Disconnect UniFi? Faro devices and history will remain.</span>
                  <button type="button" className="secondary" onClick={() => setConfirmDisconnect(false)}>Keep connected</button>
                  <button type="button" className="danger-outline icon-text-button" disabled={phase !== "idle"} onClick={() => void disconnect()}><Unplug size={15} /><span>{phase === "disconnecting" ? "Disconnecting" : "Disconnect"}</span></button>
                </div>
              ) : (
                <>
                  <button type="button" className="secondary icon-text-button" onClick={() => setConfirmDisconnect(true)}><Unplug size={15} /><span>Disconnect</span></button>
                  <button type="button" className="icon-text-button" disabled={phase !== "idle"} onClick={() => void syncNow()}><RefreshCw className={phase === "syncing" ? "spin" : ""} size={15} /><span>{phase === "syncing" ? "Synchronizing" : "Sync now"}</span></button>
                </>
              )}
            </footer>
          </div>
        ) : (
          <form className="integration-setup" onSubmit={(event) => void testConnection(event)}>
            <div className="integration-step">
              <span>1</span>
              <div><strong>Connect to your local console</strong><p>Create a Network API key in UniFi under Control Plane → Integrations. Faro only requests client information.</p></div>
            </div>
            <label className="integration-field">
              <span>Console address</span>
              <div><Router size={17} /><input type="text" inputMode="url" autoComplete="url" value={baseURL} onChange={(event) => { setBaseURL(event.target.value); setSites([]); setCertificate(null); }} placeholder="https://192.168.1.1" required /></div>
              <small>Use the console's private IP or local hostname. Cloud URLs are not accepted.</small>
            </label>
            <label className="integration-field">
              <span>Network API key</span>
              <div><KeyRound size={17} /><input type="password" autoComplete="off" value={apiKey} onChange={(event) => { setAPIKey(event.target.value); setSites([]); }} placeholder="Paste the key shown by UniFi" required /></div>
              <small>The key is encrypted before it is saved and is never shown again.</small>
            </label>

            {certificate && (
              <div className="certificate-review">
                <AlertTriangle size={20} />
                <div>
                  <strong>This console uses a certificate your system does not recognize</strong>
                  <p>Compare the fingerprint with your UniFi console before trusting it. Faro will reject the connection if this certificate changes.</p>
                  <dl>
                    <div><dt>Subject</dt><dd>{certificate.subject || "Not provided"}</dd></div>
                    <div><dt>Expires</dt><dd>{formatTimestamp(certificate.expires_at)}</dd></div>
                    <div><dt>SHA-256</dt><dd>{certificate.fingerprint_sha256}</dd></div>
                  </dl>
                  <label><input type="checkbox" checked={certificateTrusted} onChange={(event) => setCertificateTrusted(event.target.checked)} /><span>I verified and trust this local console certificate.</span></label>
                  <button type="button" className="secondary icon-text-button" disabled={!certificateTrusted || phase !== "idle"} onClick={() => void trustAndRetry()}><ShieldCheck size={16} /><span>Trust and test again</span></button>
                </div>
              </div>
            )}

            {sites.length > 0 && (
              <div className="integration-site-step">
                <div className="integration-step"><span>2</span><div><strong>Choose the UniFi site</strong><p>Faro will synchronize connected clients from this site every minute.</p></div></div>
                <label className="integration-field">
                  <span>Site</span>
                  <select value={siteID} onChange={(event) => setSiteID(event.target.value)}>
                    {sites.map((site) => <option key={site.id} value={site.id}>{site.name || site.id}</option>)}
                  </select>
                </label>
              </div>
            )}

            <footer className="integration-actions">
              <span><LockKeyhole size={15} /> Read-only and local-first</span>
              {sites.length > 0
                ? <button type="button" className="icon-text-button" disabled={!siteID || phase !== "idle"} onClick={() => void connect()}><Link2 size={16} /><span>{phase === "connecting" ? "Connecting" : "Connect and sync"}</span></button>
                : <button type="submit" className="icon-text-button" disabled={!baseURL.trim() || !apiKey.trim() || phase !== "idle" || Boolean(certificate)}><RefreshCw className={phase === "testing" ? "spin" : ""} size={16} /><span>{phase === "testing" ? "Testing" : "Test connection"}</span></button>
              }
            </footer>
          </form>
        )}
      </section>

      <aside className="panel integration-explainer">
        <ShieldCheck size={20} />
        <div><strong>What this changes</strong><span>MAC addresses keep a device's Faro history, name, icon, and protection attached when its IP changes.</span></div>
        <div><strong>What this does not change</strong><span>Faro does not modify UniFi networks, DHCP, firewall rules, Wi-Fi, or DNS settings.</span></div>
      </aside>
    </div>
  );
}

function IntegrationFact({ icon, label, value }: { icon: ReactNode; label: string; value: string | number }) {
  return <div className="integration-fact"><span>{icon}</span><div><small>{label}</small><strong>{value}</strong></div></div>;
}

function formatTimestamp(value?: string) {
  if (!value) return "Not yet";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}
