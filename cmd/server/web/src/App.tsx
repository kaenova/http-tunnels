import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { Navigate, RouterProvider, createBrowserRouter } from "react-router-dom"
import { Toaster } from "sonner"

import { TooltipProvider } from "@/components/ui/tooltip"
import { AdminLayout } from "@/pages/admin-layout"
import { DashboardPage } from "@/pages/dashboard-page"
import { LoginPage } from "@/pages/login-page"
import { RequestActivityDetailPage } from "@/pages/request-activity-detail-page"
import { RequestActivityPage } from "@/pages/request-activity-page"
import { TunnelDetailPage } from "@/pages/tunnel-detail-page"
import { TunnelsPage } from "@/pages/tunnels-page"

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 5_000,
      retry: false,
    },
  },
})

const router = createBrowserRouter([
  {
    path: "/admin/auth/login",
    element: <LoginPage />,
  },
  {
    path: "/admin",
    element: <AdminLayout />,
    children: [
      {
        index: true,
        element: <DashboardPage />,
      },
      {
        path: "tunnels",
        element: <TunnelsPage />,
      },
      {
        path: "tunnels/:tunnelId",
        element: <TunnelDetailPage />,
      },
      {
        path: "request-activity",
        element: <RequestActivityPage />,
      },
      {
        path: "request-activity/:requestId",
        element: <RequestActivityDetailPage />,
      },
    ],
  },
  {
    path: "*",
    element: <Navigate to="/admin" replace />,
  },
])

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <TooltipProvider>
        <RouterProvider router={router} />
        <Toaster richColors position="top-right" />
      </TooltipProvider>
    </QueryClientProvider>
  )
}
