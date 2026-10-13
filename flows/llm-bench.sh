#!/usr/bin/env bash
# Comparable load for the "LLM performance" Grafana dashboard (uid obol-llm):
# the same streaming chat request, N times per model, one at a time, through
# the stack's LiteLLM. LiteLLM's Prometheus metrics do the measuring; this
# script only generates identical traffic and prints a dashboard link scoped
# to the measured window. See docs/observability.md "LLM performance".
#
#   flows/llm-bench.sh qwen3.5:9b qwen36-deep
#   N=50 MAX_TOKENS=512 PROMPT='Summarise the Ethereum merge.' flows/llm-bench.sh <model>...
#
# Models are LiteLLM model names (`obol model list`). Each model first gets
# WARMUP untimed requests (model load / cold cache), then the measured
# window starts. Requests are sequential so queueing does not inflate TTFT.
#
# Env: N (20), WARMUP (1), MAX_TOKENS (256), PROMPT, OBOL (obol binary),
#      LITELLM_URL (skip the port-forward, e.g. from a pod:
#      http://litellm.llm.svc.cluster.local:4000), LITELLM_KEY (default:
#      `obol model token`), GRAFANA_URL (http://obol.stack:8080/grafana),
#      SCRAPE_WAIT (35s pause before printing the link).
set -euo pipefail

[ $# -ge 1 ] || { sed -n '2,19p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }

N="${N:-20}"
WARMUP="${WARMUP:-1}"
MAX_TOKENS="${MAX_TOKENS:-256}"
PROMPT="${PROMPT:-Explain in about 200 words how a distributed validator splits a validator key between operators.}"
OBOL="${OBOL:-obol}"
GRAFANA_URL="${GRAFANA_URL:-http://obol.stack:8080/grafana}"
SCRAPE_WAIT="${SCRAPE_WAIT:-35}" # > the 30s ServiceMonitor interval, so the last requests are scraped

pf_pid=""
cleanup() { if [ -n "$pf_pid" ]; then kill "$pf_pid" 2>/dev/null || true; fi; }
trap cleanup EXIT

if [ -z "${LITELLM_URL:-}" ]; then
	port=$((20000 + RANDOM % 20000))
	"$OBOL" kubectl -n llm port-forward svc/litellm "$port:4000" >/dev/null 2>&1 &
	pf_pid=$!
	LITELLM_URL="http://127.0.0.1:$port"
	for _ in $(seq 1 30); do
		curl -sf "$LITELLM_URL/health/readiness" >/dev/null 2>&1 && break
		sleep 1
	done
fi
LITELLM_KEY="${LITELLM_KEY:-$("$OBOL" model token)}"

body() {
	python3 -c 'import json,sys; print(json.dumps({"model": sys.argv[1], "stream": True, "max_tokens": int(sys.argv[2]), "temperature": 0, "messages": [{"role": "user", "content": sys.argv[3]}]}))' \
		"$1" "$MAX_TOKENS" "$PROMPT"
}

# Prints "<http_code> <total_seconds>"; the SSE body is discarded.
send() {
	curl -sS -N -o /dev/null -w '%{http_code} %{time_total}\n' --max-time 600 \
		-H "Authorization: Bearer $LITELLM_KEY" -H 'Content-Type: application/json' \
		-d "$(body "$1")" "$LITELLM_URL/v1/chat/completions" || echo "000 0"
}

for model in "$@"; do
	for _ in $(seq 1 "$WARMUP"); do send "$model" >/dev/null; done
done

start_ms=$(($(date +%s) * 1000))
for model in "$@"; do
	ok=0
	total=0
	for i in $(seq 1 "$N"); do
		read -r code secs < <(send "$model")
		[ "$code" = "200" ] && ok=$((ok + 1))
		total=$(awk -v a="$total" -v b="$secs" 'BEGIN { print a + b }')
		printf '%s  %3d/%d  HTTP %s  %6.2fs\n' "$model" "$i" "$N" "$code" "$secs"
	done
	printf '%s: %d/%d ok, mean %.2fs per request (client side)\n\n' "$model" "$ok" "$N" \
		"$(awk -v t="$total" -v n="$N" 'BEGIN { print t / n }')"
done

echo "Waiting ${SCRAPE_WAIT}s for the last scrape..."
sleep "$SCRAPE_WAIT"
end_ms=$(($(date +%s) * 1000))
echo "Scoreboard for this run (TTFT p50/p95, decode tok/s, errors per model):"
echo "  $GRAFANA_URL/d/obol-llm?from=$start_ms&to=$end_ms"
