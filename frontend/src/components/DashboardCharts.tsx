import {
  BarController,
  BarElement,
  CategoryScale,
  Filler,
  LineController,
  LineElement,
  LinearScale,
  PointElement,
  Chart as ChartJS,
  Tooltip,
  type ChartData,
  type ChartOptions,
  type Plugin
} from "chart.js";
import { Bar, Line } from "react-chartjs-2";
import { useMemo } from "react";
import { useChartColors } from "../lib/use-chart-colors";
import type { ReplayBucket } from "../api/client";

ChartJS.register(BarController, BarElement, CategoryScale, Filler, LineController, LineElement, LinearScale, PointElement, Tooltip);

type TrafficChartCanvasProps = {
  readonly activity: number[];
  readonly blocked: number[];
};

type SparklineCanvasProps = {
  readonly values?: number[];
  readonly tone: "accent" | "blocked";
};

type ReplayTimelineCanvasProps = {
  readonly buckets: ReplayBucket[];
  readonly progress: number;
  readonly onSeek: (progress: number) => void;
};

type ReplayBarGeometry = {
  readonly x: number;
  readonly y: number;
  readonly base: number;
  readonly width: number;
};

const pointCount = 24;
const labels = Array.from({ length: pointCount }, (_, index) => String(index));
export function TrafficChartCanvas({ activity, blocked }: TrafficChartCanvasProps) {
  const colors = useChartColors();
  const data = useMemo<ChartData<"line", number[], string>>(() => ({
    labels,
    datasets: [
      {
        label: "Total",
        data: normalizeSeries(activity),
        borderColor: colors.accent,
        backgroundColor: colors.fill,
        borderWidth: 2.5,
        pointRadius: 0,
        pointHoverRadius: 4,
        pointHoverBackgroundColor: colors.accent,
        pointHoverBorderColor: colors.surface,
        pointHoverBorderWidth: 2,
        tension: 0.35,
        fill: true
      },
      {
        label: "Blocked",
        data: normalizeSeries(blocked),
        borderColor: colors.blocked,
        backgroundColor: "transparent",
        borderWidth: 2.5,
        pointRadius: 0,
        pointHoverRadius: 4,
        pointHoverBackgroundColor: colors.blocked,
        pointHoverBorderColor: colors.surface,
        pointHoverBorderWidth: 2,
        tension: 0.35,
        fill: false
      }
    ]
  }), [activity, blocked, colors]);

  const options = useMemo<ChartOptions<"line">>(() => ({
    responsive: true,
    maintainAspectRatio: false,
    animation: false,
    interaction: { intersect: false, mode: "index" },
    layout: { padding: { top: 5, right: 8, bottom: 0, left: 0 } },
    plugins: {
      legend: { display: false },
      tooltip: {
        displayColors: true,
        callbacks: {
          title: (items) => formatTimeLabel(items[0]?.dataIndex ?? 0),
          label: (context) => `${context.dataset.label}: ${Number(context.parsed.y).toLocaleString()}`
        }
      }
    },
    scales: {
      x: {
        grid: { display: false },
        border: { display: false },
        ticks: {
          color: colors.axis,
          maxRotation: 0,
          autoSkip: false,
          font: { size: 10, weight: 600 },
          callback: (_value, index) => formatTimeLabel(index)
        }
      },
      y: {
        beginAtZero: true,
        border: { display: false },
        grid: { color: colors.grid, drawTicks: false },
        ticks: {
          color: colors.axis,
          maxTicksLimit: 3,
          padding: 8,
          font: { size: 10, weight: 600 },
          callback: (value) => formatAxis(Number(value))
        }
      }
    }
  }), [colors]);

  return (
    <Line
      aria-label="Queries and blocked requests over the last 24 hours"
      data={data}
      options={options}
      role="img"
      fallbackContent={<span>Traffic chart unavailable.</span>}
    />
  );
}

