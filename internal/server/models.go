package server

import "time"

type TunnelRecord struct {
	ID                 string     `json:"id"`
	Domain             string     `json:"domain"`
	RequestedSubdomain string     `json:"requestedSubdomain,omitempty"`
	State              string     `json:"state"`
	Transport          string     `json:"transport,omitempty"`
	CreatedAt          time.Time  `json:"createdAt"`
	ConnectedAt        *time.Time `json:"connectedAt,omitempty"`
	DisconnectedAt     *time.Time `json:"disconnectedAt,omitempty"`
	LastActivityAt     *time.Time `json:"lastActivityAt,omitempty"`
	TotalRequestBytes  int64      `json:"totalRequestBytes"`
	TotalResponseBytes int64      `json:"totalResponseBytes"`
	RequestCount       int64      `json:"requestCount"`
	RemoteAddr         string     `json:"remoteAddr,omitempty"`
	UserAgent          string     `json:"userAgent,omitempty"`
	DeletedAt          *time.Time `json:"deletedAt,omitempty"`
}

func (t TunnelRecord) TotalTransferredBytes() int64 {
	return t.TotalRequestBytes + t.TotalResponseBytes
}

type RequestResponseLog struct {
	ID                  string              `json:"id"`
	TunnelID            string              `json:"tunnelId"`
	Domain              string              `json:"domain"`
	Method              string              `json:"method"`
	Path                string              `json:"path"`
	RequestHeaders      map[string][]string `json:"requestHeaders,omitempty"`
	ResponseHeaders     map[string][]string `json:"responseHeaders,omitempty"`
	RequestPreview      string              `json:"requestPreview,omitempty"`
	ResponsePreview     string              `json:"responsePreview,omitempty"`
	RequestContentType  string              `json:"requestContentType,omitempty"`
	ResponseContentType string              `json:"responseContentType,omitempty"`
	RequestBytes        int64               `json:"requestBytes"`
	ResponseBytes       int64               `json:"responseBytes"`
	StatusCode          int                 `json:"statusCode"`
	StartedAt           time.Time           `json:"startedAt"`
	CompletedAt         *time.Time          `json:"completedAt,omitempty"`
	DurationMs          int64               `json:"durationMs"`
	ErrorMessage        string              `json:"errorMessage,omitempty"`
}

func (l RequestResponseLog) TotalTransferredBytes() int64 {
	return l.RequestBytes + l.ResponseBytes
}

type TunnelCreationLog struct {
	ID                 int64     `json:"id"`
	TunnelID           string    `json:"tunnelId,omitempty"`
	Domain             string    `json:"domain,omitempty"`
	RequestedSubdomain string    `json:"requestedSubdomain,omitempty"`
	RemoteAddr         string    `json:"remoteAddr,omitempty"`
	UserAgent          string    `json:"userAgent,omitempty"`
	Success            bool      `json:"success"`
	ErrorMessage       string    `json:"errorMessage,omitempty"`
	CreatedAt          time.Time `json:"createdAt"`
}

type DashboardSummary struct {
	ActiveTunnels          int64 `json:"activeTunnels"`
	ActiveHTTP2Tunnels     int64 `json:"activeHttp2Tunnels"`
	ActiveWebSocketTunnels int64 `json:"activeWebsocketTunnels"`
	ActiveTraffic          int64 `json:"activeTraffic"`
	RegisteredTunnels      int64 `json:"registeredTunnels"`
	TotalRequests          int64 `json:"totalRequests"`
	DataTransferredBytes   int64 `json:"dataTransferredBytes"`
}

type TunnelListResponse struct {
	Items      []TunnelRecord `json:"items"`
	Page       int            `json:"page"`
	PageSize   int            `json:"pageSize"`
	TotalItems int64          `json:"totalItems"`
	TotalPages int            `json:"totalPages"`
}

type RequestLogListResponse struct {
	Items      []RequestResponseLog `json:"items"`
	Page       int                  `json:"page"`
	PageSize   int                  `json:"pageSize"`
	TotalItems int64                `json:"totalItems"`
	TotalPages int                  `json:"totalPages"`
}

type RequestLogFilters struct {
	Search      string `json:"search,omitempty"`
	Subdomain   string `json:"subdomain,omitempty"`
	Method      string `json:"method,omitempty"`
	StatusClass string `json:"statusClass,omitempty"`
}

type StatusChartPoint struct {
	Bucket  string `json:"bucket"`
	TwoXX   int64  `json:"twoXX"`
	ThreeXX int64  `json:"threeXX"`
	FourXX  int64  `json:"fourXX"`
	FiveXX  int64  `json:"fiveXX"`
}

type TrafficChartPoint struct {
	Bucket        string `json:"bucket"`
	InboundBytes  int64  `json:"inboundBytes"`
	OutboundBytes int64  `json:"outboundBytes"`
}

type TunnelDetailResponse struct {
	Tunnel          TunnelRecord           `json:"tunnel"`
	StatusChart     []StatusChartPoint     `json:"statusChart"`
	TrafficChart    []TrafficChartPoint    `json:"trafficChart"`
	Logs            RequestLogListResponse `json:"logs"`
	CreationHistory []TunnelCreationLog    `json:"creationHistory"`
}

type DashboardResponse struct {
	Summary             DashboardSummary     `json:"summary"`
	StatusChart         []StatusChartPoint   `json:"statusChart"`
	TrafficChart        []TrafficChartPoint  `json:"trafficChart"`
	ActiveTunnels       []TunnelRecord       `json:"activeTunnels"`
	RecentRequests      []RequestResponseLog `json:"recentRequests"`
	RecentTunnelCreates []TunnelCreationLog  `json:"recentTunnelCreates"`
}
