"""Disposable Docker load test; see docs/dns-load-testing.md. Standard library only."""

import asyncio
import collections
from contextlib import closing
import http.cookiejar
import json
import math
import os
import socket
import sqlite3
import struct
import sys
import time
import urllib.error
import urllib.request


def question(name, qtype, ident):
    labels = b"".join(bytes([len(part)]) + part.encode() for part in name.split("."))
    return struct.pack("!6H", ident, 0x100, 1, 0, 0, 0) + labels + b"\0" + struct.pack("!HH", qtype, 1)


def read_name(packet, offset, depth=0):
    assert depth < 10, "DNS compression loop"
    labels = []
    while packet[offset]:
        size = packet[offset]
        if size & 0xC0 == 0xC0:
            name, _ = read_name(packet, ((size & 63) << 8) | packet[offset + 1], depth + 1)
            return ".".join(labels + [name]), offset + 2
        labels.append(packet[offset + 1:offset + 1 + size].decode())
        offset += size + 1
    return ".".join(labels), offset + 1


def unpack_question(packet):
    name, offset = read_name(packet, 12)
    return name, struct.unpack_from("!H", packet, offset)[0], offset + 4


def answer(packet):
    name, qtype, end = unpack_question(packet)
    missing = name.startswith("missing-")
    data = socket.inet_pton(socket.AF_INET6, "2001:db8::10") if qtype == 28 else socket.inet_aton("192.0.2.10")
    header = struct.pack("!6H", struct.unpack_from("!H", packet)[0], 0x8183 if missing else 0x8180, 1, 0 if missing else 1, 0, 0)
    record = b"" if missing else b"\xc0\x0c" + struct.pack("!HHIH", qtype, 1, 60, len(data)) + data
    return header + packet[12:end] + record


class Upstream(asyncio.DatagramProtocol):
    def connection_made(self, transport):
        self.transport = transport

    def datagram_received(self, data, address):
        # Repeatable 15–35 ms upstream RTT, independent of the resolver's cache.
        delay = 0.015 + struct.unpack_from("!H", data)[0] % 21 / 1000
        asyncio.get_running_loop().call_later(delay, self.transport.sendto, answer(data), address)


async def upstream_tcp(reader, writer):
    try:
        while True:
            size = struct.unpack("!H", await reader.readexactly(2))[0]
            data = await reader.readexactly(size)
            await asyncio.sleep(0.025)
            response = answer(data)
            writer.write(struct.pack("!H", len(response)) + response)
            await writer.drain()
    except (asyncio.IncompleteReadError, ConnectionError):
        pass
    finally:
        writer.close()
        await writer.wait_closed()


async def blocklist_http(reader, writer):
    await reader.readuntil(b"\r\n\r\n")
    body = "".join(f"b{i}.load.test\n" for i in range(100000)).encode()
    writer.write(f"HTTP/1.1 200 OK\r\nContent-Length: {len(body)}\r\nConnection: close\r\n\r\n".encode() + body)
    await writer.drain()
    writer.close()
    await writer.wait_closed()


async def serve():
    await asyncio.get_running_loop().create_datagram_endpoint(Upstream, local_addr=("0.0.0.0", 53))
    await asyncio.start_server(upstream_tcp, "0.0.0.0", 53)
    await asyncio.start_server(blocklist_http, "0.0.0.0", 8081)
    print("Controlled upstream ready", flush=True)
    await asyncio.Event().wait()


opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))


def api(path, payload=None, method=None):
    data = None if payload is None else json.dumps(payload).encode()
    request = urllib.request.Request("http://127.0.0.1:1787" + path, data=data, method=method,
                                     headers={"Content-Type": "application/json"})
    try:
        with opener.open(request, timeout=60) as response:
            return json.load(response)
    except urllib.error.HTTPError as error:
        raise RuntimeError(f"{path}: {error.code}: {error.read().decode()}") from error


