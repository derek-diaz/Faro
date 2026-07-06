import type { ReactNode } from "react";

type MetricCardProps = {
  label: string;
  value: string | number;
  tone?: "neutral" | "safe" | "blocked" | "warn";
  detail?: string;
  icon?: ReactNode;
};

export function MetricCard({ label, value, tone = "neutral", detail, icon }: MetricCardProps) {
  return (
    <section className={`metric-card ${tone}`}>
      {icon && <div className="metric-icon">{icon}</div>}
      <div>
        <span>{label}</span>
        <strong>{value}</strong>
        {detail && <small>{detail}</small>}
      </div>
    </section>
  );
}
