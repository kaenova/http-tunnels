import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import type { TunnelCreationLog } from "@/lib/api"
import { formatDateTime } from "@/lib/format"

export function RecentCreationList({
  logs,
}: {
  logs: TunnelCreationLog[]
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Recent tunnel creation requests</CardTitle>
        <CardDescription>
          Creation attempts are stored separately for analytics and auditing.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <div className="flex flex-col gap-3">
          {logs.map((log) => (
            <div
              key={log.id}
              className="flex flex-col gap-2 rounded-lg border bg-muted/30 p-3"
            >
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div className="flex flex-col gap-0.5">
                  <span className="font-medium">{log.domain || "Unknown domain"}</span>
                  <span className="text-xs text-muted-foreground">
                    {log.requestedSubdomain || "Random subdomain"}
                  </span>
                </div>
                <Badge variant={log.success ? "default" : "destructive"}>
                  {log.success ? "Success" : "Failed"}
                </Badge>
              </div>
              <div className="flex flex-col gap-1 text-sm text-muted-foreground">
                <span>{formatDateTime(log.createdAt)}</span>
                <span>{log.remoteAddr || "Unknown remote address"}</span>
                {log.errorMessage ? <span>{log.errorMessage}</span> : null}
              </div>
            </div>
          ))}
        </div>
      </CardContent>
    </Card>
  )
}
