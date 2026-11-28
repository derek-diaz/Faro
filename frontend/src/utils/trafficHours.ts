import type { TrafficHour } from "../api/client";

const dateFormat = new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric" });
const clock: Intl.DateTimeFormatOptions = { hour: "numeric", minute: "2-digit" };
const clockFormat = new Intl.DateTimeFormat(undefined, clock);

export function trafficAxisLabel(hour: TrafficHour) {
  return clockFormat.format(new Date(hour.start));
}

export function trafficHourLabel(hour: TrafficHour) {
  const start = new Date(hour.start);
  const end = new Date(hour.end);
  const startTime = start.getTimezoneOffset() === end.getTimezoneOffset()
    ? clockFormat.format(start)
    : start.toLocaleTimeString(undefined, { ...clock, timeZoneName: "short" });
  const endDate = start.toDateString() === end.toDateString() ? "" : dateFormat.format(end) + ", ";
  const endTime = end.toLocaleTimeString(undefined, {
    ...clock,
    second: end.getSeconds() ? "2-digit" : undefined,
    timeZoneName: "short"
  });
  return `${dateFormat.format(start)}, ${startTime} – ${endDate}${endTime}`;
}

export function trafficHourCounts(hour: TrafficHour) {
  return `${hour.total.toLocaleString()} ${hour.total === 1 ? "request" : "requests"} · ${hour.blocked.toLocaleString()} blocked`;
}

export function trafficActivityPath(from: string, to: string) {
  return "/activity?" + new URLSearchParams({ scope: "dns", range: "custom", from, to }).toString();
}
