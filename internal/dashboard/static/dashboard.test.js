const assert = require("node:assert/strict");
const { afterEach, beforeEach, test } = require("node:test");

const dashboard = require("./dashboard.js");

beforeEach(resetDashboardState);
afterEach(resetDashboardState);

test("targetTypeHelp describes the StatLite Metrics application format", () => {
  assert.equal(
    dashboard.targetTypeHelp("statlite-metrics"),
    "Monitors an app that exposes metrics in StatLite’s standard format."
  );
});

test("targetTypeHelp describes the Quarkus metrics endpoint", () => {
  assert.equal(
    dashboard.targetTypeHelp("quarkus"),
    "Monitors a Quarkus application through its Prometheus/OpenMetrics endpoint."
  );
});

test("periodic refresh keeps charts for a transient empty series", () => {
  dashboard.state.renderedSeriesQuery = "?target=app&range=1h";

  assert.equal(dashboard.shouldRenderSeries("?target=app&range=1h", { points: [] }), false);
  assert.equal(dashboard.shouldRenderSeries("?target=app&range=7d", { points: [] }), true);
  assert.equal(dashboard.shouldRenderSeries("?target=app&range=1h", { points: [{}] }), true);
});

test("range selection synchronizes visual and accessible state", () => {
  const originalDocument = global.document;
  const buttons = [rangeButton("1h"), rangeButton("24h"), rangeButton("7d"), rangeButton("30d")];
  global.document = { querySelectorAll: () => buttons };
  dashboard.state.range = "7d";

  try {
    dashboard.renderRangeSelection();
    assert.deepEqual(buttons.map((button) => button.active), [false, false, true, false]);
    assert.deepEqual(buttons.map((button) => button.attributes["aria-pressed"]), ["false", "false", "true", "false"]);
  } finally {
    global.document = originalDocument;
  }
});

test("hidden dashboards skip periodic refresh work", () => {
  const originalDocument = global.document;
  global.document = { hidden: true };

  try {
    assert.equal(dashboard.refreshWhenVisible(), undefined);
  } finally {
    global.document = originalDocument;
  }
});

test("startup refresh retries quickly until a successful poll and usable series appear", () => {
  dashboard.state.target = "alpha";
  const successful = { selected_target: { name: "alpha" }, monitor: { last_successful_poll_at: "2026-08-18T12:00:00Z" } };

  assert.equal(dashboard.nextRefreshDelay({ selected_target: { name: "alpha" }, monitor: {} }, { points: [] }, 1001), 1500);
  assert.equal(dashboard.nextRefreshDelay(successful, { points: [] }, 1001), 1500);
  assert.equal(dashboard.nextRefreshDelay(successful, { points: [{ requests: 1 }] }, 1001), 30000);
});

test("process gauges do not end fast refresh for an HTTP counter baseline", () => {
  const summary = {
    selected_target: { name: "alpha" },
    monitor: { last_successful_poll_at: "2026-08-18T12:00:00Z" },
    latest: { result: { samples: [
      { key: "http_requests_total", kind: "counter", value: 10 },
      { key: "process_cpu_usage", kind: "gauge", value: 0.1 }
    ] } }
  };
  const baseline = { points: [{ requests: null, process_cpu_usage: 0.1 }] };
  const delta = { points: [{ requests: 2, process_cpu_usage: 0.1 }] };

  assert.equal(dashboard.hasUsableSeries(summary, baseline), false);
  assert.equal(dashboard.nextRefreshDelay(summary, baseline, 1001), 1500);
  assert.equal(dashboard.hasUsableSeries(summary, delta), true);
  assert.equal(dashboard.nextRefreshDelay(summary, delta, 1001), 30000);
});

