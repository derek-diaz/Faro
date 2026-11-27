import type { CatalogBlocklist } from "./blocklists";

// Packettale publishes feed paths and uncompressed sizes in /stats.json.
// Use the domains variant where available. Some domain-only groups publish a single TXT file.
type Feed = [file: string, name: string, description: string, caution?: string];
type Section = Pick<CatalogBlocklist, "category" | "compatibility" | "bestFor"> & {
  topic: string;
  feeds: Feed[];
};

const sections: Section[] = [
  {
    category: "Everyday", compatibility: "Advanced", topic: "Bundle",
    bestFor: "Networks with room for a large combined list",
    feeds: [
      ["protection.domains.txt", "Protection bundle", "Combines Packettale’s protection categories into one subscription.", "Contains millions of domains. Installation and DNS reloads can take longer on small servers."],
    ],
  },
  {
    category: "Everyday", compatibility: "Balanced", topic: "Ads and security",
    bestFor: "Households that want one general-purpose list",
    feeds: [
      ["mixed.domains.txt", "Ads, trackers & threats", "Combines broad upstream lists, including OISD Small and HaGeZi Normal. If you use those lists already, expect overlap."],
    ],
  },
  {
    category: "Privacy", compatibility: "Balanced", topic: "Ads & tracking",
    bestFor: "Choosing which kinds of web tracking to block",
    feeds: [
      ["groups/ads-tracking.domains.txt", "Ads & tracking bundle", "Combines the advertising, analytics, fingerprinting and other tracking categories."],
      ["ads.domains.txt", "Advertising", "Blocks domains used to deliver advertising."],
      ["analytics.domains.txt", "Analytics", "Blocks domains that collect website and app usage statistics."],
      ["tracking.domains.txt", "Tracking", "Blocks domains used to track visitors across sites."],
      ["telemetry.domains.txt", "Telemetry", "Blocks endpoints that receive device and software usage reports."],
      ["fingerprinting.domains.txt", "Fingerprinting", "Blocks services that identify visitors from browser or device characteristics."],
      ["email-tracking.domains.txt", "Email tracking", "Blocks tracking domains used to measure email opens and clicks."],
      ["cname-tracking.domains.txt", "CNAME-cloaked trackers", "Blocks known site-specific hostnames that point to tracking services."],
      ["cname-ads.domains.txt", "CNAME-cloaked ads", "Blocks known site-specific hostnames that point to advertising services."],
      ["popup-ads.domains.txt", "Pop-up ads", "Blocks domains used by pop-up advertising networks."],
    ],
  },
  {
    category: "Security", compatibility: "Balanced", topic: "Threats",
    bestFor: "Adding protection against a specific threat",
    feeds: [
      ["groups/threats.domains.txt", "Malware & threats bundle", "Combines Packettale’s threat categories, including phishing, scams and malware.", "Large combined list. It overlaps with the individual threat feeds."],
      ["security-threats.domains.txt", "Threat intelligence", "Blocks domains identified by upstream threat intelligence sources.", "Contains over a million domains and can take time to install."],
      ["malware.domains.txt", "Malware", "Blocks domains associated with malware distribution and infrastructure."],
      ["phishing.domains.txt", "Phishing", "Blocks domains reported for impersonation and credential theft."],
      ["fraud.domains.txt", "Fraud & scams", "Blocks domains associated with fraudulent offers and scam sites."],
      ["cryptomining.domains.txt", "Cryptojacking", "Blocks domains used to mine cryptocurrency on visitors’ devices."],
      ["stalkerware.domains.txt", "Stalkerware", "Blocks known domains used by software that covertly monitors devices."],
      ["typosquatting.domains.txt", "Typosquatting", "Blocks lookalike domains that exploit common typing mistakes."],
    ],
  },
  {
    category: "Security", compatibility: "Advanced", topic: "Domain risk",
    bestFor: "Managed networks with reviewed exceptions",
    feeds: [
      ["badware-hosting.domains.txt", "High-risk hosting", "Blocks hosting domains associated with abuse.", "Legitimate sites on shared hosts can also be affected."],
      ["dga.domains.txt", "Algorithmically generated domains", "Targets newly observed domain names classified as machine-generated.", "This large feed can include legitimate domains. Allow extra memory and time for reloads."],
      ["risk.domains.txt", "Monitoring & risk policies", "Applies upstream policies for domains flagged for monitoring or elevated risk.", "A risk classification can include legitimate services."],
      ["malware-and-support.domains.txt", "Mixed security policies", "Combines broader upstream security policies covering malware and supporting services.", "Review blocked services before applying this list network-wide."],
    ],
  },
  {
    category: "Privacy", compatibility: "Balanced", topic: "Device trackers",
    bestFor: "Protection profiles for particular devices or apps",
    feeds: [
      ["groups/device-trackers.txt", "Device & app trackers bundle", "Combines tracker categories across manufacturers, streaming devices and apps."],
      ["trackers-amazon.domains.txt", "Amazon trackers", "Filters tracking endpoints associated with Amazon devices and apps."],
      ["trackers-apple.domains.txt", "Apple trackers", "Filters tracking endpoints associated with Apple devices and apps."],
      ["trackers-huawei.domains.txt", "Huawei trackers", "Filters tracking endpoints associated with Huawei devices."],
      ["trackers-lg.domains.txt", "LG webOS trackers", "Filters tracking endpoints associated with LG webOS TVs."],
      ["trackers-microsoft.domains.txt", "Microsoft trackers", "Filters tracking endpoints associated with Microsoft software."],
      ["trackers-oppo-realme.domains.txt", "OPPO & Realme trackers", "Filters tracking endpoints associated with OPPO and Realme phones."],
      ["trackers-roku.domains.txt", "Roku trackers", "Filters tracking endpoints associated with Roku streaming devices."],
      ["trackers-samsung.domains.txt", "Samsung trackers", "Filters tracking endpoints associated with Samsung devices."],
      ["trackers-tiktok.domains.txt", "TikTok trackers", "Targets tracking endpoints associated with TikTok."],
      ["trackers-vivo.domains.txt", "Vivo trackers", "Filters tracking endpoints associated with Vivo phones."],
      ["trackers-xiaomi.domains.txt", "Xiaomi trackers", "Filters tracking endpoints associated with Xiaomi devices."],
      ["game-console-ads.domains.txt", "Game console ads", "Blocks advertising domains used on game consoles."],
      ["smart-tv.domains.txt", "Smart TV ads & tracking", "Blocks advertising and tracking domains used by smart TVs."],
    ],
  },
  {
    category: "Privacy", compatibility: "Balanced", topic: "Regional filters",
    bestFor: "People who regularly browse sites in this region or language",
    feeds: [
      ["groups/regional.domains.txt", "Regional filters bundle", "Combines Packettale’s regional advertising and tracking feeds."],
      ...regionalFeeds(),
    ],
  },
  {
    category: "Content", compatibility: "Advanced", topic: "Content controls",
    bestFor: "Devices with content restrictions",
    feeds: [
      ["groups/nsfw.txt", "Adult & violent content bundle", "Combines adult-content and graphic-violence categories."],
      ["adult.domains.txt", "Adult content", "Blocks domains categorized as pornography."],
      ["violence.domains.txt", "Graphic violence", "Blocks domains categorized as gore or graphic violence."],
      ["groups/site-restrictions.txt", "Site restrictions bundle", "Combines gambling, piracy and search engines without SafeSearch."],
      ["gambling.domains.txt", "Gambling", "Blocks domains associated with gambling and betting."],
      ["piracy.domains.txt", "Piracy", "Blocks domains categorized as distributing unauthorized copies of media or software."],
      ["no-safesearch.domains.txt", "Search without SafeSearch", "Blocks listed search providers that lack a SafeSearch option."],
    ],
  },
  {
    category: "Content", compatibility: "Advanced", topic: "Service restrictions",
    bestFor: "Devices where access to these services should be restricted",
    feeds: [
      ["groups/services.domains.txt", "Internet services bundle", "Combines service restrictions across social networks, hosted apps, remote access and other categories.", "Can disrupt sign-in, notifications and access to widely used services."],
      ["social.domains.txt", "Social networks", "Restricts access to social networking services."],
      ["dating.domains.txt", "Dating services", "Restricts access to online dating platforms."],
      ["big-tech-google.domains.txt", "Google services", "Restricts domains used by Google services, including shared infrastructure."],
      ["push-notifications.domains.txt", "Push notifications", "Blocks endpoints used to deliver app and browser notifications."],
      ["ai.domains.txt", "AI services", "Restricts domains used by AI tools and platforms."],
      ["apple-bloat.domains.txt", "Apple apps & services", "Restricts selected Apple application and service domains."],
      ["microsoft-bloat.domains.txt", "Microsoft apps & services", "Restricts selected Microsoft application and service domains."],
      ["chat-widgets.domains.txt", "Chat widgets", "Blocks embedded chat services, including website support chat."],
      ["cryptocurrency.domains.txt", "Cryptocurrency services", "Restricts access to cryptocurrency platforms and services."],
      ["fonts.domains.txt", "Web fonts", "Blocks remote font providers. Sites may display fallback fonts."],
      ["marketing.domains.txt", "Marketing services", "Blocks domains used by marketing platforms."],
      ["referral-tracking.domains.txt", "Referral & affiliate links", "Blocks referral and affiliate tracking domains. Some shopping links may stop opening."],
    ],
  },
  {
    category: "Network", compatibility: "Advanced", topic: "Network controls",
    bestFor: "Managed devices with network access policies",
    feeds: [
      ["bypass.domains.txt", "DNS filtering bypass services", "Blocks known domains for alternate DNS, VPN, Tor and proxy services."],
      ["dynamic-dns.domains.txt", "Dynamic DNS services", "Restricts dynamic DNS providers. Self-hosted services can become unreachable."],
      ["url-shorteners.domains.txt", "URL shorteners", "Blocks link-shortening services, including links used in messages and QR codes."],
      ["encrypted-dns.domains.txt", "Encrypted DNS providers", "Restricts hostnames associated with encrypted DNS resolvers."],
      ["dns-resolvers.domains.txt", "DNS resolver hostnames", "Blocks listed public DNS resolver hostnames."],
      ["free-hosting.domains.txt", "Free hosting services", "Restricts domains belonging to free hosting providers, including legitimate hosted sites."],
      ["remote-access.domains.txt", "Remote access", "Restricts domains used by remote desktop and support tools."],
      ["vpn-proxy.domains.txt", "VPN & proxy services", "Restricts known VPN and proxy service hostnames."],
    ],
  },
];

function regionalFeeds(): Feed[] {
  const regions = [
    ["no", "Nordic"], ["pl", "Polish"], ["ko", "Korean"], ["vn", "Vietnamese"],
    ["sv", "Swedish"], ["ir", "Persian"], ["mk", "Macedonian"], ["zh", "Chinese"],
    ["id", "Indonesian"], ["tr", "Turkish"], ["hu", "Hungarian"], ["lt", "Lithuanian"],
    ["he", "Hebrew"],
  ];
  return regions.map(([code, region]) => [
    `regional-${code}.domains.txt`, `${region} ads & tracking`,
    `Filters advertising and tracking domains from ${region} blocklist sources.`,
  ]);
}

export const packettaleBlocklists: CatalogBlocklist[] = sections.flatMap((section) =>
  section.feeds.map(([file, name, description, caution]) => ({
    id: "packettale-" + file.replace(/(?:\.domains)?\.txt$/, "").replace("/", "-"),
    name: "Packettale " + name,
    provider: "Packettale",
    url: "https://blocklist.packettale.com/" + file,
    category: section.category,
    compatibility: section.compatibility,
    bestFor: section.bestFor,
    tags: [section.topic],
    description,
    caution,
  })),
);
