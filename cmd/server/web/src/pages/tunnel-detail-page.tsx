import type { ReactNode } from "react"
import { useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Link, Navigate, useNavigate, useParams } from "react-router-dom"
import { toast } from "sonner"
import { format, subDays } from "date-fns"

import { api, ApiError, type ChartRange } from "@/lib/api"
import { formatBytes, formatDateTime, formatDurationFrom } from "@/lib/format"
import { DeleteTunnelDialog } from "@/components/delete-tunnel-dialog"
import { PageHeader } from "@/components/page-header"
import { PageLoading } from "@/components/page-loading"
import { RecentCreationList } from "@/components/recent-creation-list"
import { RequestLogList } from "@/components/request-log-list"
import { TunnelStateBadge } from "@/components/status-badge"
import { StatusChartCard } from "@/components/charts/status-chart-card"
import { TrafficChartCard } from "@/components/charts/traffic-chart-card"
import { ChartRangePicker } from "@/components/chart-range-picker"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"

const defaultRange: ChartRange = {
  granularity: "day",
  startDate: format(subDays(new Date(), 6), "yyyy-MM-dd"),
  endDate: format(new Date(), "yyyy-MM-dd"),
}

const recentLogLimit = 5

export function TunnelDetailPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const params = useParams<{ tunnelId: string }>()
  const tunnelId = params.tunnelId
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [chartRange, setChartRange] = useState<ChartRange>(defaultRange)

  const detailQuery = useQuery({
    queryKey: ["tunnel-detail", tunnelId, recentLogLimit, chartRange],
    queryFn: () => api.tunnelDetail(tunnelId ?? "", 1, recentLogLimit, chartRange),
    enabled: !!tunnelId,
    refetchInterval: 5000,
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.deleteTunnel(id),
    onSuccess: async () => {
      toast.success("Tunnel deleted.")
      setDeleteOpen(false)
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["dashboard"] }),
        queryClient.invalidateQueries({ queryKey: ["tunnels"] }),
      ])
      navigate("/admin/tunnels", { replace: true })
    },
    onError: (error: ApiError) => {
      toast.error(error.message)
    },
  })

  if (!tunnelId) {
    return <Navigate to="/admin/tunnels" replace />
  }

  if (detailQuery.isLoading) {
    return <PageLoading />
  }

  if (detailQuery.isError || !detailQuery.data) {
    return (
      <div className="p-6">
        <p className="text-sm text-destructive">
          {(detailQuery.error as Error)?.message ||
            "The tunnel detail view could not be loaded."}
        </p>
      </div>
    )
  }

  const { tunnel } = detailQuery.data

  return (
    <div className="flex flex-col">
      <PageHeader
        title={tunnel.domain}
        description="Tunnel details, analytics charts, and request-response logging."
        breadcrumbs={[
          { label: "Admin", href: "/admin" },
          { label: "Active Subdomain", href: "/admin/tunnels" },
          { label: tunnel.domain },
        ]}
        actions={
          <Button variant="outline" onClick={() => setDeleteOpen(true)}>
            Delete tunnel
          </Button>
        }
      />
      <div className="flex flex-col gap-6 p-6">
        <Card>
          <CardHeader>
            <CardTitle>Tunnel details</CardTitle>
            <CardDescription>
              Core tunnel metadata stored in the server-side SQLite database.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
              <DetailItem label="Tunnel subdomain" value={tunnel.domain} />
              <DetailItem label="Created when" value={formatDateTime(tunnel.createdAt)} />
              <DetailItem label="How long active" value={formatDurationFrom(tunnel.createdAt)} />
              <DetailItem
                label="Total data transferred"
                value={formatBytes(
                  tunnel.totalRequestBytes + tunnel.totalResponseBytes
                )}
              />
              <DetailItem label="Requests recorded" value={String(tunnel.requestCount)} />
              <DetailItem
                label="Last activity"
                value={formatDateTime(tunnel.lastActivityAt)}
              />
              <DetailItem
                label="Connection state"
                value={<TunnelStateBadge state={tunnel.state} />}
              />
              <DetailItem
                label="Connected since"
                value={formatDateTime(tunnel.connectedAt)}
              />
              <DetailItem
                label="Client version"
                value={tunnel.clientVersion || "unknown"}
              />
              <DetailItem
                label="Remote address"
                value={tunnel.remoteAddr || "unknown"}
              />
              <DetailItem
                label="User agent"
                value={tunnel.userAgent || "unknown"}
              />
            </div>
          </CardContent>
        </Card>

        <div className="flex flex-col gap-4">
          <h2 className="text-lg font-semibold">Traffic analytics</h2>
          <ChartRangePicker value={chartRange} onChange={setChartRange} />
          <div className="grid gap-6 xl:grid-cols-2">
            <StatusChartCard data={detailQuery.data.statusChart} />
            <TrafficChartCard data={detailQuery.data.trafficChart} />
          </div>
        </div>

        <RequestLogList
          logs={detailQuery.data.logs.items}
          description={
            <>
              Recent request-response logs only. If you want to see more detail, go into{" "}
              <Link
                to="/admin/request-activity"
                className="underline underline-offset-4 hover:text-foreground"
              >
                Request Activity
              </Link>
              .
            </>
          }
          emptyDescription={
            <>
              Send traffic through this tunnel to populate the recent analytics view.
              If you want to see more detail, go into Request Activity.
            </>
          }
        />

        <RecentCreationList logs={detailQuery.data.creationHistory} />
      </div>

      <DeleteTunnelDialog
        tunnel={tunnel}
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        onConfirm={() => deleteMutation.mutate(tunnel.id)}
        isDeleting={deleteMutation.isPending}
      />
    </div>
  )
}

function DetailItem({
  label,
  value,
}: {
  label: string
  value: ReactNode
}) {
  return (
    <div className="flex flex-col gap-1 rounded-lg border bg-muted/30 p-4">
      <span className="text-xs uppercase tracking-wide text-muted-foreground">
        {label}
      </span>
      <div className="text-sm font-medium">{value}</div>
    </div>
  )
}