test("latest raw point determines readiness when aggregated points omit run identity", () => {
  const summary = {
    selected_target: { name: "alpha" },
    monitor: { last_successful_poll_at: "2026-08-18T12:00:00Z" },
    latest: {
      app_run_id: 2,
      result: { samples: [
        { key: "http_requests_total", kind: "counter", value: 10 },
        { key: "process_cpu_usage", kind: "gauge", value: 0.1 }
      ] }
    }
  };
  const aggregatedPoints = [{ requests: 5, process_cpu_usage: 0.1 }];
  const baseline = {
    points: aggregatedPoints,
    latest_point: { app_run_id: 2, requests: null, process_cpu_usage: 0.1 }
  };
  const followUp = {
    points: aggregatedPoints,
    latest_point: { app_run_id: 2, requests: 3, process_cpu_usage: 0.1 }
  };

  assert.equal(dashboard.hasUsableSeries(summary, baseline), false);
  assert.equal(dashboard.nextRefreshDelay(summary, baseline, 1001), 1500);
  assert.equal(dashboard.hasUsableSeries(summary, followUp), true);
  assert.equal(dashboard.nextRefreshDelay(summary, followUp, 1001), 30000);
});

test("gauge-only targets are ready when their series appears", () => {
  const summary = { latest: { result: { samples: [
    { key: "process_cpu_usage", kind: "gauge", value: 0.1 }
  ] } } };

  assert.equal(dashboard.hasUsableSeries(summary, { points: [{ process_cpu_usage: 0.1 }] }), true);
});

test("startup refresh falls back to the normal cadence after its bounded window", () => {
  dashboard.state.target = "alpha";
  dashboard.state.startupRefreshStartedAtByTarget.alpha = 1000;

  assert.equal(dashboard.nextRefreshDelay({ selected_target: { name: "alpha" }, monitor: {} }, { points: [] }, 11000), 30000);
});

test("a newly selected target gets its own startup retry window", () => {
  dashboard.state.target = "beta";
  dashboard.state.startupRefreshStartedAtByTarget.alpha = 1000;

  assert.equal(dashboard.nextRefreshDelay({ selected_target: { name: "beta" }, monitor: {} }, { points: [] }, 11000), 1500);
});

test("detectCapabilities keeps sparse memory but requires a valid disk pair", () => {
  const sparseMemory = dashboard.detectCapabilities([{ host_memory_used_bytes: 10 }]);
  assert.equal(sparseMemory.host, true);
  assert.equal(sparseMemory.hostMemory, true);
  assert.equal(sparseMemory.hostDisk, false);

  assert.equal(dashboard.validDiskPoint({
    host_disk_used_bytes: 10,
    host_disk_total_bytes: 20,
    host_disk_usage: 0.5
  }), true);
  assert.equal(dashboard.validDiskPoint({
    host_disk_used_bytes: 10,
    host_disk_total_bytes: 20
  }), false);
  assert.equal(dashboard.validDiskPoint({
    host_disk_used_bytes: 30,
    host_disk_total_bytes: 20,
    host_disk_usage: 1.5
  }), false);
});

test("capability detection resets for a target without metrics", () => {
  assert.equal(dashboard.detectCapabilities([{ host_cpu_usage: 0.25 }]).host, true);
  assert.deepEqual(dashboard.detectCapabilities([]), {
    requests: false,
    errors: false,
    latency: false,
    process: false,
    hostCPU: false,
    hostMemory: false,
    hostDisk: false,
    application: false,
    host: false
  });
});

test("dashboard displays embedded host fields with application data", () => {
  const embeddedHost = dashboard.detectCapabilities([{
    requests: 3,
    host_memory_used_bytes: 30,
    host_memory_total_bytes: 100
  }]);
  assert.equal(embeddedHost.application, true);
  assert.equal(embeddedHost.host, true);
  assert.equal(embeddedHost.hostMemory, true);
});

test("formatters reject non-finite inputs and show the current disk observation", () => {
  assert.equal(dashboard.formatValue(Infinity, "percent"), "Unknown");
  assert.equal(dashboard.formatValue(34, "ms"), "34 ms");
  assert.equal(dashboard.formatBytes(NaN), "Unknown");
  assert.equal(dashboard.formatCurrentResource(null, "Disk"), "No data");
  assert.equal(dashboard.formatCurrentResource({ used_bytes: 30, total_bytes: 60, usage: 0.5 }, "Disk"), "Disk — 30 B / 60 B · 50.0%");
});

