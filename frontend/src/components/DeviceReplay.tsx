import { AlertCircle, Pause, Play, RotateCcw } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { api, type DNSQuery, type DeviceReplay as DeviceReplayData } from "../api/client";
import { DomainFavicon } from "./DomainFavicon";
import { ReplayTimeline } from "./ReplayTimeline";
import { StatusBadge } from "./StatusBadge";

type DeviceReplayProps = {
  clientIP: string;
  deviceName: string;
  onDomainSelect: (domain: string) => void;
};

type ReplayRange = "1h" | "24h" | "7d" | "30d" | "all";
type ReplaySpeed = 1 | 5 | 20;

const rangeOptions: { value: ReplayRange; label: string }[] = [
  { value: "1h", label: "1 hour" },
  { value: "24h", label: "24 hours" },
  { value: "7d", label: "7 days" },
  { value: "30d", label: "30 days" },
  { value: "all", label: "All" }
];

export function DeviceReplay({ clientIP, deviceName, onDomainSelect }: DeviceReplayProps) {
  const [range, setRange] = useState<ReplayRange>("7d");
  const [data, setData] = useState<DeviceReplayData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [progress, setProgress] = useState(100);
  const [playing, setPlaying] = useState(false);
  const [speed, setSpeed] = useState<ReplaySpeed>(1);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError("");
    setPlaying(false);
    api.deviceReplay(clientIP, range)
      .then((nextData) => {
        if (!cancelled) {
          setData(nextData);
          setProgress(100);
        }
      })
      .catch((caught: unknown) => {
        if (!cancelled) setError(caught instanceof Error ? caught.message : "Failed to load replay data.");
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [clientIP, range]);

  useEffect(() => {
    if (!playing) return undefined;
    const timer = window.setInterval(() => {
      setProgress((current) => {
        const next = current + speed / 3;
        if (next >= 100) {
          setPlaying(false);
          return 100;
        }
        return next;
      });
    }, 100);
    return () => window.clearInterval(timer);
  }, [playing, speed]);

  const fromTime = data ? new Date(data.from).getTime() : 0;
  const toTime = data ? new Date(data.to).getTime() : 0;
  const cursorTime = fromTime + (progress / 100) * Math.max(0, toTime - fromTime);
  const visibleEvents = useMemo(() => data?.events.filter((event) => new Date(event.timestamp).getTime() <= cursorTime) ?? [], [cursorTime, data?.events]);
  const groupedEvents = useMemo(() => groupReplayEvents(visibleEvents), [visibleEvents]);
  const currentDomains = useMemo(() => countLabels(visibleEvents.map((event) => event.domain)).slice(0, 6), [visibleEvents]);
  const currentSources = useMemo(() => countLabels(visibleEvents.map((event) => event.source)), [visibleEvents]);
  const currentBlocked = visibleEvents.filter((event) => event.action === "blocked").length;
  const currentUniqueDomains = new Set(visibleEvents.map((event) => event.domain)).size;
  const elapsedMinutes = Math.max((cursorTime - fromTime) / 60000, 1);
  const currentRate = visibleEvents.length / elapsedMinutes;

  function togglePlayback() {
    if (!data?.events.length) return;
    if (playing) {
      setPlaying(false);
      return;
    }
    if (progress >= 100) setProgress(0);
    setPlaying(true);
  }

  function seek(nextProgress: number) {
    setPlaying(false);
    setProgress(nextProgress);
  }

  return (
    <div className="device-replay-workspace">
      <div className="replay-toolbar">
        <div className="replay-range-control" role="group" aria-label="Replay time range">
          {rangeOptions.map((option) => <button className={range === option.value ? "active" : ""} type="button" key={option.value} onClick={() => setRange(option.value)}>{option.label}</button>)}
        </div>
        <div className="replay-playback-controls">
          <button className="replay-play-button" type="button" onClick={togglePlayback} disabled={!data?.events.length || loading}>
            {playing ? <Pause size={17} /> : progress >= 100 ? <RotateCcw size={17} /> : <Play size={17} />}
            <span>{playing ? "Pause" : progress >= 100 ? "Replay" : "Play"}</span>
          </button>
          <div className="replay-speed-control" role="group" aria-label="Playback speed">
            {([1, 5, 20] as ReplaySpeed[]).map((value) => <button className={speed === value ? "active" : ""} type="button" key={value} onClick={() => setSpeed(value)}>{value}x</button>)}
          </div>
        </div>
      </div>

      {loading && <div className="replay-loading">Loading device history...</div>}
      {error && <div className="replay-error"><AlertCircle size={18} /><span>{error}</span></div>}

      {!loading && data && (
        <>
          <div className="replay-metrics">
            <ReplayMetric label="Requests observed" value={visibleEvents.length} detail={`${data.total_queries} in selected period`} />
            <ReplayMetric label="Blocked" value={currentBlocked} detail={`${data.blocked_queries} in selected period`} tone="blocked" />
            <ReplayMetric label="Domains" value={currentUniqueDomains} detail={`${data.unique_domains} in selected period`} />
            <ReplayMetric label="Average frequency" value={formatRate(currentRate)} detail="Queries per minute so far" />
          </div>

          <section className="replay-stage">
            <div className="replay-stage-heading">
              <div>
                <strong>{playing ? `Replaying ${deviceName}` : "Activity timeline"}</strong>
                <span>{formatCursorTime(cursorTime)}</span>
              </div>
              <span>{Math.round(progress)}%</span>
            </div>
            <ReplayTimeline buckets={data.buckets} progress={progress} onSeek={seek} />
            <input className="replay-scrubber" type="range" min="0" max="100" step="0.1" value={progress} onChange={(event) => seek(Number(event.target.value))} aria-label="Replay position" />
          </section>

          <div className="replay-context-note">
            <AlertCircle size={16} />
            <span>Faro observed DNS lookups from this device. A lookup does not confirm that the device connected to the resulting service.</span>
          </div>

          <div className="replay-detail-grid">
            <section className="replay-event-section">
              <div className="replay-section-heading">
                <h3>Requests at this point</h3>
                <span>{groupedEvents.length} observed</span>
              </div>
              {groupedEvents.length === 0 ? (
                <div className="replay-empty">Move the cursor forward or press Play to reveal activity.</div>
              ) : (
                <div className="replay-event-list">
                  {groupedEvents.slice(-8).reverse().map((event) => (
                    <div className="replay-event-row" key={event.key}>
                      <time>{new Date(event.timestamp).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })}</time>
                      <StatusBadge value={event.action} />
                      <button type="button" className="replay-domain-link" onClick={() => onDomainSelect(event.domain)}><DomainFavicon domain={event.domain} /><span>{event.domain}</span></button>
                      <span>{event.queryTypes.join(" · ")}</span>
                      <small>{friendlySource(event.source)}</small>
                    </div>
                  ))}
                </div>
              )}
              {data.truncated && <span className="replay-truncated">Playback is limited to the first 2,500 requests in this period; summaries include all requests.</span>}
            </section>

            <aside className="replay-insights">
              <section>
                <div className="replay-section-heading"><h3>Top domains so far</h3></div>
                <ReplayRankList items={currentDomains} empty="No domains at this point." onSelect={onDomainSelect} />
              </section>
              <section>
                <div className="replay-section-heading"><h3>Resolution path</h3></div>
                <ReplayRankList items={currentSources.map((item) => ({ ...item, label: friendlySource(item.label) }))} empty="No resolution path yet." />
              </section>
            </aside>
          </div>
        </>
      )}
    </div>
  );
}

