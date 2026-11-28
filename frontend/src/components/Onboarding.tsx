import { Checkbox } from "./ui/checkbox";
import { Input } from "./ui/input";
import { Button } from "./ui/button";
import { ArrowLeft, ArrowRight, Check, CheckCircle2, Database, Gauge, LockKeyhole, Network, ShieldCheck } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { api, type Blocklist, type DNSQuery, type EncryptedUpstreamEndpoint, type Setting } from "../api/client";
import { blocklistCatalog } from "../data/blocklists";
import { encryptedEndpointForAddresses, encryptedEndpointIndex, upstreamProviders } from "../data/upstreams";
import { ProviderLogo } from "./ProviderLogo";
import { SetupRail, setupStages } from "./SetupRail";
import { AppearanceMenu } from "./AppearanceMenu";
import { errorMessage, normalizeURL } from "../utils/formatting";
import { DnsServerAddress } from "./DnsServerAddress";
import { normalizeLANAddress, recentClientQueries, starterListScopeError, validSuffix } from "../utils/onboarding";
import type { ThemeMode } from "../theme";

type OnboardingProps = {
  readonly username: string;
  readonly onComplete: () => void;
  readonly onJoinExisting: () => void;
  readonly themeMode: ThemeMode;
  readonly onThemeModeChange: (mode: ThemeMode) => void;
};

const allProfiles = upstreamProviders.flatMap((provider) => provider.profiles.map((profile) => ({ provider, profile })));
type ProtectionChoice = { id: string; name: string; description: string; url?: string; category?: string };
const protectionChoices: ProtectionChoice[] = [
  ...blocklistCatalog.filter((item) => item.id === "oisd-small" || item.id === "hagezi-normal"),
  { id: "none", name: "No starter list", description: "Add no new filtering list. Existing protection stays in place." }
];

