#!/usr/bin/env bash
# gen-notices.sh — regenerate THIRD_PARTY_NOTICES from the real module graph.
#
# Uses google/go-licenses (Apache-2.0) to walk the FULL transitive dependency
# tree of the main package and aggregate each dependency's full license text and
# any NOTICE file into a single THIRD_PARTY_NOTICES file, which main.go embeds
# via //go:embed and `olb --licenses` prints. Run this as part of the release
# build so the attributions can never drift from what is actually linked.
#
# Requires: go, and go-licenses on PATH
#   go install github.com/google/go-licenses@latest
set -euo pipefail

cd "$(dirname "$0")/.."
out="THIRD_PARTY_NOTICES"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

if ! command -v go-licenses >/dev/null 2>&1; then
  echo "gen-notices: go-licenses not found; install with:" >&2
  echo "  go install github.com/google/go-licenses@latest" >&2
  exit 1
fi

# The main module's own LICENSE/NOTICE ship as the top-level LICENSE and NOTICE
# files; THIRD_PARTY_NOTICES is for dependencies only, so exclude it below.
mainmod="$(go list -m 2>/dev/null | head -1)"

# save copies every dependency's LICENSE (and NOTICE, when present) into a tree.
echo "gen-notices: collecting license and NOTICE files…" >&2
go-licenses save . --save_path="$tmp/licenses" --force

{
  echo "olb — third-party notices"
  echo "========================="
  echo
  echo "This binary links the following third-party modules. Their full license"
  echo "texts and any NOTICE contents are reproduced below, as required for binary"
  echo "redistribution. Generated from the module graph by google/go-licenses."
  echo
  # Deterministic order.
  find "$tmp/licenses" -type f \( -iname 'LICENSE*' -o -iname 'COPYING*' -o -iname 'NOTICE*' \) | sort | while read -r f; do
    # Module path is the directory relative to the save root.
    rel="${f#"$tmp/licenses"/}"
    mod="$(dirname "$rel")"
    # Skip the project's own module — it is not a third-party dependency.
    if [ -n "$mainmod" ] && { [ "$mod" = "$mainmod" ] || [ "${mod#"$mainmod"/}" != "$mod" ]; }; then
      continue
    fi
    kind="$(basename "$f" | tr '[:lower:]' '[:upper:]')"
    echo "--------------------------------------------------------------------------------"
    echo "${mod}  (${kind%%.*})"
    echo "--------------------------------------------------------------------------------"
    cat "$f"
    echo
  done
} >"$out"

echo "gen-notices: wrote $out ($(wc -l <"$out") lines)" >&2
