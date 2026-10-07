"use client"

import Link from "next/link"
import { usePathname } from "next/navigation"
import {
  RiAddLine,
  RiDashboard3Line,
  RiPulseLine,
  RiTrophyLine,
  RiWallet3Line,
} from "@remixicon/react"

import type { Repo } from "@/lib/api"
import { formatCount } from "@/lib/format"
import {
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar"

export function SidebarNav() {
  const pathname = usePathname()

  return (
    <SidebarGroup>
      <SidebarGroupContent>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton
              isActive={pathname === "/"}
              tooltip="Overview"
              render={<Link href="/" />}
            >
              <RiDashboard3Line />
              <span>Overview</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
          <SidebarMenuItem>
            <SidebarMenuButton
              isActive={
                pathname.startsWith("/campaigns") &&
                pathname !== "/campaigns/new"
              }
              tooltip="Campaigns"
              render={<Link href="/campaigns" />}
            >
              <RiTrophyLine />
              <span>Campaigns</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
          <SidebarMenuItem>
            <SidebarMenuButton
              isActive={pathname.startsWith("/executions")}
              tooltip="CRE executions"
              render={<Link href="/executions" />}
            >
              <RiPulseLine />
              <span>CRE executions</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
          <SidebarMenuItem>
            <SidebarMenuButton
              isActive={pathname === "/me"}
              tooltip="My rewards"
              render={<Link href="/me" />}
            >
              <RiWallet3Line />
              <span>My rewards</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
          <SidebarMenuItem>
            <SidebarMenuButton
              isActive={pathname === "/campaigns/new"}
              tooltip="New campaign"
              render={<Link href="/campaigns/new" />}
              className="text-muted-foreground"
            >
              <RiAddLine />
              <span>New campaign</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarGroupContent>
    </SidebarGroup>
  )
}

export function SidebarRepos({ repos }: { repos: Repo[] | null }) {
  const pathname = usePathname()

  return (
    <SidebarGroup className="group-data-[collapsible=icon]:hidden">
      <SidebarGroupLabel>Repositories</SidebarGroupLabel>
      <SidebarGroupContent>
        {repos === null ? (
          <p className="px-2 py-1 text-xs text-muted-foreground">
            Couldn&apos;t load repositories.
          </p>
        ) : repos.length === 0 ? (
          <p className="px-2 py-1 text-xs leading-relaxed text-muted-foreground">
            Repositories appear here once their first webhook arrives.
          </p>
        ) : (
          <SidebarMenu>
            {repos.map((repo) => {
              const href = `/repos/${repo.repository_full_name}`
              const [owner, name] = repo.repository_full_name.split("/")
              return (
                <SidebarMenuItem key={repo.repository_full_name}>
                  <SidebarMenuButton
                    isActive={pathname === href}
                    render={<Link href={href} />}
                    title={repo.repository_full_name}
                  >
                    <span className="truncate">
                      <span className="text-muted-foreground">{owner}/</span>
                      {name}
                    </span>
                  </SidebarMenuButton>
                  <SidebarMenuBadge className="text-muted-foreground tabular-nums">
                    {formatCount(repo.event_count)}
                  </SidebarMenuBadge>
                </SidebarMenuItem>
              )
            })}
          </SidebarMenu>
        )}
      </SidebarGroupContent>
    </SidebarGroup>
  )
}
