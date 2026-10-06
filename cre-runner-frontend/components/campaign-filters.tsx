"use client"

import * as React from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"

import { CAMPAIGN_STATUSES, type CampaignStatus } from "@/lib/api"
import { humanize } from "@/lib/format"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"

export function CampaignFilters({ repos }: { repos: string[] }) {
  const router = useRouter()
  const pathname = usePathname()
  const searchParams = useSearchParams()
  const [isPending, startTransition] = React.useTransition()

  const status = (searchParams.get("status") as CampaignStatus | null) ?? "all"
  const repo = searchParams.get("repo")
  const repoOptions = repo && !repos.includes(repo) ? [repo, ...repos] : repos

  function update(key: "status" | "repo", value: string | null) {
    const params = new URLSearchParams(searchParams)
    if (value && value !== "all") params.set(key, value)
    else params.delete(key)
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
          {CAMPAIGN_STATUSES.map((s) => (
            <TabsTrigger key={s} value={s}>
              {humanize(s)}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>
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
  )
}
