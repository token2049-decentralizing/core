"use client"

import * as React from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { RiCloseLine } from "@remixicon/react"

import { EXECUTION_STATUSES, type ExecutionStatus } from "@/lib/api"
import { humanize } from "@/lib/format"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"

type Key = "status" | "campaign_id" | "repo" | "pr"

export function ExecutionFilters({
  campaigns,
  repos,
}: {
  campaigns: { id: string; name: string }[]
  repos: string[]
}) {
  const router = useRouter()
  const pathname = usePathname()
  const searchParams = useSearchParams()
  const [isPending, startTransition] = React.useTransition()

  const status = (searchParams.get("status") as ExecutionStatus | null) ?? "all"
  const campaignId = searchParams.get("campaign_id")
  const repo = searchParams.get("repo")
  const pr = searchParams.get("pr")
  const repoOptions = repo && !repos.includes(repo) ? [repo, ...repos] : repos

  function update(key: Key, value: string | null) {
    const params = new URLSearchParams(searchParams)
    if (value && value !== "all") params.set(key, value)
    else params.delete(key)
    if (key === "repo") params.delete("pr") // A PR number only means something within its repo.
    params.delete("page")
    const query = params.toString()
    startTransition(() => {
      router.push(query ? `${pathname}?${query}` : pathname, { scroll: false })
    })
  }

  return (
    <div
      className="flex flex-wrap items-center justify-between gap-3 data-pending:opacity-70"
      data-pending={isPending || undefined}
    >
      <Tabs
        value={status}
        onValueChange={(value) => update("status", value as string)}
      >
        <TabsList>
          <TabsTrigger value="all">All</TabsTrigger>
          {EXECUTION_STATUSES.map((s) => (
            <TabsTrigger key={s} value={s}>
              {humanize(s)}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>
      <div className="flex flex-wrap items-center gap-2">
        {pr && (
          <Button
            variant="outline"
            size="sm"
            onClick={() => update("pr", null)}
            aria-label={`Clear PR #${pr} filter`}
          >
            PR #{pr}
            <RiCloseLine data-icon="inline-end" />
          </Button>
        )}
        <Select
          value={campaignId}
          onValueChange={(value) =>
            update("campaign_id", value as string | null)
          }
          items={[
            { value: null, label: "All campaigns" },
            ...campaigns.map((c) => ({ value: c.id, label: c.name })),
          ]}
        >
          <SelectTrigger aria-label="Campaign" className="min-w-44">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={null}>All campaigns</SelectItem>
            {campaigns.map((c) => (
              <SelectItem key={c.id} value={c.id}>
                {c.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select
          value={repo}
          onValueChange={(value) => update("repo", value as string | null)}
          items={[
            { value: null, label: "All repositories" },
            ...repoOptions.map((r) => ({ value: r, label: r })),
          ]}
        >
          <SelectTrigger aria-label="Repository" className="min-w-48">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={null}>All repositories</SelectItem>
            {repoOptions.map((r) => (
              <SelectItem key={r} value={r}>
                {r}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
    </div>
  )
}
