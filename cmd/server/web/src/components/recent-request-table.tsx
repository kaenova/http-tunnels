import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import type { RequestResponseLog } from "@/lib/api"
import { formatBytes, formatDateTime, formatDurationMs } from "@/lib/format"
import { HttpStatusBadge } from "@/components/status-badge"

export function RecentRequestTable({
  requests,
}: {
  requests: RequestResponseLog[]
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Recent request activity</CardTitle>
        <CardDescription>
          The latest request and response records captured by the tunnel server.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <div className="overflow-hidden rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Method</TableHead>
                <TableHead>Path</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Transferred</TableHead>
                <TableHead>Duration</TableHead>
                <TableHead>Started</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {requests.map((request) => (
                <TableRow key={request.id}>
                  <TableCell className="font-mono text-xs font-medium uppercase">
                    {request.method}
                  </TableCell>
                  <TableCell className="max-w-[20rem] truncate">
                    {request.path}
                  </TableCell>
                  <TableCell>
                    <HttpStatusBadge statusCode={request.statusCode} />
                  </TableCell>
                  <TableCell>
                    {formatBytes(request.requestBytes + request.responseBytes)}
                  </TableCell>
                  <TableCell>{formatDurationMs(request.durationMs)}</TableCell>
                  <TableCell>{formatDateTime(request.startedAt)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      </CardContent>
    </Card>
  )
}
