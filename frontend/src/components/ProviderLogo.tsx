import { useState } from "react";

const providerAssets: Record<string, string> = {
  adguard: "/providers/adguard.ico",
  cloudflare: "/providers/cloudflare.ico",
  comodo: "/providers/comodo.svg",
  google: "/providers/google.ico",
  level3: "/providers/level3.svg",
  opendns: "/providers/opendns.svg",
  quad9: "/providers/quad9.svg"
};

type ProviderLogoProps = {
  providerID: string;
  providerName: string;
};

export function ProviderLogo({ providerID, providerName }: ProviderLogoProps) {
  const [failed, setFailed] = useState(false);
  const source = providerAssets[providerID];

  if (!source || failed) {
    return <span className="provider-mark-fallback">{providerName.slice(0, 1).toUpperCase()}</span>;
  }

  return <img className="provider-mark" src={source} alt="" onError={() => setFailed(true)} />;
}
