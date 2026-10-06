# ContribOracle CRE

| Path | What |
| --- | --- |
| `test-workflow/` | CRE workflow (Go): PR evidence -> score -> reward decision. See its README. |
| `cmd/runner/` | HTTP API the GitHub App calls; runs the workflow in the CRE simulator. |
| `cmd/github-app-token/` | Prints a GitHub App installation token (used by `scripts/simulate.sh`). |
| `internal/ghapp/` | GitHub App token minting + caching. |
| `Dockerfile`, `docker-compose.yml` | Runner image: cre CLI + prebuilt workflow WASM + runner. |

## Run the runner in Docker

```bash
cp .env.example .env    # fill in GITHUB_APP_*, RUNNER_SHARED_SECRET (+ CRE_API_KEY if you have one)
docker compose up --build
curl localhost:8080/healthz   # ok
```

CRE auth, pick one:

- **API key** (servers): set `CRE_API_KEY` in `.env`. Keys are created at app.chain.link > Account Settings
  (the CLI has no command for it).
- **Your `cre login` session** (local dev): leave `CRE_API_KEY` empty, run `cre login` on the host, then

  ```bash
  docker compose -f docker-compose.yml -f docker-compose.login.yml up --build
  ```

  This mounts `~/.cre` (writable, the CLI refreshes the token there) and limits the runner to 1 concurrent
  evaluation. The container gets full access to your CRE account, so use it on your own machine only.

The App private key is mounted read-only from `GITHUB_APP_PRIVATE_KEY_PATH`.
On Linux the container user (uid 10001) must be able to read it: `chmod 644 github-app.pem`.

## API

### `POST /evaluate`

Headers:

```
Content-Type: application/json
X-ContribOracle-Signature: sha256=<hex HMAC-SHA256(raw body, RUNNER_SHARED_SECRET)>
```

Body:

```json
{ "repository": "owner/repo", "pr_number": 1, "campaign_id": "example-oss-2026", "event": "opened", "notes": {} }
```

`event` is `opened` (preview, never pays) or `merged`. `notes` is optional and ignored by scoring.

Responses:

| Status | Body |
| --- | --- |
| 200 | `{ "score": 30, "eligible": false, "reward": "0", "evaluation_hash": "0x…", "policy_hash": "0x…" }` |
| 400 | `{ "error": "…" }` invalid body |
| 401 | `{ "error": "invalid signature" }` |
| 502 | `{ "error": "…" }` workflow failed (PR not found, GitHub error, CRE auth…) |
| 503 | `{ "error": "busy, retry later" }` + `Retry-After` |
| 504 | `{ "error": "evaluation timed out" }` |

The call blocks until the evaluation finishes (a few seconds).
`reward` is in token base units (USDC: 6 decimals, `"445000000"` = 445 USDC).

### `GET /healthz`

`200 ok`.

## Signing a request

Node (GitHub App side):

```js
import crypto from "node:crypto";

const body = JSON.stringify({ repository, pr_number, campaign_id, event });
const sig = "sha256=" + crypto.createHmac("sha256", process.env.RUNNER_SHARED_SECRET).update(body).digest("hex");

const res = await fetch(`${RUNNER_URL}/evaluate`, {
  method: "POST",
  headers: { "Content-Type": "application/json", "X-ContribOracle-Signature": sig },
  body, // sign and send the exact same string
});
```

curl:

```bash
BODY='{"repository":"token2049-decentralizing/core","pr_number":1,"campaign_id":"example-oss-2026","event":"opened"}'
SIG="sha256=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$RUNNER_SHARED_SECRET" | awk '{print $2}')"
curl -X POST localhost:8080/evaluate -H "X-ContribOracle-Signature: $SIG" -d "$BODY"
```

## How the runner works

- Validates signature and body, then queues the request (`MAX_CONCURRENCY` running, `MAX_QUEUE` waiting, 503 beyond).
- Mints/caches a read-only GitHub App token and passes it to the workflow as the `GITHUB_TOKEN` secret.
- Runs `cre workflow simulate --wasm build/workflow.wasm`. The WASM is compiled once at image build,
  so runs are fast and can run in parallel.
- The CLI subprocess only gets `PATH`, `HOME`, `GO*`, `CRE_*` and `*_VALUE` env vars;
  the shared secret and App key never reach it.

## Develop

```bash
go test ./...                                  # all packages
go run ./cmd/runner                            # needs the same env vars as Docker; set CRE_BIN if cre is not on PATH
./scripts/simulate.sh test-workflow/payloads/opened.json
```
