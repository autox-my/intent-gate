#!/usr/bin/env bash
# The one gate: "green" means this script exits 0. CI runs exactly this script.
# Every step prints PASS, FAIL or SKIP. A SKIP (a tool missing) fails the gate, as in req.
set -uo pipefail
cd "$(dirname "$0")/.."

GOTESTSUM=gotest.tools/gotestsum@v1.12.0
STATICCHECK=honnef.co/go/tools/cmd/staticcheck@2024.1.1

status=0
log=build/check
mkdir -p "$log"

step() {  # step <name> <command...>: quiet on PASS, shows the output on FAIL
  local name=$1; shift
  local out="$log/${name// /-}.txt"
  if "$@" >"$out" 2>&1; then echo "PASS $name"; else echo "FAIL $name"; sed 's/^/     /' "$out" | tail -40; status=1; fi
}
need() {  # need <command>
  command -v "$1" >/dev/null 2>&1 && return 0
  echo "SKIP $1 not found (a SKIP fails the gate)"; status=1; return 1
}

if need go; then
  gofiles=$(git ls-files --cached --others --exclude-standard '*.go')
  if [ -n "$gofiles" ]; then
    # gofmt with no file arguments reads stdin and waits forever, so it only runs when there are files.
    step gofmt sh -c 'test -z "$(gofmt -l "$@")"' _ $gofiles
    step "go vet" go vet ./...
    step staticcheck go run "$STATICCHECK" ./...
  fi
  rm -f build/junit.xml
  step tests go run "$GOTESTSUM" --junitfile build/junit.xml -- -count=1 ./...
fi

if need req; then
  step "req canon" req canon --fail-on-flag
  step "req satisfy" req satisfy
  # Report-only until every claim links a passing test (req docs/adopting.md, "For an agent", step 4).
  # Then this line becomes: step "req check" req check --test-report build/junit.xml
  req gate --test-report build/junit.xml >"$log/req-gate.txt" 2>&1
  echo "INFO req gate exit $? (report-only until every claim is linked; $log/req-gate.txt)"
fi

[ "$status" -eq 0 ] && echo "verdict: GREEN" || echo "verdict: RED"
exit "$status"
