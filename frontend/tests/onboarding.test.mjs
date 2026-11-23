import assert from "node:assert/strict";
import { test } from "node:test";
import { normalizeLANAddress, recentClientQueries, starterListScopeError, validSuffix } from "../src/utils/onboarding.ts";

test("setup changes a starter source only when other protections cannot be affected", () => {
  const home = { id: 1, name: "Home", blocklist_ids: [10] };
  const work = { id: 2, name: "Work", blocklist_ids: [10] };
  const shared = { id: 10, enabled: false, entry_count: 100, protection_count: 2 };
  assert.match(starterListScopeError(shared, [home, work], home.id), /Work/);
  assert.match(starterListScopeError({ ...shared, enabled: true, entry_count: 0 }, [home, work], home.id), /Work/);
  assert.equal(starterListScopeError({ ...shared, enabled: true }, [home, work], home.id), "");
  assert.equal(starterListScopeError({ ...shared, protection_count: 1 }, [home], home.id), "");
  assert.match(starterListScopeError(shared, [home], home.id), /could not confirm/);
});

test("LAN address validation accepts IPv4 and IPv6 and rejects malformed or local-only addresses", () => {
  assert.equal(normalizeLANAddress(" 192.168.1.10 "), "192.168.1.10");
  assert.equal(normalizeLANAddress("[fd00:0001::10]"), "fd00:1::10");
  assert.equal(normalizeLANAddress("::ffff:192.168.1.10"), "192.168.1.10");
  for (const value of ["", "localhost", "192.168.1", "192.168.1.256", "192.168.001.10", "127.0.0.1", "0.0.0.0", "224.0.0.1", "::", "::1", "ff02::1", "::ffff:127.0.0.1", "fd00:::10", "1:2:3", "abcd:"]) {
    assert.equal(normalizeLANAddress(value), "", value);
  }
});

test("local suffix validation rejects multi-label and malformed suffixes", () => {
  assert.equal(validSuffix(" HOME "), true);
  assert.equal(validSuffix("home-lan"), true);
  for (const value of ["", "my.home", "-home", "home-", "a".repeat(64)]) assert.equal(validSuffix(value), false, value);
});

test("connection checks require fresh client traffic after applying setup", () => {
  const now = Date.parse("2026-10-03T18:00:00Z");
  const applied = now - 60_000;
  const query = (overrides = {}) => ({ timestamp: new Date(now - 5_000).toISOString(), client_ip: "192.168.1.20", domain: "example.com", ...overrides });
  const valid = query();
  const ipv6 = query({ client_ip: "fd00::20" });
  const stale = query({ timestamp: new Date(now - 120_000).toISOString() });
  const invalidTime = query({ timestamp: "not a date" });
  const future = query({ timestamp: new Date(now + 60_000).toISOString() });
  const self = query({ client_ip: "192.168.1.10" });
  const mappedSelf = query({ client_ip: "::ffff:192.168.1.10" });
  const loopback = query({ client_ip: "127.0.0.1" });
  const diagnostic = query({ domain: "faro-dns-test-123.invalid." });
  assert.deepEqual(recentClientQueries([valid, ipv6, stale, invalidTime, future, self, mappedSelf, loopback, diagnostic], "192.168.1.10", applied, now), [valid, ipv6]);
  assert.deepEqual(recentClientQueries([query({ timestamp: new Date(now - 360_000).toISOString() })], "192.168.1.10", now - 3_600_000, now), []);
});
