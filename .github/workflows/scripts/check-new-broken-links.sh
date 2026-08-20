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

# Pinned, not @latest: the two runs below are only comparable if they were
# produced by the same tool, and resolving the tag twice leaves the check one
# upstream publish away from diffing two different versions' output. A bump here
# is a deliberate change with a diff to review, like any other dependency.
MINTLIFY_VERSION=${MINTLIFY_VERSION:-4.2.810}

# mintlify ends a completed run with exactly one of these verdicts: the clean
# one exits 0, the other exits 1. The broken verdict captures the link and file
# counts so the machine-readable records below can be checked against it.
CLEAN_VERDICT='^success no broken links found$'
BROKEN_VERDICT='^found ([0-9]+) broken links? in ([0-9]+) files?$'

REPO_ROOT=""
TEMP_ROOT=""
BASE_TREE=""
BASE_REPORT=""
HEAD_REPORT=""

cleanup() {
  [ -n "$TEMP_ROOT" ] || return 0
  git -C "$REPO_ROOT" worktree remove --force "$BASE_TREE" >/dev/null 2>&1 || true
  # Keep the worktree and reports under one private mktemp directory. Deriving
  # sibling names from the worktree path would leave those report paths
  # unreserved until redirection and make cleanup act on paths we did not create.
  rm -rf "$TEMP_ROOT"
}

normalize_output() {
  # Strip every CSI escape sequence, not just the colour ones: the progress
  # spinner repaints with cursor-movement codes (ESC[2K, ESC[1A, ESC[G) and its
  # last repaint shares a line with the verdict, so a colour-only filter leaves
  # control bytes glued to the front of the one line that has to be recognized.
  # Then drop the spinner frames themselves — how many a run emits depends on
  # how long it took, so they differ between two runs of the same docs tree.
  sed -E -e 's/\x1b\[[0-9;?]*[A-Za-z]//g' \
         -e 's/[[:space:]]*$//' \
         -e '/checking for broken links/d'
}

# Mintlify renders each broken record beneath its source filename with a `⎿`
# marker. Emit one tab-separated filename/link record for each marker. Warnings
# before the verdict — including the repository's hundreds of OpenAPI $ref
# diagnostics — are deliberately ignored and cannot make a missing report look
# non-empty.
extract_broken_link_records() {
  awk '
    /^found [0-9]+ broken links? in [0-9]+ files?$/ {
      in_report = 1
      filename = ""
      next
    }
    !in_report { next }
    /^[[:space:]]*$/ {
      filename = ""
      next
    }
    index($0, "⎿") {
      if (filename == "") {
        next
      }
      link = $0
      sub(/^.*⎿/, "", link)
      sub(/^[[:space:] ]*/, "", link)
      print filename "\t" link
      next
    }
    {
      filename = $0
    }
  '
}

print_stderr_tail() {
  local stderr_file=$1
  if [ -s "$stderr_file" ]; then
    echo "  stderr:" >&2
    tail -20 "$stderr_file" | sed 's/^/    /' >&2
  fi
}

