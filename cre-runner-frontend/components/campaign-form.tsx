"use client"

import * as React from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { toast } from "sonner"
import { RiAddLine, RiCloseLine, RiErrorWarningLine } from "@remixicon/react"

import { createCampaign, type CreateCampaignInput } from "@/lib/api"
import { humanize } from "@/lib/format"
import { cn } from "@/lib/utils"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { Toggle } from "@/components/ui/toggle"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"

const ELIGIBILITY = [
  {
    key: "merged",
    label: "Merged",
    hint: "Only merged pull requests earn rewards.",
    initial: true,
  },
  {
    key: "ci_passed",
    label: "CI passed",
    hint: "All required checks must pass.",
    initial: true,
  },
  {
    key: "linked_issue",
    label: "Linked issue",
    hint: "The PR must reference an issue it fixes.",
    initial: true,
  },
  {
    key: "duplicate",
    label: "Allow duplicates",
    hint: "Reward PRs that duplicate earlier work.",
    initial: false,
  },
] as const

const SCORING = [
  { key: "issue_relevance", initial: 20 },
  { key: "correctness", initial: 25 },
  { key: "tests", initial: 15 },
  { key: "code_quality", initial: 15 },
  { key: "maintainer_review", initial: 15 },
  { key: "novelty", initial: 10 },
] as const

const AMOUNT = /^\d+(\.\d{1,6})?$/
const REPO = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/

type Errors = Partial<Record<string, string>>

const pressed =
  "aria-pressed:border-primary aria-pressed:bg-primary/10 aria-pressed:text-primary"

