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

# The process map (docs/process/sdlc-map.yml) must match the repo: its tests, then --check (stale Markdown,
# a path that matches nothing, a command intentgate does not accept). Builds ./cmd/intentgate, so it needs go.
if need python3; then
  step "process map" sh -c 'python3 -I scripts/sdlc_map_test.py && python3 -I scripts/sdlc-map.py --check'
fi

if need req; then
  step "req canon" req canon --fail-on-flag
  step "req satisfy" req satisfy
  # The release fold: every declared variant's gate against the signed Baseline(s) under baselines/,
  # plus completeness. Signing a Baseline is the human's act (scripts/sign-baseline.sh), never an agent's.
  step "req check" req check --test-report build/junit.xml
  step "req baseline" req baseline --verify baselines/phase0-2.json
fi

[ "$status" -eq 0 ] && echo "verdict: GREEN" || echo "verdict: RED"
exit "$status"
