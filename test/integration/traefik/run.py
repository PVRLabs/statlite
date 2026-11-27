"""Certify a local StatLite binary against Traefik 3.8.0 (Python 3.15, stdlib only)."""

import argparse
from contextlib import ExitStack, closing
from datetime import datetime, timedelta, timezone
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import re
import sqlite3
import subprocess
import sys
import threading
import time
from urllib.error import HTTPError, URLError
from urllib.request import urlopen

HOST = "127.0.0.1"
WEB, METRICS, ALPHA, BETA, DASH, ATTENTION = range(18980, 18986)
COUNTERS = ("http_requests_total", "http_404_total", "http_4xx_total", "http_5xx_total")
TIME = "http_request_time_total_seconds"
TARGETS = {"alpha": "alpha@file", "beta": "beta@file", "replacement": "beta-new@file"}


class Backend(BaseHTTPRequestHandler):
    def do_GET(self):
        action = self.path.rsplit("/", 1)[-1]
        if action in ("slow", "timeout"):
            time.sleep(0.12 if action == "slow" else 0.9)
        self.send_response({"ok": 200, "slow": 200, "timeout": 200, "bad": 400,
                            "error": 500}.get(action, 404))
        self.end_headers()

    def log_message(self, *_args):
        pass


def backend(stack, port):
    server = stack.enter_context(ThreadingHTTPServer((HOST, port), Backend))
    threading.Thread(target=server.serve_forever, daemon=True).start()
    stack.callback(server.shutdown)


def get(port, path):
    try:
        with urlopen(f"http://{HOST}:{port}{path}", timeout=3) as response:
            return response.status, response.read()
    except HTTPError as error:
        with error:
            return error.code, error.read()


def wait(check, description, seconds=20):
    end = time.monotonic() + seconds
    while time.monotonic() < end:
        try:
            result = check()
            if result:
                return result
        except URLError:
            pass
        time.sleep(0.05)
    raise RuntimeError(f"Timed out waiting for {description}")


def stop(process):
    if process.poll() is None:
        process.terminate()
        process.wait(timeout=8)


def start(stack, output, name, command):
    log = stack.enter_context((output / f"{name}.log").open("ab"))
    process = subprocess.Popen(command, stdout=log, stderr=log)
    stack.callback(stop, process)
    return process


def write_routes(directory, beta="beta", empty=False):
    services, routers = {}, {}
    for path, name, port in (("alpha", "alpha", ALPHA), ("beta", beta, BETA)):
        servers = [] if path == "beta" and empty else [{"url": f"http://{HOST}:{port}"}]
        services[name] = {"loadBalancer": {"serversTransport": "limited", "servers": servers}}
        routers[path] = {"rule": f"PathPrefix(`/{path}/`)", "service": name, "entryPoints": ["web"]}
    pending = directory / "pending"
    pending.write_text(json.dumps({"http": {"routers": routers, "services": services,
        "serversTransports": {"limited": {"forwardingTimeouts": {"responseHeaderTimeout": "400ms"}}}}}))
    pending.replace(directory / "dynamic.yml")


def write_statlite(path, port, database, interval, targets=TARGETS):
    config = {
        "server": {"listen": f"{HOST}:{port}"}, "storage": {"sqlite_path": str(database)},
        "polling": {"interval": interval, "timeout": "1s"},
        "attention": {"error_rate_percent": 10, "min_requests": 3, "min_errors": 1},
        "targets": [{"name": name, "type": "traefik", "url": f"http://{HOST}:{METRICS}/metrics",
                     "service": service} for name, service in targets.items()],
    }
    path.write_text(json.dumps(config))


def api(port, route, name):
    status, body = get(port, f"/api/{route}?target={name}&range=1h")
    return json.loads(body) if status == 200 else None


def latest(name, after=0, status="ok", started_after=None):
    def ready():
        snap = api(DASH, "latest", name)
        if snap and snap["poll_id"] > after and snap["status"] == status:
            started = datetime.fromisoformat(snap["result"]["poll_started_at"])
            if started_after is None or started > started_after:
                return snap
        return None
    return wait(ready, f"{name} {status} poll after {after}")


