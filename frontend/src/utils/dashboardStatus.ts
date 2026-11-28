import type { DashboardSummary } from "../api/client";
import { clientQueries, recentClientQueries } from "./onboarding";

export function dashboardStatus(summary: DashboardSummary | null, lanAddress: string, stale: boolean) {
  const activity = summary?.recent_activity ?? [];
  const noHistory = !!summary && !summary.total_queries_today && !activity.length;
  const firstDevice = noHistory && summary.observed_devices === 0;
  const recent = recentClientQueries(activity, lanAddress, 0);
  const offerConnection = !!summary && !recent.length &&
    (!summary.active_devices_today || clientQueries(activity, lanAddress).length === 0);
  const state = (headline: string, tone: "quiet" | "healthy" | "warning", action?: "health" | "providers" | "activity") =>
    ({ headline, tone, action, offerConnection, noHistory });

  if (stale) return state(summary ? "Showing the last network update." : "Network status unavailable.", "quiet");
  if (!summary) return state("Checking your network…", "quiet");
  if (!summary.cache.metrics_pending && !summary.cache.metrics_available) {
    return state("Faro couldn’t check the DNS engine.", "warning", "health");
  }
  const engine = summary.health_cards?.find((card) => card.label === "DNS");
  if (engine?.status === "critical") return state("DNS needs attention.", "warning", "health");
  if (summary.upstream_health_status === "critical") return state("DNS providers are unavailable.", "warning", "providers");
  if (summary.upstream_health_status === "degraded") return state("A DNS provider needs attention.", "warning", "providers");
  if (firstDevice) return state("Waiting for your first device.", "quiet");
  if (summary.cache.metrics_pending || summary.upstream_health_status === "unknown") return state("Checking DNS health…", "quiet");

  if (recent.length && recent.every((query) => query.rcode?.toUpperCase() === "SERVFAIL")) {
    return state("Recent DNS requests are failing.", "warning", "activity");
  }
  if (recent.some((query) => ["NOERROR", "NXDOMAIN"].includes(query.rcode?.toUpperCase() ?? ""))) {
    return state("Faro is answering DNS requests.", "healthy");
  }
  if (recent.length) return state("Faro is receiving DNS requests.", "quiet");
  return state("No recent device requests.", "quiet");
}
