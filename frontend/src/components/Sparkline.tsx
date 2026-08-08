import { lazy, Suspense } from "react";

type SparklineProps = {
  readonly values?: number[];
  readonly tone?: "accent" | "blocked";
};

const SparklineCanvas = lazy(async () => {
  const module = await import("./DashboardCharts");
  return { default: module.SparklineCanvas };
});

export function Sparkline({ values = [], tone = "accent" }: SparklineProps) {
  return (
    <span className={`sparkline ${tone}`} aria-hidden="true">
      <Suspense fallback={null}>
        <SparklineCanvas values={values} tone={tone} />
      </Suspense>
    </span>
  );
}
