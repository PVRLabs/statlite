from fastapi import FastAPI

from statlite_metrics import StatLiteMetrics


app = FastAPI(title="StatLite FastAPI demo")
metrics = StatLiteMetrics()
app.middleware("http")(metrics.middleware)


@app.get("/")
async def hello_world() -> dict[str, str]:
    return {"message": "Hello, World!"}


@app.get("/failure")
async def failure() -> None:
    raise RuntimeError("example failure")


@app.get("/statlite/metrics")
def statlite_metrics() -> dict:
    return metrics.snapshot()
