import type { PointerEvent } from "react";
import type { ReplayBucket } from "../api/client";

type ReplayTimelineProps = {
  readonly buckets: ReplayBucket[];
  readonly progress: number;
  readonly onSeek: (progress: number) => void;
};

const width = 800;
const height = 190;
const left = 30;
const right = 786;
const top = 16;
const bottom = 146;

export function ReplayTimeline({ buckets, progress, onSeek }: ReplayTimelineProps) {
  const max = Math.max(...buckets.map((bucket) => bucket.total), 1);
  const plotWidth = right - left;
  const slotWidth = plotWidth / Math.max(buckets.length, 1);
  const barWidth = Math.max(2, slotWidth - Math.min(4, slotWidth * 0.2));
  const cursorX = left + (Math.max(0, Math.min(100, progress)) / 100) * plotWidth;
  const hasData = buckets.some((bucket) => bucket.total > 0);

  function seek(event: PointerEvent<SVGSVGElement>) {
    const bounds = event.currentTarget.getBoundingClientRect();
    const scaledX = ((event.clientX - bounds.left) / bounds.width) * width;
    onSeek(Math.max(0, Math.min(100, ((scaledX - left) / plotWidth) * 100)));
  }

  return (
    <svg className="replay-timeline" viewBox={`0 0 ${width} ${height}`} onPointerDown={seek} role="img" aria-label="Device DNS activity timeline">
      {[top, (top + bottom) / 2, bottom].map((y) => <line className="replay-grid-line" key={y} x1={left} x2={right} y1={y} y2={y} />)}
      {buckets.map((bucket, index) => {
        const totalHeight = (bucket.total / max) * (bottom - top);
        const blockedHeight = (bucket.blocked / max) * (bottom - top);
        const x = left + index * slotWidth + (slotWidth - barWidth) / 2;
        return (
          <g key={`${bucket.timestamp}-${index}`}>
            <rect className="replay-bar total" x={x} y={bottom - totalHeight} width={barWidth} height={Math.max(totalHeight, bucket.total > 0 ? 2 : 0)} rx="1">
              <title>{`${new Date(bucket.timestamp).toLocaleString()}: ${bucket.total} queries, ${bucket.blocked} blocked`}</title>
            </rect>
            {bucket.blocked > 0 && <rect className="replay-bar blocked" x={x} y={bottom - blockedHeight} width={barWidth} height={Math.max(blockedHeight, 2)} rx="1" />}
          </g>
        );
      })}
      {!hasData && <text className="replay-empty-label" x={width / 2} y={(top + bottom) / 2} textAnchor="middle">No DNS activity in this period</text>}
      <line className="replay-cursor-line" x1={cursorX} x2={cursorX} y1={top - 4} y2={bottom + 6} />
      <circle className="replay-cursor-handle" cx={cursorX} cy={top - 4} r="5" />
      <text className="replay-axis-label" x={left} y="174">{formatAxisDate(buckets[0]?.timestamp)}</text>
      <text className="replay-axis-label" x={right} y="174" textAnchor="end">{formatAxisDate(buckets[buckets.length - 1]?.timestamp)}</text>
    </svg>
  );
}

function formatAxisDate(value?: string) {
  if (!value) return "";
  return new Date(value).toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}
