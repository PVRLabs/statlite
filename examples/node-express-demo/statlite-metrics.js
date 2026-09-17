const { performance } = require("node:perf_hooks");

const METRICS_PATH = "/statlite/metrics";
const startedAt = new Date(Date.now() - process.uptime() * 1000);

const counters = {
  requestsTotal: 0,
  responses404Total: 0,
  responses4xxTotal: 0,
  responses5xxTotal: 0,
  requestDurationSecondsTotal: 0,
};

let previousCpu = process.cpuUsage();
let previousCpuTime = performance.now();

function record(statusCode, durationSeconds) {
  counters.requestsTotal += 1;
  counters.requestDurationSecondsTotal += durationSeconds;
  if (statusCode === 404) counters.responses404Total += 1;
  if (statusCode >= 400 && statusCode < 500) counters.responses4xxTotal += 1;
  if (statusCode >= 500 && statusCode < 600) counters.responses5xxTotal += 1;
}

function statliteMetricsMiddleware(req, res, next) {
  if (req.path === METRICS_PATH) return next();

  const requestStarted = performance.now();
  res.once("finish", () => {
    record(res.statusCode, (performance.now() - requestStarted) / 1000);
  });
  next();
}

function snapshot() {
  const now = performance.now();
  const cpu = process.cpuUsage();
  const elapsedSeconds = (now - previousCpuTime) / 1000;
  const cpuSeconds =
    (cpu.user - previousCpu.user + cpu.system - previousCpu.system) / 1e6;
  const processCpuUsage = elapsedSeconds > 0 ? cpuSeconds / elapsedSeconds : 0;
  previousCpu = cpu;
  previousCpuTime = now;

  return {
    schema: "statlite-metrics/v1",
    status: "UP",
    started_at: startedAt.toISOString(),
    metrics: {
      requests_total: counters.requestsTotal,
      responses_404_total: counters.responses404Total,
      responses_4xx_total: counters.responses4xxTotal,
      responses_5xx_total: counters.responses5xxTotal,
      request_duration_seconds_total: counters.requestDurationSecondsTotal,
      process_cpu_usage: processCpuUsage,
      runtime_heap_used_bytes: process.memoryUsage().heapUsed,
      uptime_seconds: process.uptime(),
    },
  };
}

function statliteMetricsEndpoint(req, res) {
  res.json(snapshot());
}

module.exports = {
  METRICS_PATH,
  statliteMetricsEndpoint,
  statliteMetricsMiddleware,
};
