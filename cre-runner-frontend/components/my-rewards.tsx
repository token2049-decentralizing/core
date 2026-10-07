"use client"

import * as React from "react"
import { usePrivy } from "@privy-io/react-auth"
import { toast } from "sonner"
import {
  RiErrorWarningLine,
  RiExternalLinkLine,
  RiFileCopyLine,
  RiGithubFill,
  RiWallet3Line,
} from "@remixicon/react"

import {
  ApiError,
  getGitHubWallet,
  listExecutions,
  type ContributorWallet,
  type Execution,
} from "@/lib/api"
import { formatDate, formatUnits, TOKEN_DECIMALS } from "@/lib/format"
import { explorerAddressUrl } from "@/lib/solana"
import { ExecutionTable } from "@/components/execution-table"
import { Section } from "@/components/page"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import { embeddedSolanaAddress } from "@/components/wallet-button"
import { PRIVY_APP_ID } from "@/components/wallet-provider"

export function MyRewards() {
  if (!PRIVY_APP_ID) {
    return (
      <Empty className="border py-14">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <RiWallet3Line />
          </EmptyMedia>
          <EmptyTitle>Wallet sign-in isn&apos;t set up</EmptyTitle>
          <EmptyDescription>
            Set NEXT_PUBLIC_PRIVY_APP_ID to enable signing in with GitHub.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }
  return <SignedInRewards />
}

type Loaded = {
  login: string
  wallet: ContributorWallet | null
  executions: Execution[]
  total: number
}

function SignedInRewards() {
  const { ready, authenticated, user, login } = usePrivy()
  const githubLogin = user?.github?.username ?? null
  const address = embeddedSolanaAddress(user)
  const [loaded, setLoaded] = React.useState<Loaded | null>(null)
  const [error, setError] = React.useState<string | null>(null)

  React.useEffect(() => {
    if (!githubLogin) return
    let cancelled = false
    Promise.all([
      getGitHubWallet(githubLogin).catch((e: unknown) => {
        if (e instanceof ApiError && e.status === 404) return null
        throw e
      }),
      listExecutions({ author: githubLogin, page_size: 100 }),
    ])
      .then(([wallet, executions]) => {
        if (cancelled) return
        setLoaded({
          login: githubLogin,
          wallet,
          executions: executions.data,
          total: executions.pagination.total,
        })
      })
      .catch((e: unknown) => {
        if (!cancelled)
          setError(e instanceof Error ? e.message : "Couldn't load rewards.")
      })
    return () => {
      cancelled = true
    }
  }, [githubLogin])

  if (!ready) return <Skeleton className="h-40 w-full" />

  if (!authenticated) {
    return (
      <Empty className="border py-14">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <RiGithubFill />
          </EmptyMedia>
          <EmptyTitle>Sign in to see your rewards</EmptyTitle>
          <EmptyDescription>
            If a pull request of yours was already rewarded, your wallet is
            waiting for you. Signing in with the same GitHub account opens it.
          </EmptyDescription>
        </EmptyHeader>
        <EmptyContent>
          <Button onClick={() => login()}>
            <RiGithubFill data-icon="inline-start" />
            Sign in with GitHub
          </Button>
        </EmptyContent>
      </Empty>
    )
  }

  if (!githubLogin) {
    return (
      <Alert variant="destructive">
        <RiErrorWarningLine />
        <AlertTitle>No GitHub account</AlertTitle>
        <AlertDescription>
          Rewards are tied to GitHub accounts. Sign out and sign in with GitHub.
        </AlertDescription>
      </Alert>
    )
  }

  const data = loaded?.login === githubLogin ? loaded : null
  const mismatch =
    data?.wallet && address && data.wallet.solana_address !== address

  return (
    <div className="flex flex-col gap-10">
      {mismatch && (
        <Alert variant="destructive">
          <RiErrorWarningLine />
          <AlertTitle>Your rewards went to a different wallet</AlertTitle>
          <AlertDescription>
            cre-runner pays @{githubLogin} at {data.wallet!.solana_address}, but
            your account&apos;s wallet is {address}. This happens if you signed
            in with another GitHub account than the one that opened the pull
            requests. Ask the maintainers to check the wallet mapping.
          </AlertDescription>
        </Alert>
      )}

      <dl className="grid grid-cols-1 border-y md:grid-cols-3 md:divide-x">
        <Fact label="GitHub account">
          <a
            href={`https://github.com/${githubLogin}`}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1.5 underline-offset-4 hover:underline"
          >
            <RiGithubFill className="size-4" />@{githubLogin}
          </a>
        </Fact>
        <Fact label="Solana wallet" className="md:col-span-2">
          {address ? (
            <span className="flex flex-wrap items-center gap-2">
              <span className="text-sm break-all">{address}</span>
              <Button
                variant="ghost"
                size="icon-xs"
                aria-label="Copy wallet address"
                onClick={() => {
                  void navigator.clipboard.writeText(address)
                  toast.success("Wallet address copied")
                }}
              >
                <RiFileCopyLine />
              </Button>
              <Button
                variant="ghost"
                size="icon-xs"
                aria-label="Open in Solana Explorer"
                nativeButton={false}
                render={
                  <a
                    href={explorerAddressUrl(address)}
                    target="_blank"
                    rel="noreferrer"
                  />
                }
              >
                <RiExternalLinkLine />
              </Button>
            </span>
          ) : (
            <span className="text-sm text-muted-foreground">
              Creating your wallet…
            </span>
          )}
          {data?.wallet?.pregenerated_at && !mismatch && (
            <span className="text-[11px] text-muted-foreground">
              Created for you on {formatDate(data.wallet.pregenerated_at)},
              before your first sign-in.
            </span>
          )}
        </Fact>
      </dl>

      <Section
        title={
          data ? `Your CRE executions (${data.total})` : "Your CRE executions"
        }
      >
        {error ? (
          <p className="text-xs text-destructive">{error}</p>
        ) : !data ? (
          <Skeleton className="h-32 w-full" />
        ) : (
          <>
            <Earned executions={data.executions} />
            <ExecutionTable
              executions={data.executions}
              now={new Date().toISOString()}
              empty="No executions for your pull requests yet"
              emptyHint="Open a pull request in a repository of an active campaign. It is scored on every push and paid when merged."
            />
          </>
        )}
      </Section>
    </div>
  )
}

// Settled rewards summed per asset with BigInt: base units can exceed Number's range.
function Earned({ executions }: { executions: Execution[] }) {
  const totals = new Map<string, bigint>()
  for (const e of executions) {
    if (!e.settled || !e.reward || !e.campaign) continue
    const asset = e.campaign.reward_asset
    totals.set(asset, (totals.get(asset) ?? BigInt(0)) + BigInt(e.reward))
  }
  if (totals.size === 0) return null
  return (
    <p className="text-sm">
      Earned{" "}
      {[...totals]
        .map(
          ([asset, units]) =>
            `${formatUnits(units.toString(), TOKEN_DECIMALS[asset] ?? 0)} ${asset}`
        )
        .join(" + ")}
    </p>
  )
}

function Fact({
  label,
  className,
  children,
}: {
  label: string
  className?: string
  children: React.ReactNode
}) {
  return (
    <div
      className={`flex flex-col gap-1 py-4 pe-4 md:ps-5 md:first:ps-0 ${className ?? ""}`}
    >
      <dt className="text-[11px] text-muted-foreground">{label}</dt>
      <dd className="flex flex-col gap-1">{children}</dd>
    </div>
  )
}
