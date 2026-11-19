#!/usr/bin/env bash
# Tests for scripts/lock-slice.sh, run by `go test ./cmd/intentgate` (TestLockSliceScript) and so by
# scripts/check.sh. The script runs here in throwaway repositories with a fake `intentgate`
# (INTENTGATE_BIN), except the no-terminal case, which builds and runs the real one.
set -uo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fails=0
fail() { echo "FAIL $*"; fails=$((fails + 1)); }
ok() { echo "ok   $*"; }
expect_eq() { # expect_eq <name> <got> <want>
  if [ "$2" == "$3" ]; then ok "$1"; else fail "$1: got [$2], want [$3]"; fi
}
expect_has() { # expect_has <name> <haystack> <needle>
  case "$2" in *"$3"*) ok "$1" ;; *) fail "$1: [$2] lacks [$3]" ;; esac
}

# newrepo <dir> <spec file name>...: a repository holding a copy of the script and empty specs.
newrepo() {
  local d=$1; shift
  mkdir -p "$d/scripts" "$d/docs/specs"
  cp "$root/scripts/lock-slice.sh" "$d/scripts/lock-slice.sh" 2>/dev/null
  for f in "$@"; do echo "# spec" >"$d/docs/specs/$f"; done
  git -C "$d" init -q -b main
  git -C "$d" -c user.name=t -c user.email=t@example.com add -A
  git -C "$d" -c user.name=t -c user.email=t@example.com commit -q -m first
}

# A fake intentgate: records its arguments, the prompt file it was given and a variable from the
# environment, then writes .intent/01FAKE.yml like a successful lock (or exits FAKE_EXIT).
cat >"$tmp/fake" <<'FAKE'
#!/usr/bin/env bash
echo "$@" >"$FAKE_OUT/args"
prev=""; for a in "$@"; do [ "$prev" == "--prompt" ] && cp "$a" "$FAKE_OUT/prompt"; prev=$a; done
echo "${DOTENV_PROBE:-unset}" >"$FAKE_OUT/probe"
[ "${FAKE_EXIT:-0}" == 0 ] || exit "$FAKE_EXIT"
mkdir -p .intent && echo "id: 01FAKE" >.intent/01FAKE.yml
echo "intent lock written: .intent/01FAKE.yml"
FAKE
chmod +x "$tmp/fake"
mkdir -p "$tmp/out"
export FAKE_OUT="$tmp/out" INTENTGATE_BIN="$tmp/fake"

run() { # run <repo> <args...>: sets code, out, err
  local d=$1; shift
  out=$("$d/scripts/lock-slice.sh" "$@" 2>"$tmp/err" </dev/null); code=$?
  err=$(cat "$tmp/err")
}

# --- usage and slice lookup: exit 2
newrepo "$tmp/r1" S-01-one.md S-02-two.md S-02-dup.md
run "$tmp/r1"; expect_eq "no argument exits 2" "$code" 2
run "$tmp/r1" S-9; expect_eq "unknown slice exits 2" "$code" 2
expect_has "unknown slice is named" "$err" "S-9"
run "$tmp/r1" S-02; expect_eq "ambiguous slice exits 2" "$code" 2
expect_has "ambiguous slice lists both specs" "$err" "S-02-dup.md"
run "$tmp/r1" "S-01/../x"; expect_eq "a slice that is not S-NN exits 2" "$code" 2
[ ! -e "$tmp/out/args" ] && ok "no lock attempted for a bad slice" || fail "intentgate ran for a bad slice"

# --- a good run: the root intent line, the lock command, the staged file, the commit hint
newrepo "$tmp/r2" S-17-self-hosting.md
printf 'DOTENV_PROBE=from-dotenv-9f3a\n' >"$tmp/r2/.env"
run "$tmp/r2" S-17
expect_eq "a good run exits 0" "$code" 0
expect_eq "the root intent line" "$(cat "$tmp/out/prompt" 2>/dev/null)" "Implement S-17 as specified in docs/specs/S-17-self-hosting.md."
args=$(cat "$tmp/out/args" 2>/dev/null)
expect_has "runs lock --from-plan on the spec" "$args" "lock --from-plan docs/specs/S-17-self-hosting.md --prompt "
expect_eq "the new lock is staged" "$(git -C "$tmp/r2" diff --cached --name-only)" ".intent/01FAKE.yml"
expect_has "prints the commit command" "$out" "git commit"
expect_has "the commit command names the lock" "$out" ".intent/01FAKE.yml"
expect_eq ".env is loaded for intentgate" "$(cat "$tmp/out/probe" 2>/dev/null)" "from-dotenv-9f3a"
case "$out$err" in *from-dotenv-9f3a*) fail ".env value was printed" ;; *) ok ".env is never printed" ;; esac

# --- intentgate failing (e.g. the human did not type yes): its code passes through, nothing staged
newrepo "$tmp/r3" S-03-three.md
FAKE_EXIT=1 run "$tmp/r3" S-03
expect_eq "a failed lock passes its exit code through" "$code" 1
expect_eq "nothing is staged after a failed lock" "$(git -C "$tmp/r3" diff --cached --name-only)" ""

# --- no terminal: the real intentgate, built from the checkout, refuses exactly like lock
unset INTENTGATE_BIN
if [ -f "$root/docs/specs/S-17-self-hosting.md" ]; then
  before=$(ls "$root/.intent" 2>/dev/null | wc -l)
  out=$("$root/scripts/lock-slice.sh" S-17 2>"$tmp/err" </dev/null); code=$?
  err=$(cat "$tmp/err")
  expect_eq "without a terminal it exits 2" "$code" 2
  expect_has "without a terminal it says why" "$err" "interactive terminal"
  expect_eq "without a terminal no lock is written" "$(ls "$root/.intent" 2>/dev/null | wc -l)" "$before"
fi

[ "$fails" -eq 0 ] && echo "lock-slice tests: PASS" || echo "lock-slice tests: $fails FAILED"
[ "$fails" -eq 0 ]
