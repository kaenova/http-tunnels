const byteFormatter = new Intl.NumberFormat("en-US", {
  maximumFractionDigits: 1,
})

const numberFormatter = new Intl.NumberFormat("en-US")

const dateTimeFormatter = new Intl.DateTimeFormat("en-US", {
  dateStyle: "medium",
  timeStyle: "short",
})

const durationFormatter = new Intl.RelativeTimeFormat("en", {
  numeric: "auto",
})

export function formatBytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) {
    return "0 B"
  }

  const units = ["B", "KB", "MB", "GB", "TB"]
  let unitIndex = 0
  let size = value

  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024
    unitIndex += 1
  }

  return `${byteFormatter.format(size)} ${units[unitIndex]}`
}

export function formatCount(value: number) {
  return numberFormatter.format(value ?? 0)
}

export function formatDateTime(value?: string) {
  if (!value) {
    return "—"
  }

  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) {
    return "—"
  }

  return dateTimeFormatter.format(parsed)
}

export function formatDurationFrom(dateString?: string) {
  if (!dateString) {
    return "—"
  }

  const parsed = new Date(dateString)
  if (Number.isNaN(parsed.getTime())) {
    return "—"
  }

  const diffMs = Date.now() - parsed.getTime()
  const diffMinutes = Math.floor(diffMs / 1000 / 60)
  if (diffMinutes < 1) {
    return "just now"
  }
  if (diffMinutes < 60) {
    return durationFormatter.format(-diffMinutes, "minute")
  }

  const diffHours = Math.floor(diffMinutes / 60)
  if (diffHours < 24) {
    return durationFormatter.format(-diffHours, "hour")
  }

  const diffDays = Math.floor(diffHours / 24)
  if (diffDays < 30) {
    return durationFormatter.format(-diffDays, "day")
  }

  const diffMonths = Math.floor(diffDays / 30)
  if (diffMonths < 12) {
    return durationFormatter.format(-diffMonths, "month")
  }

  const diffYears = Math.floor(diffMonths / 12)
  return durationFormatter.format(-diffYears, "year")
}

export function formatDurationMs(durationMs: number) {
  if (!Number.isFinite(durationMs) || durationMs <= 0) {
    return "0 ms"
  }
  if (durationMs < 1000) {
    return `${Math.round(durationMs)} ms`
  }
  if (durationMs < 60_000) {
    return `${byteFormatter.format(durationMs / 1000)} s`
  }
  return `${byteFormatter.format(durationMs / 60_000)} min`
}

export function formatStatusClass(statusCode: number) {
  if (statusCode >= 200 && statusCode < 300) {
    return "2XX"
  }
  if (statusCode >= 300 && statusCode < 400) {
    return "3XX"
  }
  if (statusCode >= 400 && statusCode < 500) {
    return "4XX"
  }
  if (statusCode >= 500 && statusCode < 600) {
    return "5XX"
  }
  return "Other"
}
