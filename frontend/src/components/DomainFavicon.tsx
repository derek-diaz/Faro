import { createContext, useContext, useEffect, useState } from "react";
import { canFetchFavicon, loadFavicon, normalizeFaviconDomain } from "../api/favicons";

export const FaviconEnabledContext = createContext(false);

type DomainFaviconProps = {
  readonly domain: string;
};

export function DomainFavicon({ domain }: DomainFaviconProps) {
  const enabled = useContext(FaviconEnabledContext);
  const normalized = normalizeFaviconDomain(domain);
  const [imageSrc, setImageSrc] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);
  const initial = domain.slice(0, 1).toUpperCase();
  const canFetch = enabled && canFetchFavicon(normalized);

  useEffect(() => {
    setImageSrc(null);
    setFailed(false);

    if (!canFetch) {
      return undefined;
    }

    let active = true;
    let objectUrl: string | null = null;

    loadFavicon(normalized).then((blob) => {
      if (!active) return;
      if (!blob) {
        setFailed(true);
        return;
      }
      objectUrl = URL.createObjectURL(blob);
      setImageSrc(objectUrl);
    });

    return () => {
      active = false;
      if (objectUrl) {
        URL.revokeObjectURL(objectUrl);
      }
    };
  }, [canFetch, normalized]);

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
