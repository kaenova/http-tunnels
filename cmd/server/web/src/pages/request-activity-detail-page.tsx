import { useQuery } from "@tanstack/react-query"
import { Link, Navigate, useParams } from "react-router-dom"

import { PageHeader } from "@/components/page-header"
import { PageLoading } from "@/components/page-loading"
import { RequestLogDetailCard } from "@/components/request-log-detail-card"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { api } from "@/lib/api"
import { formatDateTime } from "@/lib/format"

export function RequestActivityDetailPage() {
  const params = useParams<{ requestId: string }>()
  const requestId = params.requestId

  const requestDetailQuery = useQuery({
    queryKey: ["request-activity-detail", requestId],
    queryFn: () => api.requestActivityDetail(requestId ?? ""),
    enabled: !!requestId,
    refetchInterval: 5000,
  })

  if (!requestId) {
    return <Navigate to="/admin/request-activity" replace />
  }

  if (requestDetailQuery.isLoading) {
    return <PageLoading />
  }

  if (requestDetailQuery.isError || !requestDetailQuery.data) {
    return (
      <div className="p-6">
        <p className="text-sm text-destructive">
          {(requestDetailQuery.error as Error)?.message ||
            "The request detail page could not be loaded."}
        </p>
      </div>
    )
  }

  const log = requestDetailQuery.data

  return (
    <div className="flex flex-col">
      <PageHeader
        title={`Request ${log.id}`}
        description="Detailed request-response record captured by the server analytics pipeline."
        breadcrumbs={[
          { label: "Admin", href: "/admin" },
          { label: "Request Activity", href: "/admin/request-activity" },
          { label: log.id },
        ]}
        actions={
          <Button asChild variant="outline">
            <Link to="/admin/request-activity">Back to activity</Link>
          </Button>
        }
      />
      <div className="flex flex-col gap-6 p-6">
        <Card>
          <CardHeader>
            <CardTitle>Request metadata</CardTitle>
            <CardDescription>
              High-level metadata for the selected request record.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
              <DetailItem label="Request ID" value={log.id} />
              <DetailItem label="Tunnel ID" value={log.tunnelId} />
              <DetailItem label="Domain" value={log.domain} />
              <DetailItem label="Started at" value={formatDateTime(log.startedAt)} />
            </div>
          </CardContent>
        </Card>

        <RequestLogDetailCard log={log} />
      </div>
    </div>
  )
}

function DetailItem({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-1 rounded-lg border bg-muted/30 p-4">
      <span className="text-xs uppercase tracking-wide text-muted-foreground">
        {label}
      </span>
      <span className="break-all text-sm font-medium">{value}</span>
    </div>
  )
}
