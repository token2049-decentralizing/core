"use client"

import { useRouter } from "next/navigation"
import { RiPlugLine, RiRefreshLine } from "@remixicon/react"

import { API_BASE_URL } from "@/lib/api"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"

export function ApiErrorState({
  status,
  message,
}: {
  status: number
  message: string
}) {
  const router = useRouter()
  const unreachable = status === 0

  return (
    <Empty className="border py-16">
      <EmptyHeader>
        <EmptyMedia variant="icon">
          <RiPlugLine />
        </EmptyMedia>
        <EmptyTitle>
          {unreachable
            ? "The API isn't responding"
            : "The API returned an error"}
        </EmptyTitle>
        <EmptyDescription>
          {unreachable ? (
            <>
              Start cre-runner or point <code>NEXT_PUBLIC_API_BASE_URL</code> at
              a running instance. Currently using <code>{API_BASE_URL}</code>.
            </>
          ) : (
            <>
              {message}
              {status >= 500 && " Check the cre-runner logs for details."}
            </>
          )}
        </EmptyDescription>
      </EmptyHeader>
      <EmptyContent>
        <Button variant="outline" onClick={() => router.refresh()}>
          <RiRefreshLine data-icon="inline-start" />
          Try again
        </Button>
      </EmptyContent>
    </Empty>
  )
}
