import { AlertTriangle, Check, CheckCircle2, Clock3, Copy, Network, Plus, RefreshCw, Server, ShieldCheck, Trash2 } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { api, type PairingCode, type RedundancyNode, type RedundancyStatus } from "../api/client";
import { copyText } from "../utils/clipboard";
import { ConfirmDialog } from "./ConfirmDialog";

type CopyState = "idle" | "copying" | "copied" | "error";

export function RedundancySettings() {
  const [status, setStatus] = useState<RedundancyStatus | null>(null);
  const [pairing, setPairing] = useState<PairingCode | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [copyState, setCopyState] = useState<CopyState>("idle");
  const copyResetTimer = useRef<number | undefined>(undefined);
  const [pendingRemoval, setPendingRemoval] = useState<RedundancyNode | null>(null);
  const [disableOpen, setDisableOpen] = useState(false);

  async function load() {
    try {
      setStatus(await api.redundancyStatus());
      setError("");
    } catch (error_) {
      setError(error_ instanceof Error ? error_.message : "Could not load redundancy status.");
    }
  }

  useEffect(() => {
    let active = true;
    const refresh = () => api.redundancyStatus().then((next) => { if (active) setStatus(next); }).catch(() => undefined);
    void refresh();
    const timer = window.setInterval(refresh, 5000);
    return () => { active = false; window.clearInterval(timer); };
  }, []);

  useEffect(() => () => window.clearTimeout(copyResetTimer.current), []);

  async function startPairing() {
    setBusy(true);
    setError("");
    try {
      const nextPairing = await api.startRedundancyPairing(status?.node_name || "Primary Faro");
      setPairing(nextPairing);
      setCopyState("idle");
      await load();
    } catch (error_) {
      setError(error_ instanceof Error ? error_.message : "Could not create a pairing code.");
    } finally {
      setBusy(false);
    }
  }

  async function copyPairingCode() {
    if (!pairing || copyState === "copying") return;
    window.clearTimeout(copyResetTimer.current);
    setCopyState("copying");
    try {
      await copyText(pairing.code);
      setCopyState("copied");
    } catch {
      setCopyState("error");
    }
    copyResetTimer.current = window.setTimeout(() => setCopyState("idle"), 2500);
  }

  async function removeNode() {
    if (!pendingRemoval) return;
    setBusy(true);
    try {
      await api.removeRedundancyNode(pendingRemoval.node_id);
      setPendingRemoval(null);
      await load();
    } catch (error_) {
      setError(error_ instanceof Error ? error_.message : "Could not remove the replica.");
    } finally {
      setBusy(false);
    }
  }

  async function disableRedundancy() {
    setBusy(true);
    setError("");
    try {
      await api.leaveRedundancy();
      window.location.reload();
    } catch (error_) {
      setError(error_ instanceof Error ? error_.message : "Could not turn off redundancy.");
      setDisableOpen(false);
      setBusy(false);
    }
  }

  if (!status) return <section className="panel redundancy-loading"><RefreshCw className="spinning" size={20} /><span>Checking Faro servers…</span></section>;
  const replicas = status.nodes.filter((node) => node.role === "replica");
  const addresses = status.nodes.map((node) => node.lan_address).filter((address): address is string => Boolean(address));

  return (
    <div className="redundancy-settings">
      <RedundancyHero status={status} replicaCount={replicas.length} busy={busy} onStartPairing={() => void startPairing()} />

      {error && <div className="settings-feedback error"><AlertTriangle size={16} /><span>{error}</span></div>}

      {pairing && <PairingCard pairing={pairing} copyState={copyState} onCopy={() => void copyPairingCode()} />}

      <RedundancyRoleContent
        status={status}
        addresses={addresses}
        onRefresh={() => void load()}
        onRemoveRequest={(node) => setPendingRemoval(node)}
        onDisableRequest={() => setDisableOpen(true)}
      />

      <RedundancyDialogs
        pendingRemoval={pendingRemoval}
        disableOpen={disableOpen}
        busy={busy}
        onCancelRemoval={() => setPendingRemoval(null)}
        onConfirmRemoval={() => void removeNode()}
        onCancelDisable={() => setDisableOpen(false)}
        onConfirmDisable={() => void disableRedundancy()}
      />
    </div>
  );
}

type RedundancyHeroProps = Readonly<{
  status: RedundancyStatus;
  replicaCount: number;
  busy: boolean;
  onStartPairing: () => void;
}>;

