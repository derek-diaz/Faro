import { Database, Home, Server, ShieldX } from "lucide-react";
import { findUpstreamAddress } from "../data/upstreams";

type ResolutionSourceProps = {
  source: string;
  upstream?: string | null;
};

export function ResolutionSource({ source, upstream }: ResolutionSourceProps) {
  const normalized = source.toLowerCase();
  if (normalized === "cache") return <span className="resolution-source cache" title="Answered from Faro's local DNS cache"><Database size={13} />Cache</span>;
  if (normalized === "local") return <span className="resolution-source local" title="Answered by a Faro Local DNS record"><Home size={13} />Local DNS</span>;
  if (normalized === "blocklist") return <span className="resolution-source blocked" title="Answered locally by a blocklist rule"><ShieldX size={13} />Blocklist</span>;
  if (normalized === "manual") return <span className="resolution-source blocked" title="Answered locally by a manual domain block"><ShieldX size={13} />Manual rule</span>;
  if (normalized === "upstream") {
    const match = upstream ? findUpstreamAddress(upstream) : null;
    const label = match?.provider.name ?? "Upstream";
    return <span className="resolution-source upstream" title={upstream ? `Resolved through ${label} (${upstream})` : "Resolved through an upstream DNS provider"}><Server size={13} />{label}</span>;
  }
  const label = source.replace(/[_-]/g, " ").replace(/^./, (letter) => letter.toUpperCase());
  return <span className="resolution-source">{label}</span>;
}
