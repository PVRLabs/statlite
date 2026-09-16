# FastAPI StatLite Metrics demo

This runnable application accompanies the canonical
[FastAPI integration guide](../../docs/integrate/python/fastapi.md). The guide
contains the complete copyable integration and explains its field semantics,
single-worker scope, and deployment caveats. This directory keeps the helper,
application wiring, configuration, and integration test executable together.

## Run the demo

From this directory, create an environment and install the pinned runtime
dependencies used by the canonical guide:

```bash
python3 -m venv .venv
source .venv/bin/activate
python -m pip install -r requirements.txt
uvicorn app:app --host 127.0.0.1 --port 8000 --workers 1
```

In another terminal, exercise normal, missing, error, and metrics responses:

```bash
curl -s http://127.0.0.1:8000/
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8000/missing
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8000/failure
curl -s http://127.0.0.1:8000/statlite/metrics
statlite inspect 'http://127.0.0.1:8000/statlite/metrics'
```

From the repository root, start StatLite with the demo configuration:

```bash
go run ./cmd/statlite --config examples/python-fastapi-demo/statlite.yaml
```

Open <http://127.0.0.1:9091>. The demo polls every 10 seconds for responsive
local feedback. Use a 30-second or longer interval in production. Its SQLite
file is created in the current working directory.

## Run the tests

From the repository root, install the test dependency into the active
environment and run the FastAPI integration checks:

```bash
python -m pip install -r examples/python-fastapi-demo/requirements-test.txt
python -m unittest examples/python-fastapi-demo/test_statlite_metrics.py
```

The tests exercise FastAPI's actual normal, 404, and unhandled 500 response
behavior. They also verify that `/statlite/metrics`, including a query string,
does not increment application counters.