export function Onboarding({ username, onComplete, onJoinExisting, themeMode, onThemeModeChange }: OnboardingProps) {
  const [step, setStep] = useState(0);
  const heading = useRef<HTMLHeadingElement>(null);
  const [customize, setCustomize] = useState(false);
  const [localSuffix, setLocalSuffix] = useState("home");
  const [lanAddress, setLanAddress] = useState(() => normalizeLANAddress(window.location.hostname));
  const [cacheEnabled, setCacheEnabled] = useState(true);
  const [cacheTTL, setCacheTTL] = useState("300");
  const [selectedProfiles, setSelectedProfiles] = useState<string[]>(["cloudflare-standard"]);
  const [customAddresses, setCustomAddresses] = useState("");
  const [upstreamTransport, setUpstreamTransport] = useState<"encrypted" | "standard">("encrypted");
  const [protection, setProtection] = useState("oisd-small");
  const [installed, setInstalled] = useState<Blocklist[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [loadError, setLoadError] = useState("");
  const [loadAttempt, setLoadAttempt] = useState(0);
  const [loaded, setLoaded] = useState(false);
  const [encryptedEndpoints, setEncryptedEndpoints] = useState<EncryptedUpstreamEndpoint[]>([]);
  const [appliedAt, setAppliedAt] = useState(0);
  const [checking, setChecking] = useState(false);
  const [checkError, setCheckError] = useState("");
  const [observed, setObserved] = useState<DNSQuery[] | null>(null);

  useEffect(() => {
    if (step === 1) heading.current?.focus();
  }, [step]);

  useEffect(() => {
    let cancelled = false;
    setLoaded(false);
    setLoadError("");
    Promise.all([api.settings(), api.blocklists(), api.upstreamCatalog(), api.protections()])
      .then(([settings, blocklists, catalog, protections]) => {
        if (cancelled) return;
        const saved = savedConfiguration(settings);
        setLocalSuffix(saved.suffix);
        if (saved.lanAddress) setLanAddress(saved.lanAddress);
        setCacheEnabled(saved.cache);
        setCacheTTL(saved.cacheTTL);
        setSelectedProfiles(saved.profiles);
        setCustomAddresses(saved.customAddresses);
        setUpstreamTransport(saved.transport);
        setCustomize(saved.customized);
        setInstalled(blocklists);
        const home = protections.find((profile) => profile.is_default);
        const installedChoice = protectionChoices.find((choice) => choice.url && blocklists.some((list) => list.enabled && home?.blocklist_ids.includes(list.id) && normalizeURL(list.url) === normalizeURL(choice.url!)));
        setProtection(installedChoice?.id ?? (blocklists.length ? "none" : "oisd-small"));
        setEncryptedEndpoints(catalog.encrypted_endpoints);
        setLoaded(true);
      })
      .catch((error_) => {
        if (!cancelled) setLoadError(errorMessage(error_, "Could not load your saved settings and provider support. Retry before applying setup."));
      });
    return () => { cancelled = true; };
  }, [loadAttempt]);

  const upstreamAddresses = useMemo(() => Array.from(new Set([
    ...allProfiles.filter(({ profile }) => selectedProfiles.includes(profile.id)).flatMap(({ profile }) => profile.addresses),
    ...customAddresses.split(",").map((address) => address.trim()).filter(Boolean)
  ])), [selectedProfiles, customAddresses]);
  const encryptedByAddress = useMemo(() => encryptedEndpointIndex(encryptedEndpoints), [encryptedEndpoints]);
  const selectedProtection = protectionChoices.find((choice) => choice.id === protection) ?? protectionChoices[0];
  const providerNames = allProfiles.filter(({ profile }) => selectedProfiles.includes(profile.id)).map(({ provider, profile }) => `${provider.name} ${profile.name}`);
  if (customAddresses.trim()) providerNames.push(customAddresses.trim());
  const recommended = localSuffix === "home" && cacheEnabled && cacheTTL === "300" && selectedProfiles.length === 1 && selectedProfiles[0] === "cloudflare-standard" && !customAddresses.trim() && upstreamTransport === "encrypted" && protection === "oisd-small";

  function useRecommendedSettings() {
    setLocalSuffix("home");
    setCacheEnabled(true);
    setCacheTTL("300");
    setSelectedProfiles(["cloudflare-standard"]);
    setCustomAddresses("");
    setUpstreamTransport("encrypted");
    setProtection("oisd-small");
    setError("");
  }

  function validationError() {
    if (!loaded) return "Load your saved settings and provider support before applying setup.";
    if (!normalizeLANAddress(lanAddress)) return "Enter a usable LAN IP for the computer running Faro, such as 192.168.1.10.";
    if (!validSuffix(localSuffix)) return "Use a simple suffix such as home, lan, or internal.";
    if (!/^\d+$/.test(cacheTTL) || Number(cacheTTL) < 30 || Number(cacheTTL) > 3600) return "Choose a cache lifetime between 30 and 3,600 seconds.";
    if (!upstreamAddresses.length) return "Select at least one upstream resolver profile or enter an address.";
    if (upstreamTransport === "encrypted" && upstreamAddresses.some((address) => !encryptedByAddress.has(address))) return "One or more selected addresses have no supported HTTPS endpoint. Remove them or choose Standard DNS in Customize.";
    return "";
  }

  async function applyConfiguration() {
    const invalid = validationError();
    if (invalid) { setError(invalid); return; }
    setBusy(true);
    setError("");
    try {
      await api.updateSettings({
        local_domain_suffix: localSuffix.trim().toLowerCase(),
        faro_lan_ip: normalizeLANAddress(lanAddress),
        dns_cache_enabled: String(cacheEnabled), dns_cache_ttl: cacheTTL,
        upstream_dns: upstreamAddresses.join(","), upstream_transport: upstreamTransport
      });
      // Re-read on every attempt; a response can be lost after creation succeeds.
      const [latestLists, profiles] = await Promise.all([api.blocklists(), api.protections()]);
      let currentLists = latestLists;
      setInstalled(currentLists);
      if (selectedProtection.url) {
        const home = profiles.find((profile) => profile.is_default);
        if (!home) throw new Error("Home protection is unavailable. Retry setup after checking Faro's health.");
        const sourceURL = selectedProtection.url;
        let existing = currentLists.find((list) => normalizeURL(list.url) === normalizeURL(sourceURL));
        if (existing) {
          const scopeError = starterListScopeError(existing, profiles, home.id);
          if (scopeError) throw new Error(scopeError);
        }
        if (!existing) {
          const created = await api.createBlocklist({ name: selectedProtection.name, url: sourceURL, enabled: true, assign_to_default: true });
          existing = { id: created.id, name: selectedProtection.name, url: sourceURL, enabled: true, entry_count: 0 };
          currentLists = [...currentLists, existing];
          setInstalled(currentLists);
        }
        if (!existing.enabled) await api.updateBlocklist({ ...existing, enabled: true });
        if (!existing.entry_count) {
          const refreshed = await api.refreshBlocklist(existing.id);
          if (!refreshed.entry_count) throw new Error("The starter list returned no domains. Retry, choose another list, or choose No starter list in Customize.");
        }
        if (!home.blocklist_ids.includes(existing.id)) {
          await api.updateProtection(home.id, {
            name: home.name, icon: home.icon, blocklist_ids: [...home.blocklist_ids, existing.id],
            allow_domains: home.allow_entries.map((entry) => entry.domain), block_domains: home.block_entries.map((entry) => entry.domain),
            device_ips: [], device_ids: home.device_ids, schedule: home.schedule
          });
        }
      }
      setLanAddress(normalizeLANAddress(lanAddress));
      setAppliedAt(Date.now());
      setObserved(null);
      setCheckError("");
      setStep(1);
    } catch (error_) {
      setError(errorMessage(error_, "Could not finish applying the configuration. Review the error and retry; installed lists will be reused."));
    } finally { setBusy(false); }
  }

  async function checkConnection() {
    setChecking(true);
    setCheckError("");
    try { setObserved(recentClientQueries(await api.queries(), lanAddress, appliedAt)); }
    catch (error_) { setObserved(null); setCheckError(errorMessage(error_, "Could not check DNS activity. Try again.")); }
    finally { setChecking(false); }
  }

  async function finish() {
    setBusy(true);
    setError("");
    try { await api.updateSettings({ onboarding_completed: "true" }); onComplete(); }
    catch (error_) { setError(errorMessage(error_, "Could not finish setup. Your DNS configuration is already applied; try again.")); }
    finally { setBusy(false); }
  }

  return <main className="onboarding-shell onboarding-guided">
    <AppearanceMenu className="public-appearance-control" themeMode={themeMode} onThemeModeChange={onThemeModeChange} />
    <SetupRail currentStep={step + 1} username={username} />
    <section className="onboarding-content"><div className="onboarding-step">
      <header><span>Step {step + 2} of {setupStages.length}</span><h1 ref={heading} tabIndex={-1}>{step === 0 ? "Start with a simple setup" : "Connect a device and check"}</h1><p>{step === 0 ? "Enter Faro’s fixed address. The recommended settings are ready, or you can customize them below." : "Open the network settings on one device and use Faro’s address as its only DNS server."}</p></header>
      {!loaded ? <div className="onboarding-load-state" role="status">{loadError ? <><p role="alert">{loadError}</p><Button variant="outline" onClick={() => setLoadAttempt((attempt) => attempt + 1)}>Retry loading setup</Button></> : <p>Loading your saved settings and provider support…</p>}</div> : step === 0 ? <fieldset disabled={busy} className="onboarding-config-fields">
        <div className="onboarding-form-section"><label><span>Faro LAN address</span><Input value={lanAddress} onChange={(event) => setLanAddress(event.target.value)} placeholder="192.168.1.10" autoFocus /><small>The fixed IP of the computer running Faro. Reserve this address in your router.</small></label></div>
        <section className="onboarding-defaults" aria-label="Setup settings"><div><ShieldCheck size={20} /><strong>{recommended ? "Recommended settings" : "Your settings"}</strong></div><p><strong>{providerNames.join(", ") || "Choose a DNS provider"}</strong> · {upstreamTransport === "encrypted" ? "Encrypted with HTTPS" : "Standard DNS"}</p><p>{selectedProtection.name} · {cacheEnabled ? `${cacheTTL}-second DNS cache` : "Cache disabled"} · .{localSuffix} local names</p><small>{recommended ? "Cloudflare Standard adds no provider filtering. Faro’s starter list handles domain blocking." : "These settings will be applied before you connect a device."}</small></section>
        <div className="onboarding-configuration-actions"><Button variant="outline" type="button" className="onboarding-customize-button" aria-expanded={customize} aria-controls="onboarding-custom-options" onClick={() => setCustomize((current) => !current)}>{customize ? "Hide customization" : "Customize settings"}</Button>{!recommended && <Button variant="outline" type="button" onClick={useRecommendedSettings}>Use recommended settings</Button>}</div>
        {customize && <div id="onboarding-custom-options" className="onboarding-custom-options">
          <section><h2>Local names and cache</h2><LocalOptions suffix={localSuffix} setSuffix={setLocalSuffix} cache={cacheEnabled} setCache={setCacheEnabled} cacheTTL={cacheTTL} setCacheTTL={setCacheTTL} /></section>
          <section><h2>DNS providers</h2><UpstreamStep selected={selectedProfiles} setSelected={setSelectedProfiles} transport={upstreamTransport} setTransport={setUpstreamTransport} encryptedByAddress={encryptedByAddress} /><div className="onboarding-form-section"><label><span>Additional upstream addresses</span><Input value={customAddresses} onChange={(event) => setCustomAddresses(event.target.value)} placeholder="Comma-separated IP addresses" /><small>Standard DNS accepts custom resolvers. HTTPS requires an address supported by Faro’s provider catalog.</small></label></div></section>
          <section><h2>Starter protection</h2><ProtectionStep selected={protection} setSelected={setProtection} />{installed.length > 0 && <p className="onboarding-helper">Your {installed.length} installed {installed.length === 1 ? "list stays" : "lists stay"} in place. This choice adds or enables a starter list in Home.</p>}</section>
        </div>}
      </fieldset> : <ConnectStep dnsAddress={lanAddress} suffix={localSuffix} providers={providerNames} transport={upstreamTransport} protection={selectedProtection.name} cache={cacheEnabled} cacheTTL={cacheTTL} checking={checking} busy={busy} observed={observed} checkError={checkError} finishError={error} onCheck={() => void checkConnection()} onFinish={() => void finish()} onEdit={() => { setError(""); setStep(0); }} />}
      {step === 0 && error && <div className="onboarding-error" role="alert">{error}</div>}
      {step === 0 && <div className="onboarding-join-existing"><span>Already have Faro running elsewhere?</span><Button variant="outline" type="button" className="secondary" onClick={onJoinExisting} disabled={busy}><Network size={16} />Join an existing Faro home</Button></div>}
      {step === 0 && <footer className="onboarding-actions">
        <span className="onboarding-helper">You can change these settings later.</span>
        <Button type="button" disabled={!loaded || busy} onClick={() => void applyConfiguration()}><span>{busy ? "Applying configuration…" : "Apply and connect"}</span><ArrowRight size={16} /></Button>
      </footer>}
    </div></section>
  </main>;
}

function LocalOptions({ suffix, setSuffix, cache, setCache, cacheTTL, setCacheTTL }: {
  readonly suffix: string; readonly setSuffix: (value: string) => void; readonly cache: boolean; readonly setCache: (value: boolean) => void; readonly cacheTTL: string; readonly setCacheTTL: (value: string) => void;
}) {
  return <div className="onboarding-form-section"><label><span>Local domain suffix</span><div className="onboarding-suffix-input"><Input variant="embedded" value={suffix} onChange={(event) => setSuffix(event.target.value)} placeholder="home" /><strong>.{suffix || "home"}</strong></div><small>For names such as plex.{suffix || "home"} and router.{suffix || "home"}.</small></label><div className="onboarding-toggle-row"><span className="onboarding-option-icon"><Gauge size={19} /></span><div><strong>DNS response cache</strong><p>Serve repeated lookups locally for lower latency.</p></div><label className="compact-toggle"><Checkbox checked={cache} onCheckedChange={(checked) => setCache(checked)} /><span>{cache ? "Enabled" : "Disabled"}</span></label></div><label><span>Cache lifetime (seconds)</span><Input type="number" min={30} max={3600} value={cacheTTL} onChange={(event) => setCacheTTL(event.target.value)} /></label></div>;
}

function UpstreamStep({ selected, setSelected, transport, setTransport, encryptedByAddress }: {
  readonly selected: string[]; readonly setSelected: (value: string[]) => void; readonly transport: "encrypted" | "standard"; readonly setTransport: (value: "encrypted" | "standard") => void; readonly encryptedByAddress: Map<string, EncryptedUpstreamEndpoint>;
}) {
  const choices = allProfiles.filter(({ profile }) => profile.recommended || selected.includes(profile.id));
  return <div className="onboarding-upstream-step">
    <section className="onboarding-privacy-choice">
      <div><span className="onboarding-option-icon"><LockKeyhole size={19} /></span><span><strong>Connection to DNS providers</strong><p>Devices still connect to Faro using ordinary DNS.</p></span></div>
      <div className="onboarding-privacy-options" role="radiogroup" aria-label="DNS provider connection">
        <label>
          <input type="radio" name="provider-transport" value="encrypted" checked={transport === "encrypted"} onChange={() => setTransport("encrypted")} />
          <span><strong>Encrypted</strong><small>Recommended · HTTPS</small></span>
        </label>
        <label>
          <input type="radio" name="provider-transport" value="standard" checked={transport === "standard"} onChange={() => setTransport("standard")} />
          <span><strong>Standard DNS</strong><small>Custom resolver support</small></span>
        </label>
      </div>
    </section>
    <div className="onboarding-choice-grid upstream-choices">{choices.map(({ provider, profile }) => {
      const active = selected.includes(profile.id);
      const endpoint = encryptedEndpointForAddresses(profile.addresses, encryptedByAddress);
      const unavailable = transport === "encrypted" && !endpoint;
      const connectionLabel = transport === "standard" ? profile.addresses.join(" · ") : endpoint?.url ?? "HTTPS not available";
      return <button type="button" className={`${active ? "selected" : ""} ${unavailable ? "unavailable" : ""}`} aria-pressed={active} disabled={unavailable && !active} key={profile.id} onClick={() => setSelected(active ? selected.filter((id) => id !== profile.id) : [...selected, profile.id])}><span className="onboarding-provider-logo"><ProviderLogo providerID={provider.id} providerName={provider.name} /></span><span className="onboarding-choice-copy"><strong>{provider.name}</strong><small>{profile.name}</small><p>{profile.description}</p><code title={connectionLabel}>{connectionLabel}</code></span><span className="onboarding-check">{active && <Check size={15} />}</span></button>;
    })}</div><p className="onboarding-helper">Providers can filter differently. Mixing filtered and unfiltered profiles can produce different answers.</p>
  </div>;
}

function ProtectionStep({ selected, setSelected }: { readonly selected: string; readonly setSelected: (value: string) => void }) {
  return <div className="onboarding-choice-list">{protectionChoices.map((choice) => <button type="button" className={selected === choice.id ? "selected" : ""} aria-pressed={selected === choice.id} key={choice.id} onClick={() => setSelected(choice.id)}><span className="onboarding-option-icon">{choice.id === "none" ? <Database size={19} /> : <ShieldCheck size={19} />}</span><span className="onboarding-choice-copy"><strong>{choice.name}</strong>{choice.category && <small>{choice.category}</small>}<p>{choice.description}</p></span><span className="onboarding-radio"><span /></span></button>)}</div>;
}

type ConnectStepProps = {
  readonly dnsAddress: string;
  readonly suffix: string;
  readonly providers: string[];
  readonly transport: "encrypted" | "standard";
  readonly protection: string;
  readonly cache: boolean;
  readonly cacheTTL: string;
  readonly checking: boolean;
  readonly busy: boolean;
  readonly observed: DNSQuery[] | null;
  readonly checkError: string;
  readonly finishError: string;
  readonly onCheck: () => void;
  readonly onFinish: () => void;
  readonly onEdit: () => void;
};

function ConnectStep({ dnsAddress, suffix, providers, transport, protection, cache, cacheTTL, checking, busy, observed, checkError, finishError, onCheck, onFinish, onEdit }: ConnectStepProps) {
  const clients = Array.from(new Set(observed?.map((query) => query.client_ip) ?? []));
  const hasActivity = clients.length > 0;

  return <div className="onboarding-connection">
    <section className="onboarding-connection-check" aria-label="Connect and check one device">
      <DnsServerAddress address={dnsAddress} />
      <p>Save the DNS setting, reconnect, and open a few websites on that device. Then check for recent requests.</p>
      <Button type="button" variant={hasActivity ? "outline" : "default"} onClick={onCheck} disabled={checking || busy}>{checking ? "Checking DNS activity…" : "Check for DNS activity"}</Button>
      <div className="onboarding-check-result" role="status" aria-atomic="true" aria-busy={checking}>
        {checkError ? <p><strong>Couldn’t check DNS activity.</strong> {checkError}</p> : hasActivity ? <>
          <p className="onboarding-observed"><CheckCircle2 size={18} />DNS activity observed from {clients.slice(0, 3).join(", ")}{clients.length > 3 ? ` and ${clients.length - 3} more` : ""}.</p>
          <p>Latest request: <strong>{observed?.[0].domain}</strong>. Check that the client IP matches the device you tested.</p>
        </> : observed && <>
          <p><strong>No recent client requests found yet.</strong></p>
          <p>Check the device’s DNS address, open another website, wait a few seconds, and try again. A VPN or private DNS can send requests elsewhere.</p>
        </>}
      </div>
      {finishError && <div className="onboarding-error" role="alert">{finishError}</div>}
      <footer className="onboarding-actions">
        <Button type="button" variant={hasActivity ? "default" : "outline"} disabled={busy || checking} onClick={onFinish}><CheckCircle2 size={16} />{busy ? "Finishing setup…" : hasActivity ? "Finish setup and open Faro" : "Finish setup and connect later"}</Button>
        <Button type="button" variant="ghost" disabled={busy || checking} onClick={onEdit}><ArrowLeft size={16} />Edit settings</Button>
      </footer>
    </section>
    <div className="onboarding-connection-details">
      <details>
        <summary>Terminal test and router setup</summary>
        <div className="onboarding-command"><span>Run on another device</span><code>nslookup example.com {dnsAddress}</code></div>
        <p>To connect your whole network, set your router’s LAN/DHCP DNS to Faro and reconnect your devices. Update IPv6 DNS too if your router supplies it.</p>
      </details>
      <details>
        <summary>Applied configuration</summary>
        <dl>
          <ReviewRow label="LAN address" value={dnsAddress} />
          <ReviewRow label="Local suffix" value={`.${suffix}`} />
          <ReviewRow label="DNS providers" value={providers.join(", ")} />
          <ReviewRow label="DNS privacy" value={transport === "encrypted" ? "Encrypted with HTTPS" : "Standard DNS"} />
          <ReviewRow label="Starter protection" value={protection} />
          <ReviewRow label="DNS cache" value={cache ? `${cacheTTL} seconds` : "Disabled"} />
        </dl>
      </details>
    </div>
  </div>;
}

function ReviewRow({ label, value }: { readonly label: string; readonly value: string }) {
  return <div><dt>{label}</dt><dd>{value}</dd></div>;
}

function savedConfiguration(settings: Setting[]) {
  const values = Object.fromEntries(settings.map((setting) => [setting.key, setting.value]));
  const configured = new Set((values.upstream_dns || "").split(",").map((value) => value.trim()).filter(Boolean));
  const untouched = !values.faro_lan_ip && configured.size === 2 && configured.has("1.1.1.1") && configured.has("9.9.9.9") && values.upstream_transport !== "encrypted";
  const preserveProviders = configured.size > 0 && !untouched;
  const profiles = preserveProviders ? allProfiles.filter(({ profile }) => profile.addresses.every((address) => configured.has(address))).map(({ profile }) => profile.id) : ["cloudflare-standard"];
  const matched = new Set(allProfiles.filter(({ profile }) => profiles.includes(profile.id)).flatMap(({ profile }) => profile.addresses));
  const customAddresses = preserveProviders ? Array.from(configured).filter((address) => !matched.has(address)).join(", ") : "";
  const suffix = values.local_domain_suffix || "home";
  const cache = values.dns_cache_enabled !== "false";
  const cacheTTL = values.dns_cache_ttl || "300";
  const transport = preserveProviders && values.upstream_transport === "standard" ? "standard" as const : "encrypted" as const;
  return { suffix, lanAddress: values.faro_lan_ip || "", cache, cacheTTL, profiles, customAddresses, transport, customized: suffix !== "home" || !cache || cacheTTL !== "300" || (preserveProviders && (transport !== "encrypted" || profiles.length !== 1 || profiles[0] !== "cloudflare-standard" || !!customAddresses)) };
}
