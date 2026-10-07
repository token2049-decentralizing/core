"use client"

import * as React from "react"
import { useRouter } from "next/navigation"
import { toast } from "sonner"
import { RiExternalLinkLine, RiUserVoiceLine } from "@remixicon/react"

import { ApiError, requestReview, type Appeal } from "@/lib/api"
import { Button } from "@/components/ui/button"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet"
import { Textarea } from "@/components/ui/textarea"

const MIN = 10
const MAX = 2000

// Asks a human to review the evaluation: the runner comments on the PR and @mentions a reviewer.
export function AppealButton({
  executionId,
  appeal,
}: {
  executionId: string
  appeal: Appeal | null
}) {
  const router = useRouter()
  const [open, setOpen] = React.useState(false)
  const [reason, setReason] = React.useState("")
  const [pending, setPending] = React.useState(false)
  const [error, setError] = React.useState<string | null>(null)

  if (appeal) {
    return (
      <Button
        variant="outline"
        nativeButton={false}
        render={
          appeal.comment_url ? (
            <a href={appeal.comment_url} target="_blank" rel="noreferrer" />
          ) : (
            <span />
          )
        }
      >
        <RiUserVoiceLine data-icon="inline-start" />
        {appeal.mentioned_login
          ? `Review requested from @${appeal.mentioned_login}`
          : "Review requested"}
        {appeal.comment_url && <RiExternalLinkLine data-icon="inline-end" />}
      </Button>
    )
  }

  const length = reason.trim().length
  const valid = length >= MIN && length <= MAX

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    if (!valid) return
    setPending(true)
    setError(null)
    try {
      const a = await requestReview(executionId, reason.trim())
      toast.success(
        a.mentioned_login
          ? `Asked @${a.mentioned_login} to review on GitHub`
          : "Review request posted on GitHub"
      )
      setOpen(false)
      router.refresh()
    } catch (err) {
      setError(
        err instanceof ApiError ? err.message : "Couldn't send the request."
      )
    } finally {
      setPending(false)
    }
  }

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger render={<Button variant="destructive" />}>
        <RiUserVoiceLine data-icon="inline-start" />
        Request review
      </SheetTrigger>
      <SheetContent className="w-full gap-0 data-[side=right]:w-full data-[side=right]:sm:max-w-md">
        <form onSubmit={submit} className="flex h-full flex-col">
          <SheetHeader className="border-b pe-12">
            <SheetTitle className="font-heading text-base">
              Request a human review
            </SheetTitle>
            <SheetDescription>
              We comment on the pull request and @mention the person who opened
              the linked issue (or merged the PR). The score stays as is until
              they act. You can send one request per evaluation.
            </SheetDescription>
          </SheetHeader>
          <div className="flex flex-1 flex-col gap-2 overflow-y-auto p-4">
            <label htmlFor="appeal-reason" className="font-medium">
              What looks wrong?
            </label>
            <Textarea
              id="appeal-reason"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder="e.g. Tests for the retry path are in pool_test.go, but the review says there are none."
              className="min-h-40"
              maxLength={MAX}
              aria-invalid={error ? true : undefined}
              autoFocus
            />
            <div className="flex justify-between gap-3 text-[11px] text-muted-foreground">
              <span>
                Point at the criterion you disagree with. Mentions are not sent.
              </span>
              <span className="shrink-0 tabular-nums">
                {length}/{MAX}
              </span>
            </div>
            {error && (
              <p className="border border-destructive/30 bg-destructive/5 p-2 text-destructive">
                {error}
              </p>
            )}
          </div>
          <SheetFooter className="flex-row justify-end gap-2 border-t">
            <Button
              type="button"
              variant="outline"
              onClick={() => setOpen(false)}
            >
              Cancel
            </Button>
            <Button
              type="submit"
              variant="destructive"
              disabled={!valid || pending}
            >
              {pending ? "Posting…" : "Post on GitHub"}
            </Button>
          </SheetFooter>
        </form>
      </SheetContent>
    </Sheet>
  )
}
