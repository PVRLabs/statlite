"""Small, copyable StatLite Metrics v1 helper for a one-process FastAPI app."""

from __future__ import annotations

import math
import threading
import time
from typing import Any, Awaitable, Callable


SCHEMA = "statlite-metrics/v1"
DEFAULT_METRICS_PATH = "/statlite/metrics"


class StatLiteMetrics:
    """Collect the fixed StatLite Metrics v1 profile in process memory.

    Counters are local to this object, so run one application process (for
    example, one Uvicorn worker) when using this simple implementation.
    """

    def __init__(self, metrics_path: str = DEFAULT_METRICS_PATH) -> None:
        self.metrics_path = metrics_path
        self._last_cpu_wall = time.monotonic()
        self._last_cpu_time = time.process_time()
        self._lock = threading.Lock()

        self._requests_total = 0
        self._responses_404_total = 0
        self._responses_4xx_total = 0
        self._responses_5xx_total = 0
        self._request_duration_seconds_total = 0.0

    async def middleware(
        self,
        request: Any,
        call_next: Callable[[Any], Awaitable[Any]],
    ) -> Any:
        """FastAPI/Starlette HTTP middleware for application request metrics."""
        if request.url.path == self.metrics_path:
            return await call_next(request)

        started = time.perf_counter()
        status_code: int | None = None
        try:
            response = await call_next(request)
            status_code = response.status_code
            return response
        except Exception:
            # FastAPI's outer error middleware will turn this into a 500.
            status_code = 500
            raise
        finally:
            self._record_request(status_code, time.perf_counter() - started)

    def snapshot(self) -> dict[str, Any]:
        """Return one complete, JSON-serializable StatLite Metrics v1 response."""
        with self._lock:
            now = time.monotonic()
            process_cpu_time = time.process_time()
            wall_delta = now - self._last_cpu_wall
            cpu_delta = process_cpu_time - self._last_cpu_time
            process_cpu_usage = cpu_delta / wall_delta if wall_delta > 0 else 0.0
            if not math.isfinite(process_cpu_usage) or process_cpu_usage < 0:
                process_cpu_usage = 0.0
            self._last_cpu_wall = now
            self._last_cpu_time = process_cpu_time

            metrics: dict[str, int | float] = {
                "requests_total": self._requests_total,
                "responses_404_total": self._responses_404_total,
                "responses_4xx_total": self._responses_4xx_total,
                "responses_5xx_total": self._responses_5xx_total,
                "request_duration_seconds_total": (
                    self._request_duration_seconds_total
                ),
                # CPU time consumed during the interval divided by wall time:
                # 1.0 means one logical CPU core was fully used.
                "process_cpu_usage": process_cpu_usage,
            }

        return {
            "schema": SCHEMA,
            "integration": "fastapi",
            "status": "UP",
            "metrics": metrics,
        }

    def _record_request(self, status_code: int | None, duration: float) -> None:
        if not math.isfinite(duration) or duration < 0:
            duration = 0.0

        with self._lock:
            self._requests_total += 1
            self._request_duration_seconds_total += duration

            if status_code == 404:
                self._responses_404_total += 1
            if status_code is not None and 400 <= status_code < 500:
                self._responses_4xx_total += 1
            if status_code is not None and 500 <= status_code < 600:
                self._responses_5xx_total += 1
