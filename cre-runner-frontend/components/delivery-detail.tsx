"use client"

import * as React from "react"
import Link from "next/link"
import { RiCheckLine, RiFileCopyLine } from "@remixicon/react"

import type { Delivery } from "@/lib/api"
import { eventColor, formatDateTime } from "@/lib/format"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"

export function DeliveryHeading({ delivery }: { delivery: Delivery }) {
  return (
    <span className="flex items-center gap-2">
      <span
        className="h-5 w-1.5 shrink-0"
        style={{ background: eventColor(delivery.event) }}
      />
      <span>{delivery.event}</span>
      {delivery.action && (
        <span className="font-normal text-muted-foreground">
          {delivery.action}
        </span>
      )}
    </span>
  )
}

function SignatureValue({ valid }: { valid: boolean | null }) {
  if (valid === null) {
    return (
      <span className="text-muted-foreground">Not checked (no secret set)</span>
    )
  }
  return valid ? (
    <span className="flex items-center gap-1.5">
      <span className="size-1.5 bg-ev-push" />
      Valid
    </span>
  ) : (
    <span className="flex items-center gap-1.5 text-destructive">
      <span className="size-1.5 bg-destructive" />
      Invalid
    </span>
  )
}

export function DeliveryDetail({ delivery }: { delivery: Delivery }) {
  const rows: [string, React.ReactNode][] = [
    ["Received", formatDateTime(delivery.received_at)],
    [
      "Repository",
      delivery.repository_full_name ? (
        <Link
          href={`/repos/${delivery.repository_full_name}`}
          className="underline-offset-4 hover:underline"
        >
          {delivery.repository_full_name}
        </Link>
      ) : (
        "–"
      ),
    ],
    ["Sender", delivery.sender_login ?? "–"],
    ["PR or issue", delivery.number !== null ? `#${delivery.number}` : "–"],
    [
      "Signature",
      <SignatureValue key="sig" valid={delivery.signature_valid} />,
    ],
    [
      "Delivery ID",
      <span key="id" className="break-all">
        {delivery.delivery_id}
      </span>,
    ],
    ["Hook ID", delivery.hook_id ?? "–"],
    ["Installation ID", delivery.installation_id ?? "–"],
  ]

  const payload = JSON.stringify(delivery.payload, null, 2)
  const headers = JSON.stringify(delivery.headers, null, 2)

  return (
    <div className="flex min-h-0 flex-col gap-6">
      <dl className="grid grid-cols-[8rem_1fr] gap-x-4 gap-y-2 text-xs">
        {rows.map(([label, value]) => (
          <React.Fragment key={label}>
            <dt className="text-muted-foreground">{label}</dt>
            <dd className="min-w-0">{value}</dd>
          </React.Fragment>
        ))}
      </dl>

      <Tabs defaultValue="payload" className="min-h-0 gap-0">
        <div className="flex items-center justify-between gap-2 border border-b-0 px-1 py-1">
          <TabsList variant="line">
            <TabsTrigger value="payload">Payload</TabsTrigger>
            <TabsTrigger value="headers">Headers</TabsTrigger>
          </TabsList>
        </div>
        <TabsContent value="payload">
          <JsonBlock json={payload} label="payload" />
        </TabsContent>
        <TabsContent value="headers">
          <JsonBlock json={headers} label="headers" />
        </TabsContent>
      </Tabs>
    </div>
  )
}

function JsonBlock({ json, label }: { json: string; label: string }) {
  const [copied, setCopied] = React.useState(false)

  async function copy() {
    await navigator.clipboard.writeText(json)
    setCopied(true)
    window.setTimeout(() => setCopied(false), 1500)
  }

  return (
    <div className="relative border bg-muted/40">
      <Button
        variant="outline"
        size="xs"
        onClick={copy}
        className="absolute end-2 top-2 z-10"
        aria-label={`Copy ${label}`}
      >
        {copied ? (
          <RiCheckLine data-icon="inline-start" />
        ) : (
          <RiFileCopyLine data-icon="inline-start" />
        )}
        {copied ? "Copied" : "Copy"}
      </Button>
      <ScrollArea className="h-[min(60vh,32rem)]">
        <pre className="p-3 pe-20 text-[11px] leading-relaxed">{json}</pre>
      </ScrollArea>
    </div>
  )
}