def setup():
    credentials = {"username": "load-test", "password": "Disposable-load-test-only-123!"}
    if not api("/api/auth/status")["configured"]:
        api("/api/auth/setup", credentials)
    else:
        api("/api/auth/login", credentials)
    api("/api/settings", {"upstream_dns": "172.30.254.2", "upstream_transport": "standard",
                          "favicon_fetching_enabled": "false", "onboarding_completed": "true"}, "PUT")
    if not any(item["name"] == "Load test" for item in api("/api/blocklists")):
        blocklist = api("/api/blocklists", {"name": "Load test", "url": "http://172.30.254.2:8081/list"})
        api(f"/api/blocklists/{blocklist['id']}/refresh", {}, "POST")
        for qtype, value in [("A", "192.0.2.20"), ("AAAA", "2001:db8::20")]:
            api("/api/dns-records", {"hostname": "printer.home", "type": qtype, "value": value})
    if os.environ.get("FARO_LOAD_PROFILES") == "1":
        names = {item["name"] for item in api("/api/protections")}
        blocklist_id = next(item["id"] for item in api("/api/blocklists") if item["name"] == "Load test")
        for number in (1, 2):
            name = f"Load group {number}"
            if name not in names:
                api("/api/protections", {"name": name, "icon": "users", "blocklist_ids": [blocklist_id],
                    "allow_domains": [], "block_domains": [],
                    "device_ips": [f"127.20.0.{i}" for i in range(1 + (number - 1) * 20, 1 + number * 20)]})
    print(json.dumps({"setup": "ready", "blocklists": api("/api/blocklists")}), flush=True)