function ReplayMetric({ label, value, detail, tone = "default" }: { label: string; value: string | number; detail: string; tone?: "default" | "blocked" }) {
  return <div className={`replay-metric ${tone}`}><span>{label}</span><strong>{value}</strong><small>{detail}</small></div>;
}

function ReplayRankList({ items, empty, onSelect }: { items: { label: string; count: number }[]; empty: string; onSelect?: (label: string) => void }) {
  const max = Math.max(...items.map((item) => item.count), 1);
  if (!items.length) return <div className="replay-empty compact">{empty}</div>;
  return <div className="replay-rank-list">{items.map((item) => <div key={item.label}><button type="button" disabled={!onSelect} onClick={() => onSelect?.(item.label)}>{item.label}</button><strong>{item.count}</strong><span><i style={{ width: `${Math.max(5, (item.count / max) * 100)}%` }} /></span></div>)}</div>;
}

type GroupedReplayEvent = {
  key: string;
  timestamp: string;
  domain: string;
  action: DNSQuery["action"];
  source: string;
  queryTypes: string[];
};

function groupReplayEvents(events: DNSQuery[]) {
  const grouped = new Map<string, GroupedReplayEvent>();
  for (const event of events) {
    const timestamp = new Date(event.timestamp);
    timestamp.setMilliseconds(0);
    const key = `${timestamp.toISOString()}-${event.domain}-${event.action}-${event.source}`;
    const existing = grouped.get(key);
    if (existing) {
      if (!existing.queryTypes.includes(event.query_type)) existing.queryTypes.push(event.query_type);
      continue;
    }
    grouped.set(key, { key, timestamp: event.timestamp, domain: event.domain, action: event.action, source: event.source, queryTypes: [event.query_type] });
  }
  return Array.from(grouped.values());
}

function countLabels(labels: string[]) {
  const counts = new Map<string, number>();
  labels.forEach((label) => counts.set(label, (counts.get(label) ?? 0) + 1));
  return Array.from(counts, ([label, count]) => ({ label, count })).sort((a, b) => b.count - a.count || a.label.localeCompare(b.label));
}

function friendlySource(source: string) {
  const labels: Record<string, string> = { upstream: "Public upstream", cache: "Faro cache", local: "Local DNS", blocklist: "Faro filtering", manual: "Manual rule" };
  return labels[source] ?? source.replace(/_/g, " ").replace(/^./, (letter) => letter.toUpperCase());
}

function formatCursorTime(timestamp: number) {
  if (!timestamp) return "No time selected";
  return new Date(timestamp).toLocaleString([], { weekday: "short", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

function formatRate(value: number) {
  if (value >= 10) return value.toFixed(0);
  if (value >= 1) return value.toFixed(1);
  return value.toFixed(2);
}