export function CampaignForm({ knownRepos }: { knownRepos: string[] }) {
  const router = useRouter()
  const [pending, setPending] = React.useState(false)
  const [errors, setErrors] = React.useState<Errors>({})
  const [serverError, setServerError] = React.useState<string | null>(null)

  const [asset, setAsset] = React.useState<"USDC" | "SOL">("USDC")
  const [status, setStatus] = React.useState<"draft" | "active">("draft")
  const [eligibility, setEligibility] = React.useState<Record<string, boolean>>(
    Object.fromEntries(ELIGIBILITY.map((e) => [e.key, e.initial]))
  )
  const [scoring, setScoring] = React.useState<Record<string, string>>(
    Object.fromEntries(SCORING.map((s) => [s.key, String(s.initial)]))
  )
  const [repos, setRepos] = React.useState<string[]>([])
  const [repoDraft, setRepoDraft] = React.useState("")

  const scoringTotal = Object.values(scoring).reduce(
    (sum, v) => sum + (Number(v) || 0),
    0
  )
  const extraRepos = repos.filter((r) => !knownRepos.includes(r))

  function addRepo() {
    const value = repoDraft
      .trim()
      .replace(/^https?:\/\/github\.com\//, "")
      .replace(/\/+$/, "")
    if (!value) return
    if (!REPO.test(value)) {
      setErrors((e) => ({
        ...e,
        repos: "Use the owner/repo format, for example octo-org/hello-world.",
      }))
      return
    }
    setErrors((e) => ({ ...e, repos: undefined }))
    setRepos((list) => (list.includes(value) ? list : [...list, value]))
    setRepoDraft("")
  }

  function validate(form: FormData) {
    const next: Errors = {}
    const name = String(form.get("name") ?? "").trim()
    const budget = String(form.get("budget") ?? "").trim()
    const maxReward = String(form.get("max_reward_per_pr") ?? "").trim()
    const minScore = String(form.get("min_score") ?? "").trim()
    const startsAt = String(form.get("starts_at") ?? "")
    const endsAt = String(form.get("ends_at") ?? "")

    if (!name) next.name = "Give the campaign a name."
    if (!AMOUNT.test(budget) || Number(budget) <= 0)
      next.budget = "Enter an amount above 0 with at most 6 decimals."
    if (!AMOUNT.test(maxReward) || Number(maxReward) <= 0)
      next.max_reward_per_pr =
        "Enter an amount above 0 with at most 6 decimals."
    else if (!next.budget && Number(maxReward) > Number(budget))
      next.max_reward_per_pr = "Can't be more than the total budget."
    if (minScore && !(/^\d+$/.test(minScore) && Number(minScore) <= 100))
      next.min_score = "Use a whole number from 0 to 100."
    if (startsAt && endsAt && new Date(endsAt) <= new Date(startsAt))
      next.ends_at = "The end date must be after the start date."
    for (const { key } of SCORING) {
      const v = scoring[key]
      if (v !== "" && !(Number(v) >= 0))
        next[`scoring.${key}`] = "Use 0 or more."
    }
    return next
  }

  async function onSubmit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    const found = validate(form)
    setErrors(found)
    setServerError(null)
    if (Object.keys(found).length > 0) {
      const first = Object.keys(found)[0]
      document.querySelector<HTMLElement>(`[name="${first}"]`)?.focus()
      return
    }

    const text = (key: string) =>
      String(form.get(key) ?? "").trim() || undefined
    const date = (key: string) => {
      const v = text(key)
      return v ? new Date(v).toISOString() : undefined
    }
    const minScore = text("min_score")

    const input: CreateCampaignInput = {
      name: text("name")!,
      description: text("description"),
      sponsor: text("sponsor"),
      reward_asset: asset,
      budget: text("budget")!,
      max_reward_per_pr: text("max_reward_per_pr")!,
      min_score: minScore ? Number(minScore) : undefined,
      eligibility,
      scoring: Object.fromEntries(
        Object.entries(scoring)
          .filter(([, v]) => v !== "")
          .map(([k, v]) => [k, Number(v)])
      ),
      status,
      starts_at: date("starts_at"),
      ends_at: date("ends_at"),
      repos,
    }

    setPending(true)
    try {
      const campaign = await createCampaign(input)
      toast.success(
        status === "active"
          ? "Campaign created and active"
          : "Draft campaign created"
      )
      router.push(`/campaigns/${campaign.id}`)
      router.refresh()
    } catch (error) {
      setServerError(
        error instanceof Error ? error.message : "Couldn't create the campaign."
      )
      window.scrollTo({ top: 0, behavior: "smooth" })
      setPending(false)
    }
  }

  return (
    <form onSubmit={onSubmit} noValidate className="flex flex-col gap-10">
      {serverError && (
        <Alert variant="destructive">
          <RiErrorWarningLine />
          <AlertTitle>The campaign wasn&apos;t created</AlertTitle>
          <AlertDescription>{serverError}</AlertDescription>
        </Alert>
      )}

      <FieldSet>
        <FieldLegend>Basics</FieldLegend>
        <FieldGroup>
          <Field data-invalid={!!errors.name || undefined}>
            <FieldLabel htmlFor="name">Name</FieldLabel>
            <Input
              id="name"
              name="name"
              placeholder="OSS bug bash, Q4"
              aria-invalid={!!errors.name}
              required
            />
            <FieldError>{errors.name}</FieldError>
          </Field>
          <Field>
            <FieldLabel htmlFor="sponsor">Sponsor</FieldLabel>
            <Input
              id="sponsor"
              name="sponsor"
              placeholder="Who is funding the rewards"
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="description">Description</FieldLabel>
            <Textarea
              id="description"
              name="description"
              rows={3}
              placeholder="What kind of contributions this campaign rewards"
            />
          </Field>
        </FieldGroup>
      </FieldSet>

      <FieldSet>
        <FieldLegend>Rewards</FieldLegend>
        <FieldGroup>
          <Field>
            <FieldLabel>Paid in</FieldLabel>
            <ToggleGroup
              variant="outline"
              spacing={0}
              value={[asset]}
              onValueChange={(v) => v[0] && setAsset(v[0] as "USDC" | "SOL")}
              aria-label="Reward asset"
            >
              <ToggleGroupItem value="USDC" className={cn("w-20", pressed)}>
                USDC
              </ToggleGroupItem>
              <ToggleGroupItem value="SOL" className={cn("w-20", pressed)}>
                SOL
              </ToggleGroupItem>
            </ToggleGroup>
          </Field>
          <div className="grid gap-5 sm:grid-cols-3">
            <Field data-invalid={!!errors.budget || undefined}>
              <FieldLabel htmlFor="budget">Total budget</FieldLabel>
              <AmountInput
                id="budget"
                name="budget"
                unit={asset}
                placeholder="10000"
                invalid={!!errors.budget}
              />
              <FieldError>{errors.budget}</FieldError>
            </Field>
            <Field data-invalid={!!errors.max_reward_per_pr || undefined}>
              <FieldLabel htmlFor="max_reward_per_pr">Max per PR</FieldLabel>
              <AmountInput
                id="max_reward_per_pr"
                name="max_reward_per_pr"
                unit={asset}
                placeholder="500"
                invalid={!!errors.max_reward_per_pr}
              />
              <FieldError>{errors.max_reward_per_pr}</FieldError>
            </Field>
            <Field data-invalid={!!errors.min_score || undefined}>
              <FieldLabel htmlFor="min_score">Minimum score</FieldLabel>
              <Input
                id="min_score"
                name="min_score"
                inputMode="numeric"
                placeholder="70"
                aria-invalid={!!errors.min_score}
              />
              {errors.min_score ? (
                <FieldError>{errors.min_score}</FieldError>
              ) : (
                <FieldDescription>
                  Out of 100. Leave empty for no minimum.
                </FieldDescription>
              )}
            </Field>
          </div>
        </FieldGroup>
      </FieldSet>

      <FieldSet>
        <FieldLegend>Who qualifies</FieldLegend>
        <FieldDescription>
          Pull requests must meet every rule switched on.
        </FieldDescription>
        <div className="grid border sm:grid-cols-2">
          {ELIGIBILITY.map((rule, i) => (
            <Field
              key={rule.key}
              orientation="horizontal"
              className={cn(
                "items-start! p-3",
                i % 2 === 0 && "sm:border-e",
                i < ELIGIBILITY.length - 2 && "sm:border-b",
                i < ELIGIBILITY.length - 1 && "max-sm:border-b"
              )}
            >
              <FieldContent>
                <FieldLabel htmlFor={`elig-${rule.key}`}>
                  {rule.label}
                </FieldLabel>
                <FieldDescription>{rule.hint}</FieldDescription>
              </FieldContent>
              <Switch
                id={`elig-${rule.key}`}
                checked={eligibility[rule.key]}
                onCheckedChange={(checked) =>
                  setEligibility((e) => ({ ...e, [rule.key]: checked }))
                }
              />
            </Field>
          ))}
        </div>
      </FieldSet>

      <FieldSet>
        <FieldLegend>Scoring weights</FieldLegend>
        <FieldDescription>
          How much each factor counts toward a PR&apos;s score.{" "}
          <span
            className={cn(
              scoringTotal === 100 ? "text-foreground" : "text-ev-issue"
            )}
          >
            Total {scoringTotal}
            {scoringTotal !== 100 && ", usually 100"}.
          </span>
        </FieldDescription>
        <div className="grid gap-x-5 gap-y-4 sm:grid-cols-3">
          {SCORING.map(({ key }) => (
            <Field
              key={key}
              data-invalid={!!errors[`scoring.${key}`] || undefined}
            >
              <FieldLabel htmlFor={`scoring-${key}`}>
                {humanize(key)}
              </FieldLabel>
              <Input
                id={`scoring-${key}`}
                name={`scoring.${key}`}
                inputMode="numeric"
                value={scoring[key]}
                onChange={(e) =>
                  setScoring((s) => ({ ...s, [key]: e.target.value }))
                }
                aria-invalid={!!errors[`scoring.${key}`]}
                className="tabular-nums"
              />
              <FieldError>{errors[`scoring.${key}`]}</FieldError>
            </Field>
          ))}
        </div>
      </FieldSet>

      <FieldSet>
        <FieldLegend>Repositories</FieldLegend>
        <FieldDescription>
          Pull requests in these repositories can earn rewards. You can attach
          up to 50.
        </FieldDescription>
        {knownRepos.length > 0 && (
          <div
            className="flex flex-wrap gap-2"
            role="group"
            aria-label="Repositories with webhook activity"
          >
            {knownRepos.map((repo) => (
              <Toggle
                key={repo}
                variant="outline"
                pressed={repos.includes(repo)}
                onPressedChange={(on) =>
                  setRepos((list) =>
                    on ? [...list, repo] : list.filter((r) => r !== repo)
                  )
                }
                className={pressed}
              >
                {repo}
              </Toggle>
            ))}
          </div>
        )}
        {extraRepos.length > 0 && (
          <ul className="flex flex-wrap gap-2">
            {extraRepos.map((repo) => (
              <li
                key={repo}
                className="flex h-8 items-center gap-1 border border-primary bg-primary/10 ps-2.5 pe-1 text-xs text-primary"
              >
                {repo}
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-xs"
                  aria-label={`Remove ${repo}`}
                  onClick={() =>
                    setRepos((list) => list.filter((r) => r !== repo))
                  }
                >
                  <RiCloseLine />
                </Button>
              </li>
            ))}
          </ul>
        )}
        <Field data-invalid={!!errors.repos || undefined}>
          <FieldLabel htmlFor="repo-draft" className="sr-only">
            Add another repository
          </FieldLabel>
          <div className="flex gap-2">
            <Input
              id="repo-draft"
              value={repoDraft}
              onChange={(e) => setRepoDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault()
                  addRepo()
                }
              }}
              placeholder="owner/repo"
              aria-invalid={!!errors.repos}
              className="max-w-xs"
            />
            <Button type="button" variant="outline" onClick={addRepo}>
              <RiAddLine data-icon="inline-start" />
              Add
            </Button>
          </div>
          <FieldError>{errors.repos}</FieldError>
        </Field>
      </FieldSet>

      <FieldSet>
        <FieldLegend>Schedule</FieldLegend>
        <div className="grid gap-5 sm:grid-cols-2">
          <Field>
            <FieldLabel htmlFor="starts_at">Starts</FieldLabel>
            <Input id="starts_at" name="starts_at" type="datetime-local" />
            <FieldDescription>
              Leave empty to start right away.
            </FieldDescription>
          </Field>
          <Field data-invalid={!!errors.ends_at || undefined}>
            <FieldLabel htmlFor="ends_at">Ends</FieldLabel>
            <Input
              id="ends_at"
              name="ends_at"
              type="datetime-local"
              aria-invalid={!!errors.ends_at}
            />
            {errors.ends_at ? (
              <FieldError>{errors.ends_at}</FieldError>
            ) : (
              <FieldDescription>
                Leave empty to run until you end it.
              </FieldDescription>
            )}
          </Field>
        </div>
        <Field orientation="horizontal" className="border p-3">
          <FieldContent>
            <FieldLabel htmlFor="activate">Activate right away</FieldLabel>
            <FieldDescription>
              Otherwise the campaign is saved as a draft that doesn&apos;t pay
              out.
            </FieldDescription>
          </FieldContent>
          <Switch
            id="activate"
            checked={status === "active"}
            onCheckedChange={(checked) =>
              setStatus(checked ? "active" : "draft")
            }
          />
        </Field>
      </FieldSet>

      <div className="sticky bottom-0 -mx-4 flex items-center justify-end gap-2 border-t bg-background/90 px-4 py-3 backdrop-blur md:-mx-8 md:px-8">
        <Button
          variant="ghost"
          nativeButton={false}
          render={<Link href="/campaigns" />}
        >
          Cancel
        </Button>
        <Button type="submit" disabled={pending}>
          {pending
            ? "Creating…"
            : status === "active"
              ? "Create and activate"
              : "Create draft"}
        </Button>
      </div>
    </form>
  )
}

function AmountInput({
  unit,
  invalid,
  ...props
}: React.ComponentProps<typeof Input> & { unit: string; invalid?: boolean }) {
  return (
    <div className="relative">
      <Input
        inputMode="decimal"
        autoComplete="off"
        aria-invalid={invalid}
        className="pe-14 tabular-nums"
        {...props}
      />
      <span className="pointer-events-none absolute end-2.5 top-1/2 -translate-y-1/2 text-[11px] text-muted-foreground">
        {unit}
      </span>
    </div>
  )
}
