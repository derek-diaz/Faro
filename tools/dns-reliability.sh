#!/usr/bin/env bash
set -Eeuo pipefail

# This is a disposable Docker smoke test for the production image. The Go
# suite covers staged validation, rollback, blocklists, DoH failure semantics,
# replica failure, and database cleanup; this script covers the process and
# container boundaries that need a real CoreDNS listener.

command -v docker >/dev/null 2>&1 || { echo "docker is required" >&2; exit 1; }
command -v curl >/dev/null 2>&1 || { echo "curl is required" >&2; exit 1; }
if command -v dig >/dev/null 2>&1; then
  dns_probe() {
    dig @127.0.0.1 -p "$dns_port" +time=2 +tries=1 +short example.com
  }
elif command -v node >/dev/null 2>&1; then
  dns_probe() {
    FARO_DNS_PROBE_PORT="$dns_port" node -e '
      const dns = require("dns").promises;
      const resolver = new dns.Resolver();
      resolver.setServers([`127.0.0.1:${process.env.FARO_DNS_PROBE_PORT}`]);
      resolver.resolve4("example.com").then(addresses => console.log(addresses.join("\n"))).catch(() => process.exit(1));
    '
  }
else
  echo "dig or node is required" >&2
  exit 1
fi

project="faro-dns-reliability-$RANDOM-$$"
ui_port="${FARO_RELIABILITY_UI_PORT:-18787}"
dns_port="${FARO_RELIABILITY_DNS_PORT:-15353}"
cookie_file="$(mktemp)"

compose_run() {
  FARO_UI_PORT="$ui_port" FARO_DNS_PORT="$dns_port" \
    docker compose -p "$project" -f docker-compose.yml -f docker-compose.build.yml "$@"
}

cleanup() {
  compose_run down -v --remove-orphans >/dev/null 2>&1 || true
  rm -f "$cookie_file"
}
trap cleanup EXIT INT TERM

assert_dns() {
  local answer
  answer="$(dns_probe)"
  if [[ -z "$answer" ]]; then
    echo "Faro did not return an address for example.com" >&2
    return 1
  fi
}

wait_for_start() {
  for _ in $(seq 1 90); do
    if curl -fsS "http://127.0.0.1:${ui_port}/healthz" >/dev/null 2>&1 && assert_dns; then
      return 0
    fi
    sleep 2
  done
  compose_run logs --tail=120 faro || true
  return 1
}

compose_run up -d --build
wait_for_start

compose_run restart faro
wait_for_start

# Switch the disposable instance to encrypted DNS before taking the control
# plane down. The query after the API failure must still use the standalone
# gateway and must not silently fall back to plaintext DNS.
curl -fsS -c "$cookie_file" \
  -H 'Content-Type: application/json' \
  -d '{"username":"reliability-admin","password":"correct-horse-battery-staple"}' \
  "http://127.0.0.1:${ui_port}/api/auth/setup" >/dev/null
curl -fsS -b "$cookie_file" \
  -H 'Content-Type: application/json' \
  -X PUT \
  -d '{"upstream_transport":"encrypted","upstream_dns":"1.1.1.1,9.9.9.9"}' \
  "http://127.0.0.1:${ui_port}/api/settings" >/dev/null
wait_for_start
if ! compose_run exec -T faro sh -c 'kill -0 "$(cat /tmp/faro-doh.pid)"'; then
  echo "standalone encrypted DNS gateway is not running" >&2
  exit 1
fi

# The web proxy can be unavailable without taking CoreDNS down.
compose_run exec -T faro sh -c 'kill -TERM "$(cat /tmp/nginx.pid)"'
sleep 2
if curl -fsS "http://127.0.0.1:${ui_port}/healthz" >/dev/null 2>&1; then
  echo "web proxy remained available after its process was terminated" >&2
  exit 1
fi
assert_dns

# The API process is now only a control-plane dependency. The accepted DoH
# snapshot and the standalone gateway must keep the encrypted path alive.
compose_run exec -T faro sh -c 'kill -TERM "$(cat /tmp/faro-api.pid)"'
sleep 2
if curl -fsS "http://127.0.0.1:${ui_port}/healthz" >/dev/null 2>&1; then
  echo "API health remained available after the API process was terminated" >&2
  exit 1
fi
if ! compose_run exec -T faro sh -c 'kill -0 "$(cat /tmp/faro-doh.pid)"'; then
  echo "standalone encrypted DNS gateway stopped with the API" >&2
  exit 1
fi
assert_dns

echo "DNS reliability smoke test passed"
