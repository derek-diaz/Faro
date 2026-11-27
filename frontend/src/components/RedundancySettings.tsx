import { Button } from "./ui/button";
import { AlertTriangle, Check, CheckCircle2, Clock3, Copy, Network, Plus, RefreshCw, Server, ShieldCheck, Trash2 } from "lucide-react";
import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { api, type PairingCode, type RedundancyNode, type RedundancyStatus } from "../api/client";
import { copyText } from "../utils/clipboard";
import { ConfirmDialog } from "./ConfirmDialog";

type CopyState = "idle" | "copying" | "copied" | "error";

export function RedundancySettings() {
  const [status, setStatus] = useState<RedundancyStatus | null>(null);
  const [pairing, setPairing] = useState<PairingCode | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [statusError, setStatusError] = useState("");
  const [copyState, setCopyState] = useState<CopyState>("idle");
  const copyResetTimer = useRef<number | undefined>(undefined);
  const [pendingRemoval, setPendingRemoval] = useState<RedundancyNode | null>(null);
  const [disableOpen, setDisableOpen] = useState(false);

  const statusRequest = useRef(0);
  const load = useCallback(async () => {
    const request = ++statusRequest.current;
    try {
      const next = await api.redundancyStatus();
      if (request !== statusRequest.current) return;
      setStatus(next);
      setStatusError("");
    } catch (error_) {
      if (request === statusRequest.current) setStatusError(error_ instanceof Error ? error_.message : "Could not load redundancy status.");
    }
  }, []);

  useEffect(() => {
    let active = true;
    let timer: number;
    const refresh = async () => {
      await load();
      if (active) timer = window.setTimeout(refresh, 5000);
    };
    void refresh();
    return () => { active = false; statusRequest.current++; window.clearTimeout(timer); };
  }, [load]);

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

  if (!status) return <section className="panel redundancy-loading" role={statusError ? "alert" : "status"}>{statusError ? <><AlertTriangle size={20} /><span>{statusError}</span><Button variant="outline" onClick={() => void load()}>Try again</Button></> : <><RefreshCw className="spinning" size={20} /><span>Checking Faro servers…</span></>}</section>;
  const replicas = status.nodes.filter((node) => node.role === "replica");
  const addresses = status.nodes.map((node) => node.lan_address).filter((address): address is string => Boolean(address));

  return (
    <div className="redundancy-settings">
      <RedundancyHero status={status} replicaCount={replicas.length} busy={busy} onStartPairing={() => void startPairing()} />

      {(error || statusError) && <div className="settings-feedback error" role="alert"><AlertTriangle size={16} /><span>{error || statusError}</span></div>}

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
        <h2 aria-live="polite">{heroHeading(status, hasReplicas)}</h2>
        <p>{heroDescription(status, hasReplicas)}</p>
      </div>
      <Button variant="default" type="button" onClick={onStartPairing} disabled={busy}><Plus size={17} />{status.role === "controller" ? "Add another server" : "Set up redundancy"}</Button>
    </section>
  );
}

function heroTone(status: RedundancyStatus, hasReplicas: boolean) {
  if (!hasReplicas) return "";
  return status.healthy ? "healthy" : "warning";
}

function heroHeading(status: RedundancyStatus, hasReplicas: boolean) {
  if (!hasReplicas) return "DNS redundancy";
  const behind = status.nodes.filter(node => node.revisions_behind > 0);
  if (behind.length === 1) return `${behind[0].name} is behind`;
  if (behind.length > 1) return `${behind.length} backup servers are behind`;
  if (status.nodes.some(node => !node.online)) return "Missing a backup server check-in";
  return status.healthy ? "All DNS servers are in sync" : "Check server health";
}

