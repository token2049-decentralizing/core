// Client against LiteSVM with the built program + Chainlink mock forwarder.
// Build first: cargo-build-sbf --arch v0 --manifest-path ../programs/contrib-oracle/Cargo.toml --sbf-out-dir ../target/deploy
import { BN, Wallet } from "@coral-xyz/anchor";
import {
  ACCOUNT_SIZE,
  AccountLayout,
  MINT_SIZE,
  MintLayout,
  TOKEN_PROGRAM_ID,
  getAssociatedTokenAddressSync,
} from "@solana/spl-token";
import { Keypair, LAMPORTS_PER_SOL, PublicKey, SystemProgram, Transaction, TransactionInstruction } from "@solana/web3.js";
import { LiteSVMProvider } from "anchor-litesvm";
import { beforeEach, expect, test } from "bun:test";
import { FailedTransactionMetadata, LiteSVM } from "litesvm";
import { createHash } from "node:crypto";
import { DEVNET_MOCK_FORWARDER, Treasury, contributionId, uuidBytes } from "./treasury";

const UUID = "6f1c2a9e-3b7d-4c1e-9a2b-1c3d4e5f6a7b";
const USDC = 1_000_000n;
const POLICY = "0x" + "07".repeat(32);
const REPO = "acme/pool";

const sha256 = (b: Uint8Array) => createHash("sha256").update(b).digest();
const disc = (name: string) => sha256(Buffer.from(`global:${name}`)).subarray(0, 8);
const u32 = (n: number) => {
  const b = Buffer.alloc(4);
  b.writeUInt32LE(n);
  return b;
};
const borshBytes = (b: Uint8Array) => Buffer.concat([u32(b.length), b]);

let svm: LiteSVM;
let sponsor: Keypair;
let t: Treasury;
let mint: PublicKey;
let fwdState: PublicKey;

function setMint(): PublicKey {
  const m = Keypair.generate().publicKey;
  const data = Buffer.alloc(MINT_SIZE);
  MintLayout.encode(
    { mintAuthorityOption: 1, mintAuthority: sponsor.publicKey, supply: 0n, decimals: 6, isInitialized: true,
      freezeAuthorityOption: 0, freezeAuthority: PublicKey.default },
    data,
  );
  svm.setAccount(m, { lamports: LAMPORTS_PER_SOL, data, owner: TOKEN_PROGRAM_ID, executable: false });
  return m;
}

function setTokenAccount(owner: PublicKey, amount: bigint): PublicKey {
  const ata = getAssociatedTokenAddressSync(mint, owner, true);
  const data = Buffer.alloc(ACCOUNT_SIZE);
  AccountLayout.encode(
    { mint, owner, amount, delegateOption: 0, delegate: PublicKey.default, state: 1, isNativeOption: 0,
      isNative: 0n, delegatedAmount: 0n, closeAuthorityOption: 0, closeAuthority: PublicKey.default },
    data,
  );
  svm.setAccount(ata, { lamports: LAMPORTS_PER_SOL, data, owner: TOKEN_PROGRAM_ID, executable: false });
  return ata;
}

const balance = (a: PublicKey) => AccountLayout.decode(svm.getAccount(a)!.data).amount;

async function send(tx: Transaction, ...signers: Keypair[]) {
  tx.recentBlockhash = svm.latestBlockhash();
  tx.feePayer = signers[0].publicKey;
  tx.sign(...signers);
  const res = svm.sendTransaction(tx);
  svm.expireBlockhash();
  if (res instanceof FailedTransactionMetadata) throw new Error(res.meta().logs().join("\n"));
}

beforeEach(async () => {
  svm = new LiteSVM();
  sponsor = Keypair.generate();
  svm.airdrop(sponsor.publicKey, 100n * BigInt(LAMPORTS_PER_SOL));
  const programId = new PublicKey((await import("../idl/contrib_oracle.json")).address);
  svm.addProgramFromFile(programId, `${import.meta.dir}/../target/deploy/contrib_oracle.so`);
  svm.addProgramFromFile(DEVNET_MOCK_FORWARDER, `${import.meta.dir}/../tests/fixtures/mock_forwarder.so`);

  const state = Keypair.generate();
  await send(
    new Transaction().add(new TransactionInstruction({
      programId: DEVNET_MOCK_FORWARDER,
      keys: [
        { pubkey: state.publicKey, isSigner: true, isWritable: true },
        { pubkey: sponsor.publicKey, isSigner: true, isWritable: true },
        { pubkey: SystemProgram.programId, isSigner: false, isWritable: false },
      ],
      data: disc("initialize"),
    })),
    sponsor, state,
  );
  fwdState = state.publicKey;

  t = new Treasury(new LiteSVMProvider(svm, new Wallet(sponsor)));
  mint = setMint();
  setTokenAccount(sponsor.publicKey, 10_000n * USDC);
});

