const timelineBarHeights = [34, 54, 42, 68, 49, 78, 58, 88, 64, 46, 72, 52, 82, 61, 40, 70, 50, 76] as const;
const tableLoadingRows = Array.from({ length: 6 }, (_, index) => index);

export function ActivityTimelineLoading() {
  return (
    <div className="activity-timeline-chart activity-timeline-loading" role="status" aria-label="Loading activity timeline">
      <span className="sr-only">Loading activity timeline…</span>
      <div className="activity-chart-skeleton" aria-hidden="true">
        <div className="activity-chart-skeleton-grid"><span /><span /><span /><span /></div>
        <div className="activity-chart-skeleton-bars">
          {timelineBarHeights.map((height, index) => <i key={index} style={{ height: `${height}%` }} />)}
        </div>
      </div>
      <div className="activity-timeline-loading-note" aria-hidden="true">
        <span className="activity-loading-spinner" />
        <span><strong>Loading timeline</strong><small>Plotting activity for this range…</small></span>
      </div>
      <div className="activity-timeline-meta" aria-hidden="true">
        <span><i className="activity-timeline-legend-swatch total" />Queries and events</span>
        <span><i className="activity-timeline-legend-swatch blocked" />Blocked</span>
        <span className="activity-timeline-bucket-size">Preparing range</span>
      </div>
    </div>
  );
}

export function ActivityTableLoading() {
  return (
    <div className="activity-table-wrap activity-table-loading" role="status" aria-label="Loading activity events">
      <span className="sr-only">Loading activity events…</span>
      <table className="monitor-table event-table" aria-hidden="true">
        <thead>
          <tr><th>Time</th><th>Result</th><th>Domain or event</th><th>Device</th><th>Type</th><th>Source</th><th /></tr>
        </thead>
        <tbody>
          {tableLoadingRows.map((row) => (
            <tr key={row}>
              <td className="time-cell"><span className="activity-skeleton-line short" /><span className="activity-skeleton-line tiny" /></td>
              <td><span className="activity-skeleton-pill" /></td>
              <td className="event-subject-cell"><span className="activity-skeleton-domain"><span className="activity-skeleton-icon" /><span className="activity-skeleton-line subject" /></span><span className="activity-skeleton-line description" /></td>
              <td><span className="activity-skeleton-line device" /></td>
              <td><span className="activity-skeleton-line type" /></td>
              <td><span className="activity-skeleton-line source" /></td>
              <td><span className="activity-skeleton-action" /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
