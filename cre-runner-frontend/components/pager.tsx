"use client"

import Link from "next/link"
import { usePathname, useSearchParams } from "next/navigation"
import { RiArrowLeftSLine, RiArrowRightSLine } from "@remixicon/react"

import type { Pagination } from "@/lib/api"
import { formatCount } from "@/lib/format"
import { Button } from "@/components/ui/button"

export function Pager({
  pagination,
  noun,
}: {
  pagination: Pagination
  noun: string
}) {
  const pathname = usePathname()
  const searchParams = useSearchParams()
  const { page, page_size, total, has_more } = pagination

  function hrefFor(target: number) {
    const params = new URLSearchParams(searchParams)
    params.delete("delivery")
    if (target <= 0) params.delete("page")
    else params.set("page", String(target))
    const query = params.toString()
    return query ? `${pathname}?${query}` : pathname
  }

  const from = total === 0 ? 0 : page * page_size + 1
  const to = Math.min(total, (page + 1) * page_size)
  const lastPage = Math.max(0, Math.ceil(total / page_size) - 1)

  return (
    <div className="flex items-center justify-between gap-4 text-xs text-muted-foreground">
      <p className="tabular-nums">
        {from > total ? (
          <>
            Page {page + 1} is past the end of {formatCount(total)} {noun}
          </>
        ) : (
          <>
            {formatCount(from)}–{formatCount(to)} of {formatCount(total)} {noun}
          </>
        )}
      </p>
      <div className="flex items-center gap-1">
        {page > 0 ? (
          <Button
            variant="outline"
            size="sm"
            nativeButton={false}
            render={
              <Link
                href={hrefFor(Math.min(page - 1, lastPage))}
                scroll={false}
              />
            }
          >
            <RiArrowLeftSLine data-icon="inline-start" />
            Newer
          </Button>
        ) : (
          <Button variant="outline" size="sm" disabled>
            <RiArrowLeftSLine data-icon="inline-start" />
            Newer
          </Button>
        )}
        {has_more ? (
          <Button
            variant="outline"
            size="sm"
            nativeButton={false}
            render={<Link href={hrefFor(page + 1)} scroll={false} />}
          >
            Older
            <RiArrowRightSLine data-icon="inline-end" />
          </Button>
        ) : (
          <Button variant="outline" size="sm" disabled>
            Older
            <RiArrowRightSLine data-icon="inline-end" />
          </Button>
        )}
      </div>
    </div>
  )
}
