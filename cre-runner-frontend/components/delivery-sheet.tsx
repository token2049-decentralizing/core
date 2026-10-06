"use client"

import * as React from "react"
import Link from "next/link"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { RiExternalLinkLine } from "@remixicon/react"

import { ApiError, getDelivery, type Delivery } from "@/lib/api"
import { DeliveryDetail, DeliveryHeading } from "@/components/delivery-detail"
import { Button } from "@/components/ui/button"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Skeleton } from "@/components/ui/skeleton"

type State =
  | { status: "loading" }
  | { status: "ready"; delivery: Delivery }
  | { status: "error"; message: string }

// Opens when the URL has ?delivery=<id>, so a selected event can be shared.
export function DeliverySheet() {
  const router = useRouter()
  const pathname = usePathname()
  const searchParams = useSearchParams()
  const deliveryId = searchParams.get("delivery")
  const [result, setResult] = React.useState<{
    id: string
    state: State
  } | null>(null)

  React.useEffect(() => {
    if (!deliveryId) return
    let cancelled = false
    getDelivery(deliveryId)
      .then((delivery) => {
        if (!cancelled)
          setResult({ id: deliveryId, state: { status: "ready", delivery } })
      })
      .catch((error: unknown) => {
        if (cancelled) return
        const message =
          error instanceof ApiError && error.status === 404
            ? "This delivery no longer exists."
            : error instanceof Error
              ? error.message
              : "Couldn't load this delivery."
        setResult({ id: deliveryId, state: { status: "error", message } })
      })
    return () => {
      cancelled = true
    }
  }, [deliveryId])

  const state: State =
    result && result.id === deliveryId ? result.state : { status: "loading" }

  function close() {
    const params = new URLSearchParams(searchParams)
    params.delete("delivery")
    const query = params.toString()
    router.replace(query ? `${pathname}?${query}` : pathname, { scroll: false })
  }

  return (
    <Sheet open={Boolean(deliveryId)} onOpenChange={(open) => !open && close()}>
      <SheetContent className="w-full gap-0 data-[side=right]:w-full data-[side=right]:sm:max-w-2xl">
        <SheetHeader className="border-b pe-12">
          <SheetTitle className="font-heading text-base">
            {state.status === "ready" ? (
              <DeliveryHeading delivery={state.delivery} />
            ) : (
              "Delivery"
            )}
          </SheetTitle>
          <SheetDescription className="break-all">
            {deliveryId}
          </SheetDescription>
        </SheetHeader>
        <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto p-4">
          {state.status === "loading" && (
            <div className="flex flex-col gap-3">
              {Array.from({ length: 6 }, (_, i) => (
                <Skeleton key={i} className="h-4 w-full max-w-md" />
              ))}
              <Skeleton className="mt-4 h-80 w-full" />
            </div>
          )}
          {state.status === "error" && (
            <p className="border border-destructive/30 bg-destructive/5 p-3 text-xs text-destructive">
              {state.message}
            </p>
          )}
          {state.status === "ready" && (
            <>
              <DeliveryDetail delivery={state.delivery} />
              <Button
                variant="outline"
                className="self-start"
                nativeButton={false}
                render={
                  <Link href={`/deliveries/${state.delivery.delivery_id}`} />
                }
              >
                <RiExternalLinkLine data-icon="inline-start" />
                Open as page
              </Button>
            </>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}
