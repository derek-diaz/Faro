import type { ReactNode } from "react";
import { Card } from "./ui/card";
import { cn } from "../lib/utils";

export function StatCard({ label, value, detail, icon, visual, tone = "default", loading = false }: {
  label: string; value: ReactNode; detail?: ReactNode; icon: ReactNode; visual?: ReactNode;
  tone?: "default" | "blocked"; loading?: boolean;
}) {
  return <Card className={cn("faro-stat ring-0", tone === "blocked" && "faro-stat-blocked")} aria-busy={loading}>
    <div className="faro-stat-heading"><span>{label}</span><span className="faro-stat-icon" aria-hidden="true">{icon}</span></div>
    {loading ? <span className="activity-stat-skeleton" aria-hidden="true" /> : <strong className="faro-stat-value">{value}</strong>}
    {(detail || visual) && <div className="faro-stat-footer"><span>{detail}</span>{visual}</div>}
  </Card>;
}
