#!/usr/bin/env bash
# Offline end-to-end demo against the bundled fixture monorepo.
# Run via `make demo` (which builds bin/paved first). Writes only under .demo-out/.
set -euo pipefail
cd "$(dirname "$0")/.."
PAVED="$PWD/bin/paved"
FIX="fixtures/acme-monorepo"
OUT=".demo-out"
PORT="${PAVED_DEMO_PORT:-18728}"

if [ -t 1 ]; then B=$'\033[1m' N=$'\033[0m'; else B='' N=''; fi
step() { printf '\n%s== %s%s\n$ %s\n' "$B" "$1" "$N" "$2"; }
run() { step "$1" "paved ${*:2}"; "$PAVED" --root "$FIX" "${@:2}"; }

step "1. Validate the catalog (one deliberate error: a disbanded owning team)" "paved catalog validate"
"$PAVED" --root "$FIX" catalog validate || echo "(exit $? — validation found errors, as expected)"

run "2. Orphans: entities detached from owners, systems or consumers" catalog orphans
run "3. Who owns the payments SDK? (with escalation chain)" graph owner payments-sdk
run "4. Who owns legacy-reporting?" graph owner legacy-reporting
run "5. What depends on the payments database? (through API consumption too)" graph dependents resource:payments-db
run "6. Blast radius of the ledger service, grouped by team to page" graph impact ledger-service
run "7. Shortest dependency path from checkout to the ledger DB" graph path checkout-web resource:ledger-db

run "8. Production-readiness scorecard" score run production-readiness --details
run "9. Team rollup (group hierarchy)" score rollup production-readiness
run "10. Dependency freshness (libyears) for checkout-web" score deps checkout-web

step "11. Scaffolder dry run: a new Go service, validated against the live catalog" \
  "paved scaffold run go-service --dry-run --set name=invoice-service ... (diff of catalog-info.yaml shown)"
"$PAVED" --root "$FIX" scaffold run go-service --dry-run \
  --set name=invoice-service --set "description=Generates and sends customer invoices." \
  --set owner=team-payments --set system=billing --set withDatabase=true \
  --set oncallUrl=https://oncall.acme.example/schedules/payments \
  | awk '/^plan:/,/^$/ {print} /^\+\+\+ b\/services\/invoice-service\/catalog-info.yaml/ {p=1} p && /^$/ {p=0} p {print}'

step "12. Dry run with a typo in the owner is caught before anything is written" \
  "paved scaffold run go-service --dry-run --set owner=team-paymnets ..."
"$PAVED" --root "$FIX" scaffold run go-service --dry-run \
  --set name=invoice-service --set "description=Generates and sends customer invoices." \
  --set owner=team-paymnets | grep '^check:' || true

step "13. Apply into a scratch copy of the monorepo, then re-validate and re-score" \
  "cp -R $FIX $OUT/acme && paved --root $OUT/acme scaffold run go-service ..."
rm -rf "$OUT" && mkdir -p "$OUT" && cp -R "$FIX" "$OUT/acme"
"$PAVED" --root "$OUT/acme" scaffold run go-service \
  --set name=invoice-service --set "description=Generates and sends customer invoices." \
  --set owner=team-payments --set system=billing --set withDatabase=true \
  --set oncallUrl=https://oncall.acme.example/schedules/payments | tail -n 1
"$PAVED" --root "$OUT/acme" catalog stats | head -n 1
"$PAVED" --root "$OUT/acme" graph owned team-payments
"$PAVED" --root "$OUT/acme" score run hygiene | grep -E 'Entities:|invoice-service'

step "14. REST API: start the server, query it, stop it" "paved serve --addr 127.0.0.1:$PORT"
"$PAVED" --root "$FIX" --log-level warn serve --addr "127.0.0.1:$PORT" &
SERVER=$!
trap 'kill $SERVER 2>/dev/null || true' EXIT
for _ in $(seq 1 50); do curl -fsS "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1 && break; sleep 0.1; done
echo '$ curl /api/v1/entities/resource/default/payments-db/dependents?transitive=false'
curl -fsS "http://127.0.0.1:$PORT/api/v1/entities/resource/default/payments-db/dependents?transitive=false"
echo '$ curl /api/v1/entities/component/default/recommendations-ml/owner'
curl -fsS "http://127.0.0.1:$PORT/api/v1/entities/component/default/recommendations-ml/owner"
echo '$ curl -X POST /api/v1/templates/python-worker/dry-run (summary only)'
curl -fsS -X POST "http://127.0.0.1:$PORT/api/v1/templates/python-worker/dry-run" \
  -d '{"values": {"name": "audit-worker", "owner": "team-platform", "topic": "audit.events"}}' \
  | awk '/"summary"/,/}/'
kill "$SERVER" && wait "$SERVER" 2>/dev/null || true
trap - EXIT
printf '\ndemo complete (scratch output in %s/)\n' "$OUT"
