import assert from "node:assert/strict";
import { test } from "node:test";
import { canFetchFavicon, loadFavicon } from "../src/api/favicons.ts";

test("local, diagnostic, reserved, and malformed domains never request icons", async (t) => {
  const fetch = t.mock.method(globalThis, "fetch", () => { throw new Error("unexpected request"); });
  for (const domain of ["version.bind", "ads.example", "faro-dns-test-178847778033769400.invalid", "printer.home", "router.lan", "printer.local", "foo.localhost", "foo.test", "1.0.0.127.in-addr.arpa", "localhost", "127.0.0.1", "bad..com", "-bad.com", "bad-.com", "www.www.www.example.com"]) {
    assert.equal(canFetchFavicon(domain), false, domain);
    assert.equal(await loadFavicon(domain), null, domain);
  }
  assert.equal(fetch.mock.callCount(), 0);
  assert.equal(canFetchFavicon(" Example.COM. "), true);
});

test("concurrent rows and remounts share a normalized domain request", async (t) => {
  let resolve;
  const fetch = t.mock.method(globalThis, "fetch", () => new Promise((done) => { resolve = done; }));
  const first = loadFavicon(" Shared.COM. ");
  const second = loadFavicon("shared.com");
  assert.equal(first, second);
  assert.equal(fetch.mock.callCount(), 1);
  assert.equal(fetch.mock.calls[0].arguments[0], "/api/favicons/shared.com");
  resolve(new Response(new Blob(["icon"], { type: "image/png" })));
  const blob = await first;
  assert.equal(await loadFavicon("shared.com"), blob);
  assert.equal(fetch.mock.callCount(), 1);
});

test("missing icons stay cached until the retry window expires", async (t) => {
  let now = Date.now();
  t.mock.method(Date, "now", () => now);
  const fetch = t.mock.method(globalThis, "fetch", async () => new Response("placeholder", { headers: { "X-Faro-Favicon": "placeholder" } }));
  assert.equal(await loadFavicon("missing.com"), null);
  assert.equal(await loadFavicon("missing.com"), null);
  assert.equal(fetch.mock.callCount(), 1);
  now += 15 * 60 * 1000 + 1;
  assert.equal(await loadFavicon("missing.com"), null);
  assert.equal(fetch.mock.callCount(), 2);
});

test("HTTP and network failures do not produce repeated requests", async (t) => {
  const fetch = t.mock.method(globalThis, "fetch", async () => new Response(null, { status: 404 }));
  assert.equal(await loadFavicon("http-failure.com"), null);
  assert.equal(await loadFavicon("http-failure.com"), null);
  assert.equal(fetch.mock.callCount(), 1);
  fetch.mock.mockImplementation(async () => { throw new TypeError("offline"); });
  assert.equal(await loadFavicon("network-failure.com"), null);
  assert.equal(await loadFavicon("network-failure.com"), null);
  assert.equal(fetch.mock.callCount(), 2);
});

test("a disabled server response does not hide icons after enabling", async (t) => {
  const fetch = t.mock.method(globalThis, "fetch", async () => new Response(null, { status: 204, headers: { "Cache-Control": "no-store", "X-Faro-Favicon": "placeholder" } }));
  assert.equal(await loadFavicon("toggle.com"), null);
  fetch.mock.mockImplementation(async () => new Response("icon"));
  assert.ok(await loadFavicon("toggle.com") instanceof Blob);
  assert.equal(fetch.mock.callCount(), 2);
});

test("the browser cache remains bounded", async (t) => {
  const fetch = t.mock.method(globalThis, "fetch", async () => new Response(null, { headers: { "X-Faro-Favicon": "placeholder" } }));
  for (let index = 0; index < 257; index += 1) await loadFavicon(`bounded-${index}.com`);
  await loadFavicon("bounded-256.com");
  assert.equal(fetch.mock.callCount(), 257);
  await loadFavicon("bounded-0.com");
  assert.equal(fetch.mock.callCount(), 258);
});
