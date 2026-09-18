import threading
import time

from django.http import JsonResponse
from django.views.decorators.http import require_GET


METRICS_PATH = "/statlite/metrics"


class StatLiteMetrics:
    def __init__(self):
        self._lock = threading.Lock()
        self._previous_cpu = time.process_time()
        self._previous_cpu_time = time.monotonic()
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
            }

        return {
            "schema": "statlite-metrics/v1",
            "integration": "django",
            "status": "UP",
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
