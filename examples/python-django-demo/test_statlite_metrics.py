"""Integration checks for the Django StatLite Metrics demo."""

import os
import unittest


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
        self.assertEqual(snapshot["status"], "UP")
        self.assertNotIn("started_at", snapshot)
        self.assertIn("process_cpu_usage", snapshot["metrics"])
        self.assertNotIn("runtime_heap_used_bytes", snapshot["metrics"])
        self.assertNotIn("uptime_seconds", snapshot["metrics"])

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
