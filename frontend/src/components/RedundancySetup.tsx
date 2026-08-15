import { AlertTriangle, ArrowLeft, Check, CheckCircle2, Copy, LogOut, Network, RefreshCw, Server, ShieldCheck } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode, type SubmitEvent } from "react";
import { api, type RedundancyPublicStatus } from "../api/client";
import type { ThemeMode } from "../theme";
import { AppearanceMenu } from "./AppearanceMenu";
import { copyText } from "../utils/clipboard";
import { normalizeControllerAddress } from "../utils/controllerAddress";
import { BrandLogo } from "./BrandLogo";
import { ConfirmDialog } from "./ConfirmDialog";

type JoinExistingFaroProps = {
  readonly onBack: () => void;
  readonly onJoined: (status: RedundancyPublicStatus) => void;
  readonly themeMode: ThemeMode;
  readonly onThemeModeChange: (mode: ThemeMode) => void;
};

export function JoinExistingFaro({ onBack, onJoined, themeMode, onThemeModeChange }: JoinExistingFaroProps) {
  const currentHost = window.location.hostname;
  const [controllerURL, setControllerURL] = useState("");
  const [pairingCode, setPairingCode] = useState("");
  const [nodeName, setNodeName] = useState("Backup Faro");
  const [lanAddress, setLANAddress] = useState(isIPv4(currentHost) ? currentHost : "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const joinInFlight = useRef(false);

  async function join(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault();
    if (joinInFlight.current) return;
    joinInFlight.current = true;
    const normalizedControllerURL = normalizeControllerAddress(controllerURL);
    setControllerURL(normalizedControllerURL);
    setBusy(true);
    setError("");
    try {
      const result = await api.joinRedundancy({
        controller_url: normalizedControllerURL,
        pairing_code: pairingCode.trim(),
        node_name: nodeName.trim(),
        lan_address: lanAddress.trim()
      });
      onJoined(result.status);
    } catch (error_) {
      setError(error_ instanceof Error ? error_.message : "Could not join the existing Faro home.");
    } finally {
      joinInFlight.current = false;
      setBusy(false);
    }
  }

  function normalizeControllerInput() {
    setControllerURL((current) => normalizeControllerAddress(current));
  }

  return (
    <main className="redundancy-setup-shell">
      <AppearanceMenu className="public-appearance-control" themeMode={themeMode} onThemeModeChange={onThemeModeChange} />
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
          <label><span>Existing Faro address</span><input value={controllerURL} onChange={(event) => setControllerURL(event.target.value)} onBlur={normalizeControllerInput} inputMode="url" autoComplete="url" placeholder="192.168.1.10 or faro.local" required /><small>Enter an IP address or hostname. Faro adds port 1787 when you leave the port out.</small></label>
          <label><span>Pairing code</span><textarea rows={3} value={pairingCode} onChange={(event) => setPairingCode(event.target.value)} placeholder="FARO1.…" required /><small>The one-time code expires after 10 minutes.</small></label>
          <div className="redundancy-join-grid">
            <label><span>Name this server</span><input value={nodeName} onChange={(event) => setNodeName(event.target.value)} maxLength={40} required placeholder="Upstairs Faro" /></label>
            <label><span>This server's LAN address</span><input value={lanAddress} onChange={(event) => setLANAddress(event.target.value)} required placeholder="LAN IP address" /></label>
          </div>
          {error && <div className="auth-error" role="alert">{error}</div>}
          <footer><button type="button" className="secondary" onClick={onBack} disabled={busy}><ArrowLeft size={16} />Back</button><button type="submit" disabled={busy}><Network size={16} />{busy ? "Pairing server…" : "Join Faro home"}</button></footer>
        </form>
      </section>
    </main>
  );
}

type ReplicaNodeScreenProps = {
  readonly initialStatus: RedundancyPublicStatus;
  readonly configured: boolean;
  readonly authenticated: boolean;
  readonly username?: string;
  readonly themeMode: ThemeMode;
  readonly onThemeModeChange: (mode: ThemeMode) => void;
  readonly onLeft: (status: RedundancyPublicStatus) => void;
};

export function ReplicaNodeScreen({ initialStatus, configured, authenticated, username, themeMode, onThemeModeChange, onLeft }: ReplicaNodeScreenProps) {
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
  const requiresAuthentication = configured && !authenticated;
  async function leaveRedundancy() {
    if (requiresAuthentication && (!adminName.trim() || !adminPassword)) {
      setLeaveError("Enter the Faro administrator username and password.");
      return;
    }
    setLeaveBusy(true);
    setLeaveError("");
    try {
      if (requiresAuthentication) await api.login(adminName.trim(), adminPassword);
      const result = await api.leaveRedundancy();
      onLeft(result.status);
    } catch (error_) {
      setLeaveError(error_ instanceof Error ? error_.message : "Could not return this server to standalone mode.");
    } finally {
      setLeaveBusy(false);
    }
  }
  const synchronized = status.config_revision > 0 && Boolean(status.last_sync_at) && !status.last_error;
  const statusMessage = replicaStatusMessage(status);
  const openLeaveDialog = () => {
    setLeaveError("");
    setLeaveOpen(true);
  };
  const closeLeaveDialog = () => {
    setLeaveOpen(false);
    setLeaveError("");
    setAdminPassword("");
  };
  return (
    <main className="replica-status-shell">
      <AppearanceMenu className="public-appearance-control" themeMode={themeMode} onThemeModeChange={onThemeModeChange} />
      <header><BrandLogo /><strong>Faro</strong><span>Additional DNS server</span></header>
      <ReplicaStatusCard status={status} synchronized={synchronized} statusMessage={statusMessage} copyState={copyState} onCopyControllerAddress={() => void copyControllerAddress()} onOpenLeave={openLeaveDialog} />
      {leaveOpen && <LeaveRedundancyDialog requiresAuthentication={requiresAuthentication} busy={leaveBusy} leaveError={leaveError} adminName={adminName} adminPassword={adminPassword} onAdminNameChange={setAdminName} onAdminPasswordChange={setAdminPassword} onCancel={closeLeaveDialog} onConfirm={() => void leaveRedundancy()} />}
    </main>
  );
}

type ReplicaStatusCardProps = Readonly<{
  status: RedundancyPublicStatus;
  synchronized: boolean;
  statusMessage: string;
  copyState: CopyState;
  onCopyControllerAddress: () => void;
  onOpenLeave: () => void;
}>;

function ReplicaStatusCard({ status, synchronized, statusMessage, copyState, onCopyControllerAddress, onOpenLeave }: ReplicaStatusCardProps) {
  return (
    <section className="replica-status-card">
      <span className={`replica-status-icon ${synchronized ? "healthy" : "warning"}`}>{replicaStatusIcon(synchronized, Boolean(status.last_error))}</span>
      <div className="replica-status-copy">
        <span>{replicaStatusHeading(synchronized, Boolean(status.last_error))}</span>
        <h1>{status.node_name || "Backup Faro"}</h1>
        <p>{synchronized ? "This server is synchronized and answering DNS with the controller's last accepted configuration." : statusMessage}</p>
      </div>
      <div className="replica-status-details">
        <div><span>Controller</span><strong>{status.controller_url || "Not connected"}</strong>{status.controller_url && <ReplicaCopyButton copyState={copyState} onCopy={onCopyControllerAddress} />}</div>
        <div><span>DNS settings</span><strong>{status.config_revision > 0 ? "Up to date" : "Waiting for first sync"}</strong></div>
        <div><span>Last synchronized</span><strong>{formatSyncTime(status.last_sync_at)}</strong></div>
      </div>
      {status.last_error && <div className="replica-error"><AlertTriangle size={17} /><span>{statusMessage}</span></div>}
      <footer className="replica-status-footer">
        <div><Server size={16} /><span>Changes are made on the primary Faro server. This backup keeps serving its last safe DNS settings during an outage.</span></div>
        <button type="button" className="secondary replica-leave-button" onClick={onOpenLeave}><LogOut size={15} />Leave Faro home</button>
      </footer>
    </section>
  );
}

function replicaStatusIcon(synchronized: boolean, hasError: boolean): ReactNode {
  if (synchronized) return <ShieldCheck size={30} />;
  return <RefreshCw className={hasError ? "" : "spinning"} size={30} />;
}

function replicaStatusHeading(synchronized: boolean, hasError: boolean) {
  if (synchronized) return "Redundancy active";
  if (hasError) return "Synchronization needs attention";
  return "Joining your Faro home";
}

type ReplicaCopyButtonProps = Readonly<{
  copyState: CopyState;
  onCopy: () => void;
}>;

function ReplicaCopyButton({ copyState, onCopy }: ReplicaCopyButtonProps) {
  return (
    <button type="button" className={`replica-copy-button ${copyState}`} aria-label="Copy controller address" disabled={copyState === "copying"} onClick={onCopy}>
      {replicaCopyIcon(copyState)}
      {replicaCopyLabel(copyState)}
    </button>
  );
}

function replicaCopyIcon(copyState: CopyState): ReactNode {
  switch (copyState) {
    case "copied": return <Check size={14} />;
    case "error": return <AlertTriangle size={14} />;
    case "copying": return <RefreshCw className="spinning" size={14} />;
    default: return <Copy size={14} />;
  }
}

function replicaCopyLabel(copyState: CopyState) {
  switch (copyState) {
    case "copied": return "Copied!";
    case "error": return "Copy failed";
    case "copying": return "Copying…";
    default: return "Copy";
  }
}

type LeaveRedundancyDialogProps = Readonly<{
  requiresAuthentication: boolean;
  busy: boolean;
  leaveError: string;
  adminName: string;
  adminPassword: string;
  onAdminNameChange: (value: string) => void;
  onAdminPasswordChange: (value: string) => void;
  onCancel: () => void;
  onConfirm: () => void;
}>;

function LeaveRedundancyDialog({ requiresAuthentication, busy, leaveError, adminName, adminPassword, onAdminNameChange, onAdminPasswordChange, onCancel, onConfirm }: LeaveRedundancyDialogProps) {
  return (
    <ConfirmDialog
      title="Leave this Faro home?"
      body="This server will stop receiving configuration from the primary Faro server and return to standalone mode."
      confirmLabel="Leave Faro home"
      busyLabel="Restoring standalone DNS…"
      busy={busy}
      icon={<LogOut size={20} />}
      autoFocusCancel={false}
      onCancel={onCancel}
      onConfirm={onConfirm}
      detail={(
        <>
          <div className="confirm-dialog-impact warning"><AlertTriangle size={18} /><span><strong>Check your router afterward</strong><small>If this server is listed as a DNS address in DHCP, remove it after Faro returns to standalone mode.</small></span></div>
          {requiresAuthentication && <ReplicaLeaveAuthentication adminName={adminName} adminPassword={adminPassword} onAdminNameChange={onAdminNameChange} onAdminPasswordChange={onAdminPasswordChange} />}
          {leaveError && <div className="replica-leave-error" role="alert">{leaveError}</div>}
        </>
      )}
    />
  );
}

type ReplicaLeaveAuthenticationProps = Readonly<{
  adminName: string;
  adminPassword: string;
  onAdminNameChange: (value: string) => void;
  onAdminPasswordChange: (value: string) => void;
}>;

function ReplicaLeaveAuthentication({ adminName, adminPassword, onAdminNameChange, onAdminPasswordChange }: ReplicaLeaveAuthenticationProps) {
  return (
    <div className="replica-leave-auth">
      <p>Administrator confirmation</p>
      <label><span>Username</span><input value={adminName} onChange={(event) => onAdminNameChange(event.target.value)} autoComplete="username" /></label>
      <label><span>Password</span><input autoFocus type="password" value={adminPassword} onChange={(event) => onAdminPasswordChange(event.target.value)} autoComplete="current-password" /></label>
    </div>
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
  const hasSafeConfiguration = status.config_revision > 0;
  if (error.includes("reach controller") || error.includes("connection refused") || error.includes("timeout")) {
    if (hasSafeConfiguration) return "The primary Faro server cannot be reached. DNS is still running safely with the last synchronized settings and will reconnect automatically.";
    return "The primary Faro server cannot be reached yet. Faro will keep trying automatically.";
  }
  if (error.includes("apply controller configuration")) {
    if (hasSafeConfiguration) return "The latest update was not safe to apply. DNS is still running with the last safe settings; check the primary Faro server for details.";
    return "The first DNS configuration could not be applied safely. Check the primary Faro server for details.";
  }
  if (hasSafeConfiguration) return "DNS is still running safely with the last synchronized settings. Faro will retry automatically.";
  return "Faro could not finish the first synchronization and will retry automatically.";
}
