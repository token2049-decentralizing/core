# Kudoz dashboard

The web app at [kudoz.dev](https://kudoz.dev): repositories and their webhook activity, reward campaigns, CRE executions with score breakdowns, live PR status, and "My rewards" for contributors. Sponsors sign in with GitHub through Privy to manage campaigns and fund their Solana devnet treasury.

```bash
pnpm install
pnpm dev        # http://localhost:3000
```

| Variable | Value |
| --- | --- |
| `NEXT_PUBLIC_API_BASE_URL` | Kudoz API (`cre-runner`), e.g. `https://cre-runner.fly.dev` |
| `NEXT_PUBLIC_PRIVY_APP_ID` | Privy app ID; without it, sign-in and campaign management are disabled |
| `NEXT_PUBLIC_SOLANA_RPC_URL` | Solana devnet RPC (default public devnet, which rate-limits) |
| `NEXT_PUBLIC_CONTRIB_ORACLE_PROGRAM_ID` | `contrib_oracle` program (default `FSy2V61Tvm6bVHV4dGtoJS7T16eE7ZNjvGHEyT3aw6MA`) |

See [`../cre-runner/DEPLOY.md`](../cre-runner/DEPLOY.md) for the full setup.
