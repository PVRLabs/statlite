const express = require("express");
const {
  METRICS_PATH,
  statliteMetricsEndpoint,
  statliteMetricsMiddleware,
} = require("./statlite-metrics");

function createApp() {
  const app = express();

  app.use(statliteMetricsMiddleware);
  app.get(METRICS_PATH, statliteMetricsEndpoint);

  app.get("/", (req, res) => res.json({ message: "hello" }));
  app.get("/failure", (req, res, next) => next(new Error("example failure")));

  app.use((err, req, res, next) => {
    console.error(err);
    if (res.headersSent) return next(err);
    res.status(500).json({ error: "internal server error" });
  });

  return app;
}

if (require.main === module) {
  createApp().listen(3000, "127.0.0.1", () => {
    console.log("Express demo listening on http://127.0.0.1:3000");
  });
}

module.exports = { createApp };
