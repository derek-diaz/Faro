import {
  BarController,
  BarElement,
  Chart as ChartJS,
  LinearScale,
  Tooltip,
  type ChartData,
  type ChartOptions,
  type Plugin
} from "chart.js";
import { Bar } from "react-chartjs-2";
import { useMemo } from "react";
import type { ActivityTimeline as ActivityTimelineData } from "../api/client";

ChartJS.register(BarController, BarElement, LinearScale, Tooltip);

type ActivityTimelineProps = {
  readonly timeline: ActivityTimelineData | null;
  readonly rangeLabel: string;
  readonly loading?: boolean;
};

type TimelinePoint = {
  x: number;
  y: number;
};

type BarGeometry = {
  readonly x: number;
  readonly y: number;
  readonly base: number;
  readonly width: number;
};

const accentColor = "#2cb1a4";
const blockedColor = "#d76b63";
const gridColor = "rgba(118, 145, 160, 0.28)";
const axisColor = "#7b8d9a";

export function ActivityTimeline({ timeline, rangeLabel, loading = false }: ActivityTimelineProps) {
  const buckets = timeline?.buckets ?? [];
  const hasActivity = buckets.some((bucket) => bucket.total > 0);
  const data = useMemo<ChartData<"bar", TimelinePoint[]>>(() => ({
    datasets: [{
      label: "Queries and events",
      data: buckets.map((bucket) => ({ x: new Date(bucket.timestamp).getTime(), y: bucket.total })),
      backgroundColor: accentColor,
      borderColor: accentColor,
      borderRadius: 3,
      borderSkipped: false,
      barPercentage: 0.92,
      categoryPercentage: 0.98,
      maxBarThickness: 28
    }]
  }), [buckets]);
  const options = useMemo<ChartOptions<"bar">>(() => ({
    responsive: true,
    maintainAspectRatio: false,
    animation: false,
    interaction: { intersect: false, mode: "index" },
    layout: { padding: { top: 3, right: 5, bottom: 0, left: 0 } },
    plugins: {
      legend: { display: false },
      tooltip: {
        displayColors: false,
        callbacks: {
          title: (items) => {
            const bucket = buckets[items[0]?.dataIndex ?? -1];
            return bucket ? formatBucketTitle(bucket.timestamp, bucket.total, bucket.blocked) : "Activity";
          },
          label: (context) => {
            const bucket = buckets[context.dataIndex];
            return bucket ? `Total: ${bucket.total.toLocaleString()}` : "";
          },
          afterLabel: (context) => {
            const bucket = buckets[context.dataIndex];
            return bucket ? `Blocked: ${bucket.blocked.toLocaleString()}` : "";
          }
        }
      }
    },
    scales: {
      x: {
        type: "linear",
        min: timeline ? new Date(timeline.from).getTime() : undefined,
        max: timeline ? new Date(timeline.to).getTime() : undefined,
        offset: true,
        grid: { display: false },
        border: { display: false },
        ticks: {
          color: axisColor,
          maxTicksLimit: 6,
          maxRotation: 0,
          autoSkip: true,
          font: { size: 10, weight: 600 },
          callback: (value) => formatChartTick(Number(value), timeline?.from, timeline?.to)
        }
      },
      y: {
        beginAtZero: true,
        border: { display: false },
        grid: { color: gridColor, drawTicks: false },
        ticks: { color: axisColor, precision: 0, padding: 8, font: { size: 10, weight: 600 } }
      }
    }
  }), [buckets, timeline?.bucket_seconds]);
  const blockedOverlayPlugin = useMemo<Plugin<"bar">>(() => ({
    id: "activity-blocked-overlay",
    afterDatasetsDraw: (chart) => {
      const meta = chart.getDatasetMeta(0);
      const context = chart.ctx;
      context.save();
      context.fillStyle = blockedColor;
      context.globalAlpha = 0.96;
      meta.data.forEach((element, index) => {
        const bucket = buckets[index];
        if (!bucket || bucket.total <= 0 || bucket.blocked <= 0) return;
        const bar = element as BarElement;
        const geometry = bar.getProps(["x", "y", "base", "width"], true) as BarGeometry;
        const blockedHeight = (geometry.base - geometry.y) * Math.min(bucket.blocked, bucket.total) / bucket.total;
        context.fillRect(geometry.x - geometry.width / 2, geometry.base - blockedHeight, geometry.width, blockedHeight);
      });
      context.restore();
    }
  }), [buckets]);

  return (
    <div className="activity-timeline-chart" data-loading={loading ? "true" : undefined}>
      {timeline && (
        <div className="activity-timeline-canvas">
          <Bar
            aria-label={`Activity over ${rangeLabel}`}
            data={data}
            options={options}
            plugins={[blockedOverlayPlugin]}
            role="img"
            fallbackContent={<span>Activity chart unavailable. Use the activity table below.</span>}
          />
        </div>
      )}
      <div className="sr-only" aria-label={`Activity values for ${rangeLabel}`}>
        {timeline && hasActivity ? buckets.filter((bucket) => bucket.total > 0).map((bucket) => formatBucketTitle(bucket.timestamp, bucket.total, bucket.blocked)).join(". ") : "No activity in this time range."}
      </div>
      <div className="activity-timeline-meta">
        <span><i className="activity-timeline-legend-swatch total" />Queries and events</span>
        <span><i className="activity-timeline-legend-swatch blocked" />Blocked</span>
        {timeline && <span className="activity-timeline-bucket-size">Each bar: {formatBucketSize(timeline.bucket_seconds)}</span>}
      </div>
      {!timeline && <div className="activity-timeline-empty">{loading ? "Loading timeline…" : "Choose a time range to see activity over time."}</div>}
      {timeline && !hasActivity && <div className="activity-timeline-empty">No activity in this time range.</div>}
    </div>
  );
}

function formatChartTick(value: number, from?: string, to?: string) {
  const date = new Date(value);
  const span = from && to ? new Date(to).getTime() - new Date(from).getTime() : 0;
  if (span <= 48 * 60 * 60 * 1000) {
    return date.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
  }
  return date.toLocaleDateString([], { month: "short", day: "numeric" });
}

function formatBucketTitle(timestamp: string, total: number, blocked: number) {
  const date = new Date(timestamp);
  return `${date.toLocaleString()}: ${total.toLocaleString()} total, ${blocked.toLocaleString()} blocked`;
}

function formatBucketSize(seconds: number) {
  if (seconds < 60) return `${seconds}s`;
  const minutes = seconds / 60;
  if (minutes < 60) return `${minutes}m`;
  const hours = minutes / 60;
  if (hours < 24) return `${hours}h`;
  return `${hours / 24}d`;
}
