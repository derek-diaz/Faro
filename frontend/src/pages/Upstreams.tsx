import { AlertTriangle, Check, Gauge, LockKeyhole, Network, Plus, RefreshCw, RotateCcw, Save, Server, ShieldCheck, X } from "lucide-react";
import { useEffect, useLayoutEffect, useMemo, useState, type SubmitEvent } from "react";
import { api, type EncryptedUpstreamEndpoint, type Setting, type UpstreamProbe } from "../api/client";
import { allCatalogAddresses, encryptedEndpointForAddresses, encryptedEndpointIndex, findUpstreamAddress, parseUpstreamServers, upstreamProviders, type ResolverProfile } from "../data/upstreams";
import { ProviderLogo } from "../components/ProviderLogo";
import { formatLatency, latencyTone } from "../utils/formatting";

type UpstreamsProps = {
  readonly settings: Setting[];
  readonly refresh: () => Promise<void>;
};

export function Upstreams({ settings, refresh }: UpstreamsProps) {
  const configured = useMemo(() => parseUpstreamServers(settings.find((setting) => setting.key === "upstream_dns")?.value ?? ""), [settings]);
  const configuredTransport = settings.find((setting) => setting.key === "upstream_transport")?.value === "encrypted" ? "encrypted" : "standard";
  const [selected, setSelected] = useState<string[]>(configured);
  const [transport, setTransport] = useState<"encrypted" | "standard">(configuredTransport);
  const [customInput, setCustomInput] = useState("");
  const [customError, setCustomError] = useState("");
  const [saveState, setSaveState] = useState<"idle" | "saving" | "saved" | "error">("idle");
  const [message, setMessage] = useState("");
  const [probes, setProbes] = useState<Record<string, UpstreamProbe>>({});
  const [probing, setProbing] = useState(false);
  const [probeError, setProbeError] = useState("");
  const [lastChecked, setLastChecked] = useState<string | null>(null);
  const [encryptedEndpoints, setEncryptedEndpoints] = useState<EncryptedUpstreamEndpoint[]>([]);
  const [catalogLoaded, setCatalogLoaded] = useState(false);
  const [catalogError, setCatalogError] = useState("");

  useLayoutEffect(() => {
    setSelected(configured);
    setTransport(configuredTransport);
  }, [configured.join(","), configuredTransport]);

  useEffect(() => {
    let cancelled = false;
    api.upstreamCatalog()
      .then((response) => {
        if (cancelled) return;
        setEncryptedEndpoints(response.encrypted_endpoints);
        setCatalogError("");
      })
      .catch((error_) => {
        if (cancelled) return;
        setCatalogError(error_ instanceof Error ? error_.message : "Encrypted DNS support could not be loaded.");
      })
      .finally(() => {
        if (!cancelled) setCatalogLoaded(true);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const selectedSet = useMemo(() => new Set(selected), [selected]);
  const encryptedByAddress = useMemo(() => encryptedEndpointIndex(encryptedEndpoints), [encryptedEndpoints]);
  const selectedEncryptedEndpoints = useMemo(
    () => encryptedEndpoints.filter((endpoint) => endpoint.bootstrap_ips.some((address) => selectedSet.has(address))),
    [encryptedEndpoints, selectedSet]
  );
  const unsupportedEncrypted = useMemo(
    () => selected.filter((address) => catalogLoaded && !encryptedByAddress.has(address)),
    [catalogLoaded, encryptedByAddress, selected]
  );
  const probeAddresses = useMemo(
    () => transport === "encrypted" && catalogLoaded
      ? unique([...encryptedEndpoints.map((endpoint) => endpoint.bootstrap_ips[0]).filter(Boolean), ...unsupportedEncrypted])
      : unique([...allCatalogAddresses(), ...selected]),
    [catalogLoaded, encryptedEndpoints, selected, transport, unsupportedEncrypted]
  );
  const probeKey = probeAddresses.join(",");
  const dirty = selected.join(",") !== configured.join(",") || transport !== configuredTransport;
  const selectedProfiles = upstreamProviders.flatMap((provider) => provider.profiles.filter((profile) => profile.addresses.some((address) => selectedSet.has(address))));
  const mixesFiltering = selectedProfiles.some((profile) => profile.mode === "none") && selectedProfiles.some((profile) => profile.mode !== "none");
  const fastestProfileID = fastestProfile(probes);
  const selectionCount = transport === "encrypted" && catalogLoaded ? selectedEncryptedEndpoints.length + unsupportedEncrypted.length : selected.length;
  const selectionLabel = transport === "encrypted" ? pluralize("encrypted provider", selectionCount) : pluralize("DNS server", selectionCount);

  useEffect(() => {
    let cancelled = false;
    async function runProbe() {
      setProbing(true);
      setProbeError("");
      try {
        const response = await api.probeUpstreams(probeAddresses, transport);
        if (!cancelled) {
          setProbes(Object.fromEntries(response.items.map((probe) => [probe.address, probe])));
          setLastChecked(response.items[0]?.checked_at ?? new Date().toISOString());
        }
      } catch (error_) {
        if (!cancelled) setProbeError(error_ instanceof Error ? error_.message : "Latency check failed.");
      } finally {
        if (!cancelled) setProbing(false);
      }
    }
    void runProbe();
    const timer = window.setInterval(() => void runProbe(), 30000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [probeKey, transport]);

  async function refreshLatency() {
    setProbing(true);
    setProbeError("");
    try {
      const response = await api.probeUpstreams(probeAddresses, transport);
      setProbes(Object.fromEntries(response.items.map((probe) => [probe.address, probe])));
      setLastChecked(response.items[0]?.checked_at ?? new Date().toISOString());
    } catch (error_) {
      setProbeError(error_ instanceof Error ? error_.message : "Latency check failed.");
    } finally {
      setProbing(false);
    }
  }

  function toggleProfile(profile: ResolverProfile) {
    const fullySelected = profile.addresses.every((address) => selectedSet.has(address));
    setSelected((current) => fullySelected
      ? current.filter((address) => !profile.addresses.includes(address))
      : unique([...current, ...profile.addresses]));
    setSaveState("idle");
    setMessage("");
  }

  function addCustomServers(event: SubmitEvent) {
    event.preventDefault();
    const candidates = parseUpstreamServers(customInput.replace(/\s+/g, ","));
    if (candidates.length === 0 || candidates.some((server) => !isIPAddress(server))) {
      setCustomError("Enter valid IPv4 or IPv6 addresses separated by commas or spaces.");
      return;
    }
    setSelected((current) => unique([...current, ...candidates]));
    setCustomInput("");
    setCustomError("");
    setSaveState("idle");
  }

  function chooseTransport(next: "encrypted" | "standard") {
    if (next === "encrypted" && !catalogLoaded) {
      setSaveState("error");
      setMessage("Faro is still checking which providers support encrypted DNS.");
      return;
    }
    if (next === "encrypted" && catalogError) {
      setSaveState("error");
      setMessage("Encrypted DNS support is unavailable right now. Standard DNS settings are still available.");
      return;
    }
    if (next === "encrypted" && unsupportedEncrypted.length > 0) {
      setSaveState("error");
      setMessage("One or more selected resolvers do not offer a supported HTTPS endpoint. Remove them or keep Standard DNS.");
      return;
    }
    setTransport(next);
    setSaveState("idle");
    setMessage("");
  }

  async function save() {
    if (selected.length === 0) {
      setSaveState("error");
      setMessage("Select at least one upstream DNS server.");
      return;
    }
    if (transport === "encrypted" && (!catalogLoaded || catalogError)) {
      setSaveState("error");
      setMessage("Faro could not verify encrypted DNS support. Try again or use Standard DNS.");
      return;
    }
    if (transport === "encrypted" && unsupportedEncrypted.length > 0) {
      setSaveState("error");
      setMessage("Remove resolvers without a supported HTTPS endpoint before saving encrypted DNS.");
      return;
    }
    setSaveState("saving");
    setMessage("");
    try {
      await api.updateSettings({ upstream_dns: selected.join(","), upstream_transport: transport });
      await refresh();
      setSaveState("saved");
      setMessage("Upstreams saved and DNS reloaded.");
    } catch (error_) {
      setSaveState("error");
      setMessage(error_ instanceof Error ? error_.message : "Failed to update upstreams.");
    }
  }

  return (
    <div className="upstreams-layout">
      <UpstreamCatalog transport={transport} lastChecked={lastChecked} probing={probing} refreshLatency={refreshLatency} probeError={probeError} catalogError={catalogError} probes={probes} selectedSet={selectedSet} fastestProfileID={fastestProfileID} catalogLoaded={catalogLoaded} encryptedByAddress={encryptedByAddress} chooseTransport={chooseTransport} toggleProfile={toggleProfile} customInput={customInput} setCustomInput={setCustomInput} customError={customError} addCustomServers={addCustomServers} />
      <UpstreamSelectionPanel selectionCount={selectionCount} selectionLabel={selectionLabel} dirty={dirty} transport={transport} mixesFiltering={mixesFiltering} selected={selected} selectedSet={selectedSet} selectedEncryptedEndpoints={selectedEncryptedEndpoints} unsupportedEncrypted={unsupportedEncrypted} probes={probes} probing={probing} catalogLoaded={catalogLoaded} catalogError={catalogError} saveState={saveState} message={message} configured={configured} configuredTransport={configuredTransport} setSelected={setSelected} setTransport={setTransport} setMessage={setMessage} setSaveState={setSaveState} save={save} />
    </div>
  );
}

type UpstreamCatalogProps = {
  readonly transport: "encrypted" | "standard";
  readonly lastChecked: string | null;
  readonly probing: boolean;
  readonly refreshLatency: () => Promise<void>;
  readonly probeError: string;
  readonly catalogError: string;
  readonly probes: Record<string, UpstreamProbe>;
  readonly selectedSet: Set<string>;
  readonly fastestProfileID: string | null;
  readonly catalogLoaded: boolean;
  readonly encryptedByAddress: Map<string, EncryptedUpstreamEndpoint>;
  readonly chooseTransport: (transport: "encrypted" | "standard") => void;
  readonly toggleProfile: (profile: ResolverProfile) => void;
  readonly customInput: string;
  readonly setCustomInput: (value: string) => void;
  readonly customError: string;
  readonly addCustomServers: (event: SubmitEvent) => void;
};

function UpstreamCatalog(props: UpstreamCatalogProps) {
  const { transport, lastChecked, probing, refreshLatency, probeError, catalogError, probes, selectedSet, fastestProfileID, catalogLoaded, encryptedByAddress, chooseTransport, toggleProfile, customInput, setCustomInput, customError, addCustomServers } = props;
  return <div className="upstream-catalog"><section className="upstream-privacy-panel panel" aria-labelledby="upstream-privacy-title"><div className="upstream-privacy-copy"><span className="upstream-privacy-icon">{transport === "encrypted" ? <LockKeyhole size={20} /> : <Network size={20} />}</span><div><h2 id="upstream-privacy-title">Connection to DNS providers</h2><p>{privacyDescription(transport)}</p></div></div><TransportChoices transport={transport} onChoose={chooseTransport} /></section><section className="upstream-toolbar" aria-label="Resolver comparison controls"><div className="upstream-live-state"><span><Gauge size={15} /> Live latency</span><small>{lastChecked ? `Checked ${new Date(lastChecked).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}` : "Checking providers..."}</small></div><div className="upstream-toolbar-actions"><button className="secondary compact-button" type="button" onClick={() => void refreshLatency()} disabled={probing} aria-label="Refresh upstream latency"><RefreshCw className={probing ? "spinning" : ""} size={15} /><span>Refresh</span></button></div></section>{(probeError || catalogError) && <div className="upstream-probe-error"><AlertTriangle size={16} /><span>{catalogError || probeError}</span></div>}<ProviderList probes={probes} selectedSet={selectedSet} fastestProfileID={fastestProfileID} transport={transport} catalogLoaded={catalogLoaded} encryptedByAddress={encryptedByAddress} probing={probing} onToggle={toggleProfile} /><section className="panel custom-upstream-panel"><div className="panel-title dashboard-panel-title"><div><h2>Custom resolvers</h2><p>{customResolverDescription(transport)}</p></div></div><form className="custom-upstream-form" onSubmit={addCustomServers}><input disabled={transport === "encrypted"} value={customInput} onChange={(event) => setCustomInput(event.target.value)} placeholder="192.0.2.53 or 2001:db8::53" aria-label="Custom DNS server addresses" /><button type="submit" disabled={transport === "encrypted"}><Plus size={16} /><span>Add servers</span></button></form>{customError && <span className="custom-upstream-error">{customError}</span>}</section></div>;
}

function TransportChoices({ transport, onChoose }: { readonly transport: "encrypted" | "standard"; readonly onChoose: (transport: "encrypted" | "standard") => void }) {
  return <div className="upstream-transport-choices" role="radiogroup" aria-label="Connection privacy"><button type="button" role="radio" aria-checked={transport === "encrypted"} className={transport === "encrypted" ? "selected" : ""} onClick={() => onChoose("encrypted")}><LockKeyhole size={17} /><span><strong>Encrypted</strong><small>Recommended · HTTPS</small></span>{transport === "encrypted" && <Check size={15} />}</button><button type="button" role="radio" aria-checked={transport === "standard"} className={transport === "standard" ? "selected" : ""} onClick={() => onChoose("standard")}><Network size={17} /><span><strong>Standard DNS</strong><small>Maximum compatibility</small></span>{transport === "standard" && <Check size={15} />}</button></div>;
}

function ProviderList({ probes, selectedSet, fastestProfileID, transport, catalogLoaded, encryptedByAddress, probing, onToggle }: { readonly probes: Record<string, UpstreamProbe>; readonly selectedSet: Set<string>; readonly fastestProfileID: string | null; readonly transport: "encrypted" | "standard"; readonly catalogLoaded: boolean; readonly encryptedByAddress: Map<string, EncryptedUpstreamEndpoint>; readonly probing: boolean; readonly onToggle: (profile: ResolverProfile) => void }) {
  return <div className="provider-list">{upstreamProviders.map((provider) => <ProviderPanel key={provider.id} provider={provider} probes={probes} selectedSet={selectedSet} fastestProfileID={fastestProfileID} transport={transport} catalogLoaded={catalogLoaded} encryptedByAddress={encryptedByAddress} probing={probing} onToggle={onToggle} />)}</div>;
}

function ProviderPanel({ provider, probes, selectedSet, fastestProfileID, transport, catalogLoaded, encryptedByAddress, probing, onToggle }: { readonly provider: typeof upstreamProviders[number]; readonly probes: Record<string, UpstreamProbe>; readonly selectedSet: Set<string>; readonly fastestProfileID: string | null; readonly transport: "encrypted" | "standard"; readonly catalogLoaded: boolean; readonly encryptedByAddress: Map<string, EncryptedUpstreamEndpoint>; readonly probing: boolean; readonly onToggle: (profile: ResolverProfile) => void }) {
  const providerProbe = bestProbe(provider.profiles.flatMap((profile) => profile.addresses), probes);
  return <section className="provider-panel"><header className="provider-header"><div className="provider-identity"><span className="provider-logo"><ProviderLogo providerID={provider.id} providerName={provider.name} /></span><div><h2>{provider.name}</h2><p>{provider.description}</p></div></div><div className="provider-live-summary"><span>{pluralize("option", provider.profiles.length)}</span><ProbeBadge probe={providerProbe} loading={probing && !providerProbe} /></div></header><div className="resolver-profile-list">{provider.profiles.map((profile) => <ResolverProfileButton key={profile.id} profile={profile} probes={probes} selectedSet={selectedSet} fastestProfileID={fastestProfileID} transport={transport} catalogLoaded={catalogLoaded} encryptedByAddress={encryptedByAddress} probing={probing} onToggle={onToggle} />)}</div></section>;
}

function ResolverProfileButton({ profile, probes, selectedSet, fastestProfileID, transport, catalogLoaded, encryptedByAddress, probing, onToggle }: { readonly profile: ResolverProfile; readonly probes: Record<string, UpstreamProbe>; readonly selectedSet: Set<string>; readonly fastestProfileID: string | null; readonly transport: "encrypted" | "standard"; readonly catalogLoaded: boolean; readonly encryptedByAddress: Map<string, EncryptedUpstreamEndpoint>; readonly probing: boolean; readonly onToggle: (profile: ResolverProfile) => void }) {
  const selectedCount = profile.addresses.filter((address) => selectedSet.has(address)).length;
  const fullySelected = selectedCount === profile.addresses.length;
  const partiallySelected = selectedCount > 0 && !fullySelected;
  const profileProbe = bestProbe(profile.addresses, probes);
  const encryptedEndpoint = encryptedEndpointForAddresses(profile.addresses, encryptedByAddress);
  const encryptedUnavailable = transport === "encrypted" && catalogLoaded && !encryptedEndpoint;
  const endpointText = encryptedEndpoint?.url ?? (catalogLoaded ? "Not offered by Faro" : "Checking support…");
  return <button className={`resolver-profile ${fullySelected ? "selected" : ""} ${partiallySelected ? "partial" : ""} ${encryptedUnavailable ? "unavailable" : ""}`} type="button" onClick={() => onToggle(profile)} aria-pressed={fullySelected} disabled={encryptedUnavailable}>{(fullySelected || partiallySelected) && <span className="profile-check" aria-hidden="true">{fullySelected ? <Check size={16} /> : selectedCount}</span>}<span className="profile-copy"><span className="profile-title-row"><strong>{profile.name}</strong><span className="profile-title-badges">{profile.id === fastestProfileID && <em className="fastest">Fastest</em>}{profile.recommended && <em>Recommended</em>}{transport === "encrypted" && encryptedEndpoint && <em className="encrypted"><LockKeyhole size={11} /> HTTPS available</em>}{transport === "encrypted" && !catalogLoaded && <em>Checking HTTPS</em>}{encryptedUnavailable && <em className="standard-only">Standard only</em>}</span></span><span>{profile.description}</span><span className="profile-latency"><Gauge size={14} /><ProbeText probe={profileProbe} loading={probing && !profileProbe} /></span><ProfileEndpoint profile={profile} transport={transport} endpoint={encryptedEndpoint} endpointText={endpointText} probes={probes} probing={probing} /></span><span className="profile-badges">{profile.badges.map((badge) => <span key={badge}>{badge}</span>)}</span></button>;
}

function ProfileEndpoint({ profile, transport, endpoint, endpointText, probes, probing }: { readonly profile: ResolverProfile; readonly transport: "encrypted" | "standard"; readonly endpoint: EncryptedUpstreamEndpoint | null; readonly endpointText: string; readonly probes: Record<string, UpstreamProbe>; readonly probing: boolean }) {
  if (transport === "encrypted") return <span className="profile-doh-endpoint"><small>HTTPS endpoint</small><code title={endpoint?.url}>{endpointText}</code></span>;
  return <span className="profile-addresses">{profile.addresses.map((address) => <span key={address}><code>{address}</code><ProbeText probe={probes[address]} compact loading={probing && !probes[address]} /></span>)}</span>;
}

function UpstreamSelectionPanel({ selectionCount, selectionLabel, dirty, transport, mixesFiltering, selected, selectedSet, selectedEncryptedEndpoints, unsupportedEncrypted, probes, probing, catalogLoaded, catalogError, saveState, message, configured, configuredTransport, setSelected, setTransport, setMessage, setSaveState, save }: { readonly selectionCount: number; readonly selectionLabel: string; readonly dirty: boolean; readonly transport: "encrypted" | "standard"; readonly mixesFiltering: boolean; readonly selected: string[]; readonly selectedSet: Set<string>; readonly selectedEncryptedEndpoints: EncryptedUpstreamEndpoint[]; readonly unsupportedEncrypted: string[]; readonly probes: Record<string, UpstreamProbe>; readonly probing: boolean; readonly catalogLoaded: boolean; readonly catalogError: string; readonly saveState: "idle" | "saving" | "saved" | "error"; readonly message: string; readonly configured: string[]; readonly configuredTransport: "encrypted" | "standard"; readonly setSelected: (value: string[]) => void; readonly setTransport: (value: "encrypted" | "standard") => void; readonly setMessage: (value: string) => void; readonly setSaveState: (value: "idle" | "saving" | "saved" | "error") => void; readonly save: () => Promise<void> }) {
  return <aside className="upstream-selection-panel panel"><div className="selection-heading"><div><span>Current selection</span><strong>{selectionCount} {selectionLabel}</strong></div><span className={`selection-status ${dirty ? "pending" : ""}`}>{dirty ? <AlertTriangle size={15} /> : <ShieldCheck size={15} />} {dirty ? "Unsaved" : "Active"}</span></div><SelectionPrivacyState transport={transport} />{mixesFiltering && <div className="selection-policy-note"><AlertTriangle size={16} /><div><strong>Mixed filtering</strong><span>Filtered and unfiltered resolvers are selected, so blocking results can vary.</span></div></div>}<SelectedServerList selected={selected} selectedSet={selectedSet} transport={transport} selectedEncryptedEndpoints={selectedEncryptedEndpoints} unsupportedEncrypted={unsupportedEncrypted} probes={probes} probing={probing} catalogLoaded={catalogLoaded} setSelected={setSelected} /><div className="selection-note"><strong>{transport === "encrypted" ? "Private by design" : "How latency is measured"}</strong><span>{selectionNote(transport)}</span></div><div className="selection-actions"><button type="button" className="secondary" disabled={!dirty} onClick={() => { setSelected(configured); setTransport(configuredTransport); setMessage(""); setSaveState("idle"); }}><RotateCcw size={16} /><span>Reset</span></button><button type="button" disabled={!dirty || saveState === "saving" || selected.length === 0 || (transport === "encrypted" && (!catalogLoaded || Boolean(catalogError) || unsupportedEncrypted.length > 0))} onClick={() => void save()}><Save size={16} /><span>{saveState === "saving" ? "Saving" : "Save upstreams"}</span></button></div>{message && <span className={`selection-message ${saveState === "error" ? "error" : ""}`}>{message}</span>}</aside>;
}

function SelectionPrivacyState({ transport }: { readonly transport: "encrypted" | "standard" }) {
  return <div className={`selection-privacy-state ${transport}`}>{transport === "encrypted" ? <LockKeyhole size={17} /> : <Network size={17} />}<div><strong>{transport === "encrypted" ? "Encrypted connection" : "Standard connection"}</strong><span>{transport === "encrypted" ? "DNS over HTTPS · no plaintext fallback" : "Traditional DNS on port 53"}</span></div></div>;
}

function SelectedServerList({ selected, selectedSet, transport, selectedEncryptedEndpoints, unsupportedEncrypted, probes, probing, catalogLoaded, setSelected }: { readonly selected: string[]; readonly selectedSet: Set<string>; readonly transport: "encrypted" | "standard"; readonly selectedEncryptedEndpoints: EncryptedUpstreamEndpoint[]; readonly unsupportedEncrypted: string[]; readonly probes: Record<string, UpstreamProbe>; readonly probing: boolean; readonly catalogLoaded: boolean; readonly setSelected: (value: string[]) => void }) {
  if (selected.length === 0) return <div className="selected-server-list"><div className="selection-empty">Choose a provider profile or add a custom resolver.</div></div>;
  if (transport === "encrypted") return <div className="selected-server-list">{!catalogLoaded && <div className="selection-empty">Checking encrypted provider support…</div>}{selectedEncryptedEndpoints.map((endpoint) => <SelectedEncryptedServer key={endpoint.url} endpoint={endpoint} selected={selected} selectedSet={selectedSet} probes={probes} probing={probing} setSelected={setSelected} />)}{unsupportedEncrypted.map((server) => <SelectedUnsupportedServer key={server} server={server} selected={selected} probe={probes[server]} probing={probing} setSelected={setSelected} />)}</div>;
  return <div className="selected-server-list">{selected.map((server) => <SelectedStandardServer key={server} server={server} selected={selected} probe={probes[server]} probing={probing} setSelected={setSelected} />)}</div>;
}

function SelectedEncryptedServer({ endpoint, selected, selectedSet, probes, probing, setSelected }: { readonly endpoint: EncryptedUpstreamEndpoint; readonly selected: string[]; readonly selectedSet: Set<string>; readonly probes: Record<string, UpstreamProbe>; readonly probing: boolean; readonly setSelected: (value: string[]) => void }) {
  const selectedAddresses = endpoint.bootstrap_ips.filter((address) => selectedSet.has(address));
  const match = selectedAddresses.map(findUpstreamAddress).find(Boolean);
  const probe = bestProbe(endpoint.bootstrap_ips, probes);
  return <div className="selected-server encrypted-endpoint"><span className="selected-provider-logo">{match ? <ProviderLogo providerID={match.provider.id} providerName={match.provider.name} /> : <LockKeyhole size={15} />}</span><div><strong>{match ? `${match.provider.name} · ${match.profile.name}` : endpoint.name}</strong><code title={endpoint.url}>{endpoint.url}</code><span>{pluralize("provider address", selectedAddresses.length)} available</span></div><ProbeBadge probe={probe} loading={probing && !probe} compact /><button className="icon-button" type="button" onClick={() => setSelected(selected.filter((address) => !endpoint.bootstrap_ips.includes(address)))} aria-label={`Remove ${endpoint.name}`}><X size={15} /></button></div>;
}

function SelectedUnsupportedServer({ server, selected, probe, probing, setSelected }: { readonly server: string; readonly selected: string[]; readonly probe?: UpstreamProbe; readonly probing: boolean; readonly setSelected: (value: string[]) => void }) {
  const match = findUpstreamAddress(server);
  return <div className="selected-server unsupported-endpoint"><span className="selected-provider-logo">{match ? <ProviderLogo providerID={match.provider.id} providerName={match.provider.name} /> : <Server size={15} />}</span><div><strong>{server}</strong><span>No supported HTTPS endpoint</span></div><ProbeBadge probe={probe} loading={probing && !probe} compact /><button className="icon-button" type="button" onClick={() => setSelected(selected.filter((address) => address !== server))} aria-label={`Remove ${server}`}><X size={15} /></button></div>;
}

function SelectedStandardServer({ server, selected, probe, probing, setSelected }: { readonly server: string; readonly selected: string[]; readonly probe?: UpstreamProbe; readonly probing: boolean; readonly setSelected: (value: string[]) => void }) {
  const match = findUpstreamAddress(server);
  return <div className="selected-server"><span className="selected-provider-logo">{match ? <ProviderLogo providerID={match.provider.id} providerName={match.provider.name} /> : <Server size={15} />}</span><div><strong>{server}</strong><span>{match ? `${match.provider.name} · ${match.profile.name}` : "Custom resolver"}</span></div><ProbeBadge probe={probe} loading={probing && !probe} compact /><button className="icon-button" type="button" onClick={() => setSelected(selected.filter((address) => address !== server))} aria-label={`Remove ${server}`}><X size={15} /></button></div>;
}

function privacyDescription(transport: "encrypted" | "standard") {
  return transport === "encrypted" ? "Faro keeps requests private between your home and the selected providers." : "Faro uses regular DNS for compatibility with custom or restricted networks.";
}

function customResolverDescription(transport: "encrypted" | "standard") {
  return transport === "encrypted" ? "Custom IP resolvers require Standard DNS. Faro will never send to them unencrypted without your choice." : "Add plain DNS servers that are not in the provider catalog.";
}

function selectionNote(transport: "encrypted" | "standard") {
  return transport === "encrypted" ? "If one encrypted provider is unavailable, Faro tries another selected encrypted provider. It never silently falls back to plaintext." : "Response time is measured from the Faro host. Your devices may see slightly different results.";
}

function unique(values: string[]) {
  return Array.from(new Set(values));
}

function pluralize(label: string, count: number) {
  return `${label}${count === 1 ? "" : "s"}`;
}

function isIPAddress(value: string) {
  if (value.includes(":")) {
    try {
      new URL(`https://[${value}]/`);
      return true;
    } catch {
      return false;
    }
  }
  const parts = value.split(".");
  return parts.length === 4 && parts.every((part) => /^\d{1,3}$/.test(part) && Number(part) >= 0 && Number(part) <= 255);
}

function bestProbe(addresses: string[], probes: Record<string, UpstreamProbe>) {
  return addresses
    .map((address) => probes[address])
    .filter((probe): probe is UpstreamProbe => probe?.status === "online" && probe.latency_ms !== null)
    .sort((left, right) => (left.latency_ms ?? Infinity) - (right.latency_ms ?? Infinity))[0];
}

function fastestProfile(probes: Record<string, UpstreamProbe>) {
  let fastestID: string | null = null;
  let fastestLatency = Infinity;
  for (const provider of upstreamProviders) {
    for (const profile of provider.profiles) {
      const probe = bestProbe(profile.addresses, probes);
      if (probe?.latency_ms !== null && probe?.latency_ms !== undefined && probe.latency_ms < fastestLatency) {
        fastestID = profile.id;
        fastestLatency = probe.latency_ms;
      }
    }
  }
  return fastestID;
}

function ProbeText({ probe, loading = false, compact = false }: { readonly probe?: UpstreamProbe; readonly loading?: boolean; readonly compact?: boolean }) {
  const state = probeState(probe, loading);
  return <span className={`probe-text ${state.tone} ${compact ? "compact" : ""}`}>{state.label}</span>;
}

function ProbeBadge({ probe, loading = false, compact = false }: { readonly probe?: UpstreamProbe; readonly loading?: boolean; readonly compact?: boolean }) {
  const state = probeState(probe, loading);
  return <span className={`probe-badge ${state.tone} ${compact ? "compact" : ""}`}><span />{state.label}</span>;
}

function probeState(probe?: UpstreamProbe, loading = false) {
  if (!probe) return { label: loading ? "Testing" : "Not tested", tone: "pending" };
  if (probe.status === "online" && probe.latency_ms !== null) return { label: `${formatLatency(probe.latency_ms)} ms`, tone: latencyTone(probe.latency_ms) };
  return { label: "Unavailable", tone: "offline" };
}
