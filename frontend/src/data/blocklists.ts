export type CatalogBlocklist = {
  id: string;
  name: string;
  provider: string;
  description: string;
  category: "Balanced" | "Privacy" | "Strict" | "Security" | "Classic";
  intensity: string;
  tags: string[];
  url: string;
  recommended?: boolean;
};

export const blocklistCatalog: CatalogBlocklist[] = [
  { id: "oisd-small", name: "OISD Small", provider: "OISD", description: "A conservative all-purpose list designed to minimize site breakage.", category: "Balanced", intensity: "Light", tags: ["Ads", "Tracking"], url: "https://small.oisd.nl/", recommended: true },
  { id: "oisd-big", name: "OISD Big", provider: "OISD", description: "Broader coverage for ads, trackers, malware, and telemetry.", category: "Privacy", intensity: "Strong", tags: ["Ads", "Malware", "Telemetry"], url: "https://big.oisd.nl/" },
  { id: "hagezi-light", name: "HaGeZi Light", provider: "HaGeZi", description: "Light protection for networks where compatibility is the priority.", category: "Balanced", intensity: "Light", tags: ["Ads", "Tracking"], url: "https://raw.githubusercontent.com/hagezi/dns-blocklists/main/hosts/light.txt" },
  { id: "hagezi-normal", name: "HaGeZi Normal", provider: "HaGeZi", description: "All-round privacy protection for most home networks.", category: "Privacy", intensity: "Balanced", tags: ["Ads", "Tracking", "Telemetry"], url: "https://raw.githubusercontent.com/hagezi/dns-blocklists/main/hosts/multi.txt", recommended: true },
  { id: "hagezi-pro", name: "HaGeZi Pro", provider: "HaGeZi", description: "Extended blocking with stronger privacy and badware coverage.", category: "Privacy", intensity: "Strong", tags: ["Ads", "Badware", "Scams"], url: "https://raw.githubusercontent.com/hagezi/dns-blocklists/main/hosts/pro.txt" },
  { id: "hagezi-ultimate", name: "HaGeZi Ultimate", provider: "HaGeZi", description: "Aggressive all-in-one filtering for users comfortable troubleshooting exceptions.", category: "Strict", intensity: "Maximum", tags: ["Ads", "Malware", "Scams"], url: "https://raw.githubusercontent.com/hagezi/dns-blocklists/main/hosts/ultimate.txt" },
  { id: "hagezi-tif", name: "HaGeZi Threat Intelligence", provider: "HaGeZi", description: "Focused threat intelligence feed intended to complement a general list.", category: "Security", intensity: "Supplemental", tags: ["Malware", "Phishing", "Threats"], url: "https://raw.githubusercontent.com/hagezi/dns-blocklists/main/hosts/tif.txt", recommended: true },
  { id: "stevenblack-unified", name: "StevenBlack Unified", provider: "StevenBlack", description: "Long-running consolidated hosts list for ads and malware.", category: "Classic", intensity: "Balanced", tags: ["Ads", "Malware"], url: "https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts" },
  { id: "stevenblack-social", name: "StevenBlack + Social", provider: "StevenBlack", description: "The unified list with social-network domains added.", category: "Strict", intensity: "Strong", tags: ["Ads", "Malware", "Social"], url: "https://raw.githubusercontent.com/StevenBlack/hosts/master/alternates/social/hosts" },
  { id: "adaway", name: "AdAway Hosts", provider: "AdAway", description: "A compact community-maintained list focused on mobile and web advertising.", category: "Classic", intensity: "Light", tags: ["Ads", "Mobile"], url: "https://adaway.org/hosts.txt" },
  { id: "onehosts-lite", name: "1Hosts Lite", provider: "1Hosts", description: "Lightweight ad and tracker blocking with a compatibility-first profile.", category: "Balanced", intensity: "Light", tags: ["Ads", "Tracking"], url: "https://o0.pages.dev/Lite/hosts.txt" },
  { id: "urlhaus", name: "URLhaus Malware", provider: "abuse.ch", description: "Active malware-distribution hostnames from the URLhaus project.", category: "Security", intensity: "Supplemental", tags: ["Malware", "Threats"], url: "https://urlhaus.abuse.ch/downloads/hostfile/" },
  { id: "phishing-army", name: "Phishing Army Extended", provider: "Phishing Army", description: "Focused protection against active phishing and credential-theft domains.", category: "Security", intensity: "Supplemental", tags: ["Phishing", "Scams"], url: "https://phishing.army/download/phishing_army_blocklist_extended.txt" }
];
