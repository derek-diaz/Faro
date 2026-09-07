type StatusBadgeProps = {
  readonly value: string;
};

export function StatusBadge({ value }: StatusBadgeProps) {
  const normalized = value.toLowerCase();
  return <Badge variant={normalized === "blocked" ? "destructive" : "secondary"} className={normalized === "blocked" ? undefined : "bg-accent text-accent-foreground"}>{normalized === "blocked" ? "Blocked" : "Allowed"}</Badge>;
}
import { Badge } from "./ui/badge";
