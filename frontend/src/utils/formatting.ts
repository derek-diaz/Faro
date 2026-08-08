export function formatNumber(value: number) {
  return new Intl.NumberFormat().format(value);
}

export function formatLatency(value: number) {
  if (value >= 100) return Math.round(value).toString();
  const decimals = value >= 10 ? 0 : 1;
  return value.toFixed(decimals);
}

export function latencyTone(value: number) {
  if (value < 40) return "fast";
  if (value < 100) return "moderate";
  return "slow";
}

export function normalizeURL(value: string) {
  const trimmed = value.trim();
  let end = trimmed.length;
  while (end > 0 && trimmed[end - 1] === "/") end -= 1;
  return trimmed.slice(0, end).toLowerCase();
}

export function errorMessage(caught: unknown, fallback: string) {
  return caught instanceof Error && caught.message ? caught.message : fallback;
}
