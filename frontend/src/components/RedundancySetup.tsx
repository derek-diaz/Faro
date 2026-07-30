import { AlertTriangle, ArrowLeft, Check, CheckCircle2, Copy, LogOut, Network, RefreshCw, Server, ShieldCheck } from "lucide-react";
import { useEffect, useState, type FormEvent } from "react";
import { api, type RedundancyPublicStatus } from "../api/client";
import { copyText } from "../utils/clipboard";
import { BrandLogo } from "./BrandLogo";
import { ConfirmDialog } from "./ConfirmDialog";

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

export function ReplicaNodeScreen({ initialStatus, authenticated, username, onLeft }: {
  initialStatus: RedundancyPublicStatus;
  authenticated: boolean;
  username?: string;
  onLeft: (status: RedundancyPublicStatus) => void;
}) {
  const [status, setStatus] = useState(initialStatus);
  const [copyState, setCopyState] = useState<CopyState>("idle");
  const [leaveOpen, setLeaveOpen] = useState(false);
  const [leaveBusy, setLeaveBusy] = useState(false);
  const [leaveError, setLeaveError] = useState("");
  const [adminName, setAdminName] = useState(username ?? "");
  const [adminPassword, setAdminPassword] = useState("");
  useEffect(() => {
    let active = true;
    const refresh = () => api.redundancyPublic().then((next) => {
      if (!active) return;
      if (next.role !== "replica") onLeft(next);
      else setStatus(next);
    }).catch(() => undefined);
    const timer = window.setInterval(refresh, 5000);
    return () => { active = false; window.clearInterval(timer); };
  }, [onLeft]);
  async function copyControllerAddress() {
    if (!status.controller_url || copyState === "copying") return;
    setCopyState("copying");
    try {
      await copyText(status.controller_url);
      setCopyState("copied");
    } catch {
      setCopyState("error");
    }
    window.setTimeout(() => setCopyState("idle"), 2500);
  }
  async function leaveRedundancy() {
    if (!authenticated && (!adminName.trim() || !adminPassword)) {
      setLeaveError("Enter the Faro administrator username and password.");
      return;
    }
    setLeaveBusy(true);
    setLeaveError("");
    try {
      if (!authenticated) await api.login(adminName.trim(), adminPassword);
      const result = await api.leaveRedundancy();
      onLeft(result.status);
    } catch (caught) {
      setLeaveError(caught instanceof Error ? caught.message : "Could not return this server to standalone mode.");
    } finally {
      setLeaveBusy(false);
    }
  }
  const synchronized = status.config_revision > 0 && Boolean(status.last_sync_at) && !status.last_error;
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
          <div><span>Controller</span><strong>{status.controller_url || "Not connected"}</strong>{status.controller_url && <button type="button" className={`replica-copy-button ${copyState}`} aria-label="Copy controller address" disabled={copyState === "copying"} onClick={() => void copyControllerAddress()}>{copyState === "copied" ? <Check size={14} /> : copyState === "error" ? <AlertTriangle size={14} /> : copyState === "copying" ? <RefreshCw className="spinning" size={14} /> : <Copy size={14} />}{copyState === "copied" ? "Copied!" : copyState === "error" ? "Copy failed" : copyState === "copying" ? "Copying…" : "Copy"}</button>}</div>
          <div><span>DNS settings</span><strong>{status.config_revision > 0 ? "Up to date" : "Waiting for first sync"}</strong></div>
          <div><span>Last synchronized</span><strong>{formatSyncTime(status.last_sync_at)}</strong></div>
        </div>
        {status.last_error && <div className="replica-error"><AlertTriangle size={17} /><span>{statusMessage}</span></div>}
        <footer className="replica-status-footer">
          <div><Server size={16} /><span>Changes are made on the primary Faro server. This backup keeps serving its last safe DNS settings during an outage.</span></div>
          <button type="button" className="secondary replica-leave-button" onClick={() => { setLeaveError(""); setLeaveOpen(true); }}><LogOut size={15} />Leave Faro home</button>
        </footer>
      </section>
      {leaveOpen && (
        <ConfirmDialog
          title="Leave this Faro home?"
          body="This server will stop receiving configuration from the primary Faro server and return to standalone mode."
          confirmLabel="Leave Faro home"
          busyLabel="Restoring standalone DNS…"
          busy={leaveBusy}
          onCancel={() => { setLeaveOpen(false); setLeaveError(""); setAdminPassword(""); }}
          onConfirm={() => void leaveRedundancy()}
          detail={(
            <>
              <div className="confirm-dialog-impact warning"><AlertTriangle size={18} /><span><strong>Update your router first</strong><small>Remove this server's DNS address from DHCP before leaving, or devices may continue sending requests to it while it is being reconfigured.</small></span></div>
              {!authenticated && (
                <div className="replica-leave-auth">
                  <p>Administrator confirmation</p>
                  <label><span>Username</span><input value={adminName} onChange={(event) => setAdminName(event.target.value)} autoComplete="username" /></label>
                  <label><span>Password</span><input type="password" value={adminPassword} onChange={(event) => setAdminPassword(event.target.value)} autoComplete="current-password" /></label>
                </div>
              )}
              {leaveError && <div className="replica-leave-error" role="alert">{leaveError}</div>}
            </>
          )}
        />
      )}
    </main>
  );
}

type CopyState = "idle" | "copying" | "copied" | "error";

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
      ? "The primary Faro server cannot be reached. DNS is still running safely with the last synchronized settings and will reconnect automatically."
      : "The primary Faro server cannot be reached yet. Faro will keep trying automatically.";
  }
  if (error.includes("apply controller configuration")) {
    return status.config_revision > 0
      ? "The latest update was not safe to apply. DNS is still running with the last safe settings; check the primary Faro server for details."
      : "The first DNS configuration could not be applied safely. Check the primary Faro server for details.";
  }
  return status.config_revision > 0
    ? "DNS is still running safely with the last synchronized settings. Faro will retry automatically."
    : "Faro could not finish the first synchronization and will retry automatically.";
}
