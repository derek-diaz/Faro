const publicDomainPattern = /^[a-z0-9][a-z0-9.-]*\.[a-z]{2,}$/;
const localSuffixes = ["home", "lan", "local", "localhost", "invalid", "test", "example", "bind", "arpa"];
const cacheLifetime = 15 * 60 * 1000;
const maxCachedFavicons = 256;
const faviconCache = new Map<string, { result: Promise<Blob | null>; expiresAt: number }>();

export function normalizeFaviconDomain(domain: string) {
  return domain.trim().toLowerCase().replace(/\.$/, "");
}

export function canFetchFavicon(domain: string) {
  const normalized = normalizeFaviconDomain(domain);
  if (normalized.length > 253 || !publicDomainPattern.test(normalized)) return false;

  const labels = normalized.split(".");
  if (!labels.every((label) => label.length > 0 && label.length <= 63 && !label.startsWith("-") && !label.endsWith("-"))) return false;
  if (localSuffixes.includes(labels[labels.length - 1])) return false;

  let repeatedLabels = 1;
  for (let index = 1; index < labels.length; index += 1) {
    repeatedLabels = labels[index] === labels[index - 1] ? repeatedLabels + 1 : 1;
    if (repeatedLabels >= 3) return false;
  }
  return true;
}

export function loadFavicon(domain: string): Promise<Blob | null> {
  const normalized = normalizeFaviconDomain(domain);
  if (!canFetchFavicon(normalized)) return Promise.resolve(null);

  const cached = faviconCache.get(normalized);
  if (cached && cached.expiresAt > Date.now()) return cached.result;
  faviconCache.delete(normalized);

  // Share pending requests and cache misses as well as images across rows and
  // remounts. Each component owns its object URL, so eviction cannot break images.
  const entry = { result: Promise.resolve<Blob | null>(null), expiresAt: Infinity };
  entry.result = fetch(`/api/favicons/${encodeURIComponent(normalized)}`)
    .then((response) => {
      if (response.status === 204 || response.headers.get("Cache-Control") === "no-store") {
        if (faviconCache.get(normalized) === entry) faviconCache.delete(normalized);
      }
      if (!response.ok || response.status === 204 || response.headers.get("X-Faro-Favicon") === "placeholder") return null;
      return response.blob();
    })
    .catch(() => null)
    .then((blob) => {
      entry.expiresAt = Date.now() + cacheLifetime;
      return blob;
    });
  faviconCache.set(normalized, entry);
  if (faviconCache.size > maxCachedFavicons) {
    faviconCache.delete(faviconCache.keys().next().value!);
  }
  return entry.result;
}
