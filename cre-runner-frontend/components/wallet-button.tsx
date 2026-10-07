"use client"

import * as React from "react"
import Link from "next/link"
import { usePrivy, type User } from "@privy-io/react-auth"
import { toast } from "sonner"
import {
  RiFileCopyLine,
  RiGithubFill,
  RiLogoutBoxLine,
  RiTrophyLine,
} from "@remixicon/react"

import { shortAddress } from "@/lib/solana"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Skeleton } from "@/components/ui/skeleton"
import { PRIVY_APP_ID } from "@/components/wallet-provider"

// The user's Privy Solana embedded wallet (not an external wallet they linked).
export function embeddedSolanaAddress(user: User | null) {
  const wallet = user?.linkedAccounts.find(
    (a) =>
      a.type === "wallet" &&
      a.chainType === "solana" &&
      (a.walletClientType === "privy" || a.walletClientType === "privy-v2")
  )
  return wallet && "address" in wallet ? wallet.address : null
}

export function WalletButton() {
  // Hooks need the provider, which only exists when Privy is configured.
  if (!PRIVY_APP_ID) return null
  return <PrivyWalletButton />
}

function PrivyWalletButton() {
  const { ready, authenticated, user, login, logout } = usePrivy()

  if (!ready) return <Skeleton className="h-8 w-28" />
  if (!authenticated) {
    return (
      <Button variant="outline" onClick={() => login()}>
        <RiGithubFill data-icon="inline-start" />
        Sign in
      </Button>
    )
  }

  const githubLogin = user?.github?.username ?? null
  const address = embeddedSolanaAddress(user)

  return (
    <DropdownMenu>
      <DropdownMenuTrigger render={<Button variant="outline" />}>
        <RiGithubFill data-icon="inline-start" />
        <span className="max-w-28 truncate">{githubLogin ?? "Account"}</span>
        {address && (
          <span className="hidden text-muted-foreground md:inline">
            {shortAddress(address)}
          </span>
        )}
        <span className="border border-ev-issue/40 bg-ev-issue/10 px-1 text-[10px] font-medium text-foreground">
          Devnet
        </span>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-56">
        <DropdownMenuGroup>
          <DropdownMenuLabel>
            {address ? "Solana devnet wallet" : "Creating your wallet…"}
          </DropdownMenuLabel>
          {address && (
            <DropdownMenuItem
              onClick={() => {
                void navigator.clipboard.writeText(address)
                toast.success("Wallet address copied")
              }}
            >
              <RiFileCopyLine />
              <span className="truncate">{shortAddress(address)}</span>
            </DropdownMenuItem>
          )}
          <DropdownMenuItem render={<Link href="/me" />}>
            <RiTrophyLine />
            My rewards
          </DropdownMenuItem>
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem onClick={() => void logout()}>
          <RiLogoutBoxLine />
          Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
