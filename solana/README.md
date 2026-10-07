# ContribOracle campaign treasury (Solana / Anchor)

Sponsors fund a campaign vault. The CRE workflow writes a signed `RewardReport`; the Chainlink forwarder
calls `on_report`, which checks the caller and the campaign rules, then pays the contributor **once per PR**.

```
CRE workflow ── WriteReportFromRewardReport ──► forwarder (keystone / devnet mock)
                                                   │ CPI on_report(metadata, payload)
                                                   ▼
                                    contrib_oracle ── vault ──► contributor token account
                                                   └── Payout PDA (exists = already paid)
```

| Path | What |
| --- | --- |
| `programs/contrib-oracle/` | Anchor program |
| `idl/contrib_oracle.json` | IDL (input for `cre generate-bindings solana` and the TS client) |
| `tests/` | Rust end-to-end tests: LiteSVM + Chainlink's real mock forwarder (`tests/fixtures`) |
| `scripts/` | Admin CLI + TS client (`treasury.ts`), tested against LiteSVM |

## Program

| Instruction | Who | What |
| --- | --- | --- |
| `initialize(forwarder_program, forwarder_state, workflow_owner)` | first caller = admin | One-time config. **Run right after deploy.** |
| `update_config(...)` | admin | Switch forwarder (mock -> keystone) or workflow owner |
| `create_campaign(campaign_id, max_reward_per_pr, policy_hash)` | sponsor | Campaign PDA + program-owned vault for one mint |
| `fund_campaign(amount)` | anyone | Deposit into the vault |
| `set_campaign_status(active\|paused)` | sponsor | Pause/resume payouts |
| `close_campaign` | sponsor | Refund the rest, close the vault |
| `on_report(metadata, payload)` | forwarder only | Pay a reward |

`on_report` rejects the report unless:

- the caller is the configured forwarder: `forwarder_state` matches config, and `forwarder_authority` is the
  forwarder's signer PDA `["forwarder", forwarder_state, contrib_oracle]`;
- the workflow owner matches (if `workflow_owner` is set; zeros = any);
- the campaign is active, `policy_hash` matches (if set), `0 < amount <= max_reward_per_pr`, the vault has the funds,
  and the token account belongs to `recipient`;
- the PR wasn't paid yet: the `Payout` PDA `["payout", campaign, contribution_id]` must not exist.

