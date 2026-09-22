"""Integration checks for the Django StatLite Metrics demo."""

import os
import time
import unittest
from datetime import datetime


os.environ.setdefault("DJANGO_SETTINGS_MODULE", "demo.settings")

import django


django.setup()

from django.test import Client

from demo.statlite_metrics import metrics


class StatLiteMetricsTests(unittest.TestCase):
    def setUp(self):
        with metrics._lock:
            metrics._requests = 0
            metrics._responses_404 = 0
            metrics._responses_4xx = 0
            metrics._responses_5xx = 0
            metrics._duration_seconds = 0.0
        self.client = Client(raise_request_exception=False)

    def test_snapshot_has_only_promised_application_and_process_fields(self):
        snapshot = self.client.get("/statlite/metrics").json()

        self.assertEqual(snapshot["schema"], "statlite-metrics/v1")
        self.assertEqual(snapshot["integration"], "django")
        self.assertEqual(snapshot["status"], "UP")
        self.assertIsNotNone(datetime.fromisoformat(snapshot["started_at"].replace("Z", "+00:00")))
        self.assertIn("process_cpu_usage", snapshot["metrics"])
        self.assertIsInstance(snapshot["metrics"]["runtime_heap_used_bytes"], int)
        self.assertGreaterEqual(snapshot["metrics"]["runtime_heap_used_bytes"], 0)
        self.assertGreaterEqual(snapshot["metrics"]["uptime_seconds"], 0)

    def test_real_responses_are_counted_and_metrics_endpoint_is_excluded(self):
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

    def test_process_metrics_remain_valid_across_snapshots(self):
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
