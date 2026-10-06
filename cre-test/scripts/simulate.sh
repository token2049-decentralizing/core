#!/usr/bin/env bash
# Runs the workflow simulation with a fresh GitHub App token.
# Usage (from cre-test/): ./scripts/simulate.sh test-workflow/payloads/opened.json
set -euo pipefail
cd "$(dirname "$0")/.."

payload="${1:?usage: simulate.sh <payload.json>}"

set -a; source .env; set +a

# Token only lives in this process's env; never written to disk.
GITHUB_TOKEN_VALUE="$(go run ./cmd/github-app-token)"
export GITHUB_TOKEN_VALUE
[ -n "$GITHUB_TOKEN_VALUE" ] || { echo "failed to mint GitHub token" >&2; exit 1; }
export REVIEWER_TOKEN_VALUE="${REVIEWER_TOKEN:-}" # Workflow secret REVIEWER_TOKEN.

cre workflow simulate test-workflow --target "${CRE_TARGET:-local-simulation}" \
  --non-interactive --trigger-index 0 \
  --http-payload "$(cat "${payload}")"
