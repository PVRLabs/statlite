# Flask integration smoke test

Run from the public repository root with Go and Python 3.14.

```sh
go build -o /tmp/statlite-flask ./cmd/statlite
python3 -m venv /tmp/statlite-flask-venv
/tmp/statlite-flask-venv/bin/python -m pip install -r test/integration/flask/requirements.txt
/tmp/statlite-flask-venv/bin/python test/integration/flask/run.py /tmp/statlite-flask
```

The test creates one local Flask application with the default
`PrometheusMetrics(app)` exporter and runs StatLite against its metrics
endpoint. It checks collection, counts for three responses (200, 404, and
500), and latency availability. StatLite uses loopback port 19090. Pass
`--port` to use another port.