export function ReplayTimelineCanvas({ buckets, progress, onSeek }: ReplayTimelineCanvasProps) {
  const colors = useChartColors();
  const safeProgress = Math.max(0, Math.min(100, progress));
  const data = useMemo<ChartData<"bar", number[], string>>(() => ({
    labels: buckets.map((_, index) => String(index)),
    datasets: [{
      label: "Queries",
      data: buckets.map((bucket) => bucket.total),
      backgroundColor: colors.accent,
      borderColor: colors.accent,
      borderRadius: 2,
      borderSkipped: false,
      barPercentage: 0.94,
      categoryPercentage: 0.98,
      maxBarThickness: 28
    }]
  }), [buckets, colors]);

  const options = useMemo<ChartOptions<"bar">>(() => ({
    responsive: true,
    maintainAspectRatio: false,
    animation: false,
    interaction: { intersect: false, mode: "index" },
    layout: { padding: { top: 7, right: 4, bottom: 0, left: 0 } },
    onClick: (event, _elements, chart) => {
      const x = event.x;
      const { left, right } = chart.chartArea;
      if (typeof x !== "number" || right <= left) return;
      onSeek(Math.max(0, Math.min(100, ((x - left) / (right - left)) * 100)));
    },
    plugins: {
      legend: { display: false },
      tooltip: {
        displayColors: false,
        callbacks: {
          title: (items) => {
            const bucket = buckets[items[0]?.dataIndex ?? -1];
            return bucket ? new Date(bucket.timestamp).toLocaleString() : "Activity";
          },
          label: (context) => {
            const bucket = buckets[context.dataIndex];
            return bucket ? `Queries: ${bucket.total.toLocaleString()}` : "";
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
        grid: { display: false },
        border: { display: false },
        ticks: {
          color: colors.axis,
          maxTicksLimit: 2,
          maxRotation: 0,
          autoSkip: true,
          font: { size: 10, weight: 600 },
          callback: (_value, index) => formatAxisDate(buckets[index]?.timestamp)
        }
      },
      y: {
        beginAtZero: true,
        border: { display: false },
        grid: { color: colors.grid, drawTicks: false },
        ticks: { display: false }
      }
    }
  }), [buckets, onSeek, colors]);

  const overlayPlugin = useMemo<Plugin<"bar">>(() => ({
    id: "replay-overlays",
    afterDatasetsDraw: (chart) => {
      const context = chart.ctx;
      const meta = chart.getDatasetMeta(0);
      context.save();
      context.fillStyle = colors.blocked;
      context.globalAlpha = 0.96;
      meta.data.forEach((element, index) => {
        const bucket = buckets[index];
        if (!bucket || bucket.total <= 0 || bucket.blocked <= 0) return;
        const bar = element as BarElement;
        const geometry = bar.getProps(["x", "y", "base", "width"], true) as ReplayBarGeometry;
        const blockedHeight = (geometry.base - geometry.y) * Math.min(bucket.blocked, bucket.total) / bucket.total;
        context.fillRect(geometry.x - geometry.width / 2, geometry.base - blockedHeight, geometry.width, blockedHeight);
      });

      const { left, right, top, bottom } = chart.chartArea;
      const cursorX = left + (safeProgress / 100) * Math.max(0, right - left);
      context.globalAlpha = 1;
      context.strokeStyle = colors.cursor;
      context.lineWidth = 1.4;
      context.setLineDash([4, 3]);
      context.beginPath();
      context.moveTo(cursorX, top - 4);
      context.lineTo(cursorX, bottom + 6);
      context.stroke();
      context.setLineDash([]);
      context.fillStyle = colors.cursor;
      context.beginPath();
      context.arc(cursorX, top - 4, 5, 0, Math.PI * 2);
      context.fill();
      context.restore();
    }
  }), [buckets, safeProgress, colors]);

  return (
    <Bar
      aria-label="Device DNS activity timeline"
      data={data}
      options={options}
      plugins={[overlayPlugin]}
      role="img"
      fallbackContent={<span>Replay chart unavailable.</span>}
    />
  );
}

export function SparklineCanvas({ values = [], tone }: SparklineCanvasProps) {
  const colors = useChartColors();
  const data = useMemo<ChartData<"line", number[], string>>(() => ({
    labels,
    datasets: [{
      data: normalizeSeries(values),
      borderColor: tone === "blocked" ? colors.blocked : colors.accent,
      borderWidth: 2.5,
      pointRadius: 0,
      tension: 0.35,
      fill: false
    }]
  }), [tone, values, colors]);

  const options = useMemo<ChartOptions<"line">>(() => ({
    responsive: true,
    maintainAspectRatio: false,
    animation: false,
    events: [],
    plugins: {
      legend: { display: false },
      tooltip: { enabled: false }
    },
    scales: {
      x: { display: false },
      y: { display: false, beginAtZero: true }
    },
    elements: { line: { capBezierPoints: true } }
  }), []);

  return <Line className="sparkline-canvas" data={data} options={options} role="presentation" />;
}

function normalizeSeries(values: number[]) {
  const normalized = values.slice(-pointCount);
  while (normalized.length < pointCount) normalized.unshift(0);
  return normalized;
}

function formatTimeLabel(index: number) {
  if (index === 0) return "24h ago";
  if (index === 12) return "12h ago";
  if (index === pointCount - 1) return "Now";
  return "";
}

function formatAxisDate(value?: string) {
  if (!value) return "";
  return new Date(value).toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}

function formatAxis(value: number) {
  if (value >= 1000) return `${(value / 1000).toFixed(value >= 10000 ? 0 : 1)}k`;
  if (value > 0 && value < 1) return value.toFixed(1);
  return String(Math.round(value));
}
