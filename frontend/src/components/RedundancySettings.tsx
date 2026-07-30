import { AlertTriangle, Check, CheckCircle2, Clock3, Copy, Network, Plus, RefreshCw, Server, ShieldCheck, Trash2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
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
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Could not load redundancy status.");
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
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Could not create a pairing code.");
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
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Could not remove the replica.");
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
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Could not turn off redundancy.");
      setDisableOpen(false);
      setBusy(false);
    }
  }

  if (!status) return <section className="panel redundancy-loading"><RefreshCw className="spinning" size={20} /><span>Checking Faro servers…</span></section>;
  const replicas = status.nodes.filter((node) => node.role === "replica");
  const addresses = status.nodes.map((node) => node.lan_address).filter(Boolean);

  return (
    <div className="redundancy-settings">
      <section className="panel redundancy-hero">
        <span className={`redundancy-hero-icon ${status.role === "controller" && replicas.length ? status.healthy ? "healthy" : "warning" : ""}`}><Network size={25} /></span>
        <div>
          <span>DNS redundancy</span>
          <h2>{status.role === "controller" && replicas.length ? `Protected by ${status.nodes.length} Faro servers` : "Keep DNS online when this server is unavailable"}</h2>
          <p>{status.role === "controller" && replicas.length
            ? status.healthy ? "Every server is online and running the same validated DNS configuration." : "At least one server needs attention or has not accepted the latest configuration."
            : "Pair another Faro container running on a different computer, NAS, or VM host."}</p>
        </div>
        <button type="button" onClick={() => void startPairing()} disabled={busy}><Plus size={17} />{status.role === "controller" ? "Add another server" : "Set up redundancy"}</button>
      </section>

      {error && <div className="settings-feedback error"><AlertTriangle size={16} /><span>{error}</span></div>}

      {pairing && (
        <section className="panel redundancy-pairing">
          <div className="panel-title"><div><h2>Pair an additional Faro server</h2><p>On the new installation, choose “Join an existing Faro home” and paste this code.</p></div><span><Clock3 size={14} />Expires {formatTime(pairing.expires_at)}</span></div>
          <div className="redundancy-pairing-code">
            <code>{pairing.code}</code>
            <button
              type="button"
              className={`secondary redundancy-copy-button ${copyState}`}
              disabled={copyState === "copying"}
              onClick={() => void copyPairingCode()}
              aria-live="polite"
            >
              {copyState === "copied" ? <Check size={16} /> : copyState === "error" ? <AlertTriangle size={16} /> : copyState === "copying" ? <RefreshCw className="spinning" size={16} /> : <Copy size={16} />}
              <span>{copyState === "copied" ? "Copied!" : copyState === "error" ? "Copy failed" : copyState === "copying" ? "Copying…" : "Copy code"}</span>
            </button>
          </div>
          <div className="redundancy-pairing-note"><ShieldCheck size={17} /><span>The code is used to establish a unique encrypted connection. It is never stored on either server after pairing.</span></div>
        </section>
      )}

      {status.role === "controller" ? (
        <>
          <section className="panel redundancy-nodes-panel">
            <div className="panel-title with-actions"><div><h2>Faro servers</h2><p>Backup servers keep serving their last safe DNS settings if this primary server is offline.</p></div><button type="button" className="secondary" onClick={() => void load()}><RefreshCw size={15} />Refresh</button></div>
            <div className="redundancy-node-list">
              {status.nodes.map((node) => {
                const display = nodeDisplayState(node, status.config_revision);
                return (
                  <article className={`redundancy-node ${display.tone}`} key={node.node_id}>
                    <header className="redundancy-node-heading">
                      <span className={`redundancy-node-mark ${node.online ? "online" : "offline"}`}><Server size={20} /></span>
                      <div className="redundancy-node-name">
                        <strong>{node.name}</strong>
                        <span>{node.role === "controller" ? "Primary server" : "Backup server"}{node.lan_address ? ` · ${node.lan_address}` : ""}</span>
                      </div>
                      {node.role === "replica"
                        ? <button type="button" className="icon-button danger-icon" aria-label={`Remove ${node.name}`} title={`Remove ${node.name}`} onClick={() => setPendingRemoval(node)}><Trash2 size={16} /></button>
                        : <span className="redundancy-controller-badge"><Check size={13} />Primary</span>}
                    </header>
                    <div className="redundancy-node-summary">
                      <div className="redundancy-node-state">
                        <span className={`redundancy-node-state-icon ${display.tone}`}>
                          {display.tone === "healthy" ? <CheckCircle2 size={17} /> : display.tone === "syncing" ? <RefreshCw className="spinning" size={17} /> : <AlertTriangle size={17} />}
                        </span>
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
              })}
            </div>
          </section>

          <section className="panel redundancy-router">
            <div><Network size={20} /><span><strong>Router DNS addresses</strong><small>Distribute every address below through DHCP. Clients may use any of them.</small></span></div>
            <div className="redundancy-addresses">{addresses.length ? addresses.map((address, index) => <code key={address}>{index === 0 ? "Primary" : `DNS ${index + 1}`}: {address}</code>) : <span>Add the LAN address for this Faro server under DNS & interface.</span>}</div>
          </section>

          <section className="panel redundancy-disable">
            <div><strong>Stop using redundancy</strong><small>Return this installation to a standalone Faro server.</small></div>
            <button type="button" className="secondary danger-outline" onClick={() => setDisableOpen(true)}>Turn off redundancy</button>
          </section>
        </>
      ) : (
        <section className="panel redundancy-start-guide">
          <div><span>1</span><p><strong>Deploy the same Faro container</strong> on another physical host or VM.</p></div>
          <div><span>2</span><p><strong>Open the new installation</strong> and choose “Join an existing Faro home.”</p></div>
          <div><span>3</span><p><strong>Paste the temporary pairing code</strong> and add both DNS addresses to your router.</p></div>
        </section>
      )}

      {pendingRemoval && <ConfirmDialog title={`Remove ${pendingRemoval.name}?`} body="This server will no longer receive configuration updates. DNS may continue using its last accepted configuration until the container is reset or reconfigured." confirmLabel="Remove server" busyLabel="Removing…" busy={busy} onCancel={() => setPendingRemoval(null)} onConfirm={() => void removeNode()} detail={<div className="confirm-dialog-impact warning"><AlertTriangle size={18} /><span><strong>Remove it from your router too</strong><small>Otherwise some devices may continue sending DNS requests to the disconnected server.</small></span></div>} />}
      {disableOpen && <ConfirmDialog title="Turn off DNS redundancy?" body="This Faro server will become standalone and all paired servers will stop receiving configuration updates." confirmLabel="Turn off redundancy" busyLabel="Turning off…" busy={busy} onCancel={() => setDisableOpen(false)} onConfirm={() => void disableRedundancy()} detail={<div className="confirm-dialog-impact warning"><AlertTriangle size={18} /><span><strong>Update every additional server and your router</strong><small>Open each backup Faro server and choose “Leave Faro home,” then remove its DNS address from DHCP.</small></span></div>} />}
    </div>
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

function nodeDisplayState(node: RedundancyNode, primaryRevision: number) {
  if (!node.online) {
    return {
      tone: "warning",
      label: "Offline",
      detail: node.config_revision > 0 ? "Serving its last safe DNS settings" : "This server cannot be reached",
      settingsLabel: node.config_revision > 0 ? "Last safe copy" : "Waiting",
      settingsDetail: node.config_revision > 0 ? "Available during the outage" : "No settings received yet",
    } as const;
  }
  if (node.last_error) {
    return {
      tone: "warning",
      label: "Needs attention",
      detail: "Faro will retry synchronization automatically",
      settingsLabel: node.config_revision > 0 ? "Last safe copy" : "Waiting",
      settingsDetail: node.config_revision > 0 ? "Latest update was not applied" : "No settings received yet",
    } as const;
  }
  if (node.config_revision < 1) {
    return {
      tone: "syncing",
      label: node.role === "controller" ? "Preparing" : "Connecting",
      detail: node.role === "controller" ? "Getting DNS settings ready" : "Waiting for DNS settings from the primary server",
      settingsLabel: "Preparing",
      settingsDetail: node.role === "controller" ? "Creating the first safe copy" : "First sync has not finished",
    } as const;
  }
  if (node.config_revision !== primaryRevision) {
    return {
      tone: "syncing",
      label: "Synchronizing",
      detail: "Receiving the latest DNS settings",
      settingsLabel: "Updating",
      settingsDetail: "The last safe copy remains active",
    } as const;
  }
  return {
    tone: "healthy",
    label: "Ready",
    detail: node.role === "controller" ? "Serving as the primary Faro server" : node.last_sync_at ? `Last synchronized ${formatRelative(node.last_sync_at)}` : "Configuration accepted",
    settingsLabel: node.role === "controller" ? "Ready to share" : "Up to date",
    settingsDetail: node.role === "controller" ? "Source for backup servers" : "Matches the primary server",
  } as const;
}

function friendlyNodeError(node: RedundancyNode) {
  const error = (node.last_error ?? "").toLowerCase();
  if (error.includes("apply") || error.includes("coredns") || error.includes("configuration")) {
    return "This server rejected the latest DNS update and is still using its last safe settings.";
  }
  return "This server could not finish synchronization and will retry automatically.";
}
