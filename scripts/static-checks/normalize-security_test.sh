#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
temp=$(mktemp -d)
trap 'rm -rf "$temp"' EXIT

printf '%s\n' '[{"RuleID":"test","File":"tracked.txt","Commit":"abc","Fingerprint":"known"}]' >"$temp/current.json"
printf '%s\n' '{"gitleaks":["known"]}' >"$temp/baseline.json"
python3 "$SCRIPT_DIR/normalize-security.py" --kind gitleaks --input "$temp/current.json" --baseline "$temp/baseline.json" >/dev/null
printf '%s\n' '[{"RuleID":"test","File":"tracked.txt","Commit":"abc","Fingerprint":"known"},{"RuleID":"new","File":"new.txt","Commit":"def","Fingerprint":"new"}]' >"$temp/current.json"
if python3 "$SCRIPT_DIR/normalize-security.py" --kind gitleaks --input "$temp/current.json" --baseline "$temp/baseline.json" >/dev/null 2>&1; then
  printf 'security baseline accepted a new fingerprint\n' >&2
  exit 1
fi

printf '%s\n' '{"gitleaks":[],"gosec":[],"govulncheck":[]}' >"$temp/baseline.json"
printf '%s\n' '{"finding":{"osv":"GO-TEST","trace":[{"module":"example.com/dependency","version":"v1.0.0"}]}}' >"$temp/current.json"
python3 "$SCRIPT_DIR/normalize-security.py" --kind govulncheck --input "$temp/current.json" --baseline "$temp/baseline.json" >/dev/null
cat >"$temp/current.json" <<'JSON'
{
  "finding": {
    "osv": "GO-TEST",
    "trace": [
      {
        "module": "example.com/dependency",
        "package": "example.com/dependency/vulnerable",
        "function": "Vulnerable"
      }
    ]
  }
}
JSON
if python3 "$SCRIPT_DIR/normalize-security.py" --kind govulncheck --input "$temp/current.json" --baseline "$temp/baseline.json" >/dev/null 2>&1; then
  printf 'security baseline accepted a new reachable vulnerability\n' >&2
  exit 1
fi
printf 'security baseline tests passed\n'
