import { AlertTriangle, ArrowLeft, CheckCircle2, Copy, Network, RefreshCw, Server, ShieldCheck } from "lucide-react";
import { useEffect, useState, type FormEvent } from "react";
import { api, type RedundancyPublicStatus } from "../api/client";
import { BrandLogo } from "./BrandLogo";

export function JoinExistingFaro({ onBack, onJoined }: {
  onBack: () => void;
  onJoined: (status: RedundancyPublicStatus) => void;
}) {
  const currentHost = window.location.hostname;
  const [controllerURL, setControllerURL] = useState("");
  const [pairingCode, setPairingCode] = useState("");
  const [nodeName, setNodeName] = useState("Backup Faro");
  const [lanAddress, setLANAddress] = useState(isIPv4(currentHost) ? currentHost : "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function join(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const result = await api.joinRedundancy({
        controller_url: controllerURL.trim(),
        pairing_code: pairingCode.trim(),
        node_name: nodeName.trim(),
        lan_address: lanAddress.trim()
      });
      onJoined(result.status);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Could not join the existing Faro home.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="redundancy-setup-shell">
      <aside className="redundancy-setup-context">
        <div className="auth-brand"><BrandLogo /><strong className="brand-wordmark">Faro</strong></div>
        <div><span>DNS redundancy</span><h1>Join your existing Faro home.</h1><p>This server will receive the same validated DNS configuration and continue serving it when the controller is unavailable.</p></div>
        <ul>
          <li><ShieldCheck size={16} />Encrypted, authenticated synchronization</li>
          <li><CheckCircle2 size={16} />Configuration is validated before activation</li>
          <li><Network size={16} />No inbound connection to this replica is required</li>
        </ul>
      </aside>
      <section className="redundancy-setup-main">
        <form className="redundancy-join-form" onSubmit={(event) => void join(event)}>
          <header><span>Additional DNS server</span><h2>Connect this Faro server</h2><p>Generate a pairing code from Settings → Redundancy on your existing Faro server.</p></header>
          <label><span>Existing Faro address</span><input value={controllerURL} onChange={(event) => setControllerURL(event.target.value)} placeholder="http://192.168.1.20:1787" required /><small>The private address you normally use to open Faro.</small></label>
          <label><span>Pairing code</span><textarea rows={3} value={pairingCode} onChange={(event) => setPairingCode(event.target.value)} placeholder="FARO1.…" required /><small>The one-time code expires after 10 minutes.</small></label>
          <div className="redundancy-join-grid">
            <label><span>Name this server</span><input value={nodeName} onChange={(event) => setNodeName(event.target.value)} maxLength={40} required placeholder="Upstairs Faro" /></label>
            <label><span>This server's LAN address</span><input value={lanAddress} onChange={(event) => setLANAddress(event.target.value)} required placeholder="192.168.1.21" /></label>
          </div>
          {error && <div className="auth-error" role="alert">{error}</div>}
          <footer><button type="button" className="secondary" onClick={onBack} disabled={busy}><ArrowLeft size={16} />Back</button><button type="submit" disabled={busy}><Network size={16} />{busy ? "Pairing server…" : "Join Faro home"}</button></footer>
        </form>
      </section>
    </main>
  );
}

export function ReplicaNodeScreen({ initialStatus }: { initialStatus: RedundancyPublicStatus }) {
  const [status, setStatus] = useState(initialStatus);
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    let active = true;
    const refresh = () => api.redundancyPublic().then((next) => { if (active) setStatus(next); }).catch(() => undefined);
    const timer = window.setInterval(refresh, 5000);
    return () => { active = false; window.clearInterval(timer); };
  }, []);
  const synchronized = Boolean(status.last_sync_at) && !status.last_error;
  const statusMessage = replicaStatusMessage(status);
  return (
    <main className="replica-status-shell">
      <header><BrandLogo /><strong>Faro</strong><span>Additional DNS server</span></header>
      <section className="replica-status-card">
        <span className={`replica-status-icon ${synchronized ? "healthy" : "warning"}`}>{synchronized ? <ShieldCheck size={30} /> : <RefreshCw className={!status.last_error ? "spinning" : ""} size={30} />}</span>
        <div className="replica-status-copy">
          <span>{synchronized ? "Redundancy active" : status.last_error ? "Synchronization needs attention" : "Joining your Faro home"}</span>
          <h1>{status.node_name || "Backup Faro"}</h1>
          <p>{synchronized ? "This server is synchronized and answering DNS with the controller's last accepted configuration." : statusMessage}</p>
        </div>
        <div className="replica-status-details">
          <div><span>Controller</span><strong>{status.controller_url || "Not connected"}</strong>{status.controller_url && <button type="button" aria-label="Copy controller address" onClick={() => { void navigator.clipboard.writeText(status.controller_url!); setCopied(true); window.setTimeout(() => setCopied(false), 1500); }}><Copy size={14} />{copied ? "Copied" : "Copy"}</button>}</div>
          <div><span>Configuration</span><strong>Revision {status.config_revision}</strong></div>
          <div><span>Last synchronized</span><strong>{formatSyncTime(status.last_sync_at)}</strong></div>
        </div>
        {status.last_error && <div className="replica-error"><AlertTriangle size={17} /><span>{statusMessage}</span></div>}
        <footer><Server size={16} /><span>Configuration changes are made on the controller. This replica keeps serving the last accepted revision during an outage.</span></footer>
      </section>
    </main>
  );
}

function isIPv4(value: string) {
  return /^\d{1,3}(?:\.\d{1,3}){3}$/.test(value);
}

function formatSyncTime(value?: string) {
  if (!value) return "Waiting for first sync";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString();
}

function replicaStatusMessage(status: RedundancyPublicStatus) {
  if (!status.last_error) return "Waiting for the first validated configuration snapshot.";
  const error = status.last_error.toLowerCase();
  if (error.includes("reach controller") || error.includes("connection refused") || error.includes("timeout")) {
    return status.config_revision > 0
      ? `The primary Faro server cannot be reached. DNS is still running safely on revision ${status.config_revision} and will reconnect automatically.`
      : "The primary Faro server cannot be reached yet. Faro will keep trying automatically.";
  }
  if (error.includes("apply controller configuration")) {
    return status.config_revision > 0
      ? `The latest update was not safe to apply. DNS is still running on revision ${status.config_revision}; check the primary Faro server for details.`
      : "The first DNS configuration could not be applied safely. Check the primary Faro server for details.";
  }
  return status.config_revision > 0
    ? `DNS is still running safely on revision ${status.config_revision}. Faro will retry synchronization automatically.`
    : "Faro could not finish the first synchronization and will retry automatically.";
}
