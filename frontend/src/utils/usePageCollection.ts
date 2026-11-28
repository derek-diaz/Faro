import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";

/** Retain a signed-in page's collection until a mutation requests a fresh copy. */
export function usePageCollection<T>(load: () => Promise<T>, needed: boolean) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState("");
  const request = useRef<Promise<void> | null>(null);
  // A write can finish after navigation. Refresh the page that is visible then.
  const visible = useRef(needed);
  useLayoutEffect(() => { visible.current = needed; }, [needed]);

  const refresh = useCallback(() => {
    setError("");
    const next = load().then((value) => {
      if (request.current === next) setData(value);
    }).catch((failure: unknown) => {
      if (request.current !== next) return;
      request.current = null;
      setError(failure instanceof Error ? failure.message : "Could not load Faro data.");
      throw failure;
    });
    request.current = next;
    return next;
  }, [load]);

  useEffect(() => {
    if (needed && !request.current) void refresh().catch(() => undefined);
  }, [needed, refresh]);

  function invalidate() {
    if (visible.current) return refresh();
    request.current = null;
    setData(null);
    setError("");
    return Promise.resolve();
  }

  return { data, error: needed ? error : "", refresh, invalidate };
}
