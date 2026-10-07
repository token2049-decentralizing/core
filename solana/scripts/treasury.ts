// Client for the contrib_oracle program: PDAs, admin/sponsor instructions, the account list the CRE workflow needs.
import { BN, Program, type Idl, type Provider } from "@coral-xyz/anchor";
import { getAssociatedTokenAddressSync } from "@solana/spl-token";
import { PublicKey, SystemProgram, type AccountMeta } from "@solana/web3.js";
import { createHash } from "node:crypto";
import idl from "../idl/contrib_oracle.json";

// Chainlink mock forwarder that `cre workflow simulate --broadcast` uses on devnet.
export const DEVNET_MOCK_FORWARDER = new PublicKey("7kuEAA3mSC1Tz8gQjnvH7bKFda9xSPRRin9SZbH49cNK");
export const DEVNET_MOCK_FORWARDER_STATE = new PublicKey("5Tipz3yhTBdVsDbaBxZkrp7Gjf3brGq5SKkxReefPMP7");

export type Status = "active" | "paused";

// cre-runner campaign UUID -> 16 raw bytes.
export function uuidBytes(uuid: string): number[] {
  const hex = uuid.replace(/-/g, "");
  if (!/^[0-9a-fA-F]{32}$/.test(hex)) throw new Error(`invalid campaign UUID: ${uuid}`);
  return [...Buffer.from(hex, "hex")];
}

// "0x…" hex -> exactly n bytes. Empty = zeros (check disabled on-chain).
export function hexBytes(value: string | undefined, n: number): number[] {
  const hex = (value ?? "").replace(/^0x/, "");
  if (hex === "") return new Array(n).fill(0);
  if (!new RegExp(`^[0-9a-fA-F]{${n * 2}}$`).test(hex)) throw new Error(`expected ${n}-byte hex, got ${value}`);
  return [...Buffer.from(hex, "hex")];
}

// sha256("owner/repo#pr"): the pay-once key.
export function contributionId(repository: string, pr: number): Buffer {
  return createHash("sha256").update(`${repository}#${pr}`).digest();
}

export interface PayoutRecord {
  campaign: PublicKey;
  contributionId: Buffer;
  recipient: PublicKey;
  amount: bigint;
  score: number;
  evaluationHash: Buffer;
  policyHash: Buffer;
  reportId: Buffer;
  paidAt: bigint;
}

// Payout isn't in the IDL (created by raw CPI), so decode by hand. Layout: state.rs Payout.
export function decodePayout(data: Buffer): PayoutRecord {
  let o = 8; // Anchor discriminator.
  const take = (n: number) => data.subarray(o, (o += n));
  return {
    campaign: new PublicKey(take(32)),
    contributionId: Buffer.from(take(32)),
    recipient: new PublicKey(take(32)),
    amount: take(8).readBigUInt64LE(),
    score: take(1)[0],
    evaluationHash: Buffer.from(take(32)),
    policyHash: Buffer.from(take(32)),
    reportId: Buffer.from(take(2)),
    paidAt: take(8).readBigInt64LE(),
  };
}

export class Treasury {
  readonly program: Program;

  constructor(provider: Provider, programId?: PublicKey) {
    const withAddress = programId ? { ...idl, address: programId.toBase58() } : idl;
    this.program = new Program(withAddress as Idl, provider);
  }

  get programId() {
    return this.program.programId;
  }

  private get wallet(): PublicKey {
    return this.program.provider.publicKey!;
  }

  pda(...seeds: (Buffer | Uint8Array)[]) {
    return PublicKey.findProgramAddressSync(seeds, this.programId)[0];
  }

  get config() {
    return this.pda(Buffer.from("config"));
  }

  get rentPayer() {
    return this.pda(Buffer.from("rent_payer"));
  }

  campaign(uuid: string) {
    return this.pda(Buffer.from("campaign"), Buffer.from(uuidBytes(uuid)));
  }

  vault(uuid: string) {
    return this.pda(Buffer.from("vault"), this.campaign(uuid).toBuffer());
  }

  payout(uuid: string, repository: string, pr: number) {
    return this.pda(Buffer.from("payout"), this.campaign(uuid).toBuffer(), contributionId(repository, pr));
  }

  forwarderAuthority(forwarderProgram: PublicKey, forwarderState: PublicKey) {
    return PublicKey.findProgramAddressSync(
      [Buffer.from("forwarder"), forwarderState.toBuffer(), this.programId.toBuffer()],
      forwarderProgram,
    )[0];
  }