def values(snap):
    return {sample["key"]: sample["value"] for sample in snap["result"]["samples"]}


def dashboard(name):
    summary = api(DASH, "summary", name)
    assert summary["selected_target"]["name"] == name, summary
    assert summary["selected_target"]["type"] == "traefik", summary
    assert summary["latest_restart_status"] == "none", summary
    return api(DASH, "series", name)["points"]


def scrape():
    status, body = get(METRICS, "/metrics")
    assert status == 200
    return body.decode()


def durations(metrics, service):
    return sum(float(value) for labels, value in re.findall(
        r'^traefik_service_request_duration_seconds_sum\{([^}]+)\} (\S+)$', metrics, re.M)
        if f'service="{service}"' in labels)


def version(binary):
    return subprocess.check_output([binary, "version"], text=True, stderr=subprocess.STDOUT).strip()


def attention_window(binary, output, database, label, before, after, expected):
    """Use the live poll pair with a controlled 290-second cadence in a throwaway copy."""
    copy = output / f"attention-{label}.sqlite"
    with closing(sqlite3.connect(database)) as source, closing(sqlite3.connect(copy)) as dest:
        source.backup(dest)
        ids = (before["poll_id"], after["poll_id"])
        dest.execute("DELETE FROM metric_samples WHERE poll_id NOT IN (?, ?)", ids)
        dest.execute("DELETE FROM collector_events WHERE poll_id NOT IN (?, ?)", ids)
        dest.execute("DELETE FROM polls WHERE id NOT IN (?, ?)", ids)
        now = datetime.now(timezone.utc) - timedelta(seconds=1)
        for poll, offset in zip(ids, (290, 0)):
            at = now - timedelta(seconds=offset)
            started = at.isoformat(timespec="microseconds").removesuffix("+00:00").rstrip("0").rstrip(".")
            finished = (at + timedelta(seconds=1)).isoformat().removesuffix("+00:00")
            dest.execute("UPDATE polls SET started_at=?, finished_at=? WHERE id=?", (started, finished, poll))
        dest.commit()
    path = output / f"attention-{label}.json"
    write_statlite(path, ATTENTION, copy, "5m", {"alpha": TARGETS["alpha"]})
    with ExitStack() as child:
        process = start(child, output, label, [binary, "--config", str(path), "--no-poll"])
        response = wait(lambda: api(ATTENTION, "attention", "alpha"), "Attention response")
        stop(process)
    if expected:
        assert len(response["conditions"]) == 1, response
        condition = response["conditions"][0]
        assert (condition["requests"], condition["errors"], condition["rate"],
                condition["threshold"], condition["covered_seconds"]) == (10, 2, 0.2, 0.1, 290), response
    else:
        assert response["conditions"] == [] and "http_5xx_rate" in response.get("cleared_checks", []), response
    return response


