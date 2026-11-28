import { ArrowRight, ChevronLeft, ChevronRight } from "lucide-react";
import { lazy, Suspense, useCallback, useEffect, useRef, useState } from "react";
import type { DashboardSummary } from "../api/client";
import { trafficActivityPath, trafficHourCounts, trafficHourLabel } from "../utils/trafficHours";
import { TrafficChart } from "./TrafficChart";
import { Button } from "./ui/button";

const TrafficChartCanvas = lazy(async () => ({ default: (await import("./DashboardCharts")).TrafficChartCanvas }));

type DashboardTrafficProps = {
  readonly summary: DashboardSummary;
  readonly onViewHour: (from: string, to: string) => void;
};

export function DashboardTraffic({ summary, onViewHour }: DashboardTrafficProps) {
  const hours = summary.traffic_hours;
  const [selectedStart, setSelectedStart] = useState<string | null>(() => history.state?.faroTrafficHour ?? null);
  const returnLink = useRef<HTMLAnchorElement>(null);
  const index = hours?.findIndex((hour) => hour.start === selectedStart) ?? -1;
  const selectedIndex = index >= 0 ? index : (hours?.length ?? 0) - 1;
  const selected = hours?.[selectedIndex];
  const selectHour = useCallback((next: number) => {
    if (hours?.[next]) setSelectedStart(hours[next].start);
  }, [hours]);

  useEffect(() => {
    if (history.state?.faroTrafficHour && returnLink.current) returnLink.current.focus();
  }, []);

  return (
    <section className="panel query-volume-panel home-traffic" aria-label="DNS traffic">
      <div className="panel-title dashboard-panel-title">
        <div><h2>DNS traffic</h2><p>Hourly requests · local time</p></div>
        <div className="chart-legend" aria-label="Chart legend">
          <span><i className="legend-total" /> Total</span>
          <span><i className="legend-blocked" /> Blocked</span>
        </div>
      </div>
      {hours && selected ? <>
        <div className="traffic-chart">
          <div className="traffic-chart-canvas">
            <Suspense fallback={null}>
              <TrafficChartCanvas activity={summary.sparklines.activity} blocked={summary.sparklines.blocked}
                hours={hours} selectedIndex={selectedIndex} onSelect={selectHour} />
            </Suspense>
          </div>
          {hours.every((hour) => hour.total === 0) &&
            <div className="chart-empty"><strong>No DNS requests in this period</strong></div>}
        </div>
        <div className="mt-3 flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-t border-border pt-3">
          <div className="flex min-w-0 items-center gap-2">
            <Button variant="outline" size="icon-sm" type="button" aria-label="Previous hour"
              disabled={selectedIndex === 0} onClick={() => selectHour(selectedIndex - 1)}><ChevronLeft /></Button>
            <Button variant="outline" size="icon-sm" type="button" aria-label="Next hour"
              disabled={selectedIndex === hours.length - 1} onClick={() => selectHour(selectedIndex + 1)}><ChevronRight /></Button>
            <div aria-live="polite" aria-atomic="true" className="min-w-0 text-xs leading-5">
              <strong className="block font-medium">{trafficHourLabel(selected)}</strong>
              <span className="text-muted-foreground">{trafficHourCounts(selected)}</span>
            </div>
          </div>
          <a ref={returnLink} className="text-action inline-flex items-center gap-1 pointer-coarse:min-h-[44px]" href={trafficActivityPath(selected.start, selected.end)}
            onClick={(event) => {
              if (event.button || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
              event.preventDefault();
              history.replaceState({ ...history.state, faroTrafficHour: selected.start }, "");
              onViewHour(selected.start, selected.end);
            }}>
            View activity for this hour <ArrowRight size={14} aria-hidden="true" />
          </a>
        </div>
      </> : <TrafficChart activity={summary.sparklines?.activity ?? []} blocked={summary.sparklines?.blocked ?? []} />}
    </section>
  );
}
