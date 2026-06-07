import { Link } from "react-router-dom"
import { MoreHorizontalIcon, RouterIcon } from "lucide-react"

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
import type { TunnelRecord } from "@/lib/api"
import { formatBytes, formatCount, formatDurationFrom } from "@/lib/format"
import { TunnelStateBadge } from "@/components/status-badge"

export function TunnelTable({
  title,
  description,
  tunnels,
  onDelete,
  deletingId,
}: {
  title: string
  description: string
  tunnels: TunnelRecord[]
  onDelete: (tunnel: TunnelRecord) => void
  deletingId?: string
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent>
        {tunnels.length === 0 ? (
          <Empty className="border">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <RouterIcon />
              </EmptyMedia>
              <EmptyTitle>No active tunnels</EmptyTitle>
              <EmptyDescription>
                Connect a tunnel client to see active tunnel domains here.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <div className="overflow-hidden rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Subdomain</TableHead>
                  <TableHead>Client version</TableHead>
                  <TableHead>Requests recorded</TableHead>
                  <TableHead>Data transferred</TableHead>
                  <TableHead>Livetime</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {tunnels.map((tunnel) => (
                  <TableRow key={tunnel.id}>
                    <TableCell>
                      <Link
                        to={`/admin/tunnels/${tunnel.id}`}
                        className="font-medium text-foreground underline-offset-4 hover:underline"
                      >
                        {tunnel.domain}
                      </Link>
                    </TableCell>
                    <TableCell>
                      <span className="inline-flex items-center rounded-md bg-muted px-2 py-1 text-xs font-medium">
                        {tunnel.clientVersion || "unknown"}
                      </span>
                    </TableCell>
                    <TableCell>{formatCount(tunnel.requestCount)}</TableCell>
                    <TableCell>
                      {formatBytes(
                        tunnel.totalRequestBytes + tunnel.totalResponseBytes
                      )}
                    </TableCell>
                    <TableCell>{formatDurationFrom(tunnel.createdAt)}</TableCell>
                    <TableCell>
                      <TunnelStateBadge state={tunnel.state} />
                    </TableCell>
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
                              <Link to={`/admin/tunnels/${tunnel.id}`}>Details</Link>
                            </DropdownMenuItem>
                            <DropdownMenuItem
                              onClick={() => onDelete(tunnel)}
                              disabled={deletingId === tunnel.id}
                            >
                              Delete
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
