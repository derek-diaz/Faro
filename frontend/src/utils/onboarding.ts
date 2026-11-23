type ClientQuery = { timestamp: string; client_ip: string; domain: string };

export function starterListScopeError(
  list: { id: number; enabled: boolean; entry_count: number; protection_count?: number },
  profiles: Array<{ id: number; name: string; blocklist_ids: number[] }>,
  homeID: number
): string {
  if (list.enabled && list.entry_count > 0) return "";
  const users = profiles.filter((profile) => profile.blocklist_ids.includes(list.id));
  const otherUsers = users.filter((profile) => profile.id !== homeID);
  if (otherUsers.length) {
    return `This starter list is also used by ${otherUsers.map((profile) => profile.name).join(", ")}. Enabling or downloading it would change those protections too. Manage this list in Protection, or choose another starter list in Customize.`;
  }
  if ((list.protection_count ?? 0) > users.length) {
    return "Faro could not confirm every protection using this starter list. Manage the list in Protection, or choose another starter list in Customize.";
  }
  return "";
}

export function validSuffix(value: string) {
  return /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/i.test(value.trim());
}

export function normalizeLANAddress(value: string): string {
  const input = value.trim().replace(/^\[|\]$/g, "");
  if (!input.includes(":")) {
    const parts = input.split(".");
    if (parts.length !== 4 || parts.some((part) => !/^(0|[1-9]\d{0,2})$/.test(part) || Number(part) > 255)) return "";
    if (Number(parts[0]) === 0 || Number(parts[0]) === 127 || Number(parts[0]) >= 224) return "";
    return input;
  }
  try {
    const address = new URL(`http://[${input}]/`).hostname.slice(1, -1);
    if (address === "::" || address === "::1" || address.startsWith("ff")) return "";
    if (address.startsWith("::ffff:")) {
      const words = address.slice(7).split(":");
      if (words.length !== 2) return "";
      const first = Number.parseInt(words[0], 16);
      const second = Number.parseInt(words[1], 16);
      return normalizeLANAddress(`${first >> 8}.${first & 255}.${second >> 8}.${second & 255}`);
    }
    return address;
  } catch { return ""; }
}

export function recentClientQueries<T extends ClientQuery>(queries: T[], lanAddress: string, appliedAt: number, now = Date.now()): T[] {
  const faroAddress = normalizeLANAddress(lanAddress);
  const earliest = Math.max(appliedAt, now - 5 * 60 * 1000);
  return queries.filter((query) => {
    const address = normalizeLANAddress(query.client_ip);
    const timestamp = Date.parse(query.timestamp);
    const domain = query.domain.toLowerCase().replace(/\.$/, "");
    return !!address && address !== faroAddress && timestamp >= earliest && timestamp <= now + 1000 &&
      domain !== "version.bind" && domain !== "id.server" && !domain.endsWith(".invalid");
  });
}