function RedundancyHero({ status, replicaCount, busy, onStartPairing }: RedundancyHeroProps) {
  const hasReplicas = status.role === "controller" && replicaCount > 0;
  return (
    <section className="panel redundancy-hero">
      <span className={`redundancy-hero-icon ${heroTone(status, hasReplicas)}`}><Network size={25} /></span>
      <div>
        <span>DNS redundancy</span>
        <h2>{heroHeading(status, hasReplicas)}</h2>
        <p>{heroDescription(status, hasReplicas)}</p>
      </div>
      <button type="button" onClick={onStartPairing} disabled={busy}><Plus size={17} />{status.role === "controller" ? "Add another server" : "Set up redundancy"}</button>
    </section>
  );
}

function heroTone(status: RedundancyStatus, hasReplicas: boolean) {
  if (!hasReplicas) return "";
  return status.healthy ? "healthy" : "warning";
}

function heroHeading(status: RedundancyStatus, hasReplicas: boolean) {
  if (hasReplicas) return `Protected by ${status.nodes.length} Faro servers`;
  return "Keep DNS online when this server is unavailable";
}

function heroDescription(status: RedundancyStatus, hasReplicas: boolean) {
  if (!hasReplicas) return "Pair another Faro container running on a different computer, NAS, or VM host.";
  if (status.healthy) return "Every server is online and running the same validated DNS configuration.";
  return "At least one server needs attention or has not accepted the latest configuration.";
}

type PairingCardProps = Readonly<{
  pairing: PairingCode;
  copyState: CopyState;
  onCopy: () => void;
}>;

function PairingCard({ pairing, copyState, onCopy }: PairingCardProps) {
  return (
    <section className="panel redundancy-pairing">
      <div className="panel-title"><div><h2>Pair an additional Faro server</h2><p>On the new installation, choose “Join an existing Faro home” and paste this code.</p></div><span><Clock3 size={14} />Expires {formatTime(pairing.expires_at)}</span></div>
      <div className="redundancy-pairing-code">
        <code>{pairing.code}</code>
        <button type="button" className={`secondary redundancy-copy-button ${copyState}`} disabled={copyState === "copying"} onClick={onCopy} aria-live="polite">
          {copyStateIcon(copyState)}
          <span>{copyStateLabel(copyState)}</span>
        </button>
      </div>
      <div className="redundancy-pairing-note"><ShieldCheck size={17} /><span>The code is used to establish a unique encrypted connection. It is never stored on either server after pairing.</span></div>
    </section>
  );
}

function copyStateIcon(copyState: CopyState): ReactNode {
  switch (copyState) {
    case "copied": return <Check size={16} />;
    case "error": return <AlertTriangle size={16} />;
    case "copying": return <RefreshCw className="spinning" size={16} />;
    default: return <Copy size={16} />;
  }
}

function copyStateLabel(copyState: CopyState) {
  switch (copyState) {
    case "copied": return "Copied!";
    case "error": return "Copy failed";
    case "copying": return "Copying…";
    default: return "Copy code";
  }
}

type RedundancyRoleContentProps = Readonly<{
  status: RedundancyStatus;
  addresses: string[];
  onRefresh: () => void;
  onRemoveRequest: (node: RedundancyNode) => void;
  onDisableRequest: () => void;
}>;

function RedundancyRoleContent({ status, addresses, onRefresh, onRemoveRequest, onDisableRequest }: RedundancyRoleContentProps) {
  if (status.role === "controller") {
    return <ControllerRedundancyView status={status} addresses={addresses} onRefresh={onRefresh} onRemoveRequest={onRemoveRequest} onDisableRequest={onDisableRequest} />;
  }
  return <ReplicaStartGuide />;
}

type ControllerRedundancyViewProps = Readonly<{
  status: RedundancyStatus;
  addresses: string[];
  onRefresh: () => void;
  onRemoveRequest: (node: RedundancyNode) => void;
  onDisableRequest: () => void;
}>;

function ControllerRedundancyView({ status, addresses, onRefresh, onRemoveRequest, onDisableRequest }: ControllerRedundancyViewProps) {
  return (
    <>
      <section className="panel redundancy-nodes-panel">
        <div className="panel-title with-actions"><div><h2>Faro servers</h2><p>Backup servers keep serving their last safe DNS settings if this primary server is offline.</p></div><button type="button" className="secondary" onClick={onRefresh}><RefreshCw size={15} />Refresh</button></div>
        <RedundancyNodeList nodes={status.nodes} primaryRevision={status.config_revision} onRemoveRequest={onRemoveRequest} />
      </section>

      <section className="panel redundancy-router">
        <div><Network size={20} /><span><strong>Router DNS addresses</strong><small>Distribute every address below through DHCP. Clients may use any of them.</small></span></div>
        <RouterAddresses addresses={addresses} />
      </section>

      <section className="panel redundancy-disable">
        <div><strong>Stop using redundancy</strong><small>Return this installation to a standalone Faro server.</small></div>
        <button type="button" className="secondary danger-outline" onClick={onDisableRequest}>Turn off redundancy</button>
      </section>
    </>
  );
}

