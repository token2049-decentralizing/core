// Client for the contrib_oracle Solana program (../solana): campaign PDAs, instruction
// encoding (discriminators and account order from idl/contrib_oracle.json) and account
// decoding. Transactions are compiled unsigned; the connected Privy wallet signs and sends.
import {
  AccountRole,
  address,
  appendTransactionMessageInstructions,
  compileTransaction,
  createNoopSigner,
  createSolanaRpc,
  createTransactionMessage,
  fixEncoderSize,
  getAddressDecoder,
  getAddressEncoder,
  getBytesEncoder,
  getProgramDerivedAddress,
  getStructEncoder,
  getTransactionEncoder,
  getU64Encoder,
  getU8Encoder,
  lamports,
  pipe,
  setTransactionMessageFeePayerSigner,
  setTransactionMessageLifetimeUsingBlockhash,
  type Address,
  type Instruction,
} from "@solana/kit"
import {
  findAssociatedTokenPda,
  getCreateAssociatedTokenIdempotentInstruction,
  getSyncNativeInstruction,
  TOKEN_PROGRAM_ADDRESS,
} from "@solana-program/token"
import {
  getTransferSolInstruction,
  SYSTEM_PROGRAM_ADDRESS,
} from "@solana-program/system"

import { SOLANA_CLUSTER } from "@/lib/solana"

export const PROGRAM_ID = address(
  process.env.NEXT_PUBLIC_CONTRIB_ORACLE_PROGRAM_ID ??
    "FSy2V61Tvm6bVHV4dGtoJS7T16eE7ZNjvGHEyT3aw6MA"
)

export const SOLANA_RPC_URL =
  process.env.NEXT_PUBLIC_SOLANA_RPC_URL ?? "https://api.devnet.solana.com"

// Privy chain id for signAndSendTransaction: devnet only.
export const SOLANA_CHAIN = "solana:devnet"

// Genesis hash of Solana devnet (chain-selectors: solana-devnet).
const DEVNET_GENESIS_HASH = "EtWTRABZaYq6iMfeYKouRu166VU2xqa1wcaWoxPkrZBG"
let devnetCheck: Promise<void> | undefined

// Refuses to build transactions if NEXT_PUBLIC_SOLANA_RPC_URL points at another cluster.
export function assertDevnet(): Promise<void> {
  devnetCheck ??= rpc
    .getGenesisHash()
    .send()
    .then((hash) => {
      if (hash !== DEVNET_GENESIS_HASH) {
        throw new Error(
          `NEXT_PUBLIC_SOLANA_RPC_URL is not Solana devnet (genesis ${hash}); campaigns only run on devnet.`
        )
      }
    })
    .catch((e: unknown) => {
      devnetCheck = undefined // Retry next time, e.g. after a transient RPC error.
      throw e
    })
  return devnetCheck
}

export const WRAPPED_SOL_MINT = address(
  "So11111111111111111111111111111111111111112"
)
const TOKEN_2022_PROGRAM_ADDRESS = address(
  "TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb"
)

// Default reward mint per campaign asset. USDC: Circle's devnet USDC (faucet.circle.com).
// The only reward tokens campaigns accept, per asset (Solana devnet): Circle's devnet USDC
// (faucet.circle.com) and wrapped SOL (the native mint, same address on every cluster).
// The runner refuses payouts from campaigns created with any other mint.
export const REWARD_MINT: Record<string, Address> = {
  USDC: address("4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU"),
  SOL: WRAPPED_SOL_MINT,
}

export const rpc = createSolanaRpc(SOLANA_RPC_URL)

const DISCRIMINATOR = {
  createCampaign: [111, 131, 187, 98, 160, 193, 114, 244],
  fundCampaign: [109, 57, 56, 239, 99, 111, 221, 121],
  setCampaignStatus: [228, 16, 24, 238, 203, 46, 69, 189],
  campaignAccount: [50, 40, 49, 11, 157, 220, 229, 192],
}

export const CAMPAIGN_STATUS = ["active", "paused", "closed"] as const
export type OnChainStatus = (typeof CAMPAIGN_STATUS)[number]

const text = new TextEncoder()

export function uuidBytes(uuid: string): Uint8Array {
  const hex = uuid.replace(/-/g, "")
  if (!/^[0-9a-f]{32}$/i.test(hex)) throw new Error(`not a UUID: ${uuid}`)
  return Uint8Array.from(hex.match(/../g)!.map((b) => parseInt(b, 16)))
}

export async function campaignPda(uuid: string): Promise<Address> {
  const [pda] = await getProgramDerivedAddress({
    programAddress: PROGRAM_ID,
    seeds: [text.encode("campaign"), uuidBytes(uuid)],
  })
  return pda
}

