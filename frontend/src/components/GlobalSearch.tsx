import { Database, FileText, HardDrive, ListFilter, RadioTower, Search, Shield, X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { api, type SearchItem, type SearchResults } from "../api/client";
import type { Page } from "../App";

type GlobalSearchProps = {
  readonly open: boolean;
  readonly onClose: () => void;
  readonly setPage: (page: Page) => void;
  readonly onDomainSelect: (domain: string) => void;
  readonly onDeviceSelect: (clientIP: string) => void;
};

const emptyResults: SearchResults = {
  domains: [],
  devices: [],
  events: [],
  local_records: [],
  rules: [],
  blocklists: []
};

type CommandResult = SearchItem & {
  group: keyof SearchResults;
  groupLabel: string;
};

export function GlobalSearch({ open, onClose, setPage, onDomainSelect, onDeviceSelect }: GlobalSearchProps) {
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<SearchResults>(emptyResults);
  const [activeIndex, setActiveIndex] = useState(0);

  const flatResults = useMemo(() => flattenResults(results), [results]);

  useEffect(() => {
    if (!open) {
      return undefined;
    }
    const timer = window.setTimeout(() => {
      if (query.trim() === "") {
        setResults(emptyResults);
        return;
      }
      void api.search(query).then((nextResults) => {
        setResults(nextResults);
        setActiveIndex(0);
      });
    }, 180);
    return () => window.clearTimeout(timer);
  }, [open, query]);

  useEffect(() => {
    if (open) {
      setQuery("");
      setResults(emptyResults);
      setActiveIndex(0);
    }
  }, [open]);

  if (!open) {
    return null;
  }

  function runResult(item: CommandResult) {
    switch (item.group) {
      case "domains":
        onDomainSelect(item.label);
        break;
      case "devices":
        onDeviceSelect(item.label);
        break;
      case "events":
        setPage("queries");
        break;
      case "local_records":
        setPage("records");
        break;
      case "rules":
        setPage("protection");
        break;
      case "blocklists":
        setPage("blocklists");
        break;
    }
    onClose();
  }

  return (
    <dialog
      className="modal-backdrop"
      open
      aria-label="Search Faro"
    >
      <div className="search-modal command-palette">
        <div className="search-modal-bar">
          <Search size={19} />
          <input
            autoFocus
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "ArrowDown") {
                event.preventDefault();
                setActiveIndex((index) => Math.min(index + 1, flatResults.length - 1));
              }
              if (event.key === "ArrowUp") {
                event.preventDefault();
                setActiveIndex((index) => Math.max(index - 1, 0));
              }
              if (event.key === "Enter" && flatResults[activeIndex]) {
                event.preventDefault();
                runResult(flatResults[activeIndex]);
              }
              if (event.key === "Escape") {
                onClose();
              }
            }}
            placeholder="Search devices, domains, events, rules, DNS records, blocklists"
          />
          <button className="icon-button" type="button" onClick={onClose} aria-label="Close search">
            <X size={18} />
          </button>
        </div>

        <div className="command-results">
          {flatResults.length === 0 ? (
            <p>{query.trim() === "" ? "Type to search local Faro data." : "No matches"}</p>
          ) : (
            flatResults.map((item, index) => {
              const Icon = iconForGroup(item.group);
              return (
                <button
                  className={index === activeIndex ? "active" : ""}
                  key={`${item.group}-${item.label}-${item.subtitle ?? ""}`}
                  type="button"
                  onMouseEnter={() => setActiveIndex(index)}
                  onClick={() => runResult(item)}
                >
                  <Icon size={18} />
                  <span>
                    <strong>{item.label}</strong>
                    <small>{item.subtitle ?? (typeof item.count === "number" ? `${item.count} queries` : "")}</small>
                  </span>
                  <em>{item.groupLabel}</em>
                </button>
              );
            })
          )}
        </div>
      </div>
    </dialog>
  );
}

function flattenResults(results: SearchResults): CommandResult[] {
  const groups: Array<[keyof SearchResults, string]> = [
    ["devices", "Device"],
    ["domains", "Domain"],
    ["events", "Event"],
    ["rules", "Rule"],
    ["local_records", "Local DNS"],
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
