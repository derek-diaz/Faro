import { AlertTriangle, Check, Plus, RotateCcw, Save, Server, ShieldCheck, X } from "lucide-react";
import { useEffect, useMemo, useState, type FormEvent } from "react";
import { api, type Setting } from "../api/client";
import { DomainFavicon } from "../components/DomainFavicon";

type UpstreamsProps = {
  settings: Setting[];
  refresh: () => Promise<void>;
};

type FilteringMode = "none" | "security" | "family" | "ads";

type ResolverProfile = {
  id: string;
  name: string;
  description: string;
  addresses: string[];
  mode: FilteringMode;
  badges: string[];
  recommended?: boolean;
};

type ResolverProvider = {
  id: string;
  name: string;
  domain: string;
  description: string;
  profiles: ResolverProfile[];
};

const providers: ResolverProvider[] = [
  {
    id: "cloudflare",
    name: "Cloudflare",
    domain: "cloudflare.com",
    description: "Fast, privacy-focused public DNS with optional malware and family filtering.",
    profiles: [
      { id: "cloudflare-standard", name: "Standard", description: "Fast DNS without content filtering.", addresses: ["1.1.1.1", "1.0.0.1"], mode: "none", badges: ["Private", "Unfiltered"], recommended: true },
      { id: "cloudflare-malware", name: "Malware blocking", description: "Blocks known malware and phishing domains.", addresses: ["1.1.1.2", "1.0.0.2"], mode: "security", badges: ["Security"] },
      { id: "cloudflare-family", name: "Family", description: "Blocks malware and adult content.", addresses: ["1.1.1.3", "1.0.0.3"], mode: "family", badges: ["Security", "Family"] }
    ]
  },
  {
    id: "google",
    name: "Google Public DNS",
    domain: "google.com",
    description: "Global public resolver focused on speed, security, and accurate DNS answers.",
    profiles: [
      { id: "google-standard", name: "Standard", description: "Reliable DNS without general content filtering.", addresses: ["8.8.8.8", "8.8.4.4"], mode: "none", badges: ["Global", "Unfiltered"], recommended: true }
    ]
  },
  {
    id: "quad9",
    name: "Quad9",
    domain: "quad9.net",
    description: "Privacy-first DNS with DNSSEC and optional threat blocking.",
    profiles: [
      { id: "quad9-secure", name: "Secure", description: "Blocks known malicious domains and validates DNSSEC.", addresses: ["9.9.9.9", "149.112.112.112"], mode: "security", badges: ["Malware blocking", "DNSSEC"], recommended: true },
      { id: "quad9-unfiltered", name: "No threat blocking", description: "Privacy-focused resolution without threat blocking.", addresses: ["9.9.9.10", "149.112.112.10"], mode: "none", badges: ["Private", "Unfiltered"] },
      { id: "quad9-ecs", name: "Secure + ECS", description: "Threat blocking with ECS for improved CDN location responses.", addresses: ["9.9.9.11", "149.112.112.11"], mode: "security", badges: ["Malware blocking", "ECS"] }
    ]
  },
  {
    id: "adguard",
    name: "AdGuard DNS",
    domain: "adguard-dns.io",
    description: "Public DNS with built-in ad, tracker, and family filtering options.",
    profiles: [
      { id: "adguard-default", name: "Default", description: "Blocks ads and trackers at the DNS layer.", addresses: ["94.140.14.14", "94.140.15.15"], mode: "ads", badges: ["Ads", "Trackers"], recommended: true },
      { id: "adguard-unfiltered", name: "Non-filtering", description: "AdGuard infrastructure without content filtering.", addresses: ["94.140.14.140", "94.140.14.141"], mode: "none", badges: ["Unfiltered"] },
      { id: "adguard-family", name: "Family protection", description: "Blocks ads, trackers, adult content, and enables Safe Search where possible.", addresses: ["94.140.14.15", "94.140.15.16"], mode: "family", badges: ["Ads", "Family", "Safe Search"] }
    ]
  }
];

