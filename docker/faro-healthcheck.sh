#!/bin/sh
set -eu

curl -fsS http://127.0.0.1:1787/healthz >/dev/null
curl -fsS http://127.0.0.1:9153/metrics >/dev/null