def case(index, phase):
    kind = index % 100
    qtype = 28 if (index // 7) % 4 == 0 else 1
    if kind < 20:
        name, expected = f"b{index % 1000}.load.test", "blocked"
    elif kind < 25:
        name, expected = "printer.home", "local"
    elif kind < 30:
        name, expected = f"missing-{phase}-{index}.load.test", "missing"
    elif kind < 50:
        name, expected = f"cold-{phase}-{index}.load.test", "upstream"
    else:
        name, expected = f"hot-{(index // 100 * 50 + kind) % 200}.load.test", "upstream"
    return name, qtype, expected


def validate(packet, query, expected):
    ident, flags, questions, answers, _, _ = struct.unpack_from("!6H", packet)
    name, qtype, end = unpack_question(query)
    assert ident == struct.unpack_from("!H", query)[0] and flags & 0x8000 and not flags & 0x200, "DNS header"
    assert questions == 1 and packet[12:end] == query[12:end], "DNS question"
    assert flags & 15 == (3 if expected == "missing" else 0), f"rcode={flags & 15}, {name}"
    if expected == "missing" or expected == "blocked" and qtype == 28:
        assert answers == 0, f"unexpected answer for {name}"
        return
    assert answers == 1, f"answer count={answers}, {name}"
    answer_name, offset = read_name(packet, end)
    assert answer_name == name, "answer name"
    typ, cls, _, size = struct.unpack_from("!HHIH", packet, offset)
    assert typ == qtype and cls == 1, "answer type/class"
    if expected == "blocked":
        ip = "0.0.0.0"
    elif expected == "local":
        ip = "2001:db8::20" if qtype == 28 else "192.0.2.20"
    else:
        ip = "2001:db8::10" if qtype == 28 else "192.0.2.10"
    family = socket.AF_INET6 if qtype == 28 else socket.AF_INET
    assert packet[offset + 10:offset + 10 + size] == socket.inet_pton(family, ip), f"wrong address for {name}"


async def probe(index, phase):
    name, qtype, expected = case(index, phase)
    query = question(name, qtype, index % 65536)
    source = f"127.20.0.{index % 50 + 1}"
    if index % 19 == 0:
        reader, writer = await asyncio.open_connection("127.0.0.1", 53, local_addr=(source, 0))
        try:
            writer.write(struct.pack("!H", len(query)) + query)
            await writer.drain()
            size = struct.unpack("!H", await reader.readexactly(2))[0]
            response = await reader.readexactly(size)
        finally:
            writer.close()
            await writer.wait_closed()
    else:
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as connection:
            connection.setblocking(False)
            connection.bind((source, 0))
            connection.connect(("127.0.0.1", 53))
            loop = asyncio.get_running_loop()
            await loop.sock_sendall(connection, query)
            response = await loop.sock_recv(connection, 4096)
    validate(response, query, expected)


def percentile(values, fraction):
    return round(sorted(values)[max(0, math.ceil(len(values) * fraction) - 1)], 2) if values else None


def history_db():
    return closing(sqlite3.connect("file:/config/faro.db?mode=ro", uri=True, timeout=10))


def wait_for_history(baseline, count):
    started = time.monotonic()
    with history_db() as database:
        while True:
            recorded, clients = database.execute("""
                SELECT COUNT(*), COUNT(DISTINCT client_ip) FROM dns_queries
                WHERE id > ? AND client_ip LIKE '127.20.%'
            """, (baseline,)).fetchone()
            elapsed = time.monotonic() - started
            if recorded >= count or elapsed >= 45:
                return recorded, clients, elapsed
            time.sleep(1)


async def run_phase(rate, seconds, label):
    with history_db() as database:
        baseline = database.execute("SELECT COALESCE(MAX(id), 0) FROM dns_queries").fetchone()[0]
    latencies = []
    api_times = collections.defaultdict(list)
    errors, api_errors = collections.Counter(), collections.Counter()
    active = set()
    peak_inflight, late = 0, 0
    started = time.monotonic()

    async def one(index, scheduled):
        try:
            await asyncio.wait_for(probe(index, label), 2)
        except Exception as error:
            errors[f"{type(error).__name__}: {error}"] += 1
        finally:
            # Includes scheduler delay, so overload cannot hide behind the generator.
            latencies.append((time.monotonic() - scheduled) * 1000)

    async def poll_api():
        paths = ["/api/dashboard", "/api/events?detail=rows&page_size=50", "/api/events?detail=summary&range=24h", "/api/devices?format=page&page_size=50"]
        while time.monotonic() - started < seconds:
            for path in paths:
                before = time.monotonic()
                try:
                    await asyncio.to_thread(api, path)
                    api_times[path].append((time.monotonic() - before) * 1000)
                except Exception as error:
                    api_errors[str(error)] += 1
            await asyncio.sleep(1)

    async def reload_config():
        if os.environ.get("FARO_LOAD_RELOAD") == "1":
            await asyncio.sleep(seconds / 2)
            before = time.monotonic()
            try:
                settings = await asyncio.to_thread(api, "/api/settings")
                current = next(item["value"] for item in settings if item["key"] == "dns_cache_ttl")
                ttl = "301" if current == "300" else "300"
                await asyncio.to_thread(api, "/api/settings", {"dns_cache_ttl": ttl}, "PUT")
                settings = await asyncio.to_thread(api, "/api/settings")
                assert next(item["value"] for item in settings if item["key"] == "dns_cache_ttl") == ttl
                print(json.dumps({"reload_seconds": round(time.monotonic() - before, 2), "cache_ttl": ttl}), flush=True)
            except Exception as error:
                api_errors[f"reload: {error}"] += 1

    polling = asyncio.create_task(poll_api())
    reloading = asyncio.create_task(reload_config())
    count = rate * seconds
    for index in range(count):
        scheduled = started + index / rate
        remaining = scheduled - time.monotonic()
        if remaining > 0:
            await asyncio.sleep(remaining)
        elif remaining < -0.05:
            late += 1
        task = asyncio.create_task(one(index, scheduled))
        active.add(task)
        task.add_done_callback(active.discard)
        peak_inflight = max(peak_inflight, len(active))
        if len(active) > 4096:
            raise RuntimeError("Load generator overloaded (4096 in flight)")
    await asyncio.gather(*active)
    elapsed = time.monotonic() - started
    recorded, clients, drain_seconds = await asyncio.to_thread(wait_for_history, baseline, count)
    await asyncio.gather(polling, reloading)
    with history_db() as database:
        sources = dict(database.execute("SELECT source, COUNT(*) FROM dns_queries WHERE id > ? AND client_ip LIKE '127.20.%' GROUP BY source", (baseline,)))
        duplicates = database.execute("""
            SELECT COUNT(*) FROM (
                SELECT domain FROM dns_queries WHERE id > ? AND domain LIKE ?
                GROUP BY domain HAVING COUNT(*) > 1
            )
        """, (baseline, f"cold-{label}-%")).fetchone()[0]
        wrong_local_type = database.execute("""
            SELECT COUNT(*) FROM dns_queries WHERE id > ? AND domain = 'printer.home'
              AND client_ip LIKE '127.20.%' AND (source <> 'local' OR
                  json_extract(decision_metadata, '$.local_record.type') IS NOT query_type)
        """, (baseline,)).fetchone()[0]
    all_api_times = [ms for samples in api_times.values() for ms in samples]
    result = {"phase": label, "target_qps": rate, "sent": count, "elapsed_seconds": round(elapsed, 2),
              "dns_errors": dict(errors), "dns_p50_ms": percentile(latencies, .5), "dns_p95_ms": percentile(latencies, .95),
              "dns_p99_ms": percentile(latencies, .99), "dns_max_ms": round(max(latencies), 2),
              "api_requests": len(all_api_times), "api_errors": dict(api_errors), "api_p95_ms": percentile(all_api_times, .95),
              "api_p95_ms_by_path": {path: percentile(samples, .95) for path, samples in api_times.items()},
              "history_rows": recorded, "history_clients": clients, "history_drain_seconds": round(drain_seconds, 2),
              "history_sources": sources, "duplicate_cold_queries": duplicates, "wrong_local_record_type": wrong_local_type,
              "generator_late_over_50ms": late, "peak_inflight": peak_inflight}
    print(json.dumps(result), flush=True)
    return (not errors and not api_errors and recorded == count and clients == min(50, count)
            and late == 0 and duplicates == 0 and wrong_local_type == 0)


async def load():
    await asyncio.to_thread(setup)
    await asyncio.sleep(4)
    passed = True
    for phase in os.environ.get("FARO_LOAD_PHASES", "10:60,100:60,500:60,2000:60").split(","):
        rate, seconds = map(int, phase.split(":"))
        assert 0 < rate <= 10000 and 0 < seconds <= 600
        passed = await run_phase(rate, seconds, f"q{rate}-{time.time_ns()}") and passed
    if not passed:
        raise SystemExit(1)


def self_check():
    for qtype in (1, 28):
        for name, expected in [("hot-1.load.test", "upstream"), ("missing-1.load.test", "missing")]:
            query = question(name, qtype, 42)
            validate(answer(query), query, expected)
            if expected == "upstream":
                response = answer(query)
                end = len(query)
                validate(response[:end] + query[12:-4] + response[end + 2:], query, expected)
            try:
                validate(answer(query)[1:], query, expected)
            except (AssertionError, struct.error):
                pass
            else:
                raise AssertionError("invalid packet accepted")
    assert percentile([1, 2, 3, 4], .95) == 4
    assert collections.Counter(case(i, "self-check")[2] for i in range(100)) == {"upstream": 70, "blocked": 20, "local": 5, "missing": 5}
    print("Load harness self-check passed", flush=True)


if __name__ == "__main__":
    self_check()
    if len(sys.argv) > 1 and sys.argv[1] != "self-check":
        asyncio.run(serve() if sys.argv[1] == "upstream" else load())
