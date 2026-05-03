package config

import (
	"time"

	env "github.com/caarlos0/env/v11"
)

type Loadtestd struct {
	HTTPAddr       string        `env:"HTTP_ADDR"                           envDefault:":8080"`
	MetricsAddr    string        `env:"METRICS_ADDR"                        envDefault:":9090"`
	DatabaseURL    string        `env:"DATABASE_URL"`
	OTLPEndpoint   string        `env:"OTEL_EXPORTER_OTLP_ENDPOINT"`
	OTLPTracesURL  string        `env:"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"`
	OTLPMetricsURL string        `env:"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"`
	SamplingRatio  float64       `env:"OTEL_SAMPLING_RATIO"                 envDefault:"1.0"`
	RunTimeout     time.Duration `env:"RUN_TIMEOUT"                         envDefault:"120m"`
	SweepEvery     time.Duration `env:"SWEEP_EVERY"                         envDefault:"1m"`
	ModelHTTP      string        `env:"MODEL_HTTP_URL"                      envDefault:"http://modelmesh-serving.modelmesh-serving:8008"`
	ModelGRPC      string        `env:"MODEL_GRPC_URL"                      envDefault:"modelmesh-serving.modelmesh-serving:8033"`
	WebhookURL     string        `env:"WEBHOOK_URL"                         envDefault:"http://loadtestd.loadtest-system:8080/api/v1/report"`
	K6Image        string        `env:"K6_IMAGE"                            envDefault:"load-k6:dev"`
	ControllerNS   string        `env:"MODEL_CONTROLLER_NAMESPACE"          envDefault:"modelmesh-serving"`
	BaseRuntime    string        `env:"BASE_RUNTIME_NAME"                   envDefault:"triton-2.x"`
	StorageSecret  string        `env:"STORAGE_SECRET_NAME"                 envDefault:"storage-config"`
}

func Load() (Loadtestd, error) {
	var cfg Loadtestd
	if err := env.Parse(&cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

type Executor struct {
	ConfigPath    string        `env:"CONFIG_PATH"         envDefault:"/etc/loadtest/config.json"`
	StepsURL      string        `env:"STEPS_URL"`
	StatusURL     string        `env:"STATUS_URL"`
	SamplingRatio float64       `env:"OTEL_SAMPLING_RATIO" envDefault:"1.0"`
	Timeout       time.Duration `env:"SEARCH_TIMEOUT"      envDefault:"20m"`
	SLO           int           `env:"MAX_LATENCY_MS"      envDefault:"500"`
}

func LoadExecutor() (Executor, error) {
	var cfg Executor
	if err := env.Parse(&cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}
