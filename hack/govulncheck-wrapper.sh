#!/bin/bash

# govulncheck-wrapper.sh - Run govulncheck while ignoring specified vulnerabilities
#
# Usage: ./hack/govulncheck-wrapper.sh [--config FILE] [--verbose]
#
# Configuration file format (YAML):
#   ignored_vulnerabilities:
#     - id: GO-2024-12345
#       module: github.com/example/module
#       reason: "Acceptable risk in our context"
#

set -euo pipefail

print_usage() {
  cat << 'EOF'
Usage: govulncheck-wrapper.sh [options]

Options:
  --config FILE     Path to YAML config file (default: .govulncheck-ignore.yaml)
  --verbose         Enable verbose output
  -h, --help        Show this help message

Configuration file format (YAML):
  ignored_vulnerabilities:
    - id: GO-2024-12345
      module: github.com/example/module
      reason: "Acceptable risk in our context"

EOF
}

# Default values
CONFIG_FILE=".govulncheck-ignore.yaml"
VERBOSE=0

# Parse arguments
while [[ $# -gt 0 ]]; do
  case "$1" in
    --config)
      CONFIG_FILE="$2"
      shift 2
      ;;
    --verbose)
      VERBOSE=1
      shift
      ;;
    -h|--help)
      print_usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      print_usage
      exit 1
      ;;
  esac
done

log_info() {
  echo "[INFO] $*"
}

log_error() {
  echo "[ERROR] $*" >&2
}

# Check if govulncheck is installed
if ! command -v govulncheck &> /dev/null; then
  log_error "govulncheck not found. Install with: go install golang.org/x/vuln/cmd/govulncheck@latest"
  exit 1
fi

# Check if config file exists
if [[ ! -f "$CONFIG_FILE" ]]; then
  log_error "Config file not found: $CONFIG_FILE"
  exit 1
fi

# Check if jq is installed (for parsing JSON)
if ! command -v jq &> /dev/null; then
  log_error "jq not found. Install with your package manager (e.g., apt install jq)"
  exit 1
fi

# Check if yq is installed (for parsing YAML config)
if ! command -v yq &> /dev/null; then
  log_error "yq not found. Install with: go install github.com/mikefarah/yq/v4@latest"
  exit 1
fi

[[ $VERBOSE -eq 1 ]] && log_info "Using config file: $CONFIG_FILE"

# Run govulncheck with JSON output
[[ $VERBOSE -eq 1 ]] && log_info "Running govulncheck..."
set +e
VULN_JSON=$(govulncheck -tags gcs -json ./... 2>&1)
GOVULNCHECK_EXIT=$?
set -e

# With -json, govulncheck exits 0 even when vulnerabilities are reported; a non-zero
# exit means the scan did not finish (toolchain mismatch, package load errors, etc.).
# Findings are determined from the JSON stream below, not from GOVULNCHECK_EXIT.
if [[ $GOVULNCHECK_EXIT -ne 0 ]]; then
  log_error "govulncheck failed (exit $GOVULNCHECK_EXIT)"
  echo "$VULN_JSON" >&2
  exit 1
fi

# JSON mode may emit partial records before appending plain-text errors.
if grep -qE '^govulncheck:' <<< "$VULN_JSON"; then
  log_error "govulncheck reported errors"
  echo "$VULN_JSON" >&2
  exit 1
fi

# govulncheck -json emits a stream of pretty-printed JSON objects (not one value per line).
if [[ -n "$VULN_JSON" ]]; then
  if ! jq_stderr=$(echo "$VULN_JSON" | jq -e -n 'inputs' 2>&1 >/dev/null); then
    log_error "govulncheck output is not valid JSON (govulncheck exit $GOVULNCHECK_EXIT)"
    log_error "$jq_stderr"
    log_error "raw govulncheck output:"
    echo "$VULN_JSON" >&2
    exit 1
  fi
fi

# Extract findings from the JSON stream (one pretty-printed object per value)
# Each finding has: osv (ID), fixed_version (optional), trace[0].module
# Only consider vulnerabilities where our code actually calls the vulnerable function
# (trace length > 1 means there's a call path from our code to the vulnerable function)
FINDINGS=$(echo "$VULN_JSON" | jq -c 'select(.finding) | select(.finding.trace | length > 1) | {id: .finding.osv, module: .finding.trace[0].module, fixed: .finding.fixed_version}')

if [[ -z "$FINDINGS" ]]; then
  log_info "No vulnerabilities found"
  exit 0
fi

# Get unique vulnerabilities (same ID+module can appear multiple times with different traces)
UNIQUE_VULNS=$(echo "$FINDINGS" | jq -c -s 'unique_by(.id + .module)' | jq -c '.[]')

# Parse ignored vulnerabilities from config into a format we can match
IGNORED_LIST=$(yq -r '.ignored_vulnerabilities[] | "\(.id)|\(.module)"' "$CONFIG_FILE" 2>/dev/null || true)

[[ $VERBOSE -eq 1 ]] && log_info "Ignored vulnerabilities in config: $(echo "$IGNORED_LIST" | grep -c . || echo 0)"

# Check each vulnerability
IGNORED_COUNT=0
UNIGNORED_COUNT=0
UNIGNORED_VULNS=""

while IFS= read -r vuln; do
  [[ -z "$vuln" ]] && continue

  VULN_ID=$(echo "$vuln" | jq -r '.id')
  MODULE=$(echo "$vuln" | jq -r '.module')
  FIXED=$(echo "$vuln" | jq -r '.fixed // "N/A"')

  # Check if this vulnerability is in the ignore list AND has no fix available
  # If a fix is available, we should flag it even if it's in the ignore list
  if grep -qxF "${VULN_ID}|${MODULE}" <<< "$IGNORED_LIST"; then
    if [[ "$FIXED" == "N/A" || "$FIXED" == "null" ]]; then
      ((IGNORED_COUNT++)) || true
      [[ $VERBOSE -eq 1 ]] && log_info "Ignored: $VULN_ID in $MODULE (no fix available)"
    else
      ((UNIGNORED_COUNT++)) || true
      UNIGNORED_VULNS="${UNIGNORED_VULNS}  - $VULN_ID in $MODULE (fix available: $FIXED)\n"
      [[ $VERBOSE -eq 1 ]] && log_error "Fix now available: $VULN_ID in $MODULE (fixed in: $FIXED)"
    fi
  else
    ((UNIGNORED_COUNT++)) || true
    UNIGNORED_VULNS="${UNIGNORED_VULNS}  - $VULN_ID in $MODULE (fixed: $FIXED)\n"
    [[ $VERBOSE -eq 1 ]] && log_error "Found: $VULN_ID in $MODULE (fixed: $FIXED)"
  fi
done <<< "$UNIQUE_VULNS"

# Report results
log_info "Found $UNIGNORED_COUNT unignored vulnerabilities, $IGNORED_COUNT ignored"

if [[ $UNIGNORED_COUNT -gt 0 ]]; then
  log_error "Unignored vulnerabilities found:"
  echo -e "$UNIGNORED_VULNS" >&2
  exit 1
fi

exit 0
