package telemetry

import (
	"context"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

const (
	EnvTraceparent = "TRACEPARENT"
	EnvTracestate  = "TRACESTATE"
	EnvBaggage     = "BAGGAGE"
)

func InjectEnv(ctx context.Context) map[string]string {
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	env := map[string]string{}
	if v := carrier.Get("traceparent"); v != "" {
		env[EnvTraceparent] = v
	}
	if v := carrier.Get("tracestate"); v != "" {
		env[EnvTracestate] = v
	}
	if v := carrier.Get("baggage"); v != "" {
		env[EnvBaggage] = v
	}
	return env
}

func ExtractEnv(ctx context.Context) context.Context {
	carrier := propagation.MapCarrier{}
	setCarrierValue(carrier, "traceparent", os.Getenv(EnvTraceparent))
	setCarrierValue(carrier, "tracestate", os.Getenv(EnvTracestate))
	setCarrierValue(carrier, "baggage", os.Getenv(EnvBaggage))
	return otel.GetTextMapPropagator().Extract(ctx, carrier)
}

func setCarrierValue(carrier propagation.MapCarrier, key, value string) {
	if value != "" {
		carrier.Set(key, value)
	}
}
