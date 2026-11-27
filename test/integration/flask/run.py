"""Smoke-test StatLite against Flask's default Prometheus exporter."""

import argparse
from contextlib import ExitStack
import json
from pathlib import Path
import subprocess
from tempfile import TemporaryDirectory
from threading import Thread
import time
from urllib.error import HTTPError, URLError
from urllib.request import urlopen

from flask import Flask
from prometheus_flask_exporter import PrometheusMetrics
from werkzeug.serving import make_server

HOST = "127.0.0.1"


def start_app(stack):
    app = Flask(__name__)
    PrometheusMetrics(app)
    app.add_url_rule("/ok", "ok", lambda: "ok")
    app.add_url_rule("/error", "error", lambda: ("error", 500))

    server = make_server(HOST, 0, app)
    worker = Thread(target=server.serve_forever)
    worker.start()
    stack.callback(server.server_close)
    stack.callback(worker.join)
    stack.callback(server.shutdown)
    return f"http://{HOST}:{server.server_port}"


def request_status(url):
    try:
        with urlopen(url, timeout=1) as response:
            return response.status
    except HTTPError as error:
        with error:
            return error.code


def send_batch(app_url):
    for path, expected in (("/ok", 200), ("/missing", 404), ("/error", 500)):
        assert request_status(app_url + path) == expected, path


def api(url, route):
    with urlopen(f"{url}/api/{route}?target=flask&range=1h", timeout=1) as response:
        return json.load(response)


def wait_for_poll(process, url, requests, after=0):
    deadline = time.monotonic() + 10
    latest = None
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError(f"StatLite exited with status {process.returncode}")
        try:
            latest = api(url, "latest")
            if latest["status"] == "ok" and latest["poll_id"] > after:
                samples = {sample["key"]: sample["value"] for sample in latest["result"]["samples"]}
                if samples.get("http_requests_total", 0) >= requests:
                    return latest["poll_id"]
        except URLError:
            pass
        time.sleep(0.05)
    raise RuntimeError(f"Timed out waiting for {requests} requests. Latest poll: {latest}")


def smoke(binary, port):
    url = f"http://{HOST}:{port}"
    with ExitStack() as stack:
        directory = Path(stack.enter_context(TemporaryDirectory(prefix="statlite-flask-")))
        app_url = start_app(stack)
        # Initialize all status tuples so the baseline also works on platforms
        # where the exporter does not supply a process identity.
        send_batch(app_url)
        config = directory / "statlite.json"
        config.write_text(json.dumps({
            "server": {"listen": f"{HOST}:{port}"},
            "storage": {"sqlite_path": str(directory / "statlite.sqlite")},
            "polling": {"interval": "1s", "timeout": "1s"},
            "targets": [{"name": "flask", "type": "flask", "url": f"{app_url}/metrics"}],
        }))
        # Inherit output so CI includes StatLite's startup and collection errors.
        with subprocess.Popen([binary, "--config", str(config)]) as process:
            try:
                before = wait_for_poll(process, url, requests=3)
                send_batch(app_url)
                after = wait_for_poll(process, url, requests=6, after=before)
                points = [point for point in api(url, "series")["points"] if before < point["poll_id"] <= after]
                assert points and points[-1]["poll_id"] == after, points
                for key, expected in (("requests", 3), ("http_404", 1), ("http_4xx", 1), ("http_5xx", 1)):
                    counts = [point[key] for point in points]
                    assert None not in counts and sum(counts) == expected, (key, points)
                for point in points:
                    if point["requests"] > 0:
                        assert point["average_latency_seconds"] is not None, point
            finally:
                if process.poll() is None:
                    process.terminate()
                    try:
                        process.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        process.kill()
    print("Flask smoke test passed")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("statlite", type=Path)
    parser.add_argument("--port", type=int, default=19090, help="loopback port for StatLite")
    args = parser.parse_args()
    smoke(str(args.statlite.resolve()), args.port)
