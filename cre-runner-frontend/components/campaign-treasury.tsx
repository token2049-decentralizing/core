"use client"

import * as React from "react"
import { useRouter } from "next/navigation"
import { usePrivy } from "@privy-io/react-auth"
import {
  useSignAndSendTransaction,
  useWallets,
  type ConnectedStandardSolanaWallet,
} from "@privy-io/react-auth/solana"
import {
  address,
  getBase58Decoder,
  type Address,
  type Instruction,
} from "@solana/kit"
import { toast } from "sonner"
import { RiErrorWarningLine, RiExternalLinkLine } from "@remixicon/react"

import { ApiError, updateCampaign, type Campaign } from "@/lib/api"
import {
  assertDevnet,
  associatedTokenAddress,
  buildTransaction,
  campaignPda,
  createCampaignInstruction,
  REWARD_MINT,
  fetchCampaign,
  fetchMint,
  fundCampaignInstructions,
  PROGRAM_ID,
  setStatusInstruction,
  simulate,
  solBalance,
  SOLANA_CHAIN,
  toBaseUnits,
  tokenBalance,
  vaultPda,
  WRAPPED_SOL_MINT,
  type MintInfo,
  type OnChainCampaign,
} from "@/lib/contrib-oracle"
import { formatUnits, TOKEN_DECIMALS } from "@/lib/format"
import {
  explorerAddressUrl,
  explorerTxUrl,
  shortAddress,
  SOLANA_CLUSTER,
} from "@/lib/solana"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import { embeddedSolanaAddress } from "@/components/wallet-button"
import { PRIVY_APP_ID } from "@/components/wallet-provider"

// Signs and sends with the user's wallet; returns the transaction signature.
type Signer = {
  address: Address
  send(tx: Uint8Array, action: string): Promise<string>
}

type Loaded =
  | { status: "loading" }
  | { status: "missing" }
  | { status: "error"; message: string }
  | {
      status: "ready"
      onChain: OnChainCampaign
      mint: MintInfo
      vaultBalance: bigint
    }

// The campaign's Solana treasury (contrib_oracle): create it on-chain with the sponsor's
// wallet, fund the vault, pause or resume payouts.
export function CampaignTreasury({ campaign }: { campaign: Campaign }) {
  if (!PRIVY_APP_ID) return <Treasury campaign={campaign} signer={null} />
  return <WithWallet campaign={campaign} />
}

function WithWallet({ campaign }: { campaign: Campaign }) {
  const { ready, authenticated, user, login } = usePrivy()
  const { wallets } = useWallets()
  const { signAndSendTransaction } = useSignAndSendTransaction()
  const embedded = embeddedSolanaAddress(user)
  const wallet: ConnectedStandardSolanaWallet | undefined =
    wallets.find((w) => w.address === embedded) ?? wallets[0]

  const signer = React.useMemo<Signer | null>(
    () =>
      wallet
        ? {
            address: address(wallet.address),
            async send(transaction, action) {
              const { signature } = await signAndSendTransaction({
                transaction,
                wallet,
                chain: SOLANA_CHAIN,
                options: {
                  uiOptions: {
                    description: `${action} on Solana devnet`,
                    buttonText: "Sign on devnet",
                    transactionInfo: {
                      title: "Solana devnet",
                      action,
                      contractInfo: {
                        name: "contrib_oracle",
                        url: explorerAddressUrl(PROGRAM_ID),
                      },
                    },
                  },
                },
              })
              return getBase58Decoder().decode(signature)
            },
          }
        : null,
    [wallet, signAndSendTransaction]
  )

  return (
    <Treasury
      campaign={campaign}
      signer={signer}
      signIn={ready && !authenticated ? () => login() : undefined}
    />
  )
}

