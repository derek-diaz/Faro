import { useEffect, useState } from "react";
import { ChevronDown, RefreshCw, Server } from "lucide-react";
import { api, type DashboardSummary, type EncryptedUpstreamEndpoint, type Setting, type UpstreamProbe } from "../api/client";
import { encryptedEndpointIndex, findUpstreamAddress, parseUpstreamServers } from "../data/upstreams";
import { formatTime } from "../utils/dateFormatting";
import { formatLatency, formatNumber } from "../utils/formatting";
import { ProviderLogo } from "./ProviderLogo";
import { Button } from "./ui/button";

type ProviderGroup = {
  name: string;
  providerID?: string;
  endpoint?: string;
  addresses: string[];
};

function groupProviders(addresses: string[], endpoints: EncryptedUpstreamEndpoint[]) {
  const encrypted = encryptedEndpointIndex(endpoints);
  const groups = new Map<string, ProviderGroup>();
  for (const address of addresses) {
    const match = findUpstreamAddress(address);
    const endpoint = encrypted.get(address);
    const key = endpoint?.url ?? match?.profile.id ?? address;
    const group = groups.get(key) ?? {
      name: endpoint?.name ?? match?.provider.name ?? "Custom provider",
      providerID: match?.provider.id,
      endpoint: endpoint?.url,
      addresses: []
    };
    group.addresses.push(address);
    groups.set(key, group);
  }
  return [...groups.values()];
}

export function DashboardProviders({ summary, settings, onManage }: {
  summary: DashboardSummary;
  settings: Setting[];
  onManage: () => void;
}) {
  const addresses = parseUpstreamServers(settings.find((item) => item.key === "upstream_dns")?.value ?? "");
  const transport = settings.some((item) => item.key === "upstream_transport" && item.value === "encrypted") ? "encrypted" : "standard";
  const [endpoints, setEndpoints] = useState<EncryptedUpstreamEndpoint[]>([]);
  const [manualProbes, setManualProbes] = useState<UpstreamProbe[]>([]);
  const [checking, setChecking] = useState(false);
  const [error, setError] = useState("");
  const [disclosure, setDisclosure] = useState<{ incident: string; open: boolean } | null>(null);

  useEffect(() => {
    let active = true;
    if (transport === "encrypted") {
      void api.upstreamCatalog().then((catalog) => {
        if (active) setEndpoints(catalog.encrypted_endpoints);
      }).catch(() => {
        // Configured addresses remain available if the provider catalog is offline.
      });
    }
    return () => { active = false; };
  }, [transport]);

  const probes = new Map<string, UpstreamProbe>();
  for (const probe of [...(summary.upstream_probes ?? []), ...manualProbes]) {
    const previous = probes.get(probe.address);
    if (!previous || Date.parse(probe.checked_at) >= Date.parse(previous.checked_at)) probes.set(probe.address, probe);
  }
  const failing = addresses.filter((address) => probes.get(address)?.status === "unavailable");
  const hasFailure = (group: ProviderGroup) => group.addresses.some((address) => failing.includes(address));
  const groups = groupProviders(addresses, transport === "encrypted" ? endpoints : [])
    .sort((left, right) => Number(hasFailure(right)) - Number(hasFailure(left)));
  const incident = failing.join(",") || (["critical", "degraded"].includes(summary.upstream_health_status) ? summary.upstream_health_status : "");
  const open = disclosure?.incident === incident ? disclosure.open : !!incident;
  const responding = groups.filter((group) => group.addresses.some((address) => probes.get(address)?.status === "online")).length;
  const checked = addresses.some((address) => probes.has(address));
  let providerStatus = "Awaiting checks";
  if (checked) {
    providerStatus = responding === groups.length ? `${responding} responding` : `${responding} of ${groups.length} responding`;
    if (failing.length && responding === groups.length) providerStatus += " · partial outage";
  }
  const checkedAt = [...probes.values()].reduce((latest, probe) =>
    !latest || Date.parse(probe.checked_at) > Date.parse(latest) ? probe.checked_at : latest, summary.upstream_checked_at);

  async function refresh() {
    setChecking(true);
    setError("");
    try {
      const result = await api.probeUpstreams(addresses, transport);
      setManualProbes(result.items);
    } catch {
      setError("Couldn’t check the providers. Try again.");
    } finally {
      setChecking(false);
    }
  }

  return (
    <section className="panel home-providers" aria-labelledby="home-providers-title">
      <h2 id="home-providers-title">
        <button type="button" className="home-provider-disclosure" aria-expanded={open} aria-controls="home-provider-details" onClick={() => setDisclosure({ incident, open: !open })}>
          <Server size={17} aria-hidden="true" /><span>DNS providers</span>
          <small>{providerStatus}</small>
          <ChevronDown size={17} aria-hidden="true" />
        </button>
      </h2>
      <div id="home-provider-details" hidden={!open}>
        {groups.map((group) => (
          <div className="home-provider" key={group.addresses[0]}>
            {group.providerID ? <ProviderLogo providerID={group.providerID} providerName={group.name} /> : <Server size={18} aria-hidden="true" />}
            <div><strong>{group.name}</strong>{group.endpoint && <code>{group.endpoint}</code>}
              {group.addresses.map((address) => {
                const probe = probes.get(address);
                return <div className="home-provider-address" key={address}><code>{address}</code>
                  <span className={probe?.status === "unavailable" ? "unavailable" : ""}>{probe?.status === "unavailable" ? "Unavailable" : probe?.latency_ms != null ? `${formatLatency(probe.latency_ms)} ms` : "Awaiting check"}</span></div>;
              })}
            </div>
          </div>
        ))}
        <div className="home-cache-detail">
          <strong>DNS cache</strong>
          <span>{summary.cache.enabled ? `${summary.cache.hit_rate_today.toFixed(1)}% served from cache today · ${formatNumber(summary.cache.hits_today)} upstream calls avoided` : "Off"}</span>
        </div>
        {checkedAt && <p>Last checked {formatTime(checkedAt)}</p>}
        {error && <p role="alert">{error}</p>}
        <div className="home-provider-actions">
          <Button variant="outline" disabled={checking || !addresses.length} onClick={() => void refresh()}><RefreshCw size={15} />{checking ? "Checking…" : "Check again"}</Button>
          <Button variant="link" onClick={onManage}>Manage providers</Button>
        </div>
      </div>
    </section>
  );
}
