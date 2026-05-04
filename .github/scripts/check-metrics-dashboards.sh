#!/usr/bin/env bash
# For every src/**/*_metrics.go file, extract Prometheus metric names declared
# as `Name: "..."` in CounterOpts/HistogramOpts/GaugeOpts struct literals.
# Verify each metric name appears in at least one Grafana dashboard JSON under
# deploy/monitoring/grafana/dashboards/.
#
# Why: P47 introduced emdexer_extract_cache_{hits,misses}_total but the
# matching dashboard was almost shipped without coverage. This script catches
# that class of drift in CI.
#
# Limitations: Only scans files matching `*_metrics.go`. Metrics declared
# inline in feature files (e.g., embed/provider.go) are NOT checked. The
# convention is therefore: any new metric should live in a `*_metrics.go` file.

set -euo pipefail

DASHBOARDS_DIR="deploy/monitoring/grafana/dashboards"

if [ ! -d "$DASHBOARDS_DIR" ]; then
    echo "ERROR: dashboards directory $DASHBOARDS_DIR not found"
    exit 2
fi

fail=0
checked=0
found_any=0

while IFS= read -r mf; do
    found_any=1
    metric_names=$(grep -oE 'Name:[[:space:]]+"[a-z_][a-z0-9_]*"' "$mf" \
        | grep -oE '"[a-z_][a-z0-9_]*"' \
        | tr -d '"' \
        || true)
    if [ -z "$metric_names" ]; then
        echo "ERROR: no Name: \"...\" entries found in $mf — *_metrics.go files must declare metrics with Name: \"...\" fields"
        fail=1
        continue
    fi
    while IFS= read -r metric; do
        checked=$((checked+1))
        if ! grep -rq -- "$metric" "$DASHBOARDS_DIR"/; then
            echo "MISSING DASHBOARD: metric '$metric' (declared in $mf) not referenced in any dashboard JSON under $DASHBOARDS_DIR/"
            echo "  -> Add a panel for '$metric' to a dashboard file in $DASHBOARDS_DIR/"
            fail=1
        fi
    done < <(echo "$metric_names")
done < <(find src -type f -name "*_metrics.go")

if [ "$found_any" -eq 0 ]; then
    echo "OK: no *_metrics.go files found — nothing to check."
    exit 0
fi

if [ "$fail" -eq 0 ]; then
    echo "OK: all $checked metric(s) from *_metrics.go files have dashboard coverage."
fi
exit $fail
