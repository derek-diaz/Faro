import { useEffect, useState } from "react";

type DomainFaviconProps = {
  domain: string;
};

export function DomainFavicon({ domain }: DomainFaviconProps) {
  const [imageSrc, setImageSrc] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);
  const initial = domain.slice(0, 1).toUpperCase();
  const canFetch = domain.includes(".") && !domain.endsWith(".home") && !domain.endsWith(".lan") && !domain.endsWith(".local");

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
