"use client"

import { useEffect } from "react"
import { RiErrorWarningLine, RiRefreshLine } from "@remixicon/react"

import { PageBody } from "@/components/page"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"

export default function Error({
  error,
  retry,
}: {
  error: Error & { digest?: string }
  retry: () => void
}) {
  useEffect(() => {
    console.error(error)
  }, [error])

  return (
    <PageBody>
      <Empty className="border py-20">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <RiErrorWarningLine />
          </EmptyMedia>
          <EmptyTitle>This page failed to load</EmptyTitle>
          <EmptyDescription>
            {error.digest ? `Error reference ${error.digest}. ` : ""}
            Reload the page, and check the browser console if it keeps
            happening.
          </EmptyDescription>
        </EmptyHeader>
        <EmptyContent>
          <Button variant="outline" onClick={() => retry()}>
            <RiRefreshLine data-icon="inline-start" />
            Try again
          </Button>
        </EmptyContent>
      </Empty>
    </PageBody>
  )
}
