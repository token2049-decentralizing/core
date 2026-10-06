import type { Metadata } from "next"
import { notFound } from "next/navigation"

import { attempt, getDelivery } from "@/lib/api"
import { ApiErrorState } from "@/components/api-error-state"
import { DeliveryDetail, DeliveryHeading } from "@/components/delivery-detail"
import { PageBody, PageHeader } from "@/components/page"

export const metadata: Metadata = { title: "Delivery" }

export default async function DeliveryPage(
  props: PageProps<"/deliveries/[id]">
) {
  const { id } = await props.params
  const result = await attempt(getDelivery(decodeURIComponent(id)))

  if (!result.ok) {
    if (result.error.status === 404) notFound()
    return (
      <PageBody>
        <PageHeader title="Delivery" />
        <ApiErrorState
          status={result.error.status}
          message={result.error.message}
        />
      </PageBody>
    )
  }

  const delivery = result.data

  return (
    <PageBody className="max-w-4xl">
      <PageHeader
        title={<DeliveryHeading delivery={delivery} />}
        description={
          <span className="break-all">
            GitHub delivery {delivery.delivery_id}
          </span>
        }
      />
      <DeliveryDetail delivery={delivery} />
    </PageBody>
  )
}