function Treasury({
  campaign,
  signer,
  signIn,
}: {
  campaign: Campaign
  signer: Signer | null
  signIn?: () => void
}) {
  const router = useRouter()
  const [loaded, setLoaded] = React.useState<Loaded>({ status: "loading" })
  const [reload, setReload] = React.useState(0)
  const [pending, setPending] = React.useState<string | null>(null)
  const [error, setError] = React.useState<{
    message: string
    logs?: string[]
  } | null>(null)
  const decimals = TOKEN_DECIMALS[campaign.reward_asset]

  React.useEffect(() => {
    let cancelled = false
    assertDevnet()
      .then(() => fetchCampaign(campaign.id))
      .then(async (onChain) => {
        if (!onChain) return { status: "missing" } as const
        const mint = await fetchMint(onChain.mint)
        const vaultBalance = await tokenBalance(onChain.vault)
        return { status: "ready", onChain, mint, vaultBalance } as const
      })
      .then((next) => !cancelled && setLoaded(next))
      .catch((e: unknown) => {
        if (!cancelled)
          setLoaded({
            status: "error",
            message: e instanceof Error ? e.message : String(e),
          })
      })
    return () => {
      cancelled = true
    }
  }, [campaign.id, reload])

  // Simulates first so failures show program logs instead of a wallet prompt, then signs.
  async function run(
    label: string,
    build: (s: Signer) => Promise<Instruction[]>,
    after?: () => Promise<void>
  ) {
    if (!signer) return
    setPending(label)
    setError(null)
    try {
      await assertDevnet()
      const tx = await buildTransaction(signer.address, await build(signer))
      const sim = await simulate(tx)
      if (sim.err) {
        setError({
          message: `Simulation failed: ${JSON.stringify(sim.err, bigintJSON)}`,
          logs: sim.logs.slice(-8),
        })
        return
      }
      const signature = await signer.send(tx, label)
      toast.success(`${label} confirmed`, {
        action: {
          label: "View",
          onClick: () => window.open(explorerTxUrl(signature), "_blank"),
        },
      })
      try {
        await after?.()
      } catch (e) {
        // The transaction already landed; only the bookkeeping failed.
        toast.error(
          `On Solana, but not recorded here: ${e instanceof Error ? e.message : String(e)}`
        )
      }
      setReload((n) => n + 1)
      router.refresh()
    } catch (e) {
      setError({
        message:
          e instanceof ApiError || e instanceof Error ? e.message : String(e),
      })
    } finally {
      setPending(null)
    }
  }

  // Funding needs the sponsor's tokens (wrapped SOL is wrapped from their SOL in the same tx).
  async function assertCanFund(s: Signer, mint: MintInfo, amount: bigint) {
    if (mint.address === WRAPPED_SOL_MINT) {
      const sol = await solBalance(s.address)
      if (sol < amount)
        throw new Error(
          `Your wallet has ${formatUnits(sol.toString(), 9)} SOL, funding needs ${formatUnits(amount.toString(), 9)}.`
        )
      return
    }
    const held = await tokenBalance(
      await associatedTokenAddress(s.address, mint.address, mint.tokenProgram)
    )
    if (held < amount)
      throw new Error(
        `Your wallet holds ${formatUnits(held.toString(), mint.decimals)} of this token, funding needs ${formatUnits(amount.toString(), mint.decimals)}.`
      )
  }

  if (loaded.status === "loading") return <Skeleton className="h-32 w-full" />
  if (loaded.status === "error")
    return (
      <p className="text-xs break-words text-destructive">
        Couldn&apos;t read Solana: {loaded.message}
      </p>
    )

  const walletLine = signer ? (
    <WalletBalance address={signer.address} />
  ) : signIn ? (
    <Button variant="outline" size="sm" onClick={signIn}>
      Sign in to manage
    </Button>
  ) : PRIVY_APP_ID ? (
    <p className="text-xs text-muted-foreground">Creating your wallet…</p>
  ) : null

  const errorBox = error && (
    <Alert variant="destructive">
      <RiErrorWarningLine />
      <AlertTitle>Transaction not sent</AlertTitle>
      <AlertDescription className="break-words">
        {error.message}
        {error.logs && error.logs.length > 0 && (
          <pre className="mt-2 max-h-40 overflow-auto text-[10px] leading-relaxed whitespace-pre-wrap">
            {error.logs.join("\n")}
          </pre>
        )}
      </AlertDescription>
    </Alert>
  )

  if (loaded.status === "missing") {
    return (
      <div className="flex flex-col gap-3 text-xs">
        <p className="leading-relaxed text-muted-foreground">
          Not on Solana {SOLANA_CLUSTER} yet. Create it to hold the budget in a
          vault that pays merged PRs, at most {campaign.max_reward_per_pr}{" "}
          {campaign.reward_asset} each. Your wallet becomes the sponsor.
        </p>
        {errorBox}
        {walletLine}
        {signer && (
          <CreateForm
            campaign={campaign}
            pending={pending !== null}
            onSubmit={(fundStr) =>
              run(
                "Campaign creation",
                async (s) => {
                  const expected = REWARD_MINT[campaign.reward_asset]
                  if (!expected)
                    throw new Error(
                      `${campaign.reward_asset} campaigns can't be funded on Solana.`
                    )
                  const mint = await fetchMint(expected)
                  if (mint.decimals !== decimals)
                    throw new Error(
                      `This mint has ${mint.decimals} decimals; ${campaign.reward_asset} rewards are computed with ${decimals}.`
                    )
                  const amount = fundStr
                    ? toBaseUnits(fundStr, decimals)
                    : BigInt(0)
                  if (amount > BigInt(0)) await assertCanFund(s, mint, amount)
                  const ixs = [
                    await createCampaignInstruction({
                      sponsor: s.address,
                      uuid: campaign.id,
                      mint,
                      maxRewardPerPr: toBaseUnits(
                        String(campaign.max_reward_per_pr),
                        decimals
                      ),
                    }),
                  ]
                  if (amount > BigInt(0))
                    ixs.push(
                      ...(await fundCampaignInstructions({
                        funder: s.address,
                        uuid: campaign.id,
                        mint,
                        amount,
                      }))
                    )
                  return ixs
                },
                async () => {
                  const vault = await vaultPda(await campaignPda(campaign.id))
                  await updateCampaign(campaign.id, { treasury_address: vault })
                }
              )
            }
          />
        )}
      </div>
    )
  }

  const { onChain, mint, vaultBalance } = loaded
  const amount = (units: bigint) =>
    `${formatUnits(units.toString(), mint.decimals)} ${campaign.reward_asset}`
  const isSponsor = signer?.address === onChain.sponsor
  const wrongMint = onChain.mint !== REWARD_MINT[campaign.reward_asset]
  const capMismatch =
    onChain.maxRewardPerPr !==
    toBaseUnits(String(campaign.max_reward_per_pr), decimals)

  return (
    <div className="flex flex-col gap-3 text-xs">
      <dl className="grid grid-cols-[6.5rem_1fr] gap-x-3 gap-y-1.5">
        <dt className="text-muted-foreground">Status</dt>
        <dd
          className={
            onChain.status === "active" ? "text-ev-push" : "text-ev-issue"
          }
        >
          {onChain.status === "active"
            ? "Paying out"
            : onChain.status === "paused"
              ? "Paused"
              : "Closed"}
        </dd>
        <dt className="text-muted-foreground">Vault balance</dt>
        <dd className="font-medium tabular-nums">{amount(vaultBalance)}</dd>
        <dt className="text-muted-foreground">Paid out</dt>
        <dd className="tabular-nums">
          {amount(onChain.totalPaid)} in {onChain.payoutCount.toString()} PRs
        </dd>
        <dt className="text-muted-foreground">Max per PR</dt>
        <dd className="tabular-nums">{amount(onChain.maxRewardPerPr)}</dd>
        <dt className="text-muted-foreground">Vault</dt>
        <dd>
          <ExplorerLink addr={onChain.vault} />
        </dd>
        <dt className="text-muted-foreground">Token</dt>
        <dd>
          <ExplorerLink addr={onChain.mint} />
        </dd>
        <dt className="text-muted-foreground">Sponsor</dt>
        <dd>
          <ExplorerLink addr={onChain.sponsor} />{" "}
          {isSponsor && <span className="text-muted-foreground">(you)</span>}
        </dd>
      </dl>
      {wrongMint && (
        <p className="text-destructive">
          This on-chain campaign holds a token that isn&apos;t devnet{" "}
          {campaign.reward_asset}; Kudoz won&apos;t pay rewards from it.
        </p>
      )}
      {capMismatch && (
        <p className="text-ev-issue">
          The on-chain cap differs from this campaign&apos;s max reward per PR;
          rewards above {amount(onChain.maxRewardPerPr)} will fail.
        </p>
      )}
      {errorBox}
      {walletLine}
      {isSponsor && onChain.status !== "closed" && (
        <>
          <FundForm
            unit={campaign.reward_asset}
            pending={pending !== null}
            onSubmit={(value) =>
              run("Funding", async (s) => {
                const units = toBaseUnits(value, mint.decimals)
                await assertCanFund(s, mint, units)
                return fundCampaignInstructions({
                  funder: s.address,
                  uuid: campaign.id,
                  mint,
                  amount: units,
                })
              })
            }
          />
          <Button
            variant="outline"
            size="sm"
            className="self-start"
            disabled={pending !== null}
            onClick={() =>
              run(
                onChain.status === "active" ? "Pause" : "Resume",
                async (s) => [
                  await setStatusInstruction({
                    sponsor: s.address,
                    uuid: campaign.id,
                    status: onChain.status === "active" ? "paused" : "active",
                  }),
                ]
              )
            }
          >
            {pending === "Pause" || pending === "Resume"
              ? "Signing…"
              : onChain.status === "active"
                ? "Pause payouts"
                : "Resume payouts"}
          </Button>
        </>
      )}
      {signer && !isSponsor && (
        <p className="text-muted-foreground">
          Only the sponsor wallet can fund or pause this campaign; anyone can
          send tokens to the vault.
        </p>
      )}
    </div>
  )
}

