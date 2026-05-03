package telemetry

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type Setup struct {
	Logger         *slog.Logger
	MetricsHandler http.Handler
	shutdown       []func(context.Context) error
}

func Init(ctx context.Context, service string, withProm bool, samplingRatio float64) (*Setup, error) {
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		attribute.String("service.name", service),
	))
	if err != nil {
		return nil, err
	}

	tpOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(samplingRatio))),
	}
	if tracesEnabled() {
		exp, err := otlptracehttp.New(ctx)
		if err != nil {
			return nil, err
		}
		tpOpts = append(tpOpts, sdktrace.WithBatcher(exp))
	}
	tp := sdktrace.NewTracerProvider(tpOpts...)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	var mp interface {
		Shutdown(context.Context) error
	}
	var metricsHandler http.Handler
	mpOpts := []sdkmetric.Option{sdkmetric.WithResource(res)}
	hasMetricReader := false
	if withProm {
		exp, err := prometheus.New()
		if err != nil {
			return nil, err
		}
		mpOpts = append(mpOpts, sdkmetric.WithReader(exp))
		metricsHandler = promhttp.Handler()
		hasMetricReader = true
	}
	if metricsEnabled() {
		exp, err := otlpmetrichttp.New(ctx)
		if err != nil {
			return nil, err
		}
		mpOpts = append(mpOpts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp)))
		hasMetricReader = true
	}
	if hasMetricReader {
		provider := sdkmetric.NewMeterProvider(mpOpts...)
		otel.SetMeterProvider(provider)
		mp = provider
	} else {
		otel.SetMeterProvider(noop.NewMeterProvider())
		mp = shutdownFunc(func(context.Context) error { return nil })
	}

	logger := slog.New(newTraceHandler(slog.NewTextHandler(os.Stdout, nil))).With("service", service)
	slog.SetDefault(logger)

	return &Setup{
		Logger:         logger,
		MetricsHandler: metricsHandler,
		shutdown: []func(context.Context) error{
			mp.Shutdown,
			tp.Shutdown,
		},
	}, nil
}

func (s *Setup) Shutdown(ctx context.Context) error {
	var err error
	for i := len(s.shutdown) - 1; i >= 0; i-- {
		if e := s.shutdown[i](ctx); e != nil && err == nil {
			err = e
		}
	}
	return err
}

type shutdownFunc func(context.Context) error

func (f shutdownFunc) Shutdown(ctx context.Context) error { return f(ctx) }

func tracesEnabled() bool {
	return envSet("OTEL_EXPORTER_OTLP_ENDPOINT") || envSet("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
}

func metricsEnabled() bool {
	return envSet("OTEL_EXPORTER_OTLP_ENDPOINT") || envSet("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")
}

func envSet(key string) bool {
	_, ok := os.LookupEnv(key)
	return ok
}
