"use client"

import Link from "next/link"
import { usePathname, useSearchParams } from "next/navigation"
import {
  RiChat3Line,
  RiGitBranchLine,
  RiGitMergeLine,
  RiGitPullRequestLine,
  RiRecordCircleLine,
  RiSearchLine,
} from "@remixicon/react"

import type { RepoEvent, Subject } from "@/lib/api"
import { eventColor, formatDateTime, shortRef, timeAgo } from "@/lib/format"
import { cn } from "@/lib/utils"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

export function EventTable({
  events,
  isFiltered,
}: {
  events: RepoEvent[]
  isFiltered: boolean
}) {
  const pathname = usePathname()
  const searchParams = useSearchParams()
  const selected = searchParams.get("delivery")

  function hrefFor(deliveryId: string) {
    const params = new URLSearchParams(searchParams)
    params.set("delivery", deliveryId)
    return `${pathname}?${params.toString()}`
  }

  if (events.length === 0) {
    return (
      <Empty className="border py-14">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <RiSearchLine />
          </EmptyMedia>
          <EmptyTitle>
            {isFiltered
              ? "No events match these filters"
              : "No events on this page"}
          </EmptyTitle>
          <EmptyDescription>
            {isFiltered
              ? "Try removing a filter or checking a different PR number."
              : "Go back to the first page to see the latest events."}
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  return (
    <div className="border">
      <Table>
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead className="ps-4">Event</TableHead>
            <TableHead>Subject</TableHead>
            <TableHead className="hidden md:table-cell">Sender</TableHead>
            <TableHead className="pe-4 text-end">Received</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {events.map((event) => (
            <TableRow
              key={event.id}
              data-state={
                selected === event.delivery_id ? "selected" : undefined
              }
              className="relative"
            >
              <TableCell className="ps-4">
                <Link
                  href={hrefFor(event.delivery_id)}
                  scroll={false}
                  aria-label={`Inspect ${event.event} delivery ${event.delivery_id}`}
                  className="flex items-center gap-2 outline-none after:absolute after:inset-0 focus-visible:after:ring-1 focus-visible:after:ring-ring focus-visible:after:ring-inset"
                >
                  <span
                    className="h-4 w-1 shrink-0"
                    style={{ background: eventColor(event.event) }}
                  />
                  <span className="font-medium">{event.event}</span>
                  {event.action && (
                    <span className="text-muted-foreground">
                      {event.action}
                    </span>
                  )}
                </Link>
              </TableCell>
              <TableCell className="w-full max-w-0">
                <SubjectCell event={event} />
              </TableCell>
              <TableCell className="hidden md:table-cell">
                {event.sender ? (
                  <span className="flex items-center gap-2">
                    {/* eslint-disable-next-line @next/next/no-img-element */}
                    <img
                      src={event.sender.avatar_url}
                      alt=""
                      className="size-5 shrink-0 bg-muted"
                    />
                    {event.sender.login}
                  </span>
                ) : (
                  <span className="text-muted-foreground">–</span>
                )}
              </TableCell>
              <TableCell
                className="pe-4 text-end whitespace-nowrap text-muted-foreground"
                title={formatDateTime(event.received_at)}
              >
                <span suppressHydrationWarning>
                  {timeAgo(event.received_at)}
                </span>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

function SubjectIcon({ subject, event }: { subject: Subject; event: string }) {
  if (event.includes("comment")) return <RiChat3Line />
  if (subject.type === "pull_request") {
    if (subject.merged) return <RiGitMergeLine className="text-ev-comment" />
    return (
      <RiGitPullRequestLine
        className={cn(
          subject.state === "open" ? "text-ev-push" : "text-muted-foreground"
        )}
      />
    )
  }
  return (
    <RiRecordCircleLine
      className={cn(
        subject.state === "open" ? "text-ev-push" : "text-muted-foreground"
      )}
    />
  )
}

function SubjectCell({ event }: { event: RepoEvent }) {
  if (event.subject) {
    const { subject } = event
    return (
      <span className="flex min-w-0 items-center gap-2 [&_svg]:size-3.5 [&_svg]:shrink-0">
        <SubjectIcon subject={subject} event={event.event} />
        <span className="shrink-0 text-muted-foreground tabular-nums">
          #{subject.number}
        </span>
        <a
          href={subject.html_url}
          target="_blank"
          rel="noreferrer"
          className="relative z-10 truncate underline-offset-4 hover:underline"
          title={`Open on GitHub: ${subject.title}`}
        >
          {subject.title}
        </a>
      </span>
    )
  }

  if (event.ref) {
    return (
      <span className="flex min-w-0 items-center gap-2 text-muted-foreground">
        <RiGitBranchLine className="size-3.5 shrink-0" />
        <span className="truncate text-foreground">{shortRef(event.ref)}</span>
      </span>
    )
  }

  if (event.number !== null) {
    return <span className="text-muted-foreground">#{event.number}</span>
  }

  return <span className="text-muted-foreground">–</span>
}
