import { useEffect, useRef, useState } from "react";
import type { ProtectionInput } from "../api/client";

export function useProtectionSession() {
  const [drafts, setDrafts] = useState(() => new Map<number, ProtectionInput>());
  const returnToBlocklistsFor = useRef<number | null>(null);
  const hasDrafts = drafts.size > 0;
  useEffect(() => {
    if (!hasDrafts) return;
    function confirmLeaving(event: BeforeUnloadEvent) {
      event.preventDefault();
      event.returnValue = true;
    }
    window.addEventListener("beforeunload", confirmLeaving);
    return () => window.removeEventListener("beforeunload", confirmLeaving);
  }, [hasDrafts]);
  return { drafts, setDrafts, returnToBlocklistsFor };
}

export type ProtectionSession = ReturnType<typeof useProtectionSession>;
