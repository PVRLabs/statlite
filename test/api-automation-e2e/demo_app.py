"""Deterministic, synthetic StatLite Metrics v1 producer for the E2E test."""

import argparse
import json
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def snapshot(elapsed):
    tick = min(int(elapsed // 30), 7)
    # Every value is scripted. No request workload, CPU pressure, allocation, or
    # disk consumption is created. The first two ticks establish healthy counter
    # baselines; ticks 2-5 add 5xx and latency; ticks 6-7 omit paired 5xx data.
    requests = 100 + 20 * tick
    five_xx = 8 * max(0, min(tick, 5) - 1)
    duration = 10 + 2 * min(tick, 1) + 10 * max(0, min(tick, 5) - 1)
    duration += 2 * max(0, tick - 5)
    high = tick >= 2
    metrics = {
        "requests_total": requests,
        "responses_4xx_total": 2 * tick,
        "request_duration_seconds_total": duration,
        "process_cpu_usage": 1.2 if tick >= 1 else 0.1,
        "runtime_heap_used_bytes": 268435456 if high else 67108864,
        "host_cpu_usage": 0.8 if high else 0.2,
        "host_memory_used_bytes": 95 if high else 40,
        "host_memory_total_bytes": 100,
        "host_disk_used_bytes": 94 if high else 30,
        "host_disk_total_bytes": 100,
    }
    if tick < 6:
        metrics["responses_5xx_total"] = five_xx
    return {
        "schema": "statlite-metrics/v1",
        "integration": "automation-demo-app",
        "status": "DOWN" if tick == 7 else "UP",
        "database_status": "UP",
        "metrics": metrics,
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, required=True)
    args = parser.parse_args()
    started = time.monotonic()

    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            if self.path != "/statlite/metrics":
                self.send_error(404)
                return
            body = json.dumps(snapshot(time.monotonic() - started)).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, format, *args):
            return

    server = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    print(f"automation-demo-app listening on 127.0.0.1:{args.port}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
