import threading
import time
import tracemalloc
from datetime import datetime, timezone

from django.http import JsonResponse
from django.views.decorators.http import require_GET


METRICS_PATH = "/statlite/metrics"


class StatLiteMetrics:
    def __init__(self):
        self._lock = threading.Lock()
        self._started_at = datetime.now(timezone.utc)
        self._started_monotonic = time.monotonic()
        self._previous_cpu = time.process_time()
        self._previous_cpu_time = self._started_monotonic
        if not tracemalloc.is_tracing():
            tracemalloc.start()
        self._requests = 0
        self._responses_404 = 0
        self._responses_4xx = 0
        self._responses_5xx = 0
        self._duration_seconds = 0.0

    def record(self, status_code, duration_seconds):
        with self._lock:
            self._requests += 1
            self._duration_seconds += duration_seconds
            if status_code == 404:
                self._responses_404 += 1
            if 400 <= status_code < 500:
                self._responses_4xx += 1
            if 500 <= status_code < 600:
                self._responses_5xx += 1

    def snapshot(self):
        runtime_heap_used_bytes, _peak = tracemalloc.get_traced_memory()
        with self._lock:
            now = time.monotonic()
            cpu = time.process_time()
            elapsed = now - self._previous_cpu_time
            process_cpu_usage = (
                (cpu - self._previous_cpu) / elapsed if elapsed > 0 else 0.0
            )
            self._previous_cpu = cpu
            self._previous_cpu_time = now
            metrics = {
                "requests_total": self._requests,
                "responses_404_total": self._responses_404,
                "responses_4xx_total": self._responses_4xx,
                "responses_5xx_total": self._responses_5xx,
                "request_duration_seconds_total": self._duration_seconds,
                "process_cpu_usage": process_cpu_usage,
                "runtime_heap_used_bytes": runtime_heap_used_bytes,
                "uptime_seconds": max(0.0, now - self._started_monotonic),
            }

        return {
            "schema": "statlite-metrics/v1",
            "integration": "django",
            "status": "UP",
            "started_at": self._started_at.isoformat(timespec="microseconds").replace(
                "+00:00", "Z"
            ),
            "metrics": metrics,
        }


metrics = StatLiteMetrics()


class StatLiteMetricsMiddleware:
    def __init__(self, get_response):
        self.get_response = get_response

    def __call__(self, request):
        if request.path == METRICS_PATH:
            return self.get_response(request)

        started = time.monotonic()
        try:
            response = self.get_response(request)
        except Exception:
            metrics.record(500, time.monotonic() - started)
            raise

        metrics.record(response.status_code, time.monotonic() - started)
        return response


@require_GET
def statlite_metrics_view(request):
    return JsonResponse(metrics.snapshot())
