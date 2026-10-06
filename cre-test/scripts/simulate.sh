#!/usr/bin/env bash
# Runs the workflow simulation with a fresh GitHub App token.
# Usage (from cre-test/): ./scripts/simulate.sh test-workflow/payloads/opened.json
set -euo pipefail
cd "$(dirname "$0")/.."

payload="${1:?usage: simulate.sh <payload.json>}"

set -a; source .env; set +a

# Token only lives in this process's env; never written to disk.
GITHUB_TOKEN_VALUE="$(bun scripts/github-app-token.ts)"
export GITHUB_TOKEN_VALUE

cre workflow simulate test-workflow --target staging-settings \
  --non-interactive --trigger-index 0 \
  --http-payload "$(cat "${payload}")"
