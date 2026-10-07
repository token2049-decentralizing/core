import type { Metadata } from "next"
import { notFound } from "next/navigation"

import { attempt, getCampaign, listRepos } from "@/lib/api"
import { ApiErrorState } from "@/components/api-error-state"
import { CampaignForm } from "@/components/campaign-form"
import { PageBody, PageHeader } from "@/components/page"
import { RequireWallet } from "@/components/require-wallet"

export async function generateMetadata(
  props: PageProps<"/campaigns/[id]/edit">
): Promise<Metadata> {
  const { id } = await props.params
  const result = await attempt(getCampaign(id))
  return { title: result.ok ? `Edit ${result.data.name}` : "Edit campaign" }
}

export default async function EditCampaignPage(
  props: PageProps<"/campaigns/[id]/edit">
) {
  const { id } = await props.params
  const [campaign, repos] = await Promise.all([
    attempt(getCampaign(id)),
    attempt(listRepos()),
  ])

  if (!campaign.ok) {
    if (campaign.error.status === 404) notFound()
    return (
      <PageBody>
        <PageHeader title="Edit campaign" />
        <ApiErrorState
          status={campaign.error.status}
          message={campaign.error.message}
        />
      </PageBody>
    )
  }

  return (
    <PageBody className="max-w-3xl pb-0">
      <PageHeader
        title={`Edit ${campaign.data.name}`}
        description="Changes apply to the next CRE evaluation. Past executions keep the policy they ran with."
      />
      <RequireWallet action="edit this campaign">
        <CampaignForm
          campaign={campaign.data}
          knownRepos={
            repos.ok ? repos.data.map((r) => r.repository_full_name) : []
          }
        />
      </RequireWallet>
    </PageBody>
  )
}
