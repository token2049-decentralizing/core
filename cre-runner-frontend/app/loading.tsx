import { PageBody } from "@/components/page"
import { Skeleton } from "@/components/ui/skeleton"

export default function Loading() {
  return (
    <PageBody aria-busy="true" aria-label="Loading">
      <div className="flex flex-col gap-2">
        <Skeleton className="h-7 w-64" />
        <Skeleton className="h-4 w-96 max-w-full" />
      </div>
      <Skeleton className="h-16 w-full" />
      <div className="flex flex-col gap-px border">
        {Array.from({ length: 8 }, (_, i) => (
          <Skeleton key={i} className="h-10 w-full" />
        ))}
      </div>
    </PageBody>
  )
}
