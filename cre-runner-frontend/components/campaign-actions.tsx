"use client"

import * as React from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { toast } from "sonner"
import {
  RiArrowDownSLine,
  RiDeleteBinLine,
  RiPencilLine,
} from "@remixicon/react"

import {
  ApiError,
  CAMPAIGN_STATUSES,
  deleteCampaign,
  updateCampaign,
  type Campaign,
  type CampaignStatus,
} from "@/lib/api"
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

const STATUS_ACTION: Record<CampaignStatus, string> = {
  draft: "Move back to draft",
  active: "Activate",
  paused: "Pause",
  ended: "End campaign",
}

export function CampaignActions({ campaign }: { campaign: Campaign }) {
  const router = useRouter()
  const [pending, setPending] = React.useState(false)

  async function run(action: () => Promise<void>, failure: string) {
    setPending(true)
    try {
      await action()
    } catch (error) {
      toast.error(error instanceof ApiError ? error.message : failure)
    } finally {
      setPending(false)
    }
  }

  function setStatus(status: CampaignStatus) {
    void run(async () => {
      await updateCampaign(campaign.id, { status })
      toast.success(`Campaign is now ${status}`)
      router.refresh()
    }, "Couldn't change the status.")
  }

  function remove() {
    if (
      !window.confirm(
        `Delete "${campaign.name}"? This can't be undone. Campaigns with CRE executions can only be ended.`
      )
    )
      return
    void run(async () => {
      await deleteCampaign(campaign.id)
      toast.success("Campaign deleted")
      router.push("/campaigns")
      router.refresh()
    }, "Couldn't delete the campaign.")
  }

  return (
    <>
      <Button
        variant="outline"
        nativeButton={false}
        render={<Link href={`/campaigns/${campaign.id}/edit`} />}
      >
        <RiPencilLine data-icon="inline-start" />
        Edit
      </Button>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={<Button variant="outline" disabled={pending} />}
        >
          {pending ? "Working…" : "Actions"}
          <RiArrowDownSLine data-icon="inline-end" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-48">
          <DropdownMenuGroup>
            <DropdownMenuLabel>Status</DropdownMenuLabel>
            {CAMPAIGN_STATUSES.filter((s) => s !== campaign.status).map((s) => (
              <DropdownMenuItem key={s} onClick={() => setStatus(s)}>
                {STATUS_ACTION[s]}
              </DropdownMenuItem>
            ))}
          </DropdownMenuGroup>
          <DropdownMenuSeparator />
          <DropdownMenuItem variant="destructive" onClick={remove}>
            <RiDeleteBinLine />
            Delete
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </>
  )
}