function heroDescription(status: RedundancyStatus, hasReplicas: boolean) {
  if (!hasReplicas) return "Keep DNS available during an outage. Pair another Faro server on a different computer, NAS, or VM host.";
  if (status.healthy) return "All servers have confirmed the current settings and reported DNS metrics.";
  if (status.nodes.some(node => node.revisions_behind > 0)) return `The primary is on revision ${status.config_revision}. Devices using a backup may still get older rules until it catches up.`;
  return "Review each server’s last check-in, DNS settings, and metrics below.";
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
        <Button variant="outline" type="button" className={`secondary redundancy-copy-button ${copyState}`} disabled={copyState === "copying"} onClick={onCopy} aria-live="polite">
          {copyStateIcon(copyState)}
          <span>{copyStateLabel(copyState)}</span>
        </Button>
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
        <div className="panel-title with-actions"><div><h2>Faro servers</h2><p>Backup servers keep serving their last safe DNS settings if this primary server is offline.</p></div><Button variant="outline" type="button" className="secondary" onClick={onRefresh}><RefreshCw size={15} />Refresh</Button></div>
        <RedundancyNodeList nodes={status.nodes} primaryRevision={status.config_revision} onRemoveRequest={onRemoveRequest} />
      </section>

      <section className="panel redundancy-router">
        <div><Network size={20} /><span><strong>Router DNS addresses</strong><small>Distribute every address below through DHCP. Clients may use any of them.</small></span></div>
        <RouterAddresses addresses={addresses} />
      </section>

      <section className="panel redundancy-disable">
        <div><strong>Stop using redundancy</strong><small>Return this installation to a standalone Faro server.</small></div>
        <Button variant="outline" type="button" className="secondary danger-outline" onClick={onDisableRequest}>Turn off redundancy</Button>
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
  const isPrimary = node.role === "controller";
  const behind = node.revisions_behind;
  const display = nodeDisplayState(node);
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
          <span><small>Connection</small><strong>{display.label}</strong><em>{isPrimary ? "This Faro server" : node.last_seen_at ? `Last check-in ${formatRelative(node.last_seen_at)}` : "Waiting for a check-in"}</em></span>
        </div>
        <div className="redundancy-node-revision" data-behind={behind > 0}>
          <span>DNS settings</span>
          <strong>{settingsLabel(node, primaryRevision)}</strong>
          <small>{isPrimary ? `Revision ${primaryRevision}` : node.config_revision === 0 ? `Waiting for the first copy of revision ${primaryRevision}` : `Revision ${node.config_revision} of ${primaryRevision}`}</small>
          {behind > 0 && <small>Behind for {formatDuration(node.behind_seconds)}</small>}
          {!isPrimary && node.last_sync_at && <small>Last confirmed {formatRelative(node.last_sync_at)}</small>}
        </div>
      </div>
      {node.last_error && <details className="redundancy-node-error"><summary>Sync failed. Faro will retry automatically.</summary><p>{node.last_error}</p></details>}
      <NodeDNSMetrics node={node} />
    </article>
  );
}

function nodeRoleLabel(node: RedundancyNode) {
  return node.role === "controller" ? "Primary server" : "Backup server";
}

function nodeAction(node: RedundancyNode, onRemoveRequest: (node: RedundancyNode) => void): ReactNode {
  if (node.role === "replica") {
    return <Button variant="destructive" size="icon" type="button" className="icon-button danger-icon" aria-label={`Remove ${node.name}`} title={`Remove ${node.name}`} onClick={() => onRemoveRequest(node)}><Trash2 size={16} /></Button>;
  }
  return <span className="redundancy-controller-badge"><Check size={13} />Primary</span>;
}

function nodeStateIcon(tone: "healthy" | "warning"): ReactNode {
  if (tone === "healthy") return <CheckCircle2 size={17} />;
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
  if (age < 5000) return "just now";
  if (age < 60_000) return `${Math.floor(age / 1000)}s ago`;
  if (age < 3_600_000) return `${Math.floor(age / 60_000)}m ago`;
  return `${Math.floor(age / 3_600_000)}h ago`;
}

function formatDuration(seconds: number) {
  if (seconds < 1) return "less than a second";
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  return `${Math.floor(seconds / 3600)}h ${Math.floor(seconds % 3600 / 60)}m`;
}

function nodeDisplayState(node: RedundancyNode) {
  return node.online
    ? { tone: "healthy", label: "Online" } as const
    : { tone: "warning", label: "No recent contact" } as const;
}

function settingsLabel(node: RedundancyNode, primaryRevision: number) {
  if (node.role === "controller") return "Primary copy";
  if (node.revisions_behind > 0) return `${node.revisions_behind} ${node.revisions_behind === 1 ? "update" : "updates"} behind`;
  if (node.config_revision > primaryRevision) return "Revision mismatch";
  return node.online ? "Up to date" : "Matched at last check-in";
}

function NodeDNSMetrics({ node }: { node: RedundancyNode }) {
  const report = node.dns;
  const totals = report?.totals;
  let message = "Waiting for a DNS report.";
  if (report) {
    const when = formatRelative(report.checked_at);
    if (node.dns_status === "stale") message = `Report out of date · Last checked ${when}`;
    else if (!totals) message = `CoreDNS metrics unavailable · Checked ${when}`;
    else message = `Checked ${when}`;
  }
  return <section className="redundancy-dns-metrics" aria-label={`${node.name} DNS metrics`}>
    <header><strong>DNS activity</strong><span data-warning={node.dns_status !== "available"}>{message}</span></header>
    {totals && <>
      <dl>
        <div><dt>Recent requests/s</dt><dd>{totals.requests_per_second == null ? "—" : totals.requests_per_second.toLocaleString(undefined, { maximumFractionDigits: 1 })}</dd></div>
        <div><dt>Requests</dt><dd>{totals.requests.toLocaleString()}</dd></div>
        <div><dt>Server failures</dt><dd>{totals.server_failures.toLocaleString()}</dd></div>
        <div><dt>Average response</dt><dd>{totals.mean_response_ms == null ? "—" : `${totals.mean_response_ms.toLocaleString(undefined, { maximumFractionDigits: 2 })} ms`}</dd></div>
      </dl>
      <small>Rate since the last sample. Totals and average since CoreDNS started.</small>
    </>}
    {!report && node.role === "replica" && <small>Older Faro versions report sync status only.</small>}
  </section>;
}
