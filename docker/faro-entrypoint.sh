#!/bin/bash
set -Eeuo pipefail

api_pid=""
doh_pid=""
dns_pid=""
logtee_pid=""
nginx_pid=""
fifo_path=""

shutdown() {
  trap - EXIT INT TERM
  for pid in "$nginx_pid" "$doh_pid" "$dns_pid" "$logtee_pid" "$api_pid"; do
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      kill -TERM "$pid" 2>/dev/null || true
    fi
  done
  for pid in "$nginx_pid" "$doh_pid" "$dns_pid" "$logtee_pid" "$api_pid"; do
    if [[ -n "$pid" ]]; then
      wait "$pid" 2>/dev/null || true
    fi
  done
  if [[ -n "$fifo_path" ]]; then
    rm -f "$fifo_path"
  fi
}

trap 'exit 143' INT TERM
trap shutdown EXIT

config_dir="${FARO_COREDNS_CONFIG_DIR:-/config/coredns}"
log_path="${FARO_COREDNS_LOG_PATH:-/config/logs/query.log}"
db_path="${FARO_DB_PATH:-/config/faro.db}"
favicon_dir="${FARO_FAVICON_DIR:-/config/favicons}"
upgrade_state_path="${FARO_UPGRADE_STATE_PATH:-$(dirname "$db_path")/faro-upgrade.json}"

show_upgrade_state() {
  if [[ -f "$upgrade_state_path" ]]; then
    echo "Faro upgrade state ($upgrade_state_path):" >&2
    sed -n '1,160p' "$upgrade_state_path" >&2 || true
  fi
}

mkdir -p "$config_dir" "$(dirname "$log_path")" "$(dirname "$db_path")" "$favicon_dir"

# Generated Corefiles use the resolver's canonical /etc/coredns path. Link it
# to the selected persistent directory so both standard Docker and Unraid can
# choose their preferred host layout without changing generated configuration.
if [[ "$config_dir" != "/etc/coredns" ]]; then
  rm -rf /etc/coredns
  ln -s "$config_dir" /etc/coredns
fi

start_dns_stack() {
  faro-dohproxy &
  doh_pid=$!
  echo "$doh_pid" > /tmp/faro-doh.pid

  fifo_path="/tmp/faro-coredns-output.$$"
  mkfifo "$fifo_path"
  faro-logtee \
    --path "$log_path" \
    --max-bytes "${FARO_QUERY_LOG_MAX_BYTES:-10485760}" \
    --backups "${FARO_QUERY_LOG_BACKUPS:-2}" <"$fifo_path" &
  logtee_pid=$!

  coredns -conf "$config_dir/Corefile" >"$fifo_path" 2>&1 &
  dns_pid=$!
  echo "$dns_pid" > /tmp/faro-dns.pid

  nginx -g 'daemon off;' &
  nginx_pid=$!
}

wait_for_dns_stack() {
  set +e
  wait -n "$dns_pid" "$doh_pid"
  status=$?
  set -e
  echo "CoreDNS exited; stopping the container so Docker can restart DNS" >&2
  exit "$status"
}

faro-api &
api_pid=$!
echo "$api_pid" > /tmp/faro-api.pid

api_ready=false
for _ in $(seq 1 240); do
  if ! kill -0 "$api_pid" 2>/dev/null; then
    set +e
    wait "$api_pid"
    status=$?
    set -e
    show_upgrade_state
    if [[ -s "$config_dir/Corefile" ]]; then
      echo "Faro API is unavailable; starting the persisted DNS configuration" >&2
      start_dns_stack
      wait_for_dns_stack
    fi
    exit "$status"
  fi
  if curl -fsS http://127.0.0.1:8080/healthz >/dev/null 2>&1 && [[ -s "$config_dir/Corefile" ]]; then
    api_ready=true
    break
  fi
  sleep 0.25
done

if [[ "$api_ready" != "true" ]]; then
  echo "Faro API did not produce a CoreDNS configuration within 60 seconds" >&2
  show_upgrade_state
  if [[ -s "$config_dir/Corefile" ]]; then
    echo "Faro API is still unavailable; starting the persisted DNS configuration" >&2
    start_dns_stack
    wait_for_dns_stack
  fi
  exit 1
fi

start_dns_stack

# CoreDNS is the availability boundary. The UI, reverse proxy, and API are
# control-plane processes; if one of them exits, leave CoreDNS serving its
# last-known-good files while Docker health reports the control-plane failure.
wait_for_dns_stack
