#!/usr/bin/env bash
# Usage: scripts/coverage.sh <coverprofile> <min-percent>
# Computes combined statement coverage of internal/... (cmd/paved, a 3-line
# main, is excluded) and fails if it is below the threshold.
set -euo pipefail
profile="${1:-coverage.out}"
min="${2:-85}"
filtered="$(mktemp)"
trap 'rm -f "$filtered"' EXIT
{ head -n1 "$profile"; tail -n +2 "$profile" | grep '/internal/'; } > "$filtered"
total="$(go tool cover -func="$filtered" | awk '/^total:/ { gsub("%", "", $3); print $3 }')"
awk -v t="$total" -v m="$min" 'BEGIN {
  if (t + 0 < m + 0) { printf "coverage (internal/...) %.1f%% is below the %s%% threshold\n", t, m; exit 1 }
  printf "coverage (internal/...) %.1f%% >= %s%% threshold: ok\n", t, m
}'