export async function vaultPda(campaign: Address): Promise<Address> {
  const [pda] = await getProgramDerivedAddress({
    programAddress: PROGRAM_ID,
    seeds: [text.encode("vault"), getAddressEncoder().encode(campaign)],
  })
  return pda
}

export async function associatedTokenAddress(
  owner: Address,
  mint: Address,
  tokenProgram: Address
) {
  const [ata] = await findAssociatedTokenPda({ owner, mint, tokenProgram })
  return ata
}

// Whole tokens ("150.5") to base units with string math; refuses extra precision.
export function toBaseUnits(amount: string, decimals: number): bigint {
  const m = /^(\d+)(?:\.(\d+))?$/.exec(amount.trim())
  if (!m) throw new Error(`invalid amount: ${amount}`)
  const frac = m[2] ?? ""
  if (frac.length > decimals)
    throw new Error(`${amount} has more than ${decimals} decimals`)
  return BigInt(m[1] + frac.padEnd(decimals, "0"))
}

function u64LE(data: Uint8Array, offset: number): bigint {
  return new DataView(data.buffer, data.byteOffset).getBigUint64(offset, true)
}

export type OnChainCampaign = {
  address: Address
  sponsor: Address
  mint: Address
  vault: Address
  maxRewardPerPr: bigint
  status: OnChainStatus
  totalPaid: bigint
  payoutCount: bigint
}

// disc 8 | campaign_id 16 | sponsor 32 | mint 32 | vault 32 | max_reward_per_pr 8 |
// policy_hash 32 | status 1 | total_paid 8 | payout_count 8 | bump 1 | vault_bump 1
export function decodeCampaign(
  addr: Address,
  data: Uint8Array
): OnChainCampaign {
  if (
    data.length < 179 ||
    !DISCRIMINATOR.campaignAccount.every((b, i) => data[i] === b)
  ) {
    throw new Error("account is not a contrib_oracle campaign")
  }
  const addrAt = (o: number) =>
    getAddressDecoder().decode(data.slice(o, o + 32))
  return {
    address: addr,
    sponsor: addrAt(24),
    mint: addrAt(56),
    vault: addrAt(88),
    maxRewardPerPr: u64LE(data, 120),
    status: CAMPAIGN_STATUS[data[160]] ?? "closed",
    totalPaid: u64LE(data, 161),
    payoutCount: u64LE(data, 169),
  }
}

async function fetchAccount(addr: Address) {
  const { value } = await rpc
    .getAccountInfo(addr, { encoding: "base64", commitment: "confirmed" })
    .send()
  if (!value) return null
  return {
    owner: value.owner,
    lamports: value.lamports,
    data: Uint8Array.from(atob(value.data[0]), (c) => c.charCodeAt(0)),
  }
}

export async function fetchCampaign(uuid: string) {
  const addr = await campaignPda(uuid)
  const acc = await fetchAccount(addr)
  if (!acc) return null
  if (acc.owner !== PROGRAM_ID)
    throw new Error("campaign PDA has an unexpected owner")
  return decodeCampaign(addr, acc.data)
}

export type MintInfo = {
  address: Address
  decimals: number
  tokenProgram: Address
}

export async function fetchMint(mint: Address): Promise<MintInfo> {
  const acc = await fetchAccount(mint)
  if (!acc) throw new Error(`mint ${mint} does not exist on ${SOLANA_CLUSTER}`)
  if (
    acc.owner !== TOKEN_PROGRAM_ADDRESS &&
    acc.owner !== TOKEN_2022_PROGRAM_ADDRESS
  ) {
    throw new Error(`${mint} is not a token mint`)
  }
  // SPL mint layout: mint_authority 36 | supply 8 | decimals 1.
  return { address: mint, decimals: acc.data[44], tokenProgram: acc.owner }
}

// Token account balance in base units; 0 when the account does not exist.
export async function tokenBalance(account: Address): Promise<bigint> {
  const acc = await fetchAccount(account)
  return acc && acc.data.length >= 72 ? u64LE(acc.data, 64) : BigInt(0)
}

export async function solBalance(owner: Address): Promise<bigint> {
  const { value } = await rpc
    .getBalance(owner, { commitment: "confirmed" })
    .send()
  return value
}

function ix(data: Uint8Array, accounts: [Address, AccountRole][]): Instruction {
  return {
    programAddress: PROGRAM_ID,
    accounts: accounts.map(([addr, role]) => ({ address: addr, role })),
    data,
  }
}

const bytes = (n: number) => fixEncoderSize(getBytesEncoder(), n)

