const defaultFaroPort = "1787";
const schemePattern = /^[a-z][a-z\d+\-.]*:\/\//i;

export function normalizeControllerAddress(value: string) {
  const trimmed = value.trim();
  if (!trimmed) return "";

  const candidate = withDefaultScheme(trimmed);
  try {
    const address = new URL(candidate);
    if (!hasExplicitPort(candidate)) address.port = defaultFaroPort;
    return address.toString().replace(/\/$/, "");
  } catch {
    return trimmed;
  }
}

function withDefaultScheme(value: string) {
  if (schemePattern.test(value)) return value;
  if (isBareIPv6Address(value)) return `http://[${value}]`;
  return `http://${value}`;
}

function isBareIPv6Address(value: string) {
  return value.includes(":") && !value.startsWith("[") && /^[\da-f:]+$/i.test(value);
}

function hasExplicitPort(value: string) {
  const authority = value.replace(schemePattern, "").split(/[/?#]/, 1)[0];
  if (authority.startsWith("[")) return /^\[[^\]]+\]:\d+$/.test(authority);
  return /:\d+$/.test(authority);
}
