import { Link, useLocation } from "react-router-dom"
import {
  ActivityIcon,
  FileClockIcon,
  LayoutDashboardIcon,
  LogOutIcon,
} from "lucide-react"

import { cn } from "@/lib/utils"

import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarSeparator,
  useSidebar,
} from "@/components/ui/sidebar"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { Button } from "@/components/ui/button"

const menuItems = [
  {
    title: "Dashboard",
    href: "/admin",
    icon: LayoutDashboardIcon,
  },
  {
    title: "Active Subdomain",
    href: "/admin/tunnels",
    icon: ActivityIcon,
  },
  {
    title: "Request Activity",
    href: "/admin/request-activity",
    icon: FileClockIcon,
  },
]

export function AppSidebar() {
  const location = useLocation()
  const { state, toggleSidebar } = useSidebar()
  const isCollapsed = state === "collapsed"

  return (
    <Sidebar collapsible="icon">
      <SidebarHeader>
        {isCollapsed ? (
          <Button
            variant="outline"
            size="icon"
            className="size-10"
            onClick={toggleSidebar}
          >
            <ActivityIcon />
            <span className="sr-only">Expand sidebar</span>
          </Button>
        ) : (
          <Link
            to="/admin"
            className={cn(
              "flex items-center gap-3 rounded-lg border border-sidebar-border bg-background px-3 py-3 transition-[padding,gap]",
              "group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:gap-0 group-data-[collapsible=icon]:px-2"
            )}
          >
            <div className="flex size-10 items-center justify-center rounded-md bg-sidebar-primary text-sidebar-primary-foreground">
              <ActivityIcon />
            </div>
            <div className="flex min-w-0 flex-col gap-0.5 group-data-[collapsible=icon]:hidden">
              <span className="truncate font-heading text-sm font-semibold">
                HTTP Tunnels
              </span>
              <span className="truncate text-xs text-muted-foreground">
                Admin Console
              </span>
            </div>
          </Link>
        )}
      </SidebarHeader>

      <SidebarSeparator />

      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupLabel>Navigation</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              {menuItems.map((item) => {
                const isActive =
                  item.href === "/admin"
                    ? location.pathname === "/admin"
                    : location.pathname.startsWith(item.href)

                return (
                  <SidebarMenuItem key={item.href}>
                    <SidebarMenuButton asChild isActive={isActive} tooltip={item.title}>
                      <Link to={item.href}>
                        <item.icon />
                        <span>{item.title}</span>
                      </Link>
                    </SidebarMenuButton>
                  </SidebarMenuItem>
                )
              })}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>

      {!isCollapsed ? <SidebarSeparator /> : null}

      {!isCollapsed ? (
        <SidebarFooter>
          <div
            className={cn(
              "flex items-center gap-3 rounded-lg border border-sidebar-border bg-background px-3 py-3 transition-[padding,gap]",
              "group-data-[collapsible=icon]:flex-col group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:gap-2 group-data-[collapsible=icon]:px-2"
            )}
          >
            <Avatar>
              <AvatarFallback>AU</AvatarFallback>
            </Avatar>
            <div className="flex min-w-0 flex-1 flex-col gap-0.5 group-data-[collapsible=icon]:hidden">
              <span className="truncate text-sm font-medium">Admin User Profile</span>
              <span className="truncate text-xs text-muted-foreground">
                Tunnel administrator
              </span>
            </div>
            <a
              href="/admin/auth/logout"
              className="text-muted-foreground transition-colors hover:text-foreground"
            >
              <LogOutIcon />
              <span className="sr-only">Logout</span>
            </a>
          </div>
        </SidebarFooter>
      ) : null}
    </Sidebar>
  )
}
