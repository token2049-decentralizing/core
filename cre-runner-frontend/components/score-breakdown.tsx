import type {
  EvidenceCheck,
  Finding,
  Gate,
  ReviewCard,
  Scorecard,
} from "@/lib/api"
import { cn } from "@/lib/utils"

const EVIDENCE_LABEL: Record<string, string> = {
  linked_issue: "Linked issue",
  ci: "CI",
  approval: "Approval",
  tests: "Tests",
  size: "Diff size",
}

const GATE_LABEL: Record<string, string> = {
  merged: "Merged",
  ci_passed: "CI passed",
  linked_issue: "Linked issue",
  min_score: "Min score",
}

const PERSONA_LABEL: Record<string, string> = {
  code: "Code review",
  issue: "Issue fit",
}

// Segment colors of the composition bar, one per score source.
const TONE = {
  evidence: "bg-ev-pr",
  code_reviewer: "bg-ev-ci",
  llm: "bg-ev-issue",
} as const

function reviewLabel(r: ReviewCard) {
  return (
    PERSONA_LABEL[r.persona] ??
    (r.role === "code_reviewer" ? "Code reviewer" : "LLM review")
  )
}

function humanize(name: string) {
  const s = name.replaceAll("_", " ")
  return s.charAt(0).toUpperCase() + s.slice(1)
}

// Full marks, partial, nothing: the shape of what lost points.
function ratioTone(points: number, max: number) {
  if (points >= max) return "bg-ev-push"
  if (points > 0) return "bg-ev-issue"
  return "bg-destructive"
}

function findingHref(
  repo: string,
  headSha: string | null,
  prNumber: number,
  f: Finding
) {
  if (!f.file) return null
  if (!headSha) return `https://github.com/${repo}/pull/${prNumber}/files`
  const [start, end] = f.lines.split("-")
  const anchor = start ? `#L${start}${end ? `-L${end}` : ""}` : ""
  return `https://github.com/${repo}/blob/${headSha}/${f.file
    .split("/")
    .map(encodeURIComponent)
    .join("/")}${anchor}`
}

export function ScoreBreakdown({
  card,
  score,
  repo,
  prNumber,
  headSha,
}: {
  card: Scorecard
  score: number | null
  repo: string
  prNumber: number
  headSha: string | null
}) {
  const evidenceScore = card.evidence.reduce((sum, c) => sum + c.points, 0)
  const minGate = card.gates.find((g) => g.gate === "min_score")
  const minScore = Number(minGate?.detail.split("/")[1]?.trim()) || null

  const segments = [
    {
      key: "evidence" as const,
      label: "Evidence",
      score: evidenceScore,
      bps: card.weights.evidenceBps,
    },
    ...card.reviews.map((r) => ({
      key:
        r.role === "code_reviewer"
          ? ("code_reviewer" as const)
          : ("llm" as const),
      label: reviewLabel(r),
      score: r.score,
      bps:
        r.role === "code_reviewer"
          ? card.weights.codeReviewerBps
          : card.weights.llmBps,
    })),
  ]

  return (
    <div className="flex flex-col gap-8">
      {/* How the total is made: weighted contributions on a 0-100 track. */}
      <div className="flex flex-col gap-3">
        <div className="relative h-3 bg-muted">
          <div className="flex h-full">
            {segments.map((s) => (
              <div
                key={s.key}
                className={cn(
                  "h-full border-e border-background last:border-e-0",
                  TONE[s.key]
                )}
                style={{ width: `${(s.score * s.bps) / 10000}%` }}
                title={`${s.label}: ${((s.score * s.bps) / 10000).toFixed(1)} points`}
              />
            ))}
          </div>
          {minScore !== null && (
            <div
              className="absolute -top-1 -bottom-1 w-px bg-foreground"
              style={{ left: `${minScore}%` }}
              aria-hidden
            />
          )}
        </div>
        <div className="flex flex-wrap items-baseline justify-between gap-x-6 gap-y-2 text-[11px] text-muted-foreground">
          <div className="flex flex-wrap gap-x-5 gap-y-1">
            {segments.map((s) => (
              <span key={s.key} className="inline-flex items-center gap-1.5">
                <span className={cn("size-2", TONE[s.key])} />
                <span className="text-foreground">{s.label}</span>
                <span className="tabular-nums">
                  {s.score} × {s.bps / 100}% ={" "}
                  {((s.score * s.bps) / 10000).toFixed(1)}
                </span>
              </span>
            ))}
          </div>
          <span className="tabular-nums">
            {score !== null && (
              <>
                total <span className="text-foreground">{score}</span>
              </>
            )}
            {minScore !== null && <> · min {minScore}</>}
          </span>
        </div>
      </div>

      <div className="grid gap-8 md:grid-cols-3 md:gap-6">
        <Column
          title="Evidence"
          score={evidenceScore}
          weight={card.weights.evidenceBps}
        >
          {card.evidence.map((c) => (
            <EvidenceRow key={c.check} check={c} />
          ))}
        </Column>
        {card.reviews.map((r) => (
          <Column
            key={r.role}
            title={reviewLabel(r)}
            score={r.score}
            weight={
              r.role === "code_reviewer"
                ? card.weights.codeReviewerBps
                : card.weights.llmBps
            }
          >
            {r.categories.length === 0 ? (
              <p className="text-muted-foreground">
                Stub score: no LLM reviewer was configured for this run.
              </p>
            ) : (
              r.categories.map((c) => (
                <li key={c.name} className="flex flex-col gap-1">
                  <div className="flex items-baseline justify-between gap-3">
                    <span>{humanize(c.name)}</span>
                    <Points points={c.points} max={c.max} />
                  </div>
                  <div className="h-1 bg-muted">
                    <div
                      className={cn("h-full", ratioTone(c.points, c.max))}
                      style={{ width: `${(c.points / c.max) * 100}%` }}
                    />
                  </div>
                </li>
              ))
            )}
          </Column>
        ))}
      </div>

      <div className="flex flex-col gap-2">
        <h3 className="text-[11px] text-muted-foreground">Eligibility</h3>
        <ul className="flex flex-wrap gap-2">
          {card.gates.map((g) => (
            <GateChip key={g.gate} gate={g} />
          ))}
        </ul>
      </div>

      {card.findings.length > 0 && (
        <div className="flex flex-col gap-2">
          <h3 className="text-[11px] text-muted-foreground">
            Where points were lost
          </h3>
          <ul className="flex flex-col divide-y border-y text-xs">
            {card.findings.map((f, i) => {
              const href = findingHref(repo, headSha, prNumber, f)
              return (
                <li
                  key={i}
                  className="flex flex-col gap-1 py-2.5 sm:flex-row sm:items-baseline sm:gap-3"
                >
                  <span className="flex shrink-0 items-center gap-2 sm:w-40">
                    <span
                      className={cn(
                        "size-2 shrink-0",
                        f.severity === "high"
                          ? "bg-destructive"
                          : f.severity === "medium"
                            ? "bg-ev-issue"
                            : "bg-muted-foreground/40"
                      )}
                      title={`${f.severity} severity`}
                    />
                    <span className="text-muted-foreground">
                      {[
                        PERSONA_LABEL[f.persona],
                        f.category && humanize(f.category),
                      ]
                        .filter(Boolean)
                        .join(" · ") || f.severity}
                    </span>
                  </span>
                  <span className="min-w-0 flex-1">{f.note}</span>
                  {href && (
                    <a
                      href={href}
                      target="_blank"
                      rel="noreferrer"
                      className="min-w-0 truncate text-[11px] text-muted-foreground underline-offset-4 hover:text-foreground hover:underline sm:max-w-[45%]"
                      title={f.file}
                    >
                      {f.file}
                      {f.lines && `:${f.lines}`}
                    </a>
                  )}
                </li>
              )
            })}
          </ul>
        </div>
      )}
    </div>
  )
}

