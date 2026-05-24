import { Link } from "react-router-dom"
import { MoreHorizontalIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
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
import { FileClockIcon } from "lucide-react"

export function RequestActivityTable({
  logs,
}: {
  logs: RequestResponseLog[]
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Request activity</CardTitle>
        <CardDescription>
          All recorded request-response traffic for this server, across every tunnel.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {logs.length === 0 ? (
          <Empty className="border">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <FileClockIcon />
              </EmptyMedia>
              <EmptyTitle>No request activity found</EmptyTitle>
              <EmptyDescription>
                Adjust the filters or wait for new tunneled traffic to be recorded.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <div className="overflow-hidden rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Domain</TableHead>
                  <TableHead>Method</TableHead>
                  <TableHead>Path</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Transferred</TableHead>
                  <TableHead>Duration</TableHead>
                  <TableHead>Started</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {logs.map((log) => (
                  <TableRow key={log.id}>
                    <TableCell className="max-w-[14rem] truncate font-medium">
                      {log.domain}
                    </TableCell>
                    <TableCell className="font-mono text-xs font-medium uppercase">
                      {log.method}
                    </TableCell>
                    <TableCell className="max-w-[18rem] truncate">
                      {log.path}
                    </TableCell>
                    <TableCell>
                      <HttpStatusBadge statusCode={log.statusCode} />
                    </TableCell>
                    <TableCell>
                      {formatBytes(log.requestBytes + log.responseBytes)}
                    </TableCell>
                    <TableCell>{formatDurationMs(log.durationMs)}</TableCell>
                    <TableCell>{formatDateTime(log.startedAt)}</TableCell>
                    <TableCell className="text-right">
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button variant="ghost" size="icon">
                            <MoreHorizontalIcon />
                            <span className="sr-only">Open actions</span>
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuGroup>
                            <DropdownMenuItem asChild>
                              <Link to={`/admin/request-activity/${log.id}`}>Details</Link>
                            </DropdownMenuItem>
                          </DropdownMenuGroup>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>
    </Card>
  )
}
