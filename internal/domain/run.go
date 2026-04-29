package domain

import "time"

type Protocol string

const (
	ProtocolHTTP Protocol = "HTTP"
	ProtocolGRPC Protocol = "gRPC"
)

type Status string

const (
	StatusPending Status = "PENDING"
	StatusRunning Status = "RUNNING"
	StatusSuccess Status = "SUCCESS"
	StatusFailed  Status = "FAILED"
)

type Run struct {
	ID            string
	Tenant        string
	ModelName     string
	S3Path        string
	ModelFormat   string
	TargetRPS     *int32
	Duration      *time.Duration
	MaxLatencyMS  *int32
	Payload       string
	Protocol      Protocol
	Status        Status
	FailureReason string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type Metric struct {
	RunID      string
	Timestamp  time.Time
	MetricName string
	Value      float64
}

type StartTestCmd struct {
	Tenant       string
	Name         string
	S3Path       string
	ModelFormat  string
	TargetRPS    *int32
	Duration     *time.Duration
	MaxLatencyMS *int32
	Payload      string
	Protocol     Protocol
}

type SearchStep struct {
	RunID           string  `json:"run_id"`
	StepNumber      int     `json:"step_number"`
	RPS             int     `json:"rps"`
	ActualRPS       float64 `json:"actual_rps"`
	VUs             float64 `json:"vus"`
	MaxVUs          float64 `json:"max_vus"`
	DurationSeconds int     `json:"duration_seconds"`
	P99LatencyMS    float64 `json:"p99_latency_ms"`
	ErrorRate       float64 `json:"error_rate"`
	StopReason      string  `json:"stop_reason,omitempty"`
	Error           string  `json:"error,omitempty"`
}

type Report struct {
	RunID    string
	Received time.Time
	Metrics  []MetricPoint
}

type MetricPoint struct {
	Timestamp  time.Time `json:"timestamp"`
	MetricName string    `json:"metric_name"`
	Value      float64   `json:"value"`
}
