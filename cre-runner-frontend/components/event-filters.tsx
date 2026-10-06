"use client"

import * as React from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { RiFilterOffLine, RiHashtag } from "@remixicon/react"

import type { Facet, RepoFilters } from "@/lib/api"
import { eventColor, formatCount } from "@/lib/format"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

type Key = "event" | "action" | "sender" | "number"

export function EventFilters({ filters }: { filters: RepoFilters }) {
  const router = useRouter()
  const pathname = usePathname()
  const searchParams = useSearchParams()
  const [isPending, startTransition] = React.useTransition()

  const event = searchParams.get("event")
  const action = searchParams.get("action")
  const sender = searchParams.get("sender")
  const number = searchParams.get("number") ?? ""

  function update(changes: Partial<Record<Key, string | null>>) {
    const params = new URLSearchParams(searchParams)
    for (const [key, value] of Object.entries(changes)) {
      if (value) params.set(key, value)
      else params.delete(key)
    }
    params.delete("page")
    params.delete("delivery")
    const query = params.toString()
    startTransition(() => {
      router.push(query ? `${pathname}?${query}` : pathname, { scroll: false })
    })
  }

  const actionFacets: Facet[] = event
    ? (filters.events.find((e) => e.value === event)?.actions ?? [])
    : filters.actions

  function onEventChange(value: string | null) {
    const nextActions = value
      ? (filters.events.find((e) => e.value === value)?.actions ?? [])
      : filters.actions
    const keepAction = action && nextActions.some((a) => a.value === action)
    update({ event: value, action: keepAction ? action : null })
  }

  const isFiltered = Boolean(event || action || sender || number)

  return (
    <div
      className="flex flex-wrap items-center gap-2 data-pending:opacity-70"
      data-pending={isPending || undefined}
    >
      <FacetSelect
        label="Event type"
        allLabel="All events"
        value={event}
        facets={filters.events}
        onChange={onEventChange}
        swatch
      />
      <FacetSelect
        label="Action"
        allLabel="All actions"
        value={action}
        facets={actionFacets}
        onChange={(value) => update({ action: value })}
        disabled={actionFacets.length === 0 && !action}
      />
      <Select
        value={sender}
        onValueChange={(value) => update({ sender: value as string | null })}
        items={[
          { value: null, label: "Everyone" },
          ...filters.senders.map((s) => ({ value: s.login, label: s.login })),
        ]}
      >
        <SelectTrigger aria-label="Sender" className="min-w-36">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={null}>Everyone</SelectItem>
          {filters.senders.map((s) => (
            <SelectItem key={s.login} value={s.login}>
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img src={s.avatar_url} alt="" className="size-4 shrink-0" />
              {s.login}
              <span className="ms-auto ps-3 text-muted-foreground tabular-nums">
                {formatCount(s.count)}
              </span>
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <NumberFilter
        key={number}
        initial={number}
        onCommit={(value) => update({ number: value })}
      />
      {isFiltered && (
        <Button
          variant="ghost"
          onClick={() =>
            update({ event: null, action: null, sender: null, number: null })
          }
        >
          <RiFilterOffLine data-icon="inline-start" />
          Clear filters
        </Button>
      )}
    </div>
  )
}

function FacetSelect({
  label,
  allLabel,
  value,
  facets,
  onChange,
  disabled,
  swatch,
}: {
  label: string
  allLabel: string
  value: string | null
  facets: Facet[]
  onChange: (value: string | null) => void
  disabled?: boolean
  swatch?: boolean
}) {
  // Keep a value from the URL selectable even if it has no facet entry.
  const options =
    value && !facets.some((f) => f.value === value)
      ? [{ value, count: 0 }, ...facets]
      : facets

  return (
    <Select
      value={value}
      onValueChange={(next) => onChange(next as string | null)}
      disabled={disabled}
      items={[
        { value: null, label: allLabel },
        ...options.map((f) => ({ value: f.value, label: f.value })),
      ]}
    >
      <SelectTrigger aria-label={label} className="min-w-36">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value={null}>{allLabel}</SelectItem>
        {options.map((f) => (
          <SelectItem key={f.value} value={f.value}>
            {swatch && (
              <span
                className="size-2 shrink-0"
                style={{ background: eventColor(f.value) }}
              />
            )}
            {f.value}
            <span className="ms-auto ps-3 text-muted-foreground tabular-nums">
              {formatCount(f.count)}
            </span>
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

function NumberFilter({
  initial,
  onCommit,
}: {
  initial: string
  onCommit: (value: string | null) => void
}) {
  const [value, setValue] = React.useState(initial)

  function commit() {
    const trimmed = value.replace(/^#/, "").trim()
    if (trimmed === initial) return
    onCommit(/^\d+$/.test(trimmed) ? trimmed : null)
  }

  return (
    <form
      className="relative"
      onSubmit={(e) => {
        e.preventDefault()
        commit()
      }}
    >
      <RiHashtag className="pointer-events-none absolute start-2 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
      <Input
        aria-label="PR or issue number"
        placeholder="PR or issue"
        inputMode="numeric"
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onBlur={commit}
        className="w-32 ps-7"
      />
    </form>
  )
}