test("renderPollStatus shows the latest poll state and a failed-poll summary", () => {
  const originalDocument = global.document;
  const document = dashboardDocument();
  global.document = document;

  try {
    dashboard.renderPollStatus({
      last_poll_at: "2026-07-29T17:42:00Z",
      consecutive_poll_failures: 1,
      last_poll_error_summary: "fetching statlite metrics: connection refused"
    });
    const failed = document.getElementById("poll-status-state");
    const failedTime = document.getElementById("poll-status-time");
    const error = document.getElementById("poll-error");
    assert.equal(failed.textContent, "Failed");
    assert.match(failed.className, /bad/);
    assert.match(failedTime.textContent, /^ · /);
    assert.equal(error.textContent, "fetching statlite metrics: connection refused");
    assert.equal(error.hidden, false);
    assert.equal(error.title, error.textContent);

    dashboard.renderPollStatus({ last_poll_at: "2026-07-29T17:45:00Z" });
    assert.equal(failed.textContent, "Successful");
    assert.match(failed.className, /ok/);
    assert.equal(error.hidden, true);
  } finally {
    global.document = originalDocument;
  }
});

test("footer summary reuses target health data without fetching", () => {
  const originalDocument = global.document;
  const document = dashboardDocument();
  global.document = document;

  try {
    dashboard.renderFooterSummary([
      { latest: { result: { health_status: "UP" } } },
      { latest: { result: { health_status: "OK" } } },
      { latest: { result: { health_status: "DOWN" } } },
      {}
    ], new Date("2026-09-02T12:34:56"));

    assert.equal(document.getElementById("footer-targets").textContent, "4");
    assert.equal(document.getElementById("footer-up").textContent, "2");
    assert.match(document.getElementById("footer-refresh").textContent, /12:34:56/);
  } finally {
    global.document = originalDocument;
  }
});

test("three identical events are folded across interleaved failures", () => {
  const first = { timestamp: "2026-09-02T10:57:46Z", severity: "error", type: "metrics_fetch_failed", message: "connection refused" };
  const second = { ...first, timestamp: "2026-09-02T10:57:16Z" };
  const third = { ...first, timestamp: "2026-09-02T10:56:46Z" };
  const warning = { timestamp: "2026-09-02T10:56:16Z", severity: "warning", type: "health_fetch_failed", message: "connection refused" };
  const warning2 = { ...warning, timestamp: "2026-09-02T10:55:46Z" };
  const warning3 = { ...warning, timestamp: "2026-09-02T10:55:16Z" };

  assert.deepEqual(dashboard.foldRepeatedEvents([first, warning, second, warning2, third, warning3]), [
    [first, second, third],
    [warning, warning2, warning3]
  ]);
});

test("one or two identical events remain in chronological order", () => {
  const first = { severity: "error", type: "metrics_fetch_failed", message: "connection refused" };
  const warning = { severity: "warning", type: "health_fetch_failed", message: "connection refused" };
  const second = { ...first };

  assert.deepEqual(dashboard.foldRepeatedEvents([first, warning, second]), [[first], [warning], [second]]);
});

test("event folding includes metric keys in event identity", () => {
  const first = { severity: "warning", type: "metric_warning", metric_key: "cpu", message: "missing" };
  const second = { ...first, metric_key: "memory" };

  assert.deepEqual(dashboard.foldRepeatedEvents([first, second, first]), [[first], [second], [first]]);
});

test("open event groups are retained by event identity before rerendering", () => {
  const root = {
    querySelectorAll(selector) {
      assert.equal(selector, ".event-group[open]");
      return [
        { dataset: { eventKey: "metrics-error" } },
        { dataset: { eventKey: "health-error" } }
      ];
    }
  };

  assert.deepEqual([...dashboard.openEventGroupKeys(root)], ["metrics-error", "health-error"]);
});

