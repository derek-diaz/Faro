import { AlertTriangle, Check, Gauge, LockKeyhole, Network, Plus, RefreshCw, RotateCcw, Save, Server, ShieldCheck, X } from "lucide-react";
import { useEffect, useMemo, useState, type FormEvent } from "react";
import { api, type Setting, type UpstreamProbe } from "../api/client";
import { allCatalogAddresses, findUpstreamAddress, parseUpstreamServers, upstreamProviders, type ResolverProfile } from "../data/upstreams";
import { ProviderLogo } from "../components/ProviderLogo";

type UpstreamsProps = {
  settings: Setting[];
  refresh: () => Promise<void>;
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

  useEffect(() => {
    setSelected(configured);
    setTransport(configuredTransport);
  }, [configured.join(","), configuredTransport]);

  const selectedSet = useMemo(() => new Set(selected), [selected]);
  const probeAddresses = useMemo(() => unique([...allCatalogAddresses(), ...selected]), [selected.join(",")]);
  const probeKey = probeAddresses.join(",");
  const dirty = selected.join(",") !== configured.join(",") || transport !== configuredTransport;
  const hasCustomResolvers = selected.some((address) => !findUpstreamAddress(address));
  const selectedProfiles = upstreamProviders.flatMap((provider) => provider.profiles.filter((profile) => profile.addresses.some((address) => selectedSet.has(address))));
  const mixesFiltering = selectedProfiles.some((profile) => profile.mode === "none") && selectedProfiles.some((profile) => profile.mode !== "none");
  const fastestProfileID = fastestProfile(probes);

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
      } catch (caught) {
        if (!cancelled) setProbeError(caught instanceof Error ? caught.message : "Latency check failed.");
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
    } catch (caught) {
      setProbeError(caught instanceof Error ? caught.message : "Latency check failed.");
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

  function addCustomServers(event: FormEvent) {
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
    if (next === "encrypted" && hasCustomResolvers) {
      setSaveState("error");
      setMessage("Encrypted DNS is available for Faro's listed providers. Remove custom IP resolvers or keep Standard DNS.");
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
    setSaveState("saving");
    setMessage("");
    try {
      await api.updateSettings({ upstream_dns: selected.join(","), upstream_transport: transport });
      await refresh();
      setSaveState("saved");
      setMessage("Upstreams saved and DNS reloaded.");
    } catch (caught) {
      setSaveState("error");
      setMessage(caught instanceof Error ? caught.message : "Failed to update upstreams.");
    }
  }

  return (
    <div className="upstreams-layout">
      <div className="upstream-catalog">
        <section className="upstream-privacy-panel panel" aria-labelledby="upstream-privacy-title">
          <div className="upstream-privacy-copy">
            <span className="upstream-privacy-icon">{transport === "encrypted" ? <LockKeyhole size={20} /> : <Network size={20} />}</span>
            <div>
              <h2 id="upstream-privacy-title">Connection to DNS providers</h2>
              <p>{transport === "encrypted"
                ? "Faro keeps requests private between your home and the selected providers."
                : "Faro uses regular DNS for compatibility with custom or restricted networks."}</p>
            </div>
          </div>
          <div className="upstream-transport-choices" role="radiogroup" aria-label="Connection privacy">
            <button type="button" role="radio" aria-checked={transport === "encrypted"} className={transport === "encrypted" ? "selected" : ""} onClick={() => chooseTransport("encrypted")}>
              <LockKeyhole size={17} />
              <span><strong>Encrypted</strong><small>Recommended · HTTPS</small></span>
              {transport === "encrypted" && <Check size={15} />}
            </button>
            <button type="button" role="radio" aria-checked={transport === "standard"} className={transport === "standard" ? "selected" : ""} onClick={() => chooseTransport("standard")}>
              <Network size={17} />
              <span><strong>Standard DNS</strong><small>Maximum compatibility</small></span>
              {transport === "standard" && <Check size={15} />}
            </button>
          </div>
        </section>

        <section className="upstream-toolbar" aria-label="Resolver comparison controls">
          <div className="upstream-live-state">
            <span><Gauge size={15} /> Live latency</span>
            <small>{lastChecked ? `Checked ${new Date(lastChecked).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}` : "Checking providers..."}</small>
          </div>
          <div className="upstream-toolbar-actions">
            <button className="secondary compact-button" type="button" onClick={() => void refreshLatency()} disabled={probing} aria-label="Refresh upstream latency"><RefreshCw className={probing ? "spinning" : ""} size={15} /><span>Refresh</span></button>
          </div>
        </section>

        {probeError && <div className="upstream-probe-error"><AlertTriangle size={16} /><span>{probeError}</span></div>}

        <div className="provider-list">
          {upstreamProviders.map((provider) => {
            const providerProbe = bestProbe(provider.profiles.flatMap((profile) => profile.addresses), probes);
            return <section className="provider-panel" key={provider.id}>
              <header className="provider-header">
                <div className="provider-identity">
                  <span className="provider-logo"><ProviderLogo providerID={provider.id} providerName={provider.name} /></span>
                  <div>
                    <h2>{provider.name}</h2>
                    <p>{provider.description}</p>
                  </div>
                </div>
                <div className="provider-live-summary"><span>{provider.profiles.length} option{provider.profiles.length === 1 ? "" : "s"}</span><ProbeBadge probe={providerProbe} loading={probing && !providerProbe} /></div>
              </header>

              <div className="resolver-profile-list">
                {provider.profiles.map((profile) => {
                  const selectedCount = profile.addresses.filter((address) => selectedSet.has(address)).length;
                  const fullySelected = selectedCount === profile.addresses.length;
                  const partiallySelected = selectedCount > 0 && !fullySelected;
                  const profileProbe = bestProbe(profile.addresses, probes);
                  return (
                    <button
                      className={`resolver-profile ${fullySelected ? "selected" : ""} ${partiallySelected ? "partial" : ""}`}
                      key={profile.id}
                      type="button"
                      onClick={() => toggleProfile(profile)}
                      aria-pressed={fullySelected}
                    >
                      <span className="profile-check">{fullySelected ? <Check size={16} /> : partiallySelected ? selectedCount : null}</span>
                      <span className="profile-copy">
                        <span className="profile-title-row">
                          <strong>{profile.name}</strong>
                          <span className="profile-title-badges">
                            {profile.id === fastestProfileID && <em className="fastest">Fastest</em>}
                            {profile.recommended && <em>Recommended</em>}
                            {transport === "encrypted" && <em className="encrypted"><LockKeyhole size={11} /> Encrypted</em>}
                          </span>
                        </span>
                        <span>{profile.description}</span>
                        <span className="profile-latency"><Gauge size={14} /><ProbeText probe={profileProbe} loading={probing && !profileProbe} /></span>
                        <span className="profile-addresses">{profile.addresses.map((address) => <span key={address}><code>{address}</code><ProbeText probe={probes[address]} compact loading={probing && !probes[address]} /></span>)}</span>
                      </span>
                      <span className="profile-badges">{profile.badges.map((badge) => <span key={badge}>{badge}</span>)}</span>
                    </button>
                  );
                })}
              </div>
            </section>;
          })}
        </div>

        <section className="panel custom-upstream-panel">
          <div className="panel-title dashboard-panel-title">
            <div>
              <h2>Custom resolvers</h2>
              <p>{transport === "encrypted"
                ? "Custom IP resolvers require Standard DNS. Faro will never send to them unencrypted without your choice."
                : "Add plain DNS servers that are not in the provider catalog."}</p>
            </div>
          </div>
          <form className="custom-upstream-form" onSubmit={addCustomServers}>
            <input disabled={transport === "encrypted"} value={customInput} onChange={(event) => setCustomInput(event.target.value)} placeholder="192.0.2.53 or 2001:db8::53" aria-label="Custom DNS server addresses" />
            <button type="submit" disabled={transport === "encrypted"}><Plus size={16} /><span>Add servers</span></button>
          </form>
          {customError && <span className="custom-upstream-error">{customError}</span>}
        </section>
      </div>

      <aside className="upstream-selection-panel panel">
        <div className="selection-heading">
          <div>
            <span>Current selection</span>
            <strong>{selected.length} upstream server{selected.length === 1 ? "" : "s"}</strong>
          </div>
          <span className={`selection-status ${dirty ? "pending" : ""}`}>{dirty ? <AlertTriangle size={15} /> : <ShieldCheck size={15} />} {dirty ? "Unsaved" : "Active"}</span>
        </div>

        <div className={`selection-privacy-state ${transport}`}>
          {transport === "encrypted" ? <LockKeyhole size={17} /> : <Network size={17} />}
          <div><strong>{transport === "encrypted" ? "Encrypted connection" : "Standard connection"}</strong><span>{transport === "encrypted" ? "DNS over HTTPS · no plaintext fallback" : "Traditional DNS on port 53"}</span></div>
        </div>

        {mixesFiltering && (
          <div className="selection-policy-note">
            <AlertTriangle size={16} />
            <div><strong>Mixed filtering</strong><span>Filtered and unfiltered resolvers are selected, so blocking results can vary.</span></div>
          </div>
        )}

        <div className="selected-server-list">
          {selected.length === 0 ? (
            <div className="selection-empty">Choose a provider profile or add a custom resolver.</div>
          ) : selected.map((server) => {
            const match = findUpstreamAddress(server);
            return (
              <div className="selected-server" key={server}>
                <span className="selected-provider-logo">{match ? <ProviderLogo providerID={match.provider.id} providerName={match.provider.name} /> : <Server size={15} />}</span>
                <div>
                  <strong>{server}</strong>
                  <span>{match ? `${match.provider.name} · ${match.profile.name}${transport === "encrypted" ? " · Encrypted" : ""}` : "Custom resolver"}</span>
                </div>
                <ProbeBadge probe={probes[server]} loading={probing && !probes[server]} compact />
                <button className="icon-button" type="button" onClick={() => setSelected((current) => current.filter((address) => address !== server))} aria-label={`Remove ${server}`}><X size={15} /></button>
              </div>
            );
          })}
        </div>

        <div className="selection-note">
          <strong>{transport === "encrypted" ? "Private by design" : "How latency is measured"}</strong>
          <span>{transport === "encrypted"
            ? "If one encrypted provider is unavailable, Faro tries another selected encrypted provider. It never silently falls back to plaintext."
            : "Response time is measured from the Faro host. Your devices may see slightly different results."}</span>
        </div>

        <div className="selection-actions">
          <button type="button" className="secondary" disabled={!dirty} onClick={() => { setSelected(configured); setTransport(configuredTransport); setMessage(""); setSaveState("idle"); }}><RotateCcw size={16} /><span>Reset</span></button>
          <button type="button" disabled={!dirty || saveState === "saving" || selected.length === 0} onClick={() => void save()}><Save size={16} /><span>{saveState === "saving" ? "Saving" : "Save upstreams"}</span></button>
        </div>
        {message && <span className={`selection-message ${saveState === "error" ? "error" : ""}`}>{message}</span>}
      </aside>
    </div>
  );
}

function unique(values: string[]) {
  return Array.from(new Set(values));
}

function isIPAddress(value: string) {
  if (value.includes(":")) {
    try {
      new URL(`http://[${value}]/`);
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

function ProbeText({ probe, loading = false, compact = false }: { probe?: UpstreamProbe; loading?: boolean; compact?: boolean }) {
  const state = probeState(probe, loading);
  return <span className={`probe-text ${state.tone} ${compact ? "compact" : ""}`}>{state.label}</span>;
}

function ProbeBadge({ probe, loading = false, compact = false }: { probe?: UpstreamProbe; loading?: boolean; compact?: boolean }) {
  const state = probeState(probe, loading);
  return <span className={`probe-badge ${state.tone} ${compact ? "compact" : ""}`}><span />{state.label}</span>;
}

function probeState(probe?: UpstreamProbe, loading = false) {
  if (!probe) return { label: loading ? "Testing" : "Not tested", tone: "pending" };
  if (probe.status === "online" && probe.latency_ms !== null) return { label: `${formatLatency(probe.latency_ms)} ms`, tone: latencyTone(probe.latency_ms) };
  return { label: "Unavailable", tone: "offline" };
}

function formatLatency(value: number) {
  return value >= 100 ? Math.round(value).toString() : value.toFixed(value >= 10 ? 0 : 1);
}

function latencyTone(value: number) {
  if (value < 40) return "fast";
  if (value < 100) return "moderate";
  return "slow";
}