function CreateForm({
  campaign,
  pending,
  onSubmit,
}: {
  campaign: Campaign
  pending: boolean
  onSubmit: (fund: string) => void
}) {
  const mint = REWARD_MINT[campaign.reward_asset]
  const [fund, setFund] = React.useState(String(campaign.budget))
  return (
    <form
      className="flex flex-col gap-2"
      onSubmit={(e) => {
        e.preventDefault()
        onSubmit(fund.trim())
      }}
    >
      <p className="text-[11px] leading-relaxed text-muted-foreground">
        Reward token:{" "}
        {mint ? <ExplorerLink addr={mint} /> : "unsupported asset"}{" "}
        {campaign.reward_asset === "SOL"
          ? "(wrapped SOL; your SOL is wrapped when funding)"
          : "(Circle devnet USDC; get some at faucet.circle.com)"}
      </p>
      <Label htmlFor="treasury-fund">
        Deposit now ({campaign.reward_asset})
      </Label>
      <Input
        id="treasury-fund"
        inputMode="decimal"
        value={fund}
        onChange={(e) => setFund(e.target.value)}
        className="tabular-nums"
      />
      <Button type="submit" disabled={pending || !mint} className="self-start">
        {pending ? "Signing…" : "Create on Solana"}
      </Button>
    </form>
  )
}

