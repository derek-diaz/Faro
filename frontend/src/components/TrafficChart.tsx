type TrafficChartProps = {
  activity: number[];
  blocked: number[];
};

const width = 760;
const height = 230;
const plotLeft = 44;
const plotRight = 742;
const plotTop = 18;
const plotBottom = 190;

export function TrafficChart({ activity, blocked }: TrafficChartProps) {
  const totalValues = normalizeSeries(activity);
  const blockedValues = normalizeSeries(blocked);
  const max = Math.max(...totalValues, ...blockedValues, 1);
  const totalPoints = points(totalValues, max);
  const blockedPoints = points(blockedValues, max);
  const areaPoints = `${plotLeft},${plotBottom} ${totalPoints} ${plotRight},${plotBottom}`;
  const hasData = totalValues.some((value) => value > 0) || blockedValues.some((value) => value > 0);

  return (
    <div className="traffic-chart">
      <svg viewBox={`0 0 ${width} ${height}`} role="img" aria-label="Queries and blocked requests over the last 24 hours">
        {[plotTop, (plotTop + plotBottom) / 2, plotBottom].map((y) => (
          <line className="chart-grid-line" key={y} x1={plotLeft} x2={plotRight} y1={y} y2={y} />
        ))}
        <text className="chart-axis-label" x="4" y={plotTop + 4}>{formatAxis(max)}</text>
        <text className="chart-axis-label" x="4" y={(plotTop + plotBottom) / 2 + 4}>{formatAxis(max / 2)}</text>
        <text className="chart-axis-label" x="28" y={plotBottom + 4}>0</text>
        {hasData && <polygon className="chart-area" points={areaPoints} />}
        <polyline className="chart-line total" points={totalPoints} />
        <polyline className="chart-line blocked" points={blockedPoints} />
        <text className="chart-time-label" x={plotLeft} y="218">24h ago</text>
        <text className="chart-time-label" x={(plotLeft + plotRight) / 2} y="218" textAnchor="middle">12h ago</text>
        <text className="chart-time-label" x={plotRight} y="218" textAnchor="end">Now</text>
      </svg>
      {!hasData && <div className="chart-empty"><strong>No query activity yet</strong><span>Traffic will appear here as Faro receives DNS requests.</span></div>}
    </div>
  );
}

function normalizeSeries(values: number[]) {
  const normalized = values.slice(-24);
  while (normalized.length < 24) normalized.unshift(0);
  return normalized;
}

function points(values: number[], max: number) {
  return values.map((value, index) => {
    const x = plotLeft + (index / Math.max(values.length - 1, 1)) * (plotRight - plotLeft);
    const y = plotBottom - (value / max) * (plotBottom - plotTop);
    return `${x.toFixed(1)},${y.toFixed(1)}`;
  }).join(" ");
}

function formatAxis(value: number) {
  if (value >= 1000) return `${(value / 1000).toFixed(value >= 10000 ? 0 : 1)}k`;
  if (value > 0 && value < 1) return value.toFixed(1);
  return String(Math.round(value));
}
