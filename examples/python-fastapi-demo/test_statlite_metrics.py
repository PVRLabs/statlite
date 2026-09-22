"""Integration checks for the FastAPI StatLite Metrics demo."""

from __future__ import annotations

import importlib
import sys
import time
import unittest
from datetime import datetime
from pathlib import Path

from fastapi.testclient import TestClient


sys.path.insert(0, str(Path(__file__).parent))

import app as demo_app
from statlite_metrics import SCHEMA


class StatLiteMetricsTests(unittest.TestCase):
    def setUp(self) -> None:
        self.demo = importlib.reload(demo_app)
        self.client = TestClient(self.demo.app, raise_server_exceptions=False)

    def test_snapshot_is_application_and_process_profile_without_host_fields(self) -> None:
        snapshot = self.client.get("/statlite/metrics").json()

        self.assertEqual(snapshot["schema"], SCHEMA)
        self.assertEqual(snapshot["integration"], "fastapi")
        self.assertEqual(snapshot["status"], "UP")
        self.assertIsNotNone(datetime.fromisoformat(snapshot["started_at"].replace("Z", "+00:00")))
        metrics = snapshot["metrics"]
        self.assertIn("process_cpu_usage", metrics)
        self.assertNotIn("cpu_usage", metrics)
        self.assertIsInstance(metrics["runtime_heap_used_bytes"], int)
        self.assertGreaterEqual(metrics["runtime_heap_used_bytes"], 0)
        self.assertGreaterEqual(metrics["uptime_seconds"], 0)
        self.assertNotIn("request_duration_seconds_max", metrics)
        for key in (
            "host_cpu_usage",
            "host_memory_used_bytes",
            "host_memory_total_bytes",
            "host_disk_used_bytes",
            "host_disk_total_bytes",
        ):
            self.assertNotIn(key, metrics)

    def test_real_responses_are_counted_and_metrics_endpoint_is_excluded(self) -> None:
        self.assertEqual(self.client.get("/").status_code, 200)
        self.assertEqual(self.client.get("/missing").status_code, 404)
        self.assertEqual(self.client.get("/failure").status_code, 500)

        first = self.client.get("/statlite/metrics").json()["metrics"]
        second = self.client.get("/statlite/metrics?source=test").json()["metrics"]

        for snapshot in (first, second):
            self.assertEqual(snapshot["requests_total"], 3)
            self.assertEqual(snapshot["responses_404_total"], 1)
            self.assertEqual(snapshot["responses_4xx_total"], 1)
            self.assertEqual(snapshot["responses_5xx_total"], 1)
            self.assertGreater(snapshot["request_duration_seconds_total"], 0)

    def test_process_metrics_remain_valid_across_snapshots(self) -> None:
        first = self.client.get("/statlite/metrics").json()
        time.sleep(0.001)
        second = self.client.get("/statlite/metrics").json()

        self.assertEqual(second["started_at"], first["started_at"])
        self.assertGreaterEqual(
            second["metrics"]["uptime_seconds"], first["metrics"]["uptime_seconds"]
        )
        for snapshot in (first, second):
            memory = snapshot["metrics"]["runtime_heap_used_bytes"]
            self.assertIsInstance(memory, int)
            self.assertGreaterEqual(memory, 0)
