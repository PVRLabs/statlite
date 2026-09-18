const assert = require("node:assert/strict");
const { after, before, test } = require("node:test");

const { createApp } = require("./app");

let baseUrl;
let server;

before(async () => {
  server = createApp().listen(0, "127.0.0.1");
  await new Promise((resolve, reject) => {
    server.once("listening", resolve);
    server.once("error", reject);
  });
  baseUrl = `http://127.0.0.1:${server.address().port}`;
});

after(async () => {
  if (server) await new Promise((resolve) => server.close(resolve));
});

test("counts real responses and excludes metrics polling", async () => {
  assert.equal((await fetch(`${baseUrl}/`)).status, 200);
  assert.equal((await fetch(`${baseUrl}/missing`)).status, 404);

  const originalError = console.error;
  console.error = () => {};
  try {
    assert.equal((await fetch(`${baseUrl}/failure`)).status, 500);
  } finally {
    console.error = originalError;
  }

  const first = await (await fetch(`${baseUrl}/statlite/metrics`)).json();
  const second = await (
    await fetch(`${baseUrl}/statlite/metrics?source=test`)
  ).json();

  for (const snapshot of [first, second]) {
    assert.equal(snapshot.schema, "statlite-metrics/v1");
    assert.equal(snapshot.integration, "express");
    assert.equal(snapshot.status, "UP");
    assert.equal(snapshot.metrics.requests_total, 3);
    assert.equal(snapshot.metrics.responses_404_total, 1);
    assert.equal(snapshot.metrics.responses_4xx_total, 1);
    assert.equal(snapshot.metrics.responses_5xx_total, 1);
    assert.ok(snapshot.metrics.request_duration_seconds_total > 0);
    assert.ok(snapshot.metrics.process_cpu_usage >= 0);
    assert.ok(snapshot.metrics.runtime_heap_used_bytes > 0);
    assert.ok(snapshot.metrics.uptime_seconds > 0);
    assert.ok(Date.parse(snapshot.started_at) > 0);
  }
});
