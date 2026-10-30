#!/usr/bin/env bash
# gosec against a baseline of reviewed false positives (.gosec-baseline.json).
# A finding is known when its rule, file and trimmed source line all match a baseline
# entry, so edits that only shift line numbers keep it known. Anything else fails.
# Usage: scripts/gosec-check.sh <gosec module@version>
set -euo pipefail
cd "$(dirname "$0")/.."

out=$(mktemp)
trap 'rm -f "$out"' EXIT
# gosec exits non-zero whenever it reports anything; the JSON decides below.
go run "$1" -quiet -fmt=json -out="$out" ./... || true

if ! jq -e '(."Golang errors" // {}) == {}' "$out" >/dev/null; then
  echo "gosec could not analyse the code:"; jq '."Golang errors"' "$out"; exit 1
fi

report=$(jq -r --slurpfile base .gosec-baseline.json --arg root "$PWD/" '
  [.Issues[] | (.line | split("-")[0]) as $l
    | {rule_id, file: (.file | ltrimstr($root)), line: $l, details,
       code: ([.code | split("\n")[] | select(startswith($l + ": ")) | ltrimstr($l + ": ")
               | gsub("^\\s+|\\s+$"; "")][0])}]
  | . as $found
  | [$found[] | select(. as $f | $base[0] | any(.rule_id == $f.rule_id and .file == $f.file and .code == $f.code) | not)] as $new
  | [$base[0][] | select(. as $b | $found | any(.rule_id == $b.rule_id and .file == $b.file and .code == $b.code) | not)] as $stale
  | ($stale[] | "note: baseline entry no longer found: \(.rule_id) \(.file): \(.code)"),
    ($new[] | "NEW \(.rule_id) \(.file):\(.line) \(.details)\n    \(.code)"),
    "gosec: \($found | length) findings, \(($found | length) - ($new | length)) in baseline, \($new | length) new"
' "$out")
echo "$report"
[[ $report == *", 0 new" ]]
