export type AdminSession = {
  authenticated: boolean
  configured: boolean
  message?: string
}

export type TunnelRecord = {
  id: string
  domain: string
  requestedSubdomain?: string
  state: string
  createdAt: string
  connectedAt?: string
  disconnectedAt?: string
  lastActivityAt?: string
  totalRequestBytes: number
  totalResponseBytes: number
  requestCount: number
  remoteAddr?: string
  userAgent?: string
}

export type RequestResponseLog = {
  id: string
  tunnelId: string
  domain: string
  method: string
  path: string
  requestHeaders?: Record<string, string[]>
  responseHeaders?: Record<string, string[]>
  requestPreview?: string
  responsePreview?: string
  requestContentType?: string
  responseContentType?: string
  requestBytes: number
  responseBytes: number
  statusCode: number
  startedAt: string
  completedAt?: string
  durationMs: number
  errorMessage?: string
}

export type TunnelCreationLog = {
  id: number
  tunnelId?: string
  domain?: string
  requestedSubdomain?: string
  remoteAddr?: string
  userAgent?: string
  success: boolean
  errorMessage?: string
  createdAt: string
}

export type DashboardSummary = {
  activeTunnels: number
  registeredTunnels: number
  totalRequests: number
  dataTransferredBytes: number
}

export type DashboardResponse = {
  summary: DashboardSummary
  activeTunnels: TunnelRecord[]
  recentRequests: RequestResponseLog[]
  recentTunnelCreates: TunnelCreationLog[]
}

export type TunnelListResponse = {
  items: TunnelRecord[]
  page: number
  pageSize: number
  totalItems: number
  totalPages: number
}

export type RequestLogListResponse = {
  items: RequestResponseLog[]
  page: number
  pageSize: number
  totalItems: number
  totalPages: number
}

export type RequestActivityFilters = {
  search: string
  subdomain: string
  method: string
  statusClass: string
}

export type StatusChartPoint = {
  bucket: string
  twoXX: number
  threeXX: number
  fourXX: number
  fiveXX: number
}

export type TrafficChartPoint = {
  bucket: string
  inboundBytes: number
  outboundBytes: number
}

export type TunnelDetailResponse = {
  tunnel: TunnelRecord
  statusChart: StatusChartPoint[]
  trafficChart: TrafficChartPoint[]
  logs: RequestLogListResponse
  creationHistory: TunnelCreationLog[]
}

type ErrorPayload = {
  error?: string
  message?: string
}

export class ApiError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

function buildQueryString(values: Record<string, string | number | undefined>) {
  const params = new URLSearchParams()

  Object.entries(values).forEach(([key, value]) => {
    if (value === undefined) {
      return
    }

    const normalized = String(value).trim()
    if (normalized === "") {
      return
    }

    params.set(key, normalized)
  })

  const query = params.toString()
  return query ? `?${query}` : ""
}

async function request<T>(input: string, init?: RequestInit): Promise<T> {
  const response = await fetch(input, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers ?? {}),
    },
    credentials: "same-origin",
  })

  if (!response.ok) {
    const errorPayload = (await response.json().catch(() => null)) as
      | ErrorPayload
      | null
    throw new ApiError(
      response.status,
      errorPayload?.error ?? errorPayload?.message ?? response.statusText
    )
  }

  if (response.status === 204) {
    return undefined as T
  }

  return (await response.json()) as T
}

export const api = {
  session: () => request<AdminSession>("/api/admin/auth/session"),
  login: (password: string) =>
    request<{ ok: boolean }>("/api/admin/auth/login", {
      method: "POST",
      body: JSON.stringify({ password }),
    }),
  dashboard: () => request<DashboardResponse>("/api/admin/dashboard"),
  listRequestActivity: (
    page: number,
    pageSize: number,
    filters: RequestActivityFilters
  ) =>
    request<RequestLogListResponse>(
      `/api/admin/request-activity${buildQueryString({
        page,
        pageSize,
        search: filters.search,
        subdomain: filters.subdomain,
        method: filters.method,
        statusClass: filters.statusClass,
      })}`
    ),
  requestActivityDetail: (requestId: string) =>
    request<RequestResponseLog>(`/api/admin/request-activity/${requestId}`),
  listTunnels: (page: number, pageSize: number) =>
    request<TunnelListResponse>(
      `/api/admin/tunnels${buildQueryString({ page, pageSize })}`
    ),
  tunnelDetail: (tunnelId: string, page: number, pageSize: number) =>
    request<TunnelDetailResponse>(
      `/api/admin/tunnels/${tunnelId}${buildQueryString({ page, pageSize })}`
    ),
  deleteTunnel: (tunnelId: string) =>
    request<{ ok: boolean }>(`/api/admin/tunnels/${tunnelId}`, {
      method: "DELETE",
    }),
}
