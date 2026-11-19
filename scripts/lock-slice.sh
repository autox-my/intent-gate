#!/usr/bin/env bash
# Lock a slice from its spec when the plan-approval hook can't run: the human types `yes`.
#
#   scripts/lock-slice.sh <S-NN>
#
# Builds intentgate from this checkout (or uses $INTENTGATE_BIN), loads .env if there is one, and runs
#   intentgate lock --from-plan docs/specs/<S-NN>-*.md --prompt <root intent file>
# with the root intent `Implement <S-NN> as specified in <spec path>.` Like `lock`, it needs a
# terminal: ratification is a human act. On success it stages the new .intent/<id>.yml and prints the
# commit command; it never commits.
set -euo pipefail
cd "$(dirname "$0")/.."

slice=${1:-}
if [ $# -ne 1 ] || ! [[ $slice =~ ^S-[0-9]+$ ]]; then
  echo "usage: scripts/lock-slice.sh <S-NN>" >&2
  exit 2
fi

specs=()
for f in docs/specs/"$slice"-*.md; do [ -e "$f" ] && specs+=("$f"); done
if [ ${#specs[@]} -eq 0 ]; then
  echo "lock-slice: no spec docs/specs/$slice-*.md" >&2
  exit 2
elif [ ${#specs[@]} -gt 1 ]; then
  echo "lock-slice: $slice is ambiguous, several specs match: ${specs[*]}" >&2
  exit 2
fi
spec=${specs[0]}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

bin=${INTENTGATE_BIN:-}
if [ -z "$bin" ]; then
  bin=$tmp/intentgate
  go build -o "$bin" ./cmd/intentgate
fi

if [ -f .env ]; then # keys for the plan review; sourced, never printed
  set -a
  # shellcheck disable=SC1091
  . ./.env
  set +a
fi

before=$(ls .intent/*.yml 2>/dev/null || true)
printf 'Implement %s as specified in %s.\n' "$slice" "$spec" >"$tmp/root-intent"
"$bin" lock --from-plan "$spec" --prompt "$tmp/root-intent" || exit $?

after=$(ls .intent/*.yml 2>/dev/null || true)
new=$(comm -13 <(printf '%s\n' "$before") <(printf '%s\n' "$after") | grep . || true)
if [ -z "$new" ] || [ "$(printf '%s\n' "$new" | wc -l)" -ne 1 ]; then
  echo "lock-slice: expected one new .intent/<id>.yml, found: ${new:-none}" >&2
  exit 1
fi
git add "$new"
echo
echo "Staged $new. Commit it with:"
echo "  git commit -m \"lock($slice): intent lock for $slice\" -- $new"