test("renderSeries applies capability visibility with a minimal DOM stub", () => {
  const elements = new Map();
  const document = {
    getElementById(id) {
      if (!elements.has(id)) elements.set(id, { hidden: false, textContent: "" });
      return elements.get(id);
    }
  };
  const originalDocument = global.document;
  global.document = document;
  dashboard.state.charts = chartStubs();

  try {
    dashboard.renderSeries({ points: [{ host_memory_used_bytes: 10, process_cpu_usage: 0.25, average_latency_seconds: 0.034 }] });
    assert.equal(document.getElementById("host-section").hidden, false);
    assert.equal(document.getElementById("host-runtime-chart-card").hidden, false);
    assert.equal(document.getElementById("host-disk-chart-card").hidden, true);
    assert.deepEqual(dashboard.state.charts.runtime.data.datasets[1].data, [25]);
    assert.deepEqual(dashboard.state.charts.latency.data.datasets[0].data, [34]);

    dashboard.renderSeries({ points: [] });
    assert.equal(document.getElementById("host-section").hidden, true);
  } finally {
    global.document = originalDocument;
  }
});

test("stale refresh responses cannot replace the current target", async () => {
  const originalDocument = global.document;
  const originalFetch = global.fetch;
  const originalAbortController = global.AbortController;
  const originalWindow = global.window;
  const pending = [];
  global.document = dashboardDocument();
  global.window = { location: { search: "", pathname: "/" }, history: { replaceState() {} } };
  global.fetch = (path, options) => new Promise((resolve) => pending.push({ path, resolve, signal: options.signal }));
  global.AbortController = class {
    constructor() { this.signal = { aborted: false }; }
    abort() { this.signal.aborted = true; }
  };
  dashboard.state.charts = chartStubs();
  dashboard.state.target = "alpha";
  dashboard.state.range = "1h";
  dashboard.state.refreshID = 0;
  dashboard.state.refreshController = null;

  try {
    const first = dashboard.refresh();
    dashboard.state.target = "beta";
    dashboard.state.range = "7d";
    const second = dashboard.refresh();
    assert.equal(pending[0].signal.aborted, true);
    assert.match(pending[0].path, /target=alpha&range=1h/);
    assert.match(pending[3].path, /target=beta&range=7d/);
    resolveRefresh(pending.splice(3, 3), "beta");
    await second;
    resolveRefresh(pending.splice(0, 3), "alpha");
    await first;
    assert.equal(dashboard.state.target, "beta");
  } finally {
    global.document = originalDocument;
    global.fetch = originalFetch;
    global.AbortController = originalAbortController;
    global.window = originalWindow;
  }
});

function chartStubs() {
  const chart = (datasets) => ({ data: { labels: [], datasets: Array.from({ length: datasets }, () => ({ data: [] })) }, update() {} });
  return { requests: chart(1), errors: chart(3), latency: chart(1), runtime: chart(2), hostRuntime: chart(3), hostDisk: chart(3) };
}

function rangeButton(range) {
  const button = {
    active: false,
    attributes: {},
    dataset: { range },
    classList: { toggle(_name, active) { button.active = active; } },
    setAttribute(name, value) { this.attributes[name] = value; }
  };
  return button;
}

function dashboardDocument() {
  const elements = new Map();
  return {
    getElementById(id) {
      if (!elements.has(id)) elements.set(id, { hidden: false, textContent: "", innerHTML: "", className: "", title: "", classList: { toggle() {} }, setAttribute() {} });
      return elements.get(id);
    },
    createElement() { return { children: [], appendChild() {}, classList: { toggle() {} }, setAttribute() {} }; }
  };
}

function resolveRefresh(requests, target) {
  const summary = { selected_target: { name: target }, targets: [], monitor: {}, latest: {} };
  const values = [summary, { points: [] }, []];
  requests.forEach((request, index) => request.resolve({ ok: true, json: () => Promise.resolve(values[index]) }));
}

function resetDashboardState() {
  if (dashboard.state.refreshTimer !== null) clearTimeout(dashboard.state.refreshTimer);
  dashboard.state.range = "1h";
  dashboard.state.target = "";
  dashboard.state.charts = {};
  dashboard.state.refreshID = 0;
  dashboard.state.refreshController = null;
  dashboard.state.refreshTimer = null;
  dashboard.state.startupRefreshStartedAtByTarget = Object.create(null);
  dashboard.state.renderedSeriesQuery = "";
}
