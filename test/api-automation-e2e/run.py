#!/usr/bin/env python3
"""Test the public v1 API through normal polling and shipped automation recipes."""

import json
import os
from datetime import datetime
from pathlib import Path
import shutil
import socket
import subprocess
import sys
import tempfile
import time
from urllib.error import URLError
from urllib.parse import urlencode
from urllib.request import urlopen


ROOT = Path(__file__).resolve().parents[2]
HERE = Path(__file__).resolve().parent
TARGET = "automation-demo-app"
DEADLINE_SECONDS = 480


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def get_json(base, path):
    url = f"{base}/api/v1/{path}?{urlencode({'target': TARGET})}"
    with urlopen(url, timeout=5) as response:
        return json.load(response)


def recipe(name, base, expected, **settings):
    env = os.environ.copy()
    env.update(BASE_URL=base, TARGET=TARGET, NO_PROXY="127.0.0.1,localhost")
    env.update({key: str(value) for key, value in settings.items()})
    script = ROOT / "examples/api-automation" / f"check-{name}.sh"
    result = subprocess.run(["sh", str(script)], env=env, text=True,
                            capture_output=True, timeout=20, check=False)
    print(f"recipe {name}: exit={result.returncode} {result.stdout.strip()} {result.stderr.strip()}", flush=True)
    if result.returncode != expected:
        raise AssertionError(f"{name}: expected exit {expected}, got {result.returncode}")


def latest_http(metrics):
    return next((p for p in reversed(metrics["points"])
                 if any(p[k] is not None for k in
                        ("requests", "avg_latency_ms", "http_4xx", "http_5xx"))), None)


def wait_until(label, predicate, base, processes, started, deadline, evidence):
    while time.monotonic() < deadline:
        for name, proc in processes.items():
            if proc.poll() is not None:
                raise AssertionError(f"{name} exited with {proc.returncode} while waiting for {label}")
        try:
            status = get_json(base, "status")
            metrics = get_json(base, "metrics")
            if status.get("target") != TARGET or metrics.get("target") != TARGET:
                raise AssertionError("public API returned an unexpected target")
            evidence["status"] = status
            evidence["metrics"] = metrics
            evidence["slots"].update(p["timestamp"] for p in metrics["points"])
            if predicate(status, metrics, time.monotonic() - started):
                print(f"observed {label} at {time.monotonic() - started:.1f}s; "
                      f"slots={len(evidence['slots'])}", flush=True)
                return status, metrics
        except (URLError, TimeoutError, OSError, ValueError) as exc:
            evidence["api_error"] = repr(exc)
        time.sleep(2)
    raise AssertionError(f"timed out waiting for {label}")