Payout records are paid for by the `rent_payer` PDA (forwarded accounts can't sign), so keep some SOL in it.

### Report payload (`RewardReport`, Borsh)

| Field | Type | Value |
| --- | --- | --- |
| `campaign_id` | `[u8; 16]` | cre-runner campaign UUID, raw bytes |
| `contribution_id` | `[u8; 32]` | `sha256("owner/repo#pr")` |
| `recipient` | `Pubkey` | contributor wallet |
| `amount` | `u64` | token base units (the workflow's `reward`) |
| `score` | `u8` | 0-100 |
| `evaluation_hash` | `[u8; 32]` | workflow `evaluation_hash` |
| `policy_hash` | `[u8; 32]` | workflow `policy_hash` |

### Accounts for the workflow write (order matters)

`WriteReportFromRewardReport(runtime, report, remainingAccounts, nil)`:

| # | Account | Writable |
| --- | --- | --- |
| 0 | forwarder state (config) | |
| 1 | forwarder authority `PDA(["forwarder", state, contrib_oracle], forwarder_program)` | |
| 2 | config `["config"]` | |
| 3 | campaign `["campaign", campaign_id]` | ✓ |
| 4 | vault `["vault", campaign]` | ✓ |
| 5 | mint | |
| 6 | payout `["payout", campaign, contribution_id]` | ✓ |
| 7 | recipient token account (ATA of recipient + mint) | ✓ |
| 8 | rent payer `["rent_payer"]` | ✓ |
| 9 | token program | |
| 10 | system program | |

`bun cli.ts accounts <uuid> <owner/repo> <pr> <recipient>` prints this list for a real campaign.
The recipient token account must exist before the payout (`bun cli.ts ensure-ata <mint> <wallet>`).

## Build and test

Toolchain: Rust, Solana CLI (`cargo-build-sbf`), Anchor CLI 0.31.1, Bun.

```bash
cargo-build-sbf --arch v0 --manifest-path programs/contrib-oracle/Cargo.toml --sbf-out-dir target/deploy
cargo test -p contrib-oracle-tests                          # Rust end-to-end (6 tests)
cd scripts && bun install && bun test && bun run typecheck  # TS client
anchor idl build -p contrib_oracle -o idl/contrib_oracle.json   # after changing the program
```

`--arch v0`: `cargo-build-sbf` 4.x defaults to SBPF v3, which the tests' LiteSVM (agave 2.x) can't run; v0 runs everywhere.
With Anchor: `anchor build --no-idl -- --arch v0` (Anchor passes `-- args` to the IDL build too, which breaks it).

## Deploy to devnet

```bash
solana config set --url devnet
solana-keygen new                                   # skip if you have ~/.config/solana/id.json
solana airdrop 5                                    # or https://faucet.solana.com

# first build creates target/deploy/contrib_oracle-keypair.json; keys sync puts its pubkey in declare_id! + Anchor.toml
cargo-build-sbf --arch v0 --manifest-path programs/contrib-oracle/Cargo.toml --sbf-out-dir target/deploy
anchor keys sync
cargo-build-sbf --arch v0 --manifest-path programs/contrib-oracle/Cargo.toml --sbf-out-dir target/deploy
anchor idl build -p contrib_oracle -o idl/contrib_oracle.json
solana program deploy target/deploy/contrib_oracle.so --program-id target/deploy/contrib_oracle-keypair.json
```

`anchor keys sync` writes your program id into the source; commit that change (the keypair stays local, git-ignored).
Deploying ~310 KB needs ~2.2 SOL of rent (returned if you close the program).

Then, from `scripts/` (wallet `~/.config/solana/id.json`, RPC `SOLANA_RPC_URL`, default devnet):

```bash
bun install
bun cli.ts init                                       # trusts the devnet mock forwarder (CRE simulate)
bun cli.ts fund-rent-payer 0.5                        # ~0.002 SOL per payout record
bun cli.ts create-mint --amount 10000                 # demo 6-decimal token, or use devnet USDC
bun cli.ts create-campaign <campaign-uuid> <mint> 200 --policy-hash <0x policy_hash from an evaluation>
bun cli.ts fund <campaign-uuid> 1000
bun cli.ts show <campaign-uuid>
```

Use the same UUID as the cre-runner campaign. Leave `--policy-hash` out to skip that check.

Devnet mock forwarder (used by `cre workflow simulate --broadcast`):
program `7kuEAA3mSC1Tz8gQjnvH7bKFda9xSPRRin9SZbH49cNK`, state `5Tipz3yhTBdVsDbaBxZkrp7Gjf3brGq5SKkxReefPMP7`.

## Workflow integration (todo)

1. `cre generate-bindings solana -i ../../solana/idl` from `cre-runner/cre/` (generates `WriteReportFromRewardReport`).
2. In `cre-runner/cre/test-workflow/solana.go`, for an eligible `merged` result: build `RewardReport`, derive the accounts above, call
   `WriteReportFromRewardReport`.
3. Add a Solana devnet RPC to `cre-runner/cre/project.yaml`; simulate with `--broadcast` (`CRE_SOLANA_PRIVATE_KEY` pays the fee).
4. Map GitHub user -> Solana wallet (not decided yet).

## Security notes

- **The devnet mock forwarder verifies nothing**: anyone can send a report through it, including a fake workflow
  owner. Up to `max_reward_per_pr` per unique PR id can be drained. Keep devnet campaigns small.
- Production: `update_config` to the keystone forwarder (verifies DON signatures) and set `workflow_owner`.
- `initialize` is first-come: run it in the same session as the deploy.
- Upgrade authority = deployer wallet; `solana program set-upgrade-authority --final` to freeze.
