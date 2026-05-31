package protocol

import "time"

const (
	DefaultChunkSize  = 32 * 1024
	DefaultPingPeriod = 20 * time.Second
	DefaultPongWait   = 60 * time.Second
)