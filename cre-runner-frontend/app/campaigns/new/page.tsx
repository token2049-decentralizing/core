import type { Metadata } from "next"

import { attempt, listRepos } from "@/lib/api"
import { CampaignForm } from "@/components/campaign-form"
import { PageBody, PageHeader } from "@/components/page"
import { RequireWallet } from "@/components/require-wallet"

export const metadata: Metadata = { title: "New campaign" }

export default async function NewCampaignPage() {
  const repos = await attempt(listRepos())

  return (
    <PageBody className="max-w-3xl pb-0">
      <PageHeader
        title="New campaign"
        description="Set a budget and the rules for rewarding merged pull requests. You can attach repositories now or later."
      />
      <RequireWallet action="create a campaign">
        <CampaignForm
          knownRepos={
            repos.ok ? repos.data.map((r) => r.repository_full_name) : []
          }
        />
      </RequireWallet>
    </PageBody>
  )
}