export async function createCampaignInstruction(args: {
  sponsor: Address
  uuid: string
  mint: MintInfo
  maxRewardPerPr: bigint
}): Promise<Instruction> {
  const campaign = await campaignPda(args.uuid)
  const data = getStructEncoder([
    ["discriminator", bytes(8)],
    ["campaignId", bytes(16)],
    ["maxRewardPerPr", getU64Encoder()],
    ["policyHash", bytes(32)],
  ]).encode({
    discriminator: Uint8Array.from(DISCRIMINATOR.createCampaign),
    campaignId: uuidBytes(args.uuid),
    maxRewardPerPr: args.maxRewardPerPr,
    policyHash: new Uint8Array(32), // Not enforced: the runner's policy follows the campaign settings.
  })
  return ix(new Uint8Array(data), [
    [args.sponsor, AccountRole.WRITABLE_SIGNER],
    [campaign, AccountRole.WRITABLE],
    [args.mint.address, AccountRole.READONLY],
    [await vaultPda(campaign), AccountRole.WRITABLE],
    [args.mint.tokenProgram, AccountRole.READONLY],
    [SYSTEM_PROGRAM_ADDRESS, AccountRole.READONLY],
  ])
}

export async function fundCampaignInstructions(args: {
  funder: Address
  uuid: string
  mint: MintInfo
  amount: bigint
}): Promise<Instruction[]> {
  const campaign = await campaignPda(args.uuid)
  const funderToken = await associatedTokenAddress(
    args.funder,
    args.mint.address,
    args.mint.tokenProgram
  )
  const out: Instruction[] = []
  // Wrapped SOL: move SOL into the funder's wSOL account and sync it, then fund from it.
  if (args.mint.address === WRAPPED_SOL_MINT) {
    const funder = createNoopSigner(args.funder)
    out.push(
      getCreateAssociatedTokenIdempotentInstruction({
        payer: funder,
        ata: funderToken,
        owner: args.funder,
        mint: args.mint.address,
        tokenProgram: args.mint.tokenProgram,
      }),
      getTransferSolInstruction({
        source: funder,
        destination: funderToken,
        amount: lamports(args.amount),
      }),
      getSyncNativeInstruction({ account: funderToken })
    )
  }
  const data = getStructEncoder([
    ["discriminator", bytes(8)],
    ["amount", getU64Encoder()],
  ]).encode({
    discriminator: Uint8Array.from(DISCRIMINATOR.fundCampaign),
    amount: args.amount,
  })
  out.push(
    ix(new Uint8Array(data), [
      [args.funder, AccountRole.READONLY_SIGNER],
      [campaign, AccountRole.READONLY],
      [args.mint.address, AccountRole.READONLY],
      [await vaultPda(campaign), AccountRole.WRITABLE],
      [funderToken, AccountRole.WRITABLE],
      [args.mint.tokenProgram, AccountRole.READONLY],
    ])
  )
  return out
}

export async function setStatusInstruction(args: {
  sponsor: Address
  uuid: string
  status: "active" | "paused"
}): Promise<Instruction> {
  const data = getStructEncoder([
    ["discriminator", bytes(8)],
    ["status", getU8Encoder()],
  ]).encode({
    discriminator: Uint8Array.from(DISCRIMINATOR.setCampaignStatus),
    status: CAMPAIGN_STATUS.indexOf(args.status),
  })
  return ix(new Uint8Array(data), [
    [args.sponsor, AccountRole.READONLY_SIGNER],
    [await campaignPda(args.uuid), AccountRole.WRITABLE],
  ])
}

// Compiles an unsigned v0 transaction paid by `feePayer`; the wallet signs it.
export async function buildTransaction(
  feePayer: Address,
  instructions: Instruction[]
): Promise<Uint8Array> {
  const { value: blockhash } = await rpc
    .getLatestBlockhash({ commitment: "confirmed" })
    .send()
  const message = pipe(
    createTransactionMessage({ version: 0 }),
    (m) => setTransactionMessageFeePayerSigner(createNoopSigner(feePayer), m),
    (m) => setTransactionMessageLifetimeUsingBlockhash(blockhash, m),
    (m) => appendTransactionMessageInstructions(instructions, m)
  )
  return new Uint8Array(
    getTransactionEncoder().encode(compileTransaction(message))
  )
}

// Simulates without signatures so errors (missing funds, wrong mint...) surface before
// the wallet prompt. Returns the program logs on failure.
export async function simulate(tx: Uint8Array) {
  const encoded = btoa(String.fromCharCode(...tx))
  const { value } = await rpc
    .simulateTransaction(
      encoded as Parameters<typeof rpc.simulateTransaction>[0],
      {
        encoding: "base64",
        sigVerify: false,
        replaceRecentBlockhash: true,
        commitment: "confirmed",
      }
    )
    .send()
  return { err: value.err, logs: value.logs ?? [] }
}
