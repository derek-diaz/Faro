export type FilteringMode = "none" | "security" | "family" | "ads";

export type ResolverProfile = {
  id: string;
  name: string;
  description: string;
  addresses: string[];
  mode: FilteringMode;
  badges: string[];
  recommended?: boolean;
};

export type ResolverProvider = {
  id: string;
  name: string;
  domain: string;
  description: string;
  profiles: ResolverProfile[];
};

export const upstreamProviders: ResolverProvider[] = [
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

export function findUpstreamAddress(address: string) {
  for (const provider of upstreamProviders) {
    for (const profile of provider.profiles) {
      if (profile.addresses.includes(address)) return { provider, profile };
    }
  }
  return null;
}

export function allCatalogAddresses() {
  return Array.from(new Set(upstreamProviders.flatMap((provider) => provider.profiles.flatMap((profile) => profile.addresses))));
}

export function parseUpstreamServers(value: string) {
  return Array.from(new Set(value.split(",").map((server) => server.trim()).filter(Boolean)));
}