export function Upstreams({ settings, refresh }: UpstreamsProps) {
  const configured = useMemo(() => parseServers(settings.find((setting) => setting.key === "upstream_dns")?.value ?? ""), [settings]);
  const [selected, setSelected] = useState<string[]>(configured);
  const [customInput, setCustomInput] = useState("");
  const [customError, setCustomError] = useState("");
  const [saveState, setSaveState] = useState<"idle" | "saving" | "saved" | "error">("idle");
  const [message, setMessage] = useState("");

  useEffect(() => {
    setSelected(configured);
  }, [configured.join(",")]);

  const selectedSet = useMemo(() => new Set(selected), [selected]);
  const dirty = selected.join(",") !== configured.join(",");
  const selectedProfiles = providers.flatMap((provider) => provider.profiles.filter((profile) => profile.addresses.some((address) => selectedSet.has(address))));
  const mixesFiltering = selectedProfiles.some((profile) => profile.mode === "none") && selectedProfiles.some((profile) => profile.mode !== "none");

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
    const candidates = parseServers(customInput.replace(/\s+/g, ","));
    if (candidates.length === 0 || candidates.some((server) => !isIPAddress(server))) {
      setCustomError("Enter valid IPv4 or IPv6 addresses separated by commas or spaces.");
      return;
    }
    setSelected((current) => unique([...current, ...candidates]));
    setCustomInput("");
    setCustomError("");
    setSaveState("idle");
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
      await api.updateSettings({ upstream_dns: selected.join(",") });
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
        <section className="upstream-intro-strip">
          <div>
            <strong>Plain DNS upstreams</strong>
            <span>Choose one or more profiles. Each profile adds both provider addresses for redundancy.</span>
          </div>
          <span className="configured-count"><Server size={16} /> {selected.length} server{selected.length === 1 ? "" : "s"} selected</span>
        </section>

        {mixesFiltering && (
          <div className="upstream-warning">
            <AlertTriangle size={18} />
            <div>
              <strong>Mixed filtering policies</strong>
              <span>CoreDNS may use any selected server. Combining filtered and unfiltered profiles can make provider-level blocking inconsistent.</span>
            </div>
          </div>
        )}

        <div className="provider-list">
          {providers.map((provider) => (
            <section className="provider-panel" key={provider.id}>
              <header className="provider-header">
                <div className="provider-identity">
                  <span className="provider-logo"><DomainFavicon domain={provider.domain} /></span>
                  <div>
                    <h2>{provider.name}</h2>
                    <p>{provider.description}</p>
                  </div>
                </div>
                <span>{provider.profiles.length} option{provider.profiles.length === 1 ? "" : "s"}</span>
              </header>

              <div className="resolver-profile-list">
                {provider.profiles.map((profile) => {
                  const selectedCount = profile.addresses.filter((address) => selectedSet.has(address)).length;
                  const fullySelected = selectedCount === profile.addresses.length;
                  const partiallySelected = selectedCount > 0 && !fullySelected;
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
                          {profile.recommended && <em>Recommended</em>}
                        </span>
                        <span>{profile.description}</span>
                        <span className="profile-addresses">{profile.addresses.join(" · ")}</span>
                      </span>
                      <span className="profile-badges">{profile.badges.map((badge) => <span key={badge}>{badge}</span>)}</span>
                    </button>
                  );
                })}
              </div>
            </section>
          ))}
        </div>

        <section className="panel custom-upstream-panel">
          <div className="panel-title dashboard-panel-title">
            <div>
              <h2>Custom resolvers</h2>
              <p>Add plain DNS servers that are not in the provider catalog.</p>
            </div>
          </div>
          <form className="custom-upstream-form" onSubmit={addCustomServers}>
            <input value={customInput} onChange={(event) => setCustomInput(event.target.value)} placeholder="192.0.2.53 or 2001:db8::53" aria-label="Custom DNS server addresses" />
            <button type="submit"><Plus size={16} /><span>Add servers</span></button>
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
          <span className="selection-status"><ShieldCheck size={15} /> Ready</span>
        </div>

        <div className="selected-server-list">
          {selected.length === 0 ? (
            <div className="selection-empty">Choose a provider profile or add a custom resolver.</div>
          ) : selected.map((server, index) => {
            const match = findAddress(server);
            return (
              <div className="selected-server" key={server}>
                <span className="server-order">{index + 1}</span>
                <div>
                  <strong>{server}</strong>
                  <span>{match ? `${match.provider.name} · ${match.profile.name}` : "Custom resolver"}</span>
                </div>
                <button className="icon-button" type="button" onClick={() => setSelected((current) => current.filter((address) => address !== server))} aria-label={`Remove ${server}`}><X size={15} /></button>
              </div>
            );
          })}
        </div>

        <div className="selection-note">
          <strong>How selection works</strong>
          <span>CoreDNS forwards requests across these servers. Faro blocklists and manual rules are applied before forwarding.</span>
        </div>

        <div className="selection-actions">
          <button type="button" className="secondary" disabled={!dirty} onClick={() => setSelected(configured)}><RotateCcw size={16} /><span>Reset</span></button>
          <button type="button" disabled={!dirty || saveState === "saving" || selected.length === 0} onClick={() => void save()}><Save size={16} /><span>{saveState === "saving" ? "Saving" : "Save upstreams"}</span></button>
        </div>
        {message && <span className={`selection-message ${saveState === "error" ? "error" : ""}`}>{message}</span>}
      </aside>
    </div>
  );
}

function parseServers(value: string) {
  return unique(value.split(",").map((server) => server.trim()).filter(Boolean));
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

function findAddress(address: string) {
  for (const provider of providers) {
    for (const profile of provider.profiles) {
      if (profile.addresses.includes(address)) return { provider, profile };
    }
  }
  return null;
}