def run(binary, traefik, output, hold):
    traefik_version = version(traefik)
    assert re.search(r"(?m)^Version:\s+3\.8\.0\s*$", traefik_version), traefik_version
    report = {"statlite_sha256": hashlib.file_digest(open(binary, "rb"), "sha256").hexdigest(),
              "traefik_sha256": hashlib.file_digest(open(traefik, "rb"), "sha256").hexdigest(),
              "traefik_version": traefik_version, "python": sys.version,
              "batches": {}, "attention": {}}
    database = output / "statlite.sqlite"
    cfg = output / "statlite.json"
    write_statlite(cfg, DASH, database, "3s")
    dynamic = output / "dynamic"
    dynamic.mkdir()
    write_routes(dynamic)
    with ExitStack() as stack:
        backend(stack, ALPHA)
        beta = stack.enter_context(ExitStack())
        backend(beta, BETA)

        def proxy():
            return start(stack, output, "traefik", [traefik, f"--entrypoints.web.address={HOST}:{WEB}",
                f"--entrypoints.metrics.address={HOST}:{METRICS}", "--metrics.prometheus.entrypoint=metrics",
                "--metrics.prometheus.addserviceslabels=true", "--metrics.prometheus.addrouterslabels=true",
                "--metrics.prometheus.addentrypointslabels=true", f"--providers.file.directory={dynamic}"])

        edge = proxy()
        for service in ("alpha", "beta"):
            wait(lambda: get(WEB, f"/{service}/ok")[0] == 200, f"Traefik {service} route")
        start(stack, output, "statlite", [binary, "--config", str(cfg)])
        cold = latest("replacement", status="error")
        assert "HTTP request metrics not observed" in cold["error_summary"], cold
        for service, capability in (("alpha", "compatible"), ("replacement", "partial")):
            inspect = subprocess.check_output([binary, "inspect", "--type", "traefik", "--service",
                TARGETS[service], f"http://{HOST}:{METRICS}/metrics"], text=True)
            assert f"Compatibility: {capability}" in inspect, inspect
            (output / f"inspect-{service}.txt").write_text(inspect)
        assert get(DASH, "/?target=alpha")[0] == 200 and get(DASH, "/?target=beta")[0] == 200

        def batch(label, traffic, expected, *, names=("alpha", "beta"), slow=False):
            # Baseline every target before traffic. Different targets can poll
            # at different phases, especially when one recovers from failure.
            before = {n: latest(n) for n in names}
            old = scrape()
            for path, status in traffic:
                actual, _ = get(WEB, path)
                assert actual == status, (label, path, actual, status)
            new = scrape()
            completed = datetime.now(timezone.utc)
            after = {n: latest(n, before[n]["poll_id"], started_after=completed) for n in names}
            observed = {}
            for name in names:
                first, last = before[name], after[name]
                assert last["app_run_id"] == first["app_run_id"], (label, name, first, last)
                a, b = values(first), values(last)
                assert set(b) == {*COUNTERS, TIME}, (label, name, b)
                delta = tuple(b[key] - a[key] for key in COUNTERS)
                assert delta == expected.get(name, (0, 0, 0, 0)), (label, name, delta)
                elapsed = b[TIME] - a[TIME]
                source = durations(new, TARGETS[name]) - durations(old, TARGETS[name])
                assert abs(elapsed - source) < 1e-8, (label, elapsed, source)
                # Dashboard API uses the shipped sampling and aggregation path.
                points = [point for point in dashboard(name)
                          if first["poll_id"] < point["poll_id"] <= last["poll_id"]]
                assert points and points[-1]["poll_id"] == last["poll_id"], (label, name, points)
                assert tuple(sum(point[key] for point in points)
                             for key in ("requests", "http_404", "http_4xx", "http_5xx")) == delta, points
                durations_by_poll = []
                for point in points:
                    avg = point["average_latency_seconds"]
                    assert point["heap_used_bytes"] is None and point["process_cpu_usage"] is None, point
                    if point["requests"]:
                        assert avg is not None, point
                        durations_by_poll.append(avg * point["requests"])
                    else:
                        assert avg is None, point
                assert abs(sum(durations_by_poll) - source) < 1e-8, points
                avg = sum(durations_by_poll) / delta[0] if delta[0] else None
                if slow and name == "alpha":
                    assert 0.12 <= avg < 0.4, points
                assert "process_start_time" not in last["result"], last
                observed[name] = {"deltas": delta, "duration_seconds": elapsed, "average_seconds": avg}
            report["batches"][label] = {"traffic": traffic, "observed": observed}
            print(label, flush=True)
            return before, after

        batch("scrapes-only", [], {})
        before, after = batch("healthy", [("/alpha/ok", 200)] * 3, {"alpha": (3, 0, 0, 0)})
        report["attention"]["healthy"] = attention_window(binary, output, database,
            "healthy", before["alpha"], after["alpha"], False)
        batch("404-and-400", [("/alpha/missing", 404), ("/alpha/bad", 400)], {"alpha": (2, 1, 2, 0)})
        batch("unmatched-router", [("/unmatched", 404)] * 3, {})
        batch("slow", [("/alpha/slow", 200)] * 3, {"alpha": (3, 0, 0, 0)}, slow=True)
        before, after = batch("mixed", [("/alpha/ok", 200)] * 8 + [("/alpha/error", 500)] * 2,
                              {"alpha": (10, 0, 0, 2)})
        report["attention"]["elevated"] = attention_window(binary, output, database,
            "elevated", before["alpha"], after["alpha"], True)
        before, after = batch("recovered", [("/alpha/ok", 200)] * 3, {"alpha": (3, 0, 0, 0)})
        report["attention"]["recovered"] = attention_window(binary, output, database,
            "recovered", before["alpha"], after["alpha"], False)
        batch("beta-isolation", [("/beta/error", 500)] * 2, {"beta": (2, 0, 0, 2)})
        batch("upstream-timeout", [("/alpha/timeout", 504)] * 3, {"alpha": (3, 0, 0, 3)})
        beta.close()
        batch("backend-offline", [("/beta/ok", 502)] * 3, {"beta": (3, 0, 0, 3)})
        backend(beta, BETA)
        batch("backend-restarted", [("/beta/ok", 200)] * 3, {"beta": (3, 0, 0, 0)})

        before_reload = latest("alpha")
        write_routes(dynamic, empty=True)
        # The removed upstream takes effect after the provider throttle delay.
        def reload_applied():
            snap = latest("alpha", before_reload["poll_id"])
            return snap if snap["app_run_id"] != before_reload["app_run_id"] else None
        reload_poll = wait(reload_applied, "reload baseline")
        assert next(p for p in dashboard("alpha") if p["poll_id"] == reload_poll["poll_id"])["requests"] is None
        batch("empty-pool", [("/beta/ok", 503)] * 3, {})
        write_routes(dynamic, beta="beta-new")
        wait(lambda: get(WEB, "/beta/ok")[0] == 200, "replacement route")
        latest("beta", status="error")
        latest("replacement")
        batch("replacement-service", [("/beta/ok", 200)] * 3, {"replacement": (3, 0, 0, 0)},
              names=("alpha", "replacement"))

        before_restart = latest("alpha")
        stop(edge)
        latest("alpha", before_restart["poll_id"], "error")
        proxy()
        wait(lambda: get(WEB, "/alpha/ok")[0] == 200, "proxy restart")
        restart_poll = latest("alpha", before_restart["poll_id"])
        assert restart_poll["app_run_id"] != before_restart["app_run_id"]
        assert next(p for p in dashboard("alpha") if p["poll_id"] == restart_poll["poll_id"])["requests"] is None
        wait(lambda: get(WEB, "/beta/ok")[0] == 200, "replacement route after restart")
        # Keep readiness traffic out of the next batch on both targets.
        ready_at = datetime.now(timezone.utc)
        for name in ("alpha", "replacement"):
            latest(name, started_after=ready_at)
        batch("proxy-restarted", [("/alpha/ok", 200)] * 3 + [("/alpha/error", 500)],
              {"alpha": (4, 0, 0, 1)}, names=("alpha", "replacement"))

        first = latest("alpha")
        end = time.monotonic() + 14
        traffic = 0
        while time.monotonic() < end:
            assert get(WEB, "/alpha/slow")[0] == 200
            traffic += 1
        last = latest("alpha", first["poll_id"], started_after=datetime.now(timezone.utc))
        points = [p for p in dashboard("alpha") if first["poll_id"] < p["poll_id"] <= last["poll_id"]]
        assert sum(p["requests"] or 0 for p in points) == traffic, points
        eligible = [p for p in points if p["requests"]]
        matched = [p for p in eligible if p["average_latency_seconds"] is not None]
        assert matched and all(0.12 <= p["average_latency_seconds"] < 0.4 for p in matched), matched
        report["latency_coverage"] = {"traffic": traffic, "eligible": len(eligible),
                                      "matched": len(matched), "omitted": len(eligible) - len(matched)}
        report["status"] = "PASS"
        (output / "report.json").write_text(json.dumps(report, indent=2) + "\n")
        print(f"PASS {output / 'report.json'}", flush=True)
        if hold:
            print(f"Dashboard available for {hold}s at http://{HOST}:{DASH}/", flush=True)
            time.sleep(hold)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("statlite", type=Path)
    parser.add_argument("traefik", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--hold", type=int, default=0, help="leave the dashboard up for browser inspection")
    args = parser.parse_args()
    output = args.output.resolve()
    output.mkdir()
    run(str(args.statlite.resolve()), str(args.traefik.resolve()), output, args.hold)
