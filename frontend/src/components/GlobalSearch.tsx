import { DialogSurface } from "./DialogSurface";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Database, FileText, HardDrive, ListFilter, RadioTower, Search, Shield, X } from "lucide-react";
import { useEffect, useId, useMemo, useState } from "react";
import { api, type SearchItem, type SearchResults } from "../api/client";

export type SearchSession = { query: string; index: number; resultKey?: string };

type GlobalSearchProps = {
  readonly session: SearchSession;
  readonly onClose: () => void;
  readonly onSelect: (item: CommandResult, session: SearchSession) => void;
};

const emptyResults: SearchResults = {
  domains: [],
  devices: [],
  events: [],
  local_records: [],
  rules: [],
  blocklists: []
};

export type CommandResult = SearchItem & {
  group: keyof SearchResults;
  groupLabel: string;
};

export function GlobalSearch({ session, onClose, onSelect }: GlobalSearchProps) {
  const listID = useId();
  const [query, setQuery] = useState(session.query);
  const [results, setResults] = useState<SearchResults>(emptyResults);
  const [activeIndex, setActiveIndex] = useState(session.index);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const flatResults = useMemo(() => flattenResults(results), [results]);

  useEffect(() => {
    setError("");
    setResults(emptyResults);
    setLoading(query.trim() !== "");
    if (query.trim() === "") return;

    const controller = new AbortController();
    const { signal } = controller;
    const timer = window.setTimeout(() => {
      void api.search(query, signal).then((nextResults) => {
        if (signal.aborted) return;
        setResults(nextResults);
        const items = flattenResults(nextResults);
        const restored = query === session.query ? items.findIndex((item) => resultKey(item) === session.resultKey) : -1;
        setActiveIndex((index) => restored >= 0 ? restored : Math.max(0, Math.min(index, items.length - 1)));
      }).catch(() => {
        if (!signal.aborted) setError("Search is unavailable. Try again in a moment.");
      }).finally(() => {
        if (!signal.aborted) setLoading(false);
      });
    }, 180);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [query, session]);

  const activeID = flatResults[activeIndex] ? `${listID}-${activeIndex}` : undefined;
  useEffect(() => {
    if (activeID) document.getElementById(activeID)?.scrollIntoView({ block: "nearest" });
  }, [activeID]);

  function selectResult(item: CommandResult, index: number) {
    onSelect(item, { query, index, resultKey: resultKey(item) });
  }

  return (
    <DialogSurface
      onClose={onClose}
      className="modal-backdrop"
      aria-label="Search Faro"
    >
      <div className="search-modal command-palette">
        <div className="search-modal-bar">
          <Search size={19} />
          <Input variant="embedded"
            role="combobox"
            aria-autocomplete="list"
            aria-expanded={true}
            aria-controls={listID}
            aria-activedescendant={activeID}
            autoFocus
            value={query}
            onChange={(event) => { setQuery(event.target.value); setActiveIndex(0); setResults(emptyResults); }}
            onKeyDown={(event) => {
              if (event.key === "ArrowDown") {
                event.preventDefault();
                setActiveIndex((index) => Math.max(0, Math.min(index + 1, flatResults.length - 1)));
              }
              if (event.key === "ArrowUp") {
                event.preventDefault();
                setActiveIndex((index) => Math.max(index - 1, 0));
              }
              if (event.key === "Enter" && flatResults[activeIndex]) {
                event.preventDefault();
                selectResult(flatResults[activeIndex], activeIndex);
              }
            }}
            placeholder="Search devices, domains, events, rules, DNS records, blocklists"
            aria-label="Search Faro"
          />
          <Button variant="ghost" size="icon" className="icon-button" type="button" onClick={onClose} aria-label="Close search">
            <X size={18} />
          </Button>
        </div>

        <div className="command-results">
          {error ? <p role="alert">{error}</p> : flatResults.length === 0 ? (
            <p role="status">{loading ? "Searching…" : query.trim() === "" ? "Type to search local Faro data." : "No matches"}</p>
          ) : null}
          <div id={listID} role="listbox" aria-label="Search results" aria-busy={loading}>
            {flatResults.map((item, index) => {
              const Icon = iconForGroup(item.group);
              return (
                <button
                  className={index === activeIndex ? "active" : ""}
                  key={resultKey(item)}
                  role="option"
                  id={`${listID}-${index}`}
                  aria-selected={index === activeIndex}
                  tabIndex={-1}
                  type="button"
                  onPointerMove={(event) => {
                    if (event.pointerType === "mouse" && (event.movementX || event.movementY)) setActiveIndex(index);
                  }}
                  onClick={() => selectResult(item, index)}
                >
                  <Icon size={18} />
                  <span>
                    <strong>{item.label}</strong>
                    <small>{item.subtitle ?? (typeof item.count === "number" ? `${item.count} queries` : "")}</small>
                  </span>
                  <em>{item.groupLabel}</em>
                </button>
              );
            })}
          </div>
        </div>
      </div>
    </DialogSurface>
  );
}

function resultKey(item: CommandResult) {
  return JSON.stringify([item.group, item.id, item.kind, item.group === "rules" || !item.id ? item.label : null]);
}

function flattenResults(results: SearchResults): CommandResult[] {
  const groups: Array<[keyof SearchResults, string]> = [
    ["devices", "Device"],
    ["domains", "Domain"],
    ["events", "Event"],
    ["rules", "Rule"],
    ["local_records", "Local names"],
    ["blocklists", "Blocklist"]
  ];
  return groups.flatMap(([group, groupLabel]) => results[group].map((item) => ({ ...item, group, groupLabel })));
}

function iconForGroup(group: keyof SearchResults) {
  switch (group) {
    case "devices":
      return HardDrive;
    case "domains":
      return RadioTower;
    case "events":
      return FileText;
    case "rules":
      return ListFilter;
    case "blocklists":
      return Shield;
    case "local_records":
      return Database;
  }
}
