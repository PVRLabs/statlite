package main

import (
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

func newEngine(processStartedAt time.Time) *gin.Engine {
	recorder := newStatLiteRecorder(processStartedAt, "UP")
	engine := gin.New()
	engine.Use(recorder.middleware)
	engine.Use(gin.Recovery())

	engine.GET(statLiteMetricsPath, recorder.metricsHandler)
	engine.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "hello\n") })
	engine.GET("/implicit", func(*gin.Context) {})
	engine.GET("/client-error", func(c *gin.Context) {
		c.String(http.StatusTeapot, "controlled client error\n")
	})
	engine.GET("/failure", func(c *gin.Context) {
		c.String(http.StatusInternalServerError, "controlled server error\n")
	})
	engine.GET("/panic", func(*gin.Context) { panic("controlled panic") })

	return engine
}

func main() {
	processStartedAt := time.Now()
	server := &http.Server{
		Addr:              "127.0.0.1:8080",
		Handler:           newEngine(processStartedAt),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("Go Gin demo listening on http://%s", server.Addr)
	log.Fatal(server.ListenAndServe())
}
