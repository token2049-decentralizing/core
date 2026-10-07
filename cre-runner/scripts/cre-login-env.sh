#!/usr/bin/env bash
# Prints your `cre login` session (~/.cre) as env vars for cre-runner.
# Run `cre login` first. The output is a credential: never commit or paste it.
#   ./scripts/cre-login-env.sh >> .env                                      # local / docker compose
#   ./scripts/cre-login-env.sh | xargs fly secrets set                       # Fly
set -euo pipefail
dir="${CRE_DIR:-$HOME/.cre}"
[ -f "$dir/cre.yaml" ] || { echo "no $dir/cre.yaml: run 'cre login' first" >&2; exit 1; }
echo "CRE_LOGIN_YAML=$(base64 < "$dir/cre.yaml" | tr -d '\n')"
if [ -f "$dir/context.yaml" ]; then
  echo "CRE_CONTEXT_YAML=$(base64 < "$dir/context.yaml" | tr -d '\n')"
fi
