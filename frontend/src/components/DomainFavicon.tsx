import { useEffect, useState } from "react";

type DomainFaviconProps = {
  readonly domain: string;
};

const publicDomainPattern = /^[a-z0-9][a-z0-9.-]*\.[a-z]{2,}$/;

function canFetchFavicon(domain: string) {
  const normalized = domain.trim().toLowerCase().replace(/\.$/, "");
  if (normalized.length > 253 || !publicDomainPattern.test(normalized)) {
    return false;
  }

  const labels = normalized.split(".");
  if (!labels.every((label) =>
    label.length > 0 &&
    label.length <= 63 &&
    !label.startsWith("-") &&
    !label.endsWith("-")
  )) {
    return false;
  }

  let repeatedLabels = 1;
  for (let index = 1; index < labels.length; index += 1) {
    repeatedLabels = labels[index] === labels[index - 1] ? repeatedLabels + 1 : 1;
    if (repeatedLabels >= 3) {
      return false;
    }
  }

  return !normalized.endsWith(".home") &&
    !normalized.endsWith(".lan") &&
    !normalized.endsWith(".local");
}

export function DomainFavicon({ domain }: DomainFaviconProps) {
  const [imageSrc, setImageSrc] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);
  const initial = domain.slice(0, 1).toUpperCase();
  const canFetch = canFetchFavicon(domain);

  useEffect(() => {
    setImageSrc(null);
    setFailed(false);

    if (!canFetch) {
      return undefined;
    }

    const controller = new AbortController();
    let objectUrl: string | null = null;

    fetch(`/api/favicons/${encodeURIComponent(domain)}`, { signal: controller.signal })
      .then(async (response) => {
        if (!response.ok) {
          throw new Error(`Favicon unavailable for ${domain}`);
        }
        if (response.headers.get("X-Faro-Favicon") === "placeholder") {
          return null;
        }
        return response.blob();
      })
      .then((blob) => {
        if (!blob) {
          setFailed(true);
          return;
        }
        objectUrl = URL.createObjectURL(blob);
        setImageSrc(objectUrl);
      })
      .catch((error: unknown) => {
        if (error instanceof DOMException && error.name === "AbortError") {
          return;
        }
        setFailed(true);
      });

    return () => {
      controller.abort();
      if (objectUrl) {
        URL.revokeObjectURL(objectUrl);
      }
    };
  }, [canFetch, domain]);

  if (failed || !canFetch || !imageSrc) {
    return <span className="favicon-placeholder">{initial}</span>;
  }

  return (
    <span className="favicon-frame">
      <img alt="" loading="lazy" src={imageSrc} onError={() => setFailed(true)} />
      <span>{initial}</span>
    </span>
  );
}
