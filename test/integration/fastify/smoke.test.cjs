const assert = require('node:assert/strict');
const {spawn} = require('node:child_process');
const {once} = require('node:events');
const {mkdtemp, rm, writeFile} = require('node:fs/promises');
const {tmpdir} = require('node:os');
const {join} = require('node:path');
const {test} = require('node:test');
const {setTimeout: delay} = require('node:timers/promises');
const fastify = require('fastify');
const metrics = require('fastify-metrics');

const host = '127.0.0.1';
const port = Number(process.env.STATLITE_PORT || 19090);

async function get(url) {
  return fetch(url, {signal: AbortSignal.timeout(1000)});
}

async function api(route) {
  const response = await get(`http://${host}:${port}/api/${route}?target=fastify&range=1h`);
  assert.equal(response.status, 200);
  return response.json();
}

async function waitForPoll(process, requests, after = 0) {
  const deadline = Date.now() + 10000;
  let latest;
  while (Date.now() < deadline) {
    assert.equal(process.exitCode, null, 'StatLite exited');
    assert.equal(process.signalCode, null, 'StatLite was terminated');
    try {
      latest = await api('latest');
      if (latest.status === 'ok' && latest.poll_id > after &&
          latest.result.samples?.some(sample => sample.key === 'http_requests_total' && sample.value >= requests)) {
        return latest;
      }
    } catch (error) {
      if (error.cause?.code !== 'ECONNREFUSED') throw error;
    }
    await delay(50);
  }
  assert.fail(`Timed out waiting for ${requests} requests. Latest: ${JSON.stringify(latest)}`);
}

async function send(appURL, traffic) {
  for (const [path, status] of traffic) {
    const response = await get(appURL + path);
    assert.equal(response.status, status, path);
    await response.arrayBuffer();
  }
}

async function verifySeries(before, after, expected) {
  const {points} = await api('series');
  const interval = points.filter(point => before < point.poll_id && point.poll_id <= after);
  assert.equal(interval.at(-1)?.poll_id, after);
  for (const [key, count] of Object.entries(expected)) {
    const values = interval.map(point => point[key]);
    assert(values.every(Number.isInteger), `${key}: ${JSON.stringify(interval)}`);
    assert.equal(values.reduce((a, b) => a + b, 0), count, key);
  }
  for (const point of interval) {
    if (point.requests > 0) assert(point.average_latency_seconds > 0);
  }
}

test('native Fastify exporter through StatLite', async () => {
  const binary = process.env.STATLITE_BIN;
  assert(binary, 'Set STATLITE_BIN to the built StatLite executable');
  const app = fastify();
  await app.register(metrics, {
    routeMetrics: {registeredRoutesOnly: false},
    endpoint: {url: '/metrics', config: {disableMetrics: true}},
  });
  app.get('/ok', async () => 'ok');
  app.get('/crash', async () => { throw new Error('intentional unhandled route error'); });
  const appURL = await app.listen({port: 0, host});
  let directory, child, closed;
  try {
    directory = await mkdtemp(join(tmpdir(), 'statlite-fastify-'));
    await send(appURL, [['/ok', 200], ['/missing', 404], ['/crash', 500]]);
    const config = join(directory, 'statlite.json');
    await writeFile(config, JSON.stringify({
      server: {listen: `${host}:${port}`},
      storage: {sqlite_path: join(directory, 'statlite.sqlite')},
      polling: {interval: '1s', timeout: '1s'},
      targets: [{name: 'fastify', type: 'fastify', url: appURL + '/metrics'}],
    }));
    child = spawn(binary, ['--config', config], {stdio: 'inherit'});
    closed = once(child, 'close');
    const first = await waitForPoll(child, 3);
    await send(appURL, [['/metrics', 200], ['/metrics', 200], ['/metrics', 200]]);
    const idle = await waitForPoll(child, 3, first.poll_id);
    await verifySeries(first.poll_id, idle.poll_id, {requests: 0, http_404: 0, http_4xx: 0, http_5xx: 0});

    await send(appURL, [['/ok', 200], ['/missing', 404], ['/crash', 500]]);
    const errors = await waitForPoll(child, 6, idle.poll_id);
    await verifySeries(idle.poll_id, errors.poll_id, {requests: 3, http_404: 1, http_4xx: 1, http_5xx: 1});

  } finally {
    try {
      if (child) {
        child.kill();
        const forceKill = setTimeout(() => child.kill('SIGKILL'), 5000);
        try {
          await closed;
        } finally {
          clearTimeout(forceKill);
        }
      }
    } finally {
      await app.close();
      if (directory) await rm(directory, {recursive: true, force: true});
    }
  }
});