# Validate one normalized Mintlify stdout capture and print only sortable,
# link-specific records. This is separate from collect() so the parser can be
# regression-tested without invoking Mintlify or creating a git worktree.
validate_report() {
  local output=$1 status=$2 docs_dir=$3 stderr_file=$4
  local verdict expected_links expected_files report actual_links actual_files

  if printf '%s\n' "$output" | grep -Eq "$CLEAN_VERDICT"; then
    if printf '%s\n' "$output" | grep -Eq "$BROKEN_VERDICT"; then
      echo "ERROR: mintlify@${MINTLIFY_VERSION} printed conflicting verdicts in $docs_dir (exit $status)" >&2
      print_stderr_tail "$stderr_file"
      return 1
    fi
    if [ "$status" -ne 0 ]; then
      echo "ERROR: mintlify@${MINTLIFY_VERSION} printed a clean verdict in $docs_dir but exited $status" >&2
      print_stderr_tail "$stderr_file"
      return 1
    fi
    return 0
  fi

  verdict=$(printf '%s\n' "$output" | grep -E "$BROKEN_VERDICT" | tail -1 || true)
  if [ -z "$verdict" ]; then
    echo "ERROR: mintlify@${MINTLIFY_VERSION} broken-links did not complete in $docs_dir (exit $status)" >&2
    echo "  Expected 'success no broken links found' or 'found N broken links in M files'." >&2
    printf '%s\n' "$output" | tail -20 | sed 's/^/  /' >&2
    print_stderr_tail "$stderr_file"
    return 1
  fi
  if [ "$status" -ne 1 ]; then
    echo "ERROR: mintlify@${MINTLIFY_VERSION} printed a broken-link verdict in $docs_dir but exited $status" >&2
    print_stderr_tail "$stderr_file"
    return 1
  fi

  expected_links=$(printf '%s\n' "$verdict" | sed -E "s/${BROKEN_VERDICT}/\\1/")
  expected_files=$(printf '%s\n' "$verdict" | sed -E "s/${BROKEN_VERDICT}/\\2/")
  report=$(printf '%s\n' "$output" | extract_broken_link_records)

  actual_links=0
  actual_files=0
  if [ -n "$report" ]; then
    actual_links=$(printf '%s\n' "$report" | wc -l | tr -d ' ')
    actual_files=$(printf '%s\n' "$report" | cut -f1 | LC_ALL=C sort -u | wc -l | tr -d ' ')
  fi

  if [ "$actual_links" -ne "$expected_links" ] || [ "$actual_files" -ne "$expected_files" ]; then
    echo "ERROR: mintlify@${MINTLIFY_VERSION} reported $expected_links broken link(s) in $expected_files file(s) in $docs_dir," >&2
    echo "  but captured $actual_links link record(s) in $actual_files file(s). The report format may have changed." >&2
    print_stderr_tail "$stderr_file"
    return 1
  fi

  if [ -n "$report" ]; then
    printf '%s\n' "$report" | LC_ALL=C sort
  fi
}

# Collect the broken-link report for one docs directory, normalized so two runs
# are comparable.
collect() {
  local docs_dir=$1 stderr_file=$2 output status
  set +e
  # Mintlify's report and verdict are stdout. Keep npm/install diagnostics on
  # stderr out of the normalized link set: a warning that appears on only one
  # run is not a broken documentation link and must not become a false diff.
  output=$(cd "$docs_dir" && npx --yes "mintlify@${MINTLIFY_VERSION}" broken-links 2>"$stderr_file")
  status=$?
  set -e

  output=$(printf '%s\n' "$output" | normalize_output)
  validate_report "$output" "$status" "$docs_dir" "$stderr_file"
}

main() {
  local new_links fixed_links pre_existing

  BASE_REF=${1:?usage: check-new-broken-links.sh <base-ref>}
  REPO_ROOT=$(git rev-parse --show-toplevel)
  TEMP_ROOT=$(mktemp -d)
  BASE_TREE="$TEMP_ROOT/base-tree"
  BASE_REPORT="$TEMP_ROOT/base-report"
  HEAD_REPORT="$TEMP_ROOT/head-report"
  trap cleanup EXIT

  echo "Collecting broken links at ${BASE_REF}..."
  git -C "$REPO_ROOT" worktree add --detach "$BASE_TREE" "$BASE_REF" >/dev/null
  collect "$BASE_TREE/docs" "$TEMP_ROOT/base-stderr" > "$BASE_REPORT" || exit 1

  echo "Collecting broken links at HEAD..."
  collect "$REPO_ROOT/docs" "$TEMP_ROOT/head-stderr" > "$HEAD_REPORT" || exit 1

  new_links=$(LC_ALL=C comm -13 "$BASE_REPORT" "$HEAD_REPORT" || true)
  fixed_links=$(LC_ALL=C comm -23 "$BASE_REPORT" "$HEAD_REPORT" || true)

  if [ -n "$fixed_links" ]; then
    echo "Links this change fixes:"
    printf '%s\n' "$fixed_links" | sed 's/^/  /'
  fi

  if [ -n "$new_links" ]; then
    echo "::error::this change introduces broken documentation links"
    printf '%s\n' "$new_links" | sed 's/^/  /'
    exit 1
  fi

  pre_existing=$(wc -l < "$BASE_REPORT" | tr -d ' ')
  echo "✅ No new broken links (${pre_existing} pre-existing, unchanged by this change)"
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  main "$@"
fi
