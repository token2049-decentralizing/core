"use client"

import * as React from "react"
import Link from "next/link"
import { usePathname, useRouter } from "next/navigation"
import { useTheme } from "next-themes"
import { RiMoonLine, RiSearchLine, RiSunLine } from "@remixicon/react"

import { getCampaign } from "@/lib/api"
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Separator } from "@/components/ui/separator"
import { SidebarTrigger } from "@/components/ui/sidebar"
import { WalletButton } from "@/components/wallet-button"

type Crumb = { label: string; href?: string }

function shortId(id: string) {
  return id.length > 12 ? `${id.slice(0, 8)}…` : id
}

function crumbsFor(pathname: string): Crumb[] {
  const parts = pathname.split("/").filter(Boolean).map(decodeURIComponent)
  if (parts.length === 0) return [{ label: "Overview" }]

  if (parts[0] === "campaigns") {
    if (parts.length === 1) return [{ label: "Campaigns" }]
    return [
      { label: "Campaigns", href: "/campaigns" },
      { label: parts[1] === "new" ? "New campaign" : shortId(parts[1]) },
    ]
  }

  if (parts[0] === "repos" && parts.length >= 3) {
    return [
      { label: "Overview", href: "/" },
      { label: `${parts[1]}/${parts[2]}` },
    ]
  }

  if (parts[0] === "executions") {
    if (parts.length === 1) return [{ label: "CRE executions" }]
    return [
      { label: "CRE executions", href: "/executions" },
      { label: shortId(parts[1]) },
    ]
  }

  if (parts[0] === "me") return [{ label: "My rewards" }]

  if (parts[0] === "deliveries" && parts[1]) {
    return [{ label: "Deliveries" }, { label: shortId(parts[1]) }]
  }

  return [{ label: parts.join(" / ") }]
}

export function AppToolbar() {
  const pathname = usePathname()
  const crumbs = crumbsFor(pathname)

  return (
    <header className="sticky top-0 z-20 flex h-12 shrink-0 items-center gap-2 border-b bg-background/85 px-3 backdrop-blur supports-backdrop-filter:bg-background/70 md:px-4">
      <SidebarTrigger className="-ms-1" />
      <Separator orientation="vertical" className="me-1 h-4 self-center!" />
      <Breadcrumb className="min-w-0">
        <BreadcrumbList className="flex-nowrap">
          {crumbs.map((crumb, i) => (
            <React.Fragment key={i}>
              {i > 0 && <BreadcrumbSeparator />}
              <BreadcrumbItem className="min-w-0">
                {crumb.href ? (
                  <BreadcrumbLink render={<Link href={crumb.href} />}>
                    {crumb.label}
                  </BreadcrumbLink>
                ) : (
                  <BreadcrumbPage className="truncate">
                    {crumb.label}
                  </BreadcrumbPage>
                )}
              </BreadcrumbItem>
            </React.Fragment>
          ))}
        </BreadcrumbList>
      </Breadcrumb>
      <div className="ms-auto flex items-center gap-1.5">
        <IdLookup />
        <ThemeToggle />
        <WalletButton />
      </div>
    </header>
  )
}

// Delivery IDs and campaign IDs are both UUIDs, so try the campaign first and
// fall back to the delivery page (which shows its own not-found state).
function IdLookup() {
  const router = useRouter()
  const inputRef = React.useRef<HTMLInputElement>(null)
  const [pending, setPending] = React.useState(false)

  React.useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      const target = event.target as HTMLElement | null
      if (
        event.key !== "/" ||
        target?.isContentEditable ||
        target?.tagName === "INPUT" ||
        target?.tagName === "TEXTAREA"
      ) {
        return
      }
      event.preventDefault()
      inputRef.current?.focus()
    }
    window.addEventListener("keydown", onKeyDown)
    return () => window.removeEventListener("keydown", onKeyDown)
  }, [])

  async function onSubmit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const id = new FormData(event.currentTarget).get("id")?.toString().trim()
    if (!id) return

    setPending(true)
    try {
      await getCampaign(id)
      router.push(`/campaigns/${encodeURIComponent(id)}`)
    } catch {
      router.push(`/deliveries/${encodeURIComponent(id)}`)
    } finally {
      setPending(false)
      inputRef.current?.blur()
    }
  }

  return (
    <form
      onSubmit={onSubmit}
      role="search"
      className="relative hidden sm:block"
    >
      <RiSearchLine className="pointer-events-none absolute start-2 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
      <Input
        ref={inputRef}
        name="id"
        aria-label="Open a delivery or campaign by ID"
        placeholder="Delivery or campaign ID"
        autoComplete="off"
        spellCheck={false}
        disabled={pending}
        className="h-8 w-56 ps-7 pe-7 lg:w-72"
      />
      <kbd className="pointer-events-none absolute end-2 top-1/2 -translate-y-1/2 border px-1 text-[10px] leading-4 text-muted-foreground">
        /
      </kbd>
    </form>
  )
}

function ThemeToggle() {
  const { resolvedTheme, setTheme } = useTheme()

  return (
    <Button
      variant="ghost"
      size="icon-sm"
      aria-label="Toggle dark mode"
      title="Toggle dark mode (D)"
      onClick={() => setTheme(resolvedTheme === "dark" ? "light" : "dark")}
    >
      <RiSunLine className="hidden dark:block" />
      <RiMoonLine className="dark:hidden" />
    </Button>
  )
}
