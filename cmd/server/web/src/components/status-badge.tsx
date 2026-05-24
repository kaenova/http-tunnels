import { Badge } from "@/components/ui/badge"
import { formatStatusClass } from "@/lib/format"

export function HttpStatusBadge({ statusCode }: { statusCode: number }) {
  const statusClass = formatStatusClass(statusCode)
  const variant =
    statusClass === "2XX"
      ? "default"
      : statusClass === "3XX"
        ? "secondary"
        : statusClass === "4XX"
          ? "outline"
          : "destructive"

  return <Badge variant={variant}>{statusCode}</Badge>
}

export function TunnelStateBadge({ state }: { state: string }) {
  const normalized = state.toLowerCase()
  const variant =
    normalized === "active"
      ? "default"
      : normalized === "pending"
        ? "secondary"
        : normalized === "deleted"
          ? "destructive"
          : "outline"

  return <Badge variant={variant}>{state}</Badge>
}
