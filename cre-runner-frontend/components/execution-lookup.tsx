"use client"

import * as React from "react"
import { useRouter } from "next/navigation"
import { RiArrowRightLine } from "@remixicon/react"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

// Opens the CRE executions of one pull request in one of the campaign's repos.
export function ExecutionLookup({
  campaignId,
  repos,
}: {
  campaignId: string
  repos: string[]
}) {
  const router = useRouter()
  const [repo, setRepo] = React.useState<string | null>(repos[0] ?? null)
  const [number, setNumber] = React.useState("")
  const pr = Number.parseInt(number, 10)
  const valid = repo !== null && Number.isInteger(pr) && pr > 0

  function submit(event: React.FormEvent) {
    event.preventDefault()
    if (!valid) return
    router.push(`/campaigns/${campaignId}/repos/${repo}/prs/${pr}`)
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-2 text-xs">
      <Label htmlFor="lookup-repo" className="sr-only">
        Repository
      </Label>
      <Select
        value={repo}
        onValueChange={(value) => setRepo(value as string | null)}
        items={repos.map((r) => ({ value: r, label: r }))}
      >
        <SelectTrigger
          id="lookup-repo"
          aria-label="Repository"
          className="w-full"
        >
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {repos.map((r) => (
            <SelectItem key={r} value={r}>
              {r}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <div className="flex gap-2">
        <Label htmlFor="lookup-pr" className="sr-only">
          Pull request number
        </Label>
        <Input
          id="lookup-pr"
          inputMode="numeric"
          placeholder="PR number, e.g. 42"
          value={number}
          onChange={(e) => setNumber(e.target.value.replace(/\D/g, ""))}
        />
        <Button type="submit" variant="outline" disabled={!valid}>
          View
          <RiArrowRightLine data-icon="inline-end" />
        </Button>
      </div>
    </form>
  )
}