  // Token or Token-2022, whichever owns the mint.
  async tokenProgram(mint: PublicKey) {
    const info = await this.program.provider.connection.getAccountInfo(mint);
    if (!info) throw new Error(`mint not found: ${mint.toBase58()}`);
    return info.owner;
  }

  initialize(forwarderProgram: PublicKey, forwarderState: PublicKey, workflowOwner?: string) {
    return this.program.methods
      .initialize(forwarderProgram, forwarderState, hexBytes(workflowOwner, 20))
      .accountsPartial({ admin: this.wallet, config: this.config, systemProgram: SystemProgram.programId })
      .rpc();
  }

  updateConfig(forwarderProgram: PublicKey, forwarderState: PublicKey, workflowOwner?: string) {
    return this.program.methods
      .updateConfig(forwarderProgram, forwarderState, hexBytes(workflowOwner, 20))
      .accountsPartial({ admin: this.wallet, config: this.config })
      .rpc();
  }

  async createCampaign(uuid: string, mint: PublicKey, maxRewardPerPr: bigint, policyHash?: string) {
    return this.program.methods
      .createCampaign(uuidBytes(uuid), new BN(maxRewardPerPr.toString()), hexBytes(policyHash, 32))
      .accountsPartial({
        sponsor: this.wallet,
        campaign: this.campaign(uuid),
        mint,
        vault: this.vault(uuid),
        tokenProgram: await this.tokenProgram(mint),
        systemProgram: SystemProgram.programId,
      })
      .rpc();
  }

  // Funds from the wallet's associated token account.
  async fund(uuid: string, amount: bigint) {
    const c = await this.fetchCampaign(uuid);
    const tokenProgram = await this.tokenProgram(c.mint);
    return this.program.methods
      .fundCampaign(new BN(amount.toString()))
      .accountsPartial({
        funder: this.wallet,
        campaign: this.campaign(uuid),
        mint: c.mint,
        vault: c.vault,
        funderToken: getAssociatedTokenAddressSync(c.mint, this.wallet, true, tokenProgram),
        tokenProgram,
      })
      .rpc();
  }

  setStatus(uuid: string, status: Status) {
    return this.program.methods
      .setCampaignStatus({ [status]: {} })
      .accountsPartial({ sponsor: this.wallet, campaign: this.campaign(uuid) })
      .rpc();
  }

  // Refunds the rest to the sponsor's associated token account and closes the vault.
  async close(uuid: string) {
    const c = await this.fetchCampaign(uuid);
    const tokenProgram = await this.tokenProgram(c.mint);
    return this.program.methods
      .closeCampaign()
      .accountsPartial({
        sponsor: this.wallet,
        campaign: this.campaign(uuid),
        mint: c.mint,
        vault: c.vault,
        sponsorToken: getAssociatedTokenAddressSync(c.mint, this.wallet, true, tokenProgram),
        tokenProgram,
      })
      .rpc();
  }

  fetchConfig() {
    return (this.program.account as any).config.fetch(this.config);
  }

  fetchCampaign(uuid: string) {
    return (this.program.account as any).campaign.fetch(this.campaign(uuid));
  }

  async fetchPayout(uuid: string, repository: string, pr: number): Promise<PayoutRecord | null> {
    const info = await this.program.provider.connection.getAccountInfo(this.payout(uuid, repository, pr));
    return info ? decodePayout(info.data) : null;
  }

  // Accounts for the workflow's WriteReportFromRewardReport, in order (forwarder layout + OnReport).
  async reportAccounts(uuid: string, repository: string, pr: number, recipient: PublicKey): Promise<AccountMeta[]> {
    const cfg = await this.fetchConfig();
    const c = await this.fetchCampaign(uuid);
    const tokenProgram = await this.tokenProgram(c.mint);
    const ro = (pubkey: PublicKey): AccountMeta => ({ pubkey, isSigner: false, isWritable: false });
    const rw = (pubkey: PublicKey): AccountMeta => ({ pubkey, isSigner: false, isWritable: true });
    return [
      ro(cfg.forwarderState),
      ro(this.forwarderAuthority(cfg.forwarderProgram, cfg.forwarderState)),
      ro(this.config),
      rw(this.campaign(uuid)),
      rw(c.vault),
      ro(c.mint),
      rw(this.payout(uuid, repository, pr)),
      rw(getAssociatedTokenAddressSync(c.mint, recipient, true, tokenProgram)),
      rw(this.rentPayer),
      ro(tokenProgram),
      ro(SystemProgram.programId),
    ];
  }
}
