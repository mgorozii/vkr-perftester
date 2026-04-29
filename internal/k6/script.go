package k6

import _ "embed"

//go:embed script.js
var JS string

type Config struct {
	RunID           string `json:"run_id"`
	ModelName       string `json:"model_name"`
	TargetRPS       int    `json:"target_rps"`
	Duration        string `json:"duration"`
	Protocol        string `json:"protocol"`
	HTTPURL         string `json:"http_url"`
	GRPCURL         string `json:"grpc_url"`
	WebhookURL      string `json:"webhook_url"`
	Payload         string `json:"payload"`
	EnableMetrics   bool   `json:"enable_metrics"`
	PreAllocatedVUs int    `json:"pre_allocated_vus"`
	MaxVUs          int    `json:"max_vus"`
}