function Column({
  title,
  score,
  weight,
  children,
}: {
  title: string
  score: number
  weight: number
  children: React.ReactNode
}) {
  return (
    <div className="flex min-w-0 flex-col gap-3">
      <div className="flex items-baseline justify-between gap-3 border-b pb-2">
        <h3 className="font-medium">{title}</h3>
        <span className="text-[11px] text-muted-foreground tabular-nums">
          <span className="font-heading text-base font-bold text-foreground">
            {score}
          </span>{" "}
          / 100 · {weight / 100}%
        </span>
      </div>
      <ul className="flex flex-col gap-3 text-xs">{children}</ul>
    </div>
  )
}

function EvidenceRow({ check }: { check: EvidenceCheck }) {
  return (
    <li className="flex items-start gap-2.5">
      <span
        className={cn(
          "mt-1 size-2 shrink-0",
          ratioTone(check.points, check.max)
        )}
      />
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex items-baseline justify-between gap-3">
          <span>{EVIDENCE_LABEL[check.check] ?? humanize(check.check)}</span>
          <Points points={check.points} max={check.max} />
        </div>
        <span
          className="truncate text-[11px] text-muted-foreground"
          title={check.detail}
        >
          {check.detail}
        </span>
      </div>
    </li>
  )
}

function Points({ points, max }: { points: number; max: number }) {
  return (
    <span className="shrink-0 tabular-nums">
      {points}
      <span className="text-muted-foreground">/{max}</span>
    </span>
  )
}

function GateChip({ gate }: { gate: Gate }) {
  const label = GATE_LABEL[gate.gate] ?? humanize(gate.gate)
  return (
    <li
      className={cn(
        "inline-flex h-6 items-center gap-1.5 border px-2 text-[11px]",
        !gate.required
          ? "border-dashed text-muted-foreground"
          : gate.passed
            ? "border-ev-push/40 bg-ev-push/10"
            : "border-destructive/40 bg-destructive/10 text-destructive"
      )}
      title={gate.required ? undefined : "Not required by this campaign"}
    >
      <span aria-hidden>{gate.passed ? "✓" : "✕"}</span>
      {label}
      {gate.detail && (
        <span className="tabular-nums opacity-70">{gate.detail}</span>
      )}
      {!gate.required && <span className="opacity-70">optional</span>}
    </li>
  )
}
