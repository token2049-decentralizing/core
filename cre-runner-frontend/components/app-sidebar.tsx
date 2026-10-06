import { listRepos, type Repo } from "@/lib/api"
import { ApiStatus } from "@/components/api-status"
import { SignalMark } from "@/components/signal-mark"
import { SidebarNav, SidebarRepos } from "@/components/sidebar-nav"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarHeader,
  SidebarRail,
} from "@/components/ui/sidebar"

export async function AppSidebar() {
  let repos: Repo[] | null = null
  try {
    repos = await listRepos()
  } catch {
    repos = null
  }

  return (
    <Sidebar collapsible="icon">
      <SidebarHeader className="h-12 justify-center border-b border-sidebar-border">
        <div className="flex items-center gap-2.5 px-1 group-data-[collapsible=icon]:px-0">
          <SignalMark className="size-6 shrink-0" />
          <div className="flex min-w-0 flex-col leading-tight group-data-[collapsible=icon]:hidden">
            <span className="font-heading text-sm font-bold tracking-tight">
              cre-runner
            </span>
            <span className="truncate text-[11px] text-muted-foreground">
              PR rewards from GitHub activity
            </span>
          </div>
        </div>
      </SidebarHeader>
      <SidebarContent>
        <SidebarNav />
        <SidebarRepos repos={repos} />
      </SidebarContent>
      <SidebarFooter className="border-t border-sidebar-border">
        <ApiStatus />
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  )
}
