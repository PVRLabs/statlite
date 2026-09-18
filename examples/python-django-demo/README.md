# Django StatLite Metrics demo

This tiny application accompanies the canonical
[Django integration guide](../../docs/integrate/python/django.md). The guide
contains the complete copyable integration and explains its fields,
single-worker scope, and deployment caveats.

## Run the demo

From this directory, use Python 3.14.6 and install the pinned Django version:

```bash
python3 -m venv .venv
source .venv/bin/activate
python -m pip install -r requirements.txt
python manage.py runserver 127.0.0.1:8000 --noreload
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
go run ./cmd/statlite --config examples/python-django-demo/statlite.yaml
```

Open <http://127.0.0.1:9090>. The demo polls every 10 seconds for responsive
local feedback. Use a 30-second or longer interval in production.

## Run the tests

From this directory with Django installed:

```bash
python -m unittest test_statlite_metrics.py
```

The tests exercise Django's actual normal, framework-generated 404, and 500
response behavior. They also verify that repeated `/statlite/metrics` polling
does not increment application counters.
