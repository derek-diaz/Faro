import type { CountItem } from "../api/client";
import { DomainFavicon } from "./DomainFavicon";

type DomainListProps = {
  title: string;
  items: CountItem[];
  empty: string;
  tone?: "allowed" | "blocked";
  showFavicons?: boolean;
};

export function DomainList({ title, items, empty, tone = "allowed", showFavicons = false }: DomainListProps) {
  const max = Math.max(...items.map((item) => item.count), 1);
  return (
    <section className={`panel rank-panel ${tone}`}>
      <div className="panel-title">
        <h2>{title}</h2>
        {items.length > 0 && <a href="#query-log">View all</a>}
      </div>
      <div className="rank-list">
        {items.length === 0 ? (
          <p className="empty">{empty}</p>
        ) : (
          items.map((item) => (
            <div className="rank-row" key={item.label}>
              <div className="rank-heading">
                <span className="rank-label">
                  {showFavicons && <DomainFavicon domain={item.label} />}
                  <strong>{item.label}</strong>
                </span>
                <span>{item.count} queries</span>
              </div>
              <div className="bar-track" aria-hidden="true">
                <div style={{ width: `${Math.max(8, (item.count / max) * 100)}%` }} />
              </div>
            </div>
          ))
        )}
      </div>
    </section>
  );
}
