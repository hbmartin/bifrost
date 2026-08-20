#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=check-new-broken-links.sh
# shellcheck disable=SC1091 # Resolved from this script's directory at runtime.
source "$SCRIPT_DIR/check-new-broken-links.sh"

fail() {
  echo "FAIL: $1" >&2
  exit 1
}

TEST_ROOT=$(mktemp -d)
trap 'rm -rf "$TEST_ROOT"' EXIT
STDERR_FILE="$TEST_ROOT/mintlify.stderr"
: > "$STDERR_FILE"

# The real repository emits hundreds of unrelated OpenAPI diagnostics before
# Mintlify's verdict. They must neither enter the compared report nor satisfy
# the requirement that every counted link has a machine-readable record.
POLLUTED_OUTPUT=$'warning - unrelated OpenAPI validation output\n  - paths./v1/models.$ref: ./models.yaml\nfound 2 broken links in 1 file\n\ndocs/page.mdx\n ⎿  /broken-one\n ⎿  /broken-two'
REPORT=$(validate_report "$POLLUTED_OUTPUT" 1 docs "$STDERR_FILE")
EXPECTED=$'docs/page.mdx\t/broken-one\ndocs/page.mdx\t/broken-two'
[ "$REPORT" = "$EXPECTED" ] || fail "unrelated stdout entered the broken-link report"

# Keep the source filename in every record. The same broken destination in two
# files is two findings; collapsing it would miss a move from one file to the
# other and would disagree with Mintlify's verdict count.
DUPLICATE_LINK_OUTPUT=$'found 2 broken links in 2 files\n\ndocs/a.mdx\n ⎿  /same-target\n\ndocs/b.mdx\n ⎿  /same-target'
REPORT=$(validate_report "$DUPLICATE_LINK_OUTPUT" 1 docs "$STDERR_FILE")
EXPECTED=$'docs/a.mdx\t/same-target\ndocs/b.mdx\t/same-target'
[ "$REPORT" = "$EXPECTED" ] || fail "duplicate links in different files were collapsed"

# A clean run can still print unrelated warnings. Its compared report must be
# empty rather than treating those diagnostics as pre-existing broken links.
CLEAN_OUTPUT=$'warning - unrelated OpenAPI validation output\n  - paths./v1/models.$ref: ./models.yaml\nsuccess no broken links found'
REPORT=$(validate_report "$CLEAN_OUTPUT" 0 docs "$STDERR_FILE")
[ -z "$REPORT" ] || fail "a clean run emitted non-link diagnostics as findings"

# Regression: if a future Mintlify release moves link records to stderr but
# leaves the verdict and warnings on stdout, refuse to compare instead of
# accepting two equal warning-only reports.
printf '%s\n' 'docs/page.mdx' ' ⎿  /broken-one' ' ⎿  /broken-two' > "$STDERR_FILE"
SPLIT_OUTPUT=$'warning - unrelated OpenAPI validation output\n  - paths./v1/models.$ref: ./models.yaml\nfound 2 broken links in 1 file'
if validate_report "$SPLIT_OUTPUT" 1 docs "$STDERR_FILE" > "$TEST_ROOT/split.out" 2> "$TEST_ROOT/split.err"; then
  fail "a verdict with link records only on stderr was accepted"
fi
grep -q 'captured 0 link record(s) in 0 file(s)' "$TEST_ROOT/split.err" \
  || fail "the stderr-split failure did not explain the missing records"
grep -q '⎿  /broken-one' "$TEST_ROOT/split.err" \
  || fail "the stderr-split failure did not include the stderr tail"

# Partial capture is as unsafe as total loss: a non-empty report must still
# contain exactly the link and file counts promised by the verdict.
: > "$STDERR_FILE"
PARTIAL_OUTPUT=$'found 2 broken links in 1 file\n\ndocs/page.mdx\n ⎿  /broken-one'
if validate_report "$PARTIAL_OUTPUT" 1 docs "$STDERR_FILE" > "$TEST_ROOT/partial.out" 2> "$TEST_ROOT/partial.err"; then
  fail "a partially captured broken-link report was accepted"
fi
grep -q 'captured 1 link record(s) in 1 file(s)' "$TEST_ROOT/partial.err" \
  || fail "the partial-report failure did not explain the count mismatch"

echo "check-new-broken-links parser tests passed"
