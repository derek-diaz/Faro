import { lazy, Suspense } from "react";
import type { ReplayBucket } from "../api/client";

type ReplayTimelineProps = {
  readonly buckets: ReplayBucket[];
  readonly progress: number;
  readonly onSeek: (progress: number) => void;
};

const ReplayTimelineCanvas = lazy(async () => {
  const module = await import("./DashboardCharts");
  return { default: module.ReplayTimelineCanvas };
});

export function ReplayTimeline({ buckets, progress, onSeek }: ReplayTimelineProps) {
  const hasData = buckets.some((bucket) => bucket.total > 0);

  return (
    <div className="replay-timeline">
      <div className="replay-timeline-canvas">
        <Suspense fallback={null}>
          <ReplayTimelineCanvas buckets={buckets} progress={progress} onSeek={onSeek} />
        </Suspense>
      </div>
      {!hasData && <div className="replay-empty-label">No DNS activity in this period</div>}
    </div>
  );
}
