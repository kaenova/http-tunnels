import { useQuery } from "@tanstack/react-query"
import { Navigate, Outlet, useLocation } from "react-router-dom"

import { AppSidebar } from "@/components/app-sidebar"
import { PageLoading } from "@/components/page-loading"
import { SidebarInset, SidebarProvider, SidebarTrigger } from "@/components/ui/sidebar"
import { Separator } from "@/components/ui/separator"
import { api } from "@/lib/api"

export function AdminLayout() {
  const location = useLocation()
  const sessionQuery = useQuery({
    queryKey: ["admin-session"],
    queryFn: api.session,
    retry: false,
    refetchOnWindowFocus: false,
  })

  if (sessionQuery.isLoading) {
    return <PageLoading />
  }

  if (!sessionQuery.data?.configured) {
    return <Navigate to="/admin/auth/login" replace />
  }

  if (!sessionQuery.data?.authenticated) {
    return (
      <Navigate
        to="/admin/auth/login"
        replace
        state={{ redirectTo: location.pathname }}
      />
    )
  }

  return (
    <SidebarProvider>
      <AppSidebar />
      <SidebarInset>
        <div className="flex h-14 items-center gap-3 border-b px-4">
          <SidebarTrigger />
          <Separator orientation="vertical" className="h-5" />
          <div className="flex flex-col">
            <span className="text-sm font-medium">HTTP Tunnels Admin</span>
            <span className="text-xs text-muted-foreground">
              Observe active connections and request-response analytics.
            </span>
          </div>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto">
          <Outlet />
        </div>
      </SidebarInset>
    </SidebarProvider>
  )
}
