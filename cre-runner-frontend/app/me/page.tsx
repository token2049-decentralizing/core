import type { Metadata } from "next"

import { MyRewards } from "@/components/my-rewards"
import { PageBody, PageHeader } from "@/components/page"

export const metadata: Metadata = { title: "My rewards" }

export default function MyRewardsPage() {
  return (
    <PageBody>
      <PageHeader
        title="My rewards"
        description="Rewards for your merged pull requests are sent to a Solana wallet tied to your GitHub account, even before you first sign in. Sign in with GitHub to see and use it."
      />
      <MyRewards />
    </PageBody>
  )
}
