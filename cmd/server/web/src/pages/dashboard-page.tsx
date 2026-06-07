import { useMemo, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  ActivityIcon,
  ArrowDownIcon,
  ArrowUpIcon,
  ArrowUpDownIcon,
  NetworkIcon,
  RouterIcon,
  ZapIcon,
} from "lucide-react"
import { toast } from "sonner"
import { format, subDays } from "date-fns"

import type { TunnelRecord, ChartRange } from "@/lib/api"
import { api, ApiError } from "@/lib/api"
import { formatBytes, formatCount } from "@/lib/format"
import { MetricCard } from "@/components/metric-card"
import { PageHeader } from "@/components/page-header"
import { RecentCreationList } from "@/components/recent-creation-list"
import { RecentRequestTable } from "@/components/recent-request-table"
import { TunnelTable } from "@/components/tunnel-table"
import { DeleteTunnelDialog } from "@/components/delete-tunnel-dialog"
import { PageLoading } from "@/components/page-loading"
import { StatusChartCard } from "@/components/charts/status-chart-card"
import { TrafficChartCard } from "@/components/charts/traffic-chart-card"
import { ChartRangePicker } from "@/components/chart-range-picker"

const defaultRange: ChartRange = {
  granularity: "day",
  startDate: format(subDays(new Date(), 6), "yyyy-MM-dd"),
  endDate: format(new Date(), "yyyy-MM-dd"),
}

export function DashboardPage() {
  const queryClient = useQueryClient()
  const [selectedTunnel, setSelectedTunnel] = useState<TunnelRecord | null>(null)
  const [chartRange, setChartRange] = useState<ChartRange>(defaultRange)

  const dashboardQuery = useQuery({
    queryKey: ["dashboard", chartRange],
    queryFn: () => api.dashboard(chartRange),
    refetchInterval: 5000,
  })

  const deleteMutation = useMutation({
    mutationFn: (tunnelId: string) => api.deleteTunnel(tunnelId),
    onSuccess: async () => {
      toast.success("Tunnel deleted and active connection closed.")
      setSelectedTunnel(null)
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["dashboard"] }),
        queryClient.invalidateQueries({ queryKey: ["tunnels"] }),
      ])
    },
    onError: (error: ApiError) => {
      toast.error(error.message)
    },
  })

  const metrics = useMemo(() => {
    const summary = dashboardQuery.data?.summary
    if (!summary) {
      return []
    }

    return [
      {
        title: "Active connections",
        value: formatCount(summary.activeTunnels),
        description: "Currently connected tunnel clients.",
        icon: NetworkIcon,
      },
      {
        title: "Active traffic",
        value: formatCount(summary.activeTraffic),
        description: "In-flight requests across all active tunnels right now.",
        icon: ZapIcon,
      },
      {
        title: "Recorded requests",
        value: formatCount(summary.totalRequests),
        description: "Total request-response log entries stored.",
        icon: ActivityIcon,
      },
      {
        title: "Transferred data",
        value: formatBytes(summary.dataTransferredBytes),
        description: "Inbound and outbound bytes tracked in analytics.",
        icon: ArrowUpDownIcon,
      },
      {
        title: "Inbound data",
        value: formatBytes(summary.totalRequestBytes),
        description: "Total request bytes from clients.",
        icon: ArrowDownIcon,
      },
      {
        title: "Outbound data",
        value: formatBytes(summary.totalResponseBytes),
        description: "Total response bytes to clients.",
        icon: ArrowUpIcon,
      },
      {
        title: "Registered tunnel identities",
        value: formatCount(summary.registeredTunnels),
        description: "Known tunnel domains retained in analytics history.",
        icon: RouterIcon,
      },
      {
        title: "Server version",
        value: summary.serverVersion || "dev",
        description: "Current running server build version.",
        icon: ActivityIcon,
      },
    ]
  }, [dashboardQuery.data?.summary])

  if (dashboardQuery.isLoading) {
    return <PageLoading />
  }

  if (dashboardQuery.isError || !dashboardQuery.data) {
    return (
      <div className="p-6">
        <p className="text-sm text-destructive">
          {(dashboardQuery.error as Error)?.message ||
            "The dashboard data could not be loaded."}
        </p>
      </div>
    )
  }

  return (
    <div className="flex flex-col">
      <PageHeader
        title="Dashboard"
        description="Monitor active connections, recent request-response traffic, and tunnel creation analytics."
        breadcrumbs={[{ label: "Admin", href: "/admin" }, { label: "Dashboard" }]}
      />
      <div className="flex flex-col gap-6 p-6">
        <div className="grid gap-4 lg:grid-cols-4">
          {metrics.map((metric) => (
            <MetricCard key={metric.title} {...metric} />
          ))}
        </div>

        <div className="flex flex-col gap-4">
          <h2 className="text-lg font-semibold">Traffic overview</h2>
          <ChartRangePicker value={chartRange} onChange={setChartRange} />
          <div className="grid gap-6 xl:grid-cols-2">
            <StatusChartCard data={dashboardQuery.data.statusChart} />
            <TrafficChartCard data={dashboardQuery.data.trafficChart} />
          </div>
        </div>

        <TunnelTable
          title="Active connections"
          description="Connected or registered tunnel subdomains that the admin can inspect or remove."
          tunnels={dashboardQuery.data.activeTunnels}
          onDelete={(tunnel) => setSelectedTunnel(tunnel)}
          deletingId={deleteMutation.isPending ? selectedTunnel?.id : undefined}
        />

        <div className="grid gap-6 xl:grid-cols-[2fr_1fr]">
          <RecentRequestTable requests={dashboardQuery.data.recentRequests} />
          <RecentCreationList logs={dashboardQuery.data.recentTunnelCreates} />
        </div>
      </div>

      <DeleteTunnelDialog
        tunnel={selectedTunnel}
        open={!!selectedTunnel}
        onOpenChange={(open) => {
          if (!open) {
            setSelectedTunnel(null)
          }
        }}
        onConfirm={() => {
          if (selectedTunnel) {
            deleteMutation.mutate(selectedTunnel.id)
          }
        }}
        isDeleting={deleteMutation.isPending}
      />
    </div>
  )
}