function FundForm({
  unit,
  pending,
  onSubmit,
}: {
  unit: string
  pending: boolean
  onSubmit: (amount: string) => void
}) {
  const [value, setValue] = React.useState("")
  return (
    <form
      className="flex gap-2"
      onSubmit={(e) => {
        e.preventDefault()
        if (value.trim()) onSubmit(value.trim())
      }}
    >
      <Label htmlFor="treasury-topup" className="sr-only">
        Deposit amount
      </Label>
      <Input
        id="treasury-topup"
        inputMode="decimal"
        placeholder={`Add ${unit}`}
        value={value}
        onChange={(e) => setValue(e.target.value)}
        className="tabular-nums"
      />
      <Button
        type="submit"
        variant="outline"
        disabled={pending || !value.trim()}
      >
        {pending ? "Signing…" : "Deposit"}
      </Button>
    </form>
  )
}

function WalletBalance({ address: addr }: { address: Address }) {
  const [sol, setSol] = React.useState<bigint | null>(null)
  React.useEffect(() => {
    let cancelled = false
    solBalance(addr)
      .then((v) => !cancelled && setSol(v))
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [addr])
  const low = sol !== null && sol < BigInt(10_000_000) // 0.01 SOL: rent for the campaign + vault, and fees.
  return (
    <p className="text-muted-foreground">
      Signing with <ExplorerLink addr={addr} />
      {sol !== null && ` · ${formatUnits(sol.toString(), 9)} SOL`}
      {low && (
        <span className="text-ev-issue">
          {" "}
          · needs ~0.01 SOL for rent and fees
        </span>
      )}
    </p>
  )
}

function ExplorerLink({ addr }: { addr: string }) {
  return (
    <a
      href={explorerAddressUrl(addr)}
      target="_blank"
      rel="noreferrer"
      className="inline-flex items-center gap-1 underline-offset-4 hover:underline"
    >
      {shortAddress(addr)}
      <RiExternalLinkLine className="size-3" />
    </a>
  )
}

function bigintJSON(_: string, v: unknown) {
  return typeof v === "bigint" ? v.toString() : v
}
