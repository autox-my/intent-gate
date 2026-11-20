#!/usr/bin/env bash
# Download the pinned intentgate release and verify its SHA-256 before it is installed (ADR 0005, S-21).
#
# In (environment only):
#   RELEASE_FILE  the pin: `version <tag>` and one `<sha256>  intentgate_<tag>_<os>_<arch>` per asset
#   BASE_URL      release download base (default https://github.com/autox-my/intent-gate/releases/download)
#   RUNNER_OS     Linux (anything else exits 4)
#   RUNNER_ARCH   X64 or ARM64 (anything else exits 4)
#   DEST          where the verified binary is installed, mode 0755
#
# Exit: 0 installed and verified | 4 unsupported platform | 5 no pinned release (no `version` line)
#       6 download failed | 7 not verified: checksum mismatch, or the pin is unusable (nothing at DEST).
set -euo pipefail

: "${RELEASE_FILE:?RELEASE_FILE is not set}"
: "${RUNNER_OS:?RUNNER_OS is not set}"
: "${RUNNER_ARCH:?RUNNER_ARCH is not set}"
: "${DEST:?DEST is not set}"
BASE_URL=${BASE_URL:-https://github.com/autox-my/intent-gate/releases/download}

case "$RUNNER_OS/$RUNNER_ARCH" in
  Linux/X64) platform=linux_amd64 ;;
  Linux/ARM64) platform=linux_arm64 ;;
  *) echo "intentgate: no release binary for $RUNNER_OS/$RUNNER_ARCH" >&2; exit 4 ;;
esac

# Read the pin. Comments start with #.
tag=""
want=""
pins=()
hash_re='^([0-9a-f]{64})[[:space:]]+([^[:space:]]+)$'
if [ ! -r "$RELEASE_FILE" ]; then
  echo "intentgate: cannot read the pin file $RELEASE_FILE" >&2; exit 7
fi
while IFS= read -r line || [ -n "$line" ]; do
  line=${line%$'\r'}
  line=${line#"${line%%[![:space:]]*}"}
  line=${line%"${line##*[![:space:]]}"}
  case "$line" in
    "" | "#"*) continue ;;
    "version "*)
      if [ -n "$tag" ]; then echo "intentgate: more than one version line in $RELEASE_FILE" >&2; exit 7; fi
      tag=${line#version }
      tag=${tag#"${tag%%[![:space:]]*}"}
      ;;
    *)
      if [[ $line =~ $hash_re ]]; then
        # The asset name is only known once the tag is; collect every pin and match below.
        pins+=("${BASH_REMATCH[1]} ${BASH_REMATCH[2]}")
      else
        echo "intentgate: unreadable line in $RELEASE_FILE: $line" >&2; exit 7
      fi
      ;;
  esac
done < "$RELEASE_FILE"

if [ -z "$tag" ]; then
  echo "intentgate: no pinned release in $RELEASE_FILE" >&2; exit 5
fi
if [[ ! $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "intentgate: the pinned version is not a release tag: $tag" >&2; exit 7
fi

asset="intentgate_${tag}_${platform}"
for p in "${pins[@]+"${pins[@]}"}"; do
  if [ "${p#* }" = "$asset" ]; then want=${p%% *}; fi
done
if [ -z "$want" ]; then
  echo "intentgate: $RELEASE_FILE pins no checksum for $asset" >&2; exit 7
fi

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

destdir=$(dirname "$DEST")
mkdir -p "$destdir"
tmp=$(mktemp "$destdir/.intentgate-fetch.XXXXXX")
trap 'rm -f "$tmp"' EXIT

# https (or file, for tests) only, including after a redirect.
if ! curl -fsSL --proto '=https,file' --proto-redir '=https,file' -o "$tmp" "$BASE_URL/$tag/$asset"; then
  echo "intentgate: could not download $BASE_URL/$tag/$asset" >&2; exit 6
fi

got=$(sha256 "$tmp")
if [ "$got" != "$want" ]; then
  echo "intentgate: checksum mismatch for $asset: pinned $want, downloaded $got" >&2; exit 7
fi

chmod 0755 "$tmp"
mv -f "$tmp" "$DEST"
echo "intentgate: installed $asset (sha256 $got)"
