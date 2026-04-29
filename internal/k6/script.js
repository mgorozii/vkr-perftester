import http from 'k6/http';
import inference from 'k6/x/inference';

const config = JSON.parse(open(__ENV.CONFIG_PATH));

export const options = {
  scenarios: {
    default: {
      executor: 'constant-arrival-rate',
      rate: config.target_rps,
      timeUnit: '1s',
      duration: config.duration,
      preAllocatedVUs: config.pre_allocated_vus || 10,
      maxVUs: config.max_vus || 1000
    }
  }
};

const client = inference.connect(config.http_url, config.grpc_url || "");
const model = client.model(config.model_name);
const method = config.protocol === 'gRPC' ? 'grpcPreloaded' : 'httpPreloaded';

model.loadPayload(config.payload || "");

export default function () {
  model[method]();
}

function metricKey(name, key) {
  return (name + '.' + key)
    .replaceAll('(', '')
    .replaceAll(')', '')
    .replaceAll('%', 'pct')
    .replaceAll('/', '_')
    .replaceAll('-', '_')
    .replaceAll(' ', '_');
}

function collectMetrics(data) {
  const out = [];
  for (const [name, meta] of Object.entries(data.metrics || {})) {
    const values = meta && typeof meta === 'object' ? meta.values || {} : {};
    for (const [key, value] of Object.entries(values)) {
      if (typeof value === 'number' && Number.isFinite(value)) {
        out.push({ metric_name: metricKey(name, key), value });
      }
    }
  }
  if (typeof data.state?.testRunDurationMs === 'number') {
    out.push({ metric_name: 'test_run_duration_ms', value: data.state.testRunDurationMs });
  }
  return out;
}

export function handleSummary(data) {
  if (config.enable_metrics) {
    const body = JSON.stringify({run_id: config.run_id, metrics: collectMetrics(data)});
    http.post(config.webhook_url, body, {headers: {'Content-Type': 'application/json'}});
  }
  return { 'stdout': JSON.stringify(data) };
}