def run():
    overall_started = time.monotonic()
    for tool in ("go", "curl", "jq"):
        if shutil.which(tool) is None:
            raise RuntimeError(f"required tool missing: {tool}")
    work = Path(tempfile.mkdtemp(prefix="statlite-api-e2e-"))
    logs = {}
    processes = {}
    evidence = {"slots": set()}
    success = False
    try:
        app_port = free_port()
        api_port = free_port()
        while api_port == app_port:
            api_port = free_port()
        binary = work / "statlite"
        print("building current StatLite", flush=True)
        subprocess.run(["go", "build", "-trimpath", "-o", str(binary), "./cmd/statlite"],
                       cwd=ROOT, check=True, timeout=180)
        config = work / "statlite.yaml"
        config.write_text(
            f'server:\n  listen: "127.0.0.1:{api_port}"\n'
            f'storage:\n  sqlite_path: "{work / "statlite.sqlite"}"\n'
            'polling:\n  interval: "10s"\n  timeout: "5s"\n'
            f'targets:\n  - name: "{TARGET}"\n    type: "statlite-metrics"\n'
            f'    url: "http://127.0.0.1:{app_port}/statlite/metrics"\n')
        for name, command in (
            ("application", [sys.executable, str(HERE / "demo_app.py"), "--port", str(app_port)]),
            ("statlite", [str(binary), "--config", str(config)]),
        ):
            logs[name] = open(work / f"{name}.log", "w")
            processes[name] = subprocess.Popen(command, cwd=ROOT, stdout=logs[name],
                                               stderr=subprocess.STDOUT,
                                               env={**os.environ, "PYTHONDONTWRITEBYTECODE": "1"})
            if name == "application":
                app_started = time.monotonic()
                # Allow the listener to bind before starting normal StatLite polling.
                for _ in range(50):
                    try:
                        with urlopen(f"http://127.0.0.1:{app_port}/statlite/metrics", timeout=1):
                            break
                    except (URLError, OSError):
                        if processes[name].poll() is not None:
                            raise AssertionError("application exited during startup")
                        time.sleep(0.1)
                else:
                    raise AssertionError("application did not become ready")
        base = f"http://127.0.0.1:{api_port}"
        print(f"Live v1 API: {base}/api/v1 (target={TARGET})", flush=True)
        print(f"For another terminal: export BASE_URL={base} TARGET={TARGET}", flush=True)
        print("Try: curl -fsS '" + base + "/api/v1/metrics?target=" + TARGET + "' | jq .", flush=True)
        deadline = overall_started + DEADLINE_SECONDS

        def baseline(s, m, elapsed):
            return (10 <= elapsed < 60 and s["collection_status"] == "ok"
                    and s["application_health"] == "UP"
                    and any(p["host_memory_usage"] is not None and
                            abs(p["host_memory_usage"] - 0.4) < 0.001
                            for p in m["points"]))

        wait_until("healthy baseline", baseline, base, processes, app_started, deadline, evidence)
        recipe("status", base, 0)
        recipe("host-resources", base, 0)
        recipe("http-5xx", base, 2, MIN_REQUESTS=50)
        recipe("sustained-cpu", base, 2, CONSECUTIVE_MINUTES=3)

        def elevated(s, m, elapsed):
            point = latest_http(m)
            return (90 <= elapsed < 180 and point is not None
                    and point["requests"] is not None and point["requests"] >= 20
                    and point["http_5xx_rate"] is not None
                    and abs(point["http_5xx_rate"] - 0.4) < 0.0001
                    and point["avg_latency_ms"] is not None
                    and abs(point["avg_latency_ms"] - 500) < 0.01
                    and any(p["runtime_memory_bytes"] == 268435456
                            and p["host_memory_usage"] is not None
                            and abs(p["host_memory_usage"] - 0.95) < 0.001
                            and p["host_disk_usage"] is not None
                            and abs(p["host_disk_usage"] - 0.94) < 0.001
                            for p in m["points"]))

        _, high = wait_until("derived 5xx, latency, memory and host pressure",
                             elevated, base, processes, app_started, deadline, evidence)
        print("high HTTP point:", latest_http(high), flush=True)
        recipe("http-5xx", base, 1, MIN_REQUESTS=10, MAX_5XX_RATE=0.05)
        recipe("host-resources", base, 1, MAX_MEMORY_USAGE=0.90, MAX_DISK_USAGE=0.90)

        def missing(s, m, elapsed):
            point = latest_http(m)
            return (195 <= elapsed and point is not None and point["requests"] > 0
                    and point["http_5xx_rate"] is None
                    and len(evidence["slots"]) >= 3)

        _, missing_metrics = wait_until("unavailable paired 5xx across three minute slots",
                                        missing, base, processes, app_started, deadline, evidence)
        print("missing-pair HTTP point:", latest_http(missing_metrics), flush=True)
        recipe("http-5xx", base, 2, MIN_REQUESTS=1)

        def final(s, m, elapsed):
            return elapsed >= 215 and s["application_health"] == "DOWN"

        wait_until("reported DOWN health", final, base, processes, app_started, deadline, evidence)
        recipe("status", base, 1, UNHEALTHY_VALUES="DOWN")

        def cpu_ready(s, m, elapsed):
            points = m["points"][-3:]
            return (len(points) == 3 and
                    all(p["process_cpu_cores"] is not None and
                        p["process_cpu_cores"] > 0.8 for p in points) and
                    all((datetime.fromisoformat(points[i]["timestamp"].replace("Z", "+00:00")) -
                         datetime.fromisoformat(points[i-1]["timestamp"].replace("Z", "+00:00"))).total_seconds() == 60
                        for i in (1, 2)))

        wait_until("three consecutive high CPU minute points", cpu_ready, base,
                   processes, app_started, deadline, evidence)
        recipe("sustained-cpu", base, 1, CONSECUTIVE_MINUTES=3)
        recipe("sustained-cpu", base, 0, CONSECUTIVE_MINUTES=3,
               CPU_CORES_THRESHOLD=2)
        print(f"PASS: {len(evidence['slots'])} public occupied minute slots; "
              "all recipe outcomes observed", flush=True)
        success = True
    finally:
        for proc in processes.values():
            if proc.poll() is None:
                proc.terminate()
        for proc in processes.values():
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()
        for log in logs.values():
            log.close()
        if success:
            shutil.rmtree(work)
        else:
            diagnostics = json.dumps(
                {**evidence, "slots": sorted(evidence["slots"])}, indent=2)
            (work / "last-public-api.json").write_text(diagnostics)
            print(f"--- last public API responses ---\n{diagnostics}", file=sys.stderr)
            print(f"local diagnostics directory: {work}", file=sys.stderr)
            for name in ("application", "statlite"):
                path = work / f"{name}.log"
                if path.exists():
                    print(f"--- {name} log ---\n{path.read_text()[-8000:]}", file=sys.stderr)


if __name__ == "__main__":
    try:
        run()
    except (AssertionError, RuntimeError, subprocess.CalledProcessError,
            subprocess.TimeoutExpired) as exc:
        print(f"FAIL: {exc}", file=sys.stderr)
        sys.exit(1)
