import Link from "next/link"
import { RiCompass3Line } from "@remixicon/react"

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

export default function NotFound() {
  return (
    <PageBody>
      <Empty className="border py-20">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <RiCompass3Line />
          </EmptyMedia>
          <EmptyTitle>Nothing found here</EmptyTitle>
          <EmptyDescription>
            The repository, delivery or campaign doesn&apos;t exist, or it
            hasn&apos;t received any events yet. Check the ID and try again.
          </EmptyDescription>
        </EmptyHeader>
        <EmptyContent>
          <Button
            variant="outline"
            nativeButton={false}
            render={<Link href="/" />}
          >
            Back to overview
          </Button>
        </EmptyContent>
      </Empty>
    </PageBody>
  )
}
