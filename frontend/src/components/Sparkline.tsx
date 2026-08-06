type SparklineProps = {
  readonly values?: number[];
  readonly tone?: "accent" | "blocked";
};

export function Sparkline({ values = [], tone = "accent" }: SparklineProps) {
  const safeValues = values.length > 0 ? values : Array.from({ length: 24 }, () => 0);
  const max = Math.max(...safeValues, 1);
  const points = safeValues
    .map((value, index) => {
      const x = (index / Math.max(safeValues.length - 1, 1)) * 100;
      const y = 28 - (value / max) * 24;
      return `${x.toFixed(2)},${y.toFixed(2)}`;
    })
    .join(" ");

  return (
    <svg className={`sparkline ${tone}`} viewBox="0 0 100 32" preserveAspectRatio="none" aria-hidden="true">
      <polyline points={points} />
    </svg>
  );
}
