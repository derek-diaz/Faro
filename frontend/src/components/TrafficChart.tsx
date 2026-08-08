import { lazy, Suspense } from "react";

type TrafficChartProps = {
  readonly activity: number[];
  readonly blocked: number[];
};

const TrafficChartCanvas = lazy(async () => {
  const module = await import("./DashboardCharts");
  return { default: module.TrafficChartCanvas };
});

export function TrafficChart({ activity, blocked }: TrafficChartProps) {
  const hasData = activity.some((value) => value > 0) || blocked.some((value) => value > 0);

  return (
    <div className="traffic-chart">
      <div className="traffic-chart-canvas">
        <Suspense fallback={null}>
          <TrafficChartCanvas activity={activity} blocked={blocked} />
        </Suspense>
      </div>
      {!hasData && <div className="chart-empty"><strong>No query activity yet</strong><span>Traffic will appear here as Faro receives DNS requests.</span></div>}
    </div>
  );
}