type RedundancyNodeListProps = Readonly<{
  nodes: RedundancyNode[];
  primaryRevision: number;
  onRemoveRequest: (node: RedundancyNode) => void;
}>;

function RedundancyNodeList({ nodes, primaryRevision, onRemoveRequest }: RedundancyNodeListProps) {
  return (
    <div className="redundancy-node-list">
      {nodes.map((node) => <RedundancyNodeCard key={node.node_id} node={node} primaryRevision={primaryRevision} onRemoveRequest={onRemoveRequest} />)}
    </div>
  );
}

type RedundancyNodeCardProps = Readonly<{
  node: RedundancyNode;
  primaryRevision: number;
  onRemoveRequest: (node: RedundancyNode) => void;
}>;

function RedundancyNodeCard({ node, primaryRevision, onRemoveRequest }: RedundancyNodeCardProps) {
  const display = nodeDisplayState(node, primaryRevision);
  return (
    <article className={`redundancy-node ${display.tone}`}>
      <header className="redundancy-node-heading">
        <span className={`redundancy-node-mark ${node.online ? "online" : "offline"}`}><Server size={20} /></span>
        <div className="redundancy-node-name">
          <strong>{node.name}</strong>
          <span>{nodeRoleLabel(node)}{node.lan_address ? ` · ${node.lan_address}` : ""}</span>
        </div>
        {nodeAction(node, onRemoveRequest)}
      </header>
      <div className="redundancy-node-summary">
        <div className="redundancy-node-state">
          <span className={`redundancy-node-state-icon ${display.tone}`}>{nodeStateIcon(display.tone)}</span>
          <span><small>Status</small><strong>{display.label}</strong><em>{display.detail}</em></span>
        </div>
        <div className="redundancy-node-revision">
          <span>DNS settings</span>
          <strong>{display.settingsLabel}</strong>
          <small>{display.settingsDetail}</small>
        </div>
      </div>
      {node.last_error && <div className="redundancy-node-error">{friendlyNodeError(node)}</div>}
    </article>
  );
}

function nodeRoleLabel(node: RedundancyNode) {
  return node.role === "controller" ? "Primary server" : "Backup server";
}

function nodeAction(node: RedundancyNode, onRemoveRequest: (node: RedundancyNode) => void): ReactNode {
  if (node.role === "replica") {
    return <button type="button" className="icon-button danger-icon" aria-label={`Remove ${node.name}`} title={`Remove ${node.name}`} onClick={() => onRemoveRequest(node)}><Trash2 size={16} /></button>;
  }
  return <span className="redundancy-controller-badge"><Check size={13} />Primary</span>;
}

function nodeStateIcon(tone: NodeDisplayState["tone"]): ReactNode {
  if (tone === "healthy") return <CheckCircle2 size={17} />;
  if (tone === "syncing") return <RefreshCw className="spinning" size={17} />;
  return <AlertTriangle size={17} />;
}

type RouterAddressesProps = Readonly<{
  addresses: string[];
}>;

function RouterAddresses({ addresses }: RouterAddressesProps) {
  if (!addresses.length) return <div className="redundancy-addresses"><span>Add the LAN address for this Faro server under DNS & interface.</span></div>;
  return <div className="redundancy-addresses">{addresses.map((address, index) => <code key={address}>{addressLabel(index)}: {address}</code>)}</div>;
}

function addressLabel(index: number) {
  return index === 0 ? "Primary" : `DNS ${index + 1}`;
}

function ReplicaStartGuide() {
  return (
    <section className="panel redundancy-start-guide">
      <div><span>1</span><p><strong>Deploy the same Faro container</strong> on another physical host or VM.</p></div>
      <div><span>2</span><p><strong>Open the new installation</strong> and choose “Join an existing Faro home.”</p></div>
      <div><span>3</span><p><strong>Paste the temporary pairing code</strong> and add both DNS addresses to your router.</p></div>
    </section>
  );
}

type RedundancyDialogsProps = Readonly<{
  pendingRemoval: RedundancyNode | null;
  disableOpen: boolean;
  busy: boolean;
  onCancelRemoval: () => void;
  onConfirmRemoval: () => void;
  onCancelDisable: () => void;
  onConfirmDisable: () => void;
}>;

