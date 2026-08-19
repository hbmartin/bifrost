#!/usr/bin/env bash
set -euo pipefail

# Fail only on documentation links this change breaks.
#
# `mintlify broken-links` reports the whole docs tree, and the tree already has
# broken links. Failing on the total would hand every unrelated docs pull request
# an inherited red check, and a check that is red on arrival stops being read.
# So run the same check against the comparison point and report the difference:
# the rule is "no new broken links", which is enforceable from the first commit
# and still shrinks the debt whenever someone fixes one.
#
# Usage: check-new-broken-links.sh <base-ref>

BASE_REF=${1:?usage: check-new-broken-links.sh <base-ref>}
REPO_ROOT=$(git rev-parse --show-toplevel)
BASE_TREE=$(mktemp -d)

cleanup() {
  git -C "$REPO_ROOT" worktree remove --force "$BASE_TREE" >/dev/null 2>&1 || true
  rm -rf "$BASE_TREE"
}
trap cleanup EXIT

# Collect the broken-link report for one docs directory, normalized so two runs
# are comparable: ANSI colour stripped, trailing space trimmed, blank lines and
# the "N broken links" tally dropped (the tally differs whenever the count does,
# which would otherwise register as a new finding every time).
collect() {
  local docs_dir=$1 output status
  set +e
  output=$(cd "$docs_dir" && npx --yes mintlify@latest broken-links 2>&1)
  status=$?
  set -e
  if [ "$status" -ne 0 ] && [ -z "$output" ]; then
    echo "ERROR: mintlify broken-links produced no output in $docs_dir (exit $status)" >&2
    return 1
  fi
  printf '%s\n' "$output" \
    | sed -e 's/\x1b\[[0-9;]*m//g' -e 's/[[:space:]]*$//' \
    | grep -viE '^[[:space:]]*[0-9]+ broken' \
    | sed '/^[[:space:]]*$/d' \
    | sort -u
}

echo "Collecting broken links at ${BASE_REF}..."
git -C "$REPO_ROOT" worktree add --detach "$BASE_TREE" "$BASE_REF" >/dev/null
collect "$BASE_TREE/docs" > "$BASE_TREE.base" || exit 1

echo "Collecting broken links at HEAD..."
collect "$REPO_ROOT/docs" > "$BASE_TREE.head" || exit 1

NEW_LINKS=$(comm -13 "$BASE_TREE.base" "$BASE_TREE.head" || true)
FIXED_LINKS=$(comm -23 "$BASE_TREE.base" "$BASE_TREE.head" || true)

if [ -n "$FIXED_LINKS" ]; then
  echo "Links this change fixes:"
  printf '%s\n' "$FIXED_LINKS" | sed 's/^/  /'
fi

if [ -n "$NEW_LINKS" ]; then
  echo "::error::this change introduces broken documentation links"
  printf '%s\n' "$NEW_LINKS" | sed 's/^/  /'
  exit 1
fi

PRE_EXISTING=$(wc -l < "$BASE_TREE.base" | tr -d ' ')
echo "✅ No new broken links (${PRE_EXISTING} pre-existing, unchanged by this change)"
