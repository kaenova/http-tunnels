import { useMemo, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  ActivityIcon,
  ArrowUpDownIcon,
  NetworkIcon,
  RouterIcon,
  ZapIcon,
} from "lucide-react"
import { toast } from "sonner"

import type { TunnelRecord } from "@/lib/api"
import { api, ApiError } from "@/lib/api"
import {
  chartFiltersDescription,
  defaultChartFilters,
  normalizeChartFilters,
} from "@/lib/chart-options"
import { formatBytes, formatCount } from "@/lib/format"
import { MetricCard } from "@/components/metric-card"
import { PageHeader } from "@/components/page-header"
import { RecentCreationList } from "@/components/recent-creation-list"
import { RecentRequestTable } from "@/components/recent-request-table"
import { TunnelTable } from "@/components/tunnel-table"
import { DeleteTunnelDialog } from "@/components/delete-tunnel-dialog"
import { PageLoading } from "@/components/page-loading"
import { ChartFiltersCard } from "@/components/charts/chart-filters-card"
import { StatusChartCard } from "@/components/charts/status-chart-card"
import { TrafficChartCard } from "@/components/charts/traffic-chart-card"

export function DashboardPage() {
  const queryClient = useQueryClient()
  const [selectedTunnel, setSelectedTunnel] = useState<TunnelRecord | null>(null)
  const [chartFilters, setChartFilters] = useState(defaultChartFilters)
  const [draftChartFilters, setDraftChartFilters] = useState(defaultChartFilters)

  const dashboardQuery = useQuery({
    queryKey: ["dashboard", chartFilters],
    queryFn: () => api.dashboard(chartFilters),
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
        title: "HTTP/2 active",
        value: formatCount(summary.activeHttp2Tunnels),
        description: "Active tunnels currently using the preferred HTTP/2 transport.",
        icon: NetworkIcon,
      },
      {
        title: "Websocket fallback",
        value: formatCount(summary.activeWebsocketTunnels),
        description: "Active tunnels currently running on websocket fallback.",
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
        title: "Registered tunnel identities",
        value: formatCount(summary.registeredTunnels),
        description: "Known tunnel domains retained in analytics history.",
        icon: RouterIcon,
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

  const chartDescription = chartFiltersDescription(chartFilters)

  return (
    <div className="flex flex-col">
      <PageHeader
        title="Dashboard"
        description="Monitor active connections, recent request-response traffic, and tunnel creation analytics."
        breadcrumbs={[{ label: "Admin", href: "/admin" }, { label: "Dashboard" }]}
      />
      <div className="flex flex-col gap-6 p-6">
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-6">
          {metrics.map((metric) => (
            <MetricCard key={metric.title} {...metric} />
          ))}
        </div>

        <ChartFiltersCard
          value={draftChartFilters}
          onChange={setDraftChartFilters}
          onApply={() => setChartFilters(normalizeChartFilters(draftChartFilters))}
          onReset={() => {
            setDraftChartFilters(defaultChartFilters)
            setChartFilters(defaultChartFilters)
          }}
        />

        <div className="grid gap-6 xl:grid-cols-2">
          <StatusChartCard
            data={dashboardQuery.data.statusChart}
            description={`Total status-code volume across all tunnels · ${chartDescription}`}
          />
          <TrafficChartCard
            data={dashboardQuery.data.trafficChart}
            description={`Total inbound and outbound tunnel traffic · ${chartDescription}`}
          />
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
