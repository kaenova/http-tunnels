import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Separator } from "@/components/ui/separator"
import type { RequestResponseLog } from "@/lib/api"
import { formatBytes, formatDateTime, formatDurationMs } from "@/lib/format"
import { HttpStatusBadge } from "@/components/status-badge"

export function RequestLogDetailCard({
  log,
  title = "Request details",
  description = "Inspect request and response metadata, captured headers, previews, and timing.",
}: {
  log: RequestResponseLog
  title?: string
  description?: string
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent>
        <div className="flex flex-col gap-5 rounded-lg border bg-muted/30 p-4">
          <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
            <div className="flex min-w-0 flex-col gap-1">
              <span className="font-mono text-sm font-medium uppercase">
                {log.method}
              </span>
              <span className="truncate text-sm text-muted-foreground">{log.path}</span>
              <span className="truncate text-xs text-muted-foreground">{log.domain}</span>
            </div>
            <div className="flex flex-wrap items-center gap-3 text-xs text-muted-foreground">
              <HttpStatusBadge statusCode={log.statusCode} />
              <span>{formatDurationMs(log.durationMs)}</span>
              <span>{formatBytes(log.requestBytes + log.responseBytes)}</span>
            </div>
          </div>

          {log.errorMessage ? (
            <div className="rounded-lg border border-destructive/20 bg-destructive/5 p-3 text-sm text-destructive">
              {log.errorMessage}
            </div>
          ) : null}

          <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
            <MetadataItem label="Started" value={formatDateTime(log.startedAt)} />
            <MetadataItem label="Completed" value={formatDateTime(log.completedAt)} />
            <MetadataItem label="Request size" value={formatBytes(log.requestBytes)} />
            <MetadataItem label="Response size" value={formatBytes(log.responseBytes)} />
          </div>

          <Separator />

          <div className="grid gap-5 lg:grid-cols-2">
            <PayloadSection
              title="Request"
              contentType={log.requestContentType}
              headers={log.requestHeaders}
              preview={log.requestPreview}
            />
            <PayloadSection
              title="Response"
              contentType={log.responseContentType}
              headers={log.responseHeaders}
              preview={log.responsePreview}
            />
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

function MetadataItem({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-1 rounded-lg border bg-background p-3">
      <span className="text-xs uppercase tracking-wide text-muted-foreground">
        {label}
      </span>
      <span className="text-sm font-medium">{value}</span>
    </div>
  )
}

function PayloadSection({
  title,
  contentType,
  headers,
  preview,
}: {
  title: string
  contentType?: string
  headers?: Record<string, string[]>
  preview?: string
}) {
  return (
    <div className="flex flex-col gap-3 rounded-lg border bg-background p-4">
      <div className="flex flex-col gap-1">
        <h3 className="font-heading text-sm font-semibold">{title}</h3>
        <p className="text-xs text-muted-foreground">
          {contentType || "No content-type captured"}
        </p>
      </div>
      <pre className="max-h-52 overflow-auto rounded-md bg-muted p-3 text-xs leading-relaxed text-muted-foreground">
        {JSON.stringify(headers ?? {}, null, 2)}
      </pre>
      <div className="flex flex-col gap-1">
        <span className="text-xs uppercase tracking-wide text-muted-foreground">
          Preview
        </span>
        <pre className="max-h-64 overflow-auto rounded-md bg-muted p-3 text-xs leading-relaxed whitespace-pre-wrap break-words text-foreground">
          {preview || "No text preview available for this payload."}
        </pre>
      </div>
    </div>
  )
}
