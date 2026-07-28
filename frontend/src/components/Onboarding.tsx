import { ArrowLeft, ArrowRight, Check, CheckCircle2, Database, Gauge, LockKeyhole, Network, Router, ShieldCheck } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { api, type Blocklist, type EncryptedUpstreamEndpoint, type Setting } from "../api/client";
import { blocklistCatalog } from "../data/blocklists";
import { upstreamProviders } from "../data/upstreams";
import { ProviderLogo } from "./ProviderLogo";
import { SetupRail, setupStages } from "./SetupRail";

type OnboardingProps = {
  username: string;
  onComplete: () => void;
};

const steps = setupStages.slice(1);
const onboardingProfiles = upstreamProviders.flatMap((provider) => provider.profiles.filter((profile) => profile.recommended).map((profile) => ({ provider, profile })));
type ProtectionChoice = { id: string; name: string; description: string; url?: string; category?: string; intensity?: string };
const protectionChoices: ProtectionChoice[] = [
  { id: "none", name: "No starter list", description: "Start with DNS visibility only. You can add protection later." },
  ...blocklistCatalog.filter((item) => item.id === "oisd-small" || item.id === "hagezi-normal")
];

export function Onboarding({ username, onComplete }: OnboardingProps) {
  const [step, setStep] = useState(0);
  const [localSuffix, setLocalSuffix] = useState("home");
  const [lanAddress, setLanAddress] = useState(() => detectedLANAddress(window.location.hostname));
  const [cacheEnabled, setCacheEnabled] = useState(true);
  const [selectedProfiles, setSelectedProfiles] = useState<string[]>(["cloudflare-standard", "quad9-secure"]);
  const [upstreamTransport, setUpstreamTransport] = useState<"encrypted" | "standard">("encrypted");
  const [protection, setProtection] = useState("oisd-small");
  const [installed, setInstalled] = useState<Blocklist[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [encryptedEndpoints, setEncryptedEndpoints] = useState<EncryptedUpstreamEndpoint[]>([]);
  const [catalogLoaded, setCatalogLoaded] = useState(false);

  useEffect(() => {
    Promise.all([api.settings(), api.blocklists()]).then(([settings, blocklists]) => {
      hydrateSettings(settings, setLocalSuffix, setLanAddress, setCacheEnabled, setSelectedProfiles, setUpstreamTransport);
      setInstalled(blocklists);
      const installedChoice = protectionChoices.find((choice) => choice.url && blocklists.some((list) => normalizeURL(list.url) === normalizeURL(choice.url!)));
      if (installedChoice) setProtection(installedChoice.id);
    }).catch((caught) => setError(errorMessage(caught)));
  }, []);

  useEffect(() => {
    let cancelled = false;
    api.upstreamCatalog()
      .then((response) => {
        if (!cancelled) setEncryptedEndpoints(response.encrypted_endpoints);
      })
      .catch((caught) => {
        if (!cancelled) setError(errorMessage(caught));
      })
      .finally(() => {
        if (!cancelled) setCatalogLoaded(true);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const upstreamAddresses = useMemo(() => Array.from(new Set(onboardingProfiles
    .filter(({ profile }) => selectedProfiles.includes(profile.id))
    .flatMap(({ profile }) => profile.addresses))), [selectedProfiles]);
  const encryptedByAddress = useMemo(() => encryptedEndpointIndex(encryptedEndpoints), [encryptedEndpoints]);
  const unsupportedSelectedProfiles = onboardingProfiles.filter(({ profile }) =>
    selectedProfiles.includes(profile.id) && !encryptedEndpointForAddresses(profile.addresses, encryptedByAddress)
  );
  const selectedProtection = protectionChoices.find((choice) => choice.id === protection) ?? protectionChoices[0];
  function next() {
    setError("");
    if (step === 0 && !validSuffix(localSuffix)) {
      setError("Use a simple suffix such as home, lan, or internal.");
      return;
    }
    if (step === 0 && !validIPAddress(lanAddress)) {
      setError("Enter the LAN IP of the computer running Faro, such as 192.168.1.20.");
      return;
    }
    if (step === 1 && selectedProfiles.length === 0) {
      setError("Select at least one upstream resolver profile.");
      return;
    }
    if (step === 1 && upstreamTransport === "encrypted" && !catalogLoaded) {
      setError("Faro is still checking which providers support encrypted DNS.");
      return;
    }
    if (step === 1 && upstreamTransport === "encrypted" && unsupportedSelectedProfiles.length > 0) {
      setError("Remove providers without an HTTPS endpoint or choose Standard DNS.");
      return;
    }
    setStep((current) => Math.min(steps.length - 1, current + 1));
  }

  async function finish() {
    setBusy(true);
    setError("");
    try {
      await api.updateSettings({
        local_domain_suffix: localSuffix.trim().toLowerCase(),
        faro_lan_ip: lanAddress.trim(),
        dns_cache_enabled: String(cacheEnabled),
        dns_cache_ttl: "300",
        upstream_dns: upstreamAddresses.join(","),
        upstream_transport: upstreamTransport
      });

      if (selectedProtection.url) {
        const protectionURL = selectedProtection.url;
        let existing = installed.find((list) => normalizeURL(list.url) === normalizeURL(protectionURL));
        if (!existing) {
          const created = await api.createBlocklist({ name: selectedProtection.name, url: protectionURL, enabled: true });
          const createdList: Blocklist = { id: created.id, name: selectedProtection.name, url: protectionURL, enabled: true, entry_count: 0 };
          existing = createdList;
          setInstalled((current) => [...current, createdList]);
        }
        if (existing && !existing.enabled) {
          await api.updateBlocklist({ ...existing, enabled: true });
          existing = { ...existing, enabled: true };
        }
        if (existing && !existing.entry_count) await api.refreshBlocklist(existing.id);
      }

      await api.updateSettings({ onboarding_completed: "true" });
      onComplete();
    } catch (caught) {
      setError(errorMessage(caught));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="onboarding-shell">
      <SetupRail currentStep={step + 1} username={username} />

      <section className="onboarding-content">
        <div className="onboarding-step">
          <header><span>Step {step + 2} of {setupStages.length}</span><h1>{stepHeading(step)}</h1><p>{stepIntro(step)}</p></header>

          {step === 0 && <LocalStep suffix={localSuffix} setSuffix={setLocalSuffix} lanAddress={lanAddress} setLanAddress={setLanAddress} cache={cacheEnabled} setCache={setCacheEnabled} />}
          {step === 1 && <UpstreamStep selected={selectedProfiles} setSelected={setSelectedProfiles} transport={upstreamTransport} setTransport={setUpstreamTransport} encryptedByAddress={encryptedByAddress} catalogLoaded={catalogLoaded} />}
          {step === 2 && <ProtectionStep selected={protection} setSelected={setProtection} />}
          {step === 3 && <ConnectStep dnsAddress={lanAddress} suffix={localSuffix} providers={onboardingProfiles.filter(({ profile }) => selectedProfiles.includes(profile.id)).map(({ provider, profile }) => `${provider.name} ${profile.name}`)} transport={upstreamTransport} protection={selectedProtection.name} />}

          {error && <div className="onboarding-error" role="alert">{error}</div>}
          <footer className="onboarding-actions">
            <button type="button" className="secondary" disabled={step === 0 || busy} onClick={() => { setError(""); setStep((current) => Math.max(0, current - 1)); }}><ArrowLeft size={16} /><span>Back</span></button>
            {step < steps.length - 1
              ? <button type="button" onClick={next}><span>Continue</span><ArrowRight size={16} /></button>
              : <button type="button" onClick={() => void finish()} disabled={busy}><CheckCircle2 size={16} /><span>{busy ? "Applying configuration" : "Apply and open Faro"}</span></button>}
          </footer>
        </div>
      </section>
    </main>
  );
}

function LocalStep({ suffix, setSuffix, lanAddress, setLanAddress, cache, setCache }: { suffix: string; setSuffix: (value: string) => void; lanAddress: string; setLanAddress: (value: string) => void; cache: boolean; setCache: (value: boolean) => void }) {
  return <div className="onboarding-form-section"><label><span>Faro LAN address</span><input value={lanAddress} onChange={(event) => setLanAddress(event.target.value)} placeholder="192.168.1.20" inputMode="decimal" autoFocus /><small>The fixed IP assigned to the computer running Faro. Your router will use this as its DNS server.</small></label><label><span>Local domain suffix</span><div className="onboarding-suffix-input"><input value={suffix} onChange={(event) => setSuffix(event.target.value)} placeholder="home" /><strong>.{suffix || "home"}</strong></div><small>Examples: plex.{suffix || "home"}, router.{suffix || "home"}</small></label><div className="onboarding-toggle-row"><span className="onboarding-option-icon"><Gauge size={19} /></span><div><strong>DNS response cache</strong><p>Serve repeated lookups locally for lower latency.</p></div><label className="compact-toggle"><input type="checkbox" checked={cache} onChange={(event) => setCache(event.target.checked)} /><span>{cache ? "Enabled" : "Disabled"}</span></label></div></div>;
}

function UpstreamStep({ selected, setSelected, transport, setTransport, encryptedByAddress, catalogLoaded }: { selected: string[]; setSelected: (value: string[]) => void; transport: "encrypted" | "standard"; setTransport: (value: "encrypted" | "standard") => void; encryptedByAddress: Map<string, EncryptedUpstreamEndpoint>; catalogLoaded: boolean }) {
  return <div className="onboarding-upstream-step">
    <section className="onboarding-privacy-choice">
      <div><span className="onboarding-option-icon">{transport === "encrypted" ? <LockKeyhole size={19} /> : <Network size={19} />}</span><span><strong>Connection to DNS providers</strong><p>Choose how Faro contacts providers. Devices still connect to Faro normally.</p></span></div>
      <div className="onboarding-privacy-options" role="radiogroup" aria-label="DNS provider connection">
        <button type="button" role="radio" aria-checked={transport === "encrypted"} className={transport === "encrypted" ? "selected" : ""} onClick={() => setTransport("encrypted")} disabled={!catalogLoaded}><LockKeyhole size={16} /><span><strong>Encrypted</strong><small>{catalogLoaded ? "Recommended · HTTPS" : "Checking support…"}</small></span>{transport === "encrypted" && <Check size={14} />}</button>
        <button type="button" role="radio" aria-checked={transport === "standard"} className={transport === "standard" ? "selected" : ""} onClick={() => setTransport("standard")}><Network size={16} /><span><strong>Standard DNS</strong><small>Maximum compatibility</small></span>{transport === "standard" && <Check size={14} />}</button>
      </div>
    </section>
    <div className="onboarding-choice-grid upstream-choices">{onboardingProfiles.map(({ provider, profile }) => {
      const active = selected.includes(profile.id);
      const endpoint = encryptedEndpointForAddresses(profile.addresses, encryptedByAddress);
      const unavailable = transport === "encrypted" && catalogLoaded && !endpoint;
      const connectionLabel = transport === "encrypted" ? endpoint?.url ?? (catalogLoaded ? "HTTPS not available" : "Checking HTTPS support…") : profile.addresses.join(" · ");
      return <button type="button" className={`${active ? "selected" : ""} ${unavailable ? "unavailable" : ""}`} aria-pressed={active} disabled={unavailable} key={profile.id} onClick={() => setSelected(active ? selected.filter((id) => id !== profile.id) : [...selected, profile.id])}><span className="onboarding-provider-logo"><ProviderLogo providerID={provider.id} providerName={provider.name} /></span><span className="onboarding-choice-copy"><strong>{provider.name}</strong><small>{profile.name}</small><p>{profile.description}</p><code title={connectionLabel}>{connectionLabel}</code></span><span className="onboarding-check">{active && <Check size={15} />}</span></button>;
    })}</div>
  </div>;
}

function ProtectionStep({ selected, setSelected }: { selected: string; setSelected: (value: string) => void }) {
  return <div className="onboarding-choice-list">{protectionChoices.map((choice) => <button type="button" className={selected === choice.id ? "selected" : ""} aria-pressed={selected === choice.id} key={choice.id} onClick={() => setSelected(choice.id)}><span className="onboarding-option-icon">{choice.id === "none" ? <Database size={19} /> : <ShieldCheck size={19} />}</span><span className="onboarding-choice-copy"><strong>{choice.name}</strong>{choice.category && <small>{choice.category} · {choice.intensity}</small>}<p>{choice.description}</p></span><span className="onboarding-radio"><span /></span></button>)}</div>;
}

function ConnectStep({ dnsAddress, suffix, providers, transport, protection }: { dnsAddress: string; suffix: string; providers: string[]; transport: "encrypted" | "standard"; protection: string }) {
  return <div className="onboarding-review"><section><div className="onboarding-connect-heading"><Router size={20} /><div><strong>Point your router or device at Faro</strong><p>Use Faro as the DNS server distributed by DHCP.</p></div></div><div className="dns-address-display"><span>DNS server</span><strong>{dnsAddress}</strong><code>Port 53 · UDP and TCP</code></div><div className="onboarding-command"><span>Test after setup</span><code>nslookup example.com {dnsAddress}</code></div></section><aside><h2>Configuration review</h2><ReviewRow label="LAN address" value={dnsAddress} /><ReviewRow label="Local suffix" value={`.${suffix}`} /><ReviewRow label="Upstreams" value={providers.join(", ")} /><ReviewRow label="DNS privacy" value={transport === "encrypted" ? "Encrypted with HTTPS" : "Standard DNS"} /><ReviewRow label="Starter protection" value={protection} /><ReviewRow label="DNS cache" value="300 seconds" /></aside></div>;
}

function ReviewRow({ label, value }: { label: string; value: string }) {
  return <div><span>{label}</span><strong>{value}</strong></div>;
}

function hydrateSettings(settings: Setting[], setSuffix: (value: string) => void, setLanAddress: (value: string) => void, setCache: (value: boolean) => void, setProfiles: (value: string[]) => void, setTransport: (value: "encrypted" | "standard") => void) {
  const values = Object.fromEntries(settings.map((setting) => [setting.key, setting.value]));
  if (values.local_domain_suffix) setSuffix(values.local_domain_suffix);
  if (values.faro_lan_ip) setLanAddress(values.faro_lan_ip);
  setCache(values.dns_cache_enabled !== "false");
  if (values.upstream_transport === "encrypted") setTransport("encrypted");
  const configured = new Set((values.upstream_dns || "").split(",").map((value) => value.trim()));
  const matching = onboardingProfiles.filter(({ profile }) => profile.addresses.some((address) => configured.has(address))).map(({ profile }) => profile.id);
  if (matching.length) setProfiles(matching);
}

function validSuffix(value: string) {
  return /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/i.test(value.trim());
}

function validIPAddress(value: string) {
  const parts = value.trim().split(".");
  if (parts.length === 4 && parts.every((part) => /^\d{1,3}$/.test(part) && Number(part) <= 255)) {
    return value.trim() !== "0.0.0.0" && !value.trim().startsWith("127.");
  }
  return value.includes(":") && /^[0-9a-f:]+$/i.test(value) && value !== "::" && value !== "::1";
}

function detectedLANAddress(hostname: string) {
  return validIPAddress(hostname) ? hostname : "";
}

function normalizeURL(value: string) {
  return value.trim().replace(/\/+$/, "").toLowerCase();
}

function encryptedEndpointIndex(endpoints: EncryptedUpstreamEndpoint[]) {
  const index = new Map<string, EncryptedUpstreamEndpoint>();
  endpoints.forEach((endpoint) => endpoint.bootstrap_ips.forEach((address) => index.set(address, endpoint)));
  return index;
}

function encryptedEndpointForAddresses(addresses: string[], index: Map<string, EncryptedUpstreamEndpoint>) {
  const endpoints = addresses.map((address) => index.get(address));
  const first = endpoints[0];
  if (!first || endpoints.some((endpoint) => endpoint?.url !== first.url)) return null;
  return first;
}

function errorMessage(caught: unknown) {
  return caught instanceof Error ? caught.message : "Could not apply the onboarding configuration.";
}

function stepHeading(step: number) {
  return ["Set your local network defaults", "Choose upstream DNS", "Choose starter protection", "Connect your network"][step];
}

function stepIntro(step: number) {
  return ["These defaults keep local service names consistent and repeated lookups fast.", "Faro can use multiple resolvers for resilient public lookups.", "Start with a light list, a privacy-focused list, or DNS visibility only.", "Review the configuration, then point a device or router at Faro."][step];
}
