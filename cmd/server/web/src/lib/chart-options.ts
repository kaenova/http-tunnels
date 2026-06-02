import type { ChartFilters, ChartGranularity, ChartRangePreset } from "@/lib/api"

export const chartGranularityOptions: Array<{
  value: ChartGranularity
  label: string
}> = [
  { value: "minute", label: "Minutes" },
  { value: "15-minutes", label: "15 minutes" },
  { value: "hourly", label: "Hourly" },
  { value: "daily", label: "Daily" },
  { value: "weekly", label: "Weekly" },
  { value: "monthly", label: "Monthly" },
]

export const chartRangeOptions: Array<{
  value: ChartRangePreset
  label: string
}> = [
  { value: "last-10-minutes", label: "Last 10 minutes" },
  { value: "last-15-minutes", label: "Last 15 minutes" },
  { value: "last-30-minutes", label: "Last 30 minutes" },
  { value: "last-60-minutes", label: "Last 60 minutes" },
  { value: "last-hour", label: "Last hour" },
  { value: "last-3-hours", label: "Last 3 hours" },
  { value: "last-8-hours", label: "Last 8 hours" },
  { value: "last-24-hours", label: "Last 24 hours" },
  { value: "last-24-days", label: "Last 24 days" },
  { value: "custom", label: "Custom date range" },
]

export const defaultChartFilters: ChartFilters = {
  granularity: "hourly",
  range: "last-24-hours",
}

export function normalizeChartFilters(filters: ChartFilters): ChartFilters {
  if (filters.range !== "custom") {
    return {
      granularity: filters.granularity,
      range: filters.range,
    }
  }

  return {
    granularity: filters.granularity,
    range: filters.range,
    start: filters.start,
    end: filters.end,
  }
}

export function defaultCustomChartRange(): Pick<ChartFilters, "start" | "end"> {
  const end = new Date()
  const start = new Date(end.getTime() - 24 * 60 * 60 * 1000)
  return {
    start: start.toISOString(),
    end: end.toISOString(),
  }
}

export function chartFiltersDescription(filters: ChartFilters): string {
  const granularity =
    chartGranularityOptions.find((item) => item.value === filters.granularity)?.label ??
    filters.granularity
  const range =
    chartRangeOptions.find((item) => item.value === filters.range)?.label ??
    filters.range

  if (filters.range !== "custom") {
    return `${range} · ${granularity}`
  }

  if (filters.start && filters.end) {
    return `${formatChartDate(filters.start)} → ${formatChartDate(filters.end)} · ${granularity}`
  }

  return `Custom range · ${granularity}`
}

export function chartFiltersEqual(a: ChartFilters, b: ChartFilters): boolean {
  return (
    a.granularity === b.granularity &&
    a.range === b.range &&
    (a.start ?? "") === (b.start ?? "") &&
    (a.end ?? "") === (b.end ?? "")
  )
}

export function toDateTimeLocalValue(value?: string): string {
  if (!value) {
    return ""
  }
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return ""
  }
  const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000)
  return local.toISOString().slice(0, 16)
}

export function fromDateTimeLocalValue(value: string): string | undefined {
  if (!value.trim()) {
    return undefined
  }
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return undefined
  }
  return date.toISOString()
}

function formatChartDate(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return value
  }
  return date.toLocaleString([], {
    year: "numeric",
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  })
}
