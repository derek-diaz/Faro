import assert from "node:assert/strict";
import test from "node:test";
import { packettaleBlocklists } from "../src/data/packettale.ts";

test("Packettale sources have distinct install names, IDs and feed URLs", () => {
  assert.ok(packettaleBlocklists.length > 0);
  for (const field of ["id", "name", "url"]) {
    assert.equal(new Set(packettaleBlocklists.map(item => item[field])).size, packettaleBlocklists.length, field);
  }
  for (const item of packettaleBlocklists) {
    assert.equal(item.provider, "Packettale");
    assert.match(item.url, /^https:\/\/blocklist\.packettale\.com\/(groups\/)?[a-z0-9-]+(\.domains)?\.txt$/);
  }
});

test("catalog covers every published Packettale domain category and compatible bundle", {
  skip: process.env.FARO_CHECK_LIVE_FEEDS !== "1",
}, async () => {
  const origin = "https://blocklist.packettale.com/";
  const response = await fetch(origin + "stats.json", { signal: AbortSignal.timeout(30_000) });
  assert.equal(response.status, 200);
  const { categories, groups, bundles, feeds } = await response.json();
  const keys = [...Object.keys(categories), ...Object.values(groups).map(group => group.feed), ...Object.keys(bundles)];
  const expected = [];
  for (const key of keys) {
    const feed = feeds[key + ".domains"] ?? feeds[key];
    // Match the 64 MiB download limit in internal/blocklists/blocklists.go.
    if (feed?.domains > 0 && feed.ips === 0 && feed.bytes <= 64 * 1024 * 1024) {
      expected.push(new URL(feed.url, origin).href);
    }
  }
  assert.deepEqual(packettaleBlocklists.map(item => item.url).sort(), expected.sort());
});