function RedundancyDialogs({ pendingRemoval, disableOpen, busy, onCancelRemoval, onConfirmRemoval, onCancelDisable, onConfirmDisable }: RedundancyDialogsProps) {
  return (
    <>
      {pendingRemoval && <ConfirmDialog title={`Remove ${pendingRemoval.name}?`} body="This server will no longer receive configuration updates. DNS may continue using its last accepted configuration until the container is reset or reconfigured." confirmLabel="Remove server" busyLabel="Removing…" busy={busy} onCancel={onCancelRemoval} onConfirm={onConfirmRemoval} detail={<div className="confirm-dialog-impact warning"><AlertTriangle size={18} /><span><strong>Remove it from your router too</strong><small>Otherwise some devices may continue sending DNS requests to the disconnected server.</small></span></div>} />}
      {disableOpen && <ConfirmDialog title="Turn off DNS redundancy?" body="This Faro server will become standalone and all paired servers will stop receiving configuration updates." confirmLabel="Turn off redundancy" busyLabel="Turning off…" busy={busy} onCancel={onCancelDisable} onConfirm={onConfirmDisable} detail={<div className="confirm-dialog-impact warning"><AlertTriangle size={18} /><span><strong>Update every additional server and your router</strong><small>Open each backup Faro server and choose “Leave Faro home,” then remove its DNS address from DHCP.</small></span></div>} />}
    </>
  );
}

function formatTime(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
}

function formatRelative(value: string) {
  const age = Date.now() - new Date(value).getTime();
  if (!Number.isFinite(age) || age < 0) return "just now";
  if (age < 60_000) return "just now";
  if (age < 3_600_000) return `${Math.floor(age / 60_000)}m ago`;
  return `${Math.floor(age / 3_600_000)}h ago`;
}

type NodeDisplayState = {
  readonly tone: "warning" | "syncing" | "healthy";
  readonly label: string;
  readonly detail: string;
  readonly settingsLabel: string;
  readonly settingsDetail: string;
};

function nodeDisplayState(node: RedundancyNode, primaryRevision: number): NodeDisplayState {
  if (!node.online) {
    return offlineNodeState(node);
  }
  if (node.last_error) {
    return failedNodeState(node);
  }
  if (node.config_revision < 1) {
    return initialNodeState(node);
  }
  if (node.config_revision !== primaryRevision) {
    return synchronizingNodeState();
  }
  return healthyNodeState(node);
}

function offlineNodeState(node: RedundancyNode): NodeDisplayState {
  const hasSafeCopy = node.config_revision > 0;
  return {
    tone: "warning",
    label: "Offline",
    detail: hasSafeCopy ? "Serving its last safe DNS settings" : "This server cannot be reached",
    settingsLabel: hasSafeCopy ? "Last safe copy" : "Waiting",
    settingsDetail: hasSafeCopy ? "Available during the outage" : "No settings received yet",
  };
}

function failedNodeState(node: RedundancyNode): NodeDisplayState {
  const hasSafeCopy = node.config_revision > 0;
  return {
    tone: "warning",
    label: "Needs attention",
    detail: "Faro will retry synchronization automatically",
    settingsLabel: hasSafeCopy ? "Last safe copy" : "Waiting",
    settingsDetail: hasSafeCopy ? "Latest update was not applied" : "No settings received yet",
  };
}

function initialNodeState(node: RedundancyNode): NodeDisplayState {
  const isController = node.role === "controller";
  return {
    tone: "syncing",
    label: isController ? "Preparing" : "Connecting",
    detail: isController ? "Getting DNS settings ready" : "Waiting for DNS settings from the primary server",
    settingsLabel: "Preparing",
    settingsDetail: isController ? "Creating the first safe copy" : "First sync has not finished",
  };
}

function synchronizingNodeState(): NodeDisplayState {
  return {
    tone: "syncing",
    label: "Synchronizing",
    detail: "Receiving the latest DNS settings",
    settingsLabel: "Updating",
    settingsDetail: "The last safe copy remains active",
  };
}

function healthyNodeState(node: RedundancyNode): NodeDisplayState {
  const isController = node.role === "controller";
  return {
    tone: "healthy",
    label: "Ready",
    detail: healthyNodeDetail(node, isController),
    settingsLabel: isController ? "Ready to share" : "Up to date",
    settingsDetail: isController ? "Source for backup servers" : "Matches the primary server",
  };
}

function healthyNodeDetail(node: RedundancyNode, isController: boolean) {
  if (isController) return "Serving as the primary Faro server";
  if (node.last_sync_at) return `Last synchronized ${formatRelative(node.last_sync_at)}`;
  return "Configuration accepted";
}

function friendlyNodeError(node: RedundancyNode) {
  const error = (node.last_error ?? "").toLowerCase();
  if (error.includes("apply") || error.includes("coredns") || error.includes("configuration")) {
    return "This server rejected the latest DNS update and is still using its last safe settings.";
  }
  return "This server could not finish synchronization and will retry automatically.";
}
