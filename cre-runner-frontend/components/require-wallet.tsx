"use client"

import { usePrivy } from "@privy-io/react-auth"
import { RiGithubFill, RiLock2Line } from "@remixicon/react"

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
import { PRIVY_APP_ID } from "@/components/wallet-provider"

export type WalletSession = {
  ready: boolean
  signedIn: boolean
  signIn?: () => void
}

function usePrivySession(): WalletSession {
  const { ready, authenticated, login } = usePrivy()
  return { ready, signedIn: authenticated, signIn: () => login() }
}

// Without Privy configured nobody can sign in, so management stays locked.
function useNoSession(): WalletSession {
  return { ready: true, signedIn: false }
}

// Picked once per build: the provider exists exactly when PRIVY_APP_ID is set.
export const useWalletSession = PRIVY_APP_ID ? usePrivySession : useNoSession

// Shows children only to users signed in with their wallet (GitHub via Privy).
// Creating, editing and deleting campaigns is limited to them.
export function RequireWallet({
  action,
  children,
}: {
  action: string
  children: React.ReactNode
}) {
  const { ready, signedIn, signIn } = useWalletSession()
  if (!ready) return <Skeleton className="h-64 w-full" />
  if (signedIn) return children

  return (
    <Empty className="border py-14">
      <EmptyHeader>
        <EmptyMedia variant="icon">
          <RiLock2Line />
        </EmptyMedia>
        <EmptyTitle>Sign in to {action}</EmptyTitle>
        <EmptyDescription>
          {signIn
            ? "Campaigns are managed by signed-in sponsors. Sign in with GitHub to open your Solana devnet wallet."
            : "Wallet sign-in isn't set up (NEXT_PUBLIC_PRIVY_APP_ID), so campaigns can't be managed here."}
        </EmptyDescription>
      </EmptyHeader>
      {signIn && (
        <EmptyContent>
          <Button onClick={signIn}>
            <RiGithubFill data-icon="inline-start" />
            Sign in with GitHub
          </Button>
        </EmptyContent>
      )}
    </Empty>
  )
}
