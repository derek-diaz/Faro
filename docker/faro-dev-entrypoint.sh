#!/bin/bash
set -Eeuo pipefail

air_pid=""
ui_pid=""
dns_pid=""
logtee_pid=""
fifo_path=""

shutdown() {
  trap - EXIT INT TERM
  for pid in "$ui_pid" "$dns_pid" "$logtee_pid" "$air_pid"; do
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      kill -TERM "$pid" 2>/dev/null || true
    fi
  done
  for pid in "$ui_pid" "$dns_pid" "$logtee_pid" "$air_pid"; do
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

config_dir="${FARO_COREDNS_CONFIG_DIR:-/coredns}"
log_path="${FARO_COREDNS_LOG_PATH:-/var/log/coredns/query.log}"
db_path="${FARO_DB_PATH:-/data/faro.db}"
favicon_dir="${FARO_FAVICON_DIR:-/data/favicons}"

mkdir -p "$config_dir" "$(dirname "$log_path")" "$(dirname "$db_path")" "$favicon_dir"

if [[ "$config_dir" != "/etc/coredns" ]]; then
  rm -rf /etc/coredns
  ln -s "$config_dir" /etc/coredns
fi

air -c /app/.air.toml &
air_pid=$!

(
  cd /app/frontend
  npm ci
  exec npm run dev
) &
ui_pid=$!

api_ready=false
for _ in $(seq 1 480); do
  if ! kill -0 "$air_pid" 2>/dev/null; then
    wait "$air_pid"
    exit $?
  fi
  if curl -fsS http://127.0.0.1:8080/healthz >/dev/null 2>&1 && [[ -s "$config_dir/Corefile" ]]; then
    api_ready=true
    break
  fi
  sleep 0.25
done

if [[ "$api_ready" != "true" ]]; then
  echo "Faro's development API did not produce a CoreDNS configuration within 120 seconds" >&2
  exit 1
fi

fifo_path="/tmp/faro-dev-coredns-output.$$"
mkfifo "$fifo_path"
faro-logtee \
  --path "$log_path" \
  --max-bytes "${FARO_QUERY_LOG_MAX_BYTES:-10485760}" \
  --backups "${FARO_QUERY_LOG_BACKUPS:-2}" <"$fifo_path" &
logtee_pid=$!

coredns -conf "$config_dir/Corefile" >"$fifo_path" 2>&1 &
dns_pid=$!

set +e
wait -n "$air_pid" "$ui_pid" "$dns_pid" "$logtee_pid"
status=$?
set -e
echo "A required Faro development process exited; stopping the container" >&2
exit "$status"
