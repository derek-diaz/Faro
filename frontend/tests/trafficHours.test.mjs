import assert from "node:assert/strict";
import { test } from "node:test";

process.env.TZ = "America/New_York";
const { trafficActivityPath, trafficAxisLabel, trafficHourLabel } = await import("../src/utils/trafficHours.ts");

test("repeated local hours have distinct labels and absolute DNS drill-downs", () => {
  const first = { start: "2026-11-01T05:00:00Z", end: "2026-11-01T06:00:00Z", total: 2, blocked: 1 };
  const second = { ...first, start: first.end, end: "2026-11-01T07:00:00Z" };
  assert.equal(trafficAxisLabel(first), trafficAxisLabel(second));
  assert.notEqual(trafficHourLabel(first), trafficHourLabel(second));
  const format = new Intl.DateTimeFormat(undefined, { timeZoneName: "short" });
  for (const bound of [first.start, first.end]) {
    const zone = format.formatToParts(new Date(bound)).find(part => part.type === "timeZoneName").value;
    assert.ok(trafficHourLabel(first).includes(zone));
  }
  for (const hour of [first, second]) {
    const url = new URL(trafficActivityPath(hour.start, hour.end), "http://faro.test");
    assert.equal(url.pathname, "/activity");
    assert.deepEqual(Object.fromEntries(url.searchParams), { scope: "dns", range: "custom", from: hour.start, to: hour.end });
  }
});