// Mock forwarder `report` ix, receiver accounts taken from Treasury.reportAccounts (what the workflow sends).
async function forwardReport(pr: number, recipient: PublicKey, amount: bigint) {
  const metas = await t.reportAccounts(UUID, REPO, pr, recipient);
  const payload = t.program.coder.types.encode("rewardReport", {
    campaignId: uuidBytes(UUID),
    contributionId: [...contributionId(REPO, pr)],
    recipient,
    amount: new BN(amount.toString()),
    evaluationHash: new Array(32).fill(9),
  });
  const raw = Buffer.concat([
    Buffer.from([1]), sha256(Buffer.from(`exec-${pr}`)), Buffer.alloc(12), // forwarder metadata (45)
    Buffer.alloc(32, 3), Buffer.alloc(10, 4), Buffer.alloc(20), Buffer.from([0, 1]), // workflow metadata (64)
    sha256(Buffer.concat(metas.map((m) => m.pubkey.toBuffer()))), // account hash
    borshBytes(payload),
  ]);
  const data = Buffer.concat([disc("report"), borshBytes(Buffer.concat([Buffer.from([0]), raw, Buffer.alloc(96)]))]);
  const ro = (pubkey: PublicKey) => ({ pubkey, isSigner: false, isWritable: false });
  await send(
    new Transaction().add(new TransactionInstruction({
      programId: DEVNET_MOCK_FORWARDER,
      keys: [
        ro(metas[0].pubkey),
        { pubkey: sponsor.publicKey, isSigner: true, isWritable: true },
        ro(metas[1].pubkey),
        ro(t.programId),
        ro(SystemProgram.programId),
        ...metas.slice(2),
      ],
      data,
    })),
    sponsor,
  );
}

test("sponsor flow and a forwarded payout using reportAccounts", async () => {
  await t.initialize(DEVNET_MOCK_FORWARDER, fwdState);
  await t.createCampaign(UUID, mint, 200n * USDC, POLICY);
  await t.fund(UUID, 1_000n * USDC);
  await send(
    new Transaction().add(SystemProgram.transfer({ fromPubkey: sponsor.publicKey, toPubkey: t.rentPayer, lamports: LAMPORTS_PER_SOL })),
    sponsor,
  );
  expect(balance(t.vault(UUID))).toBe(1_000n * USDC);

  const contributor = Keypair.generate().publicKey;
  const contributorAta = setTokenAccount(contributor, 0n);
  await forwardReport(42, contributor, 150n * USDC);
  expect(balance(contributorAta)).toBe(150n * USDC);

  const p = await t.fetchPayout(UUID, REPO, 42);
  expect(p?.amount).toBe(150n * USDC);
  expect(p?.recipient.equals(contributor)).toBe(true);
  expect(p?.contributionId.equals(contributionId(REPO, 42))).toBe(true);
  expect(svm.getAccount(t.payout(UUID, REPO, 43))).toBeNull(); // LiteSVMProvider throws on missing accounts.

  await expect(forwardReport(42, contributor, 150n * USDC)).rejects.toThrow("AlreadyPaid");

  await t.setStatus(UUID, "paused");
  await expect(forwardReport(43, contributor, 1n * USDC)).rejects.toThrow("CampaignNotActive");
  await t.setStatus(UUID, "active");

  const c = await t.fetchCampaign(UUID);
  expect(c.totalPaid.toString()).toBe((150n * USDC).toString());
  expect(c.payoutCount.toNumber()).toBe(1);

  await t.close(UUID);
  expect(balance(getAssociatedTokenAddressSync(mint, sponsor.publicKey))).toBe(9_850n * USDC);
});
