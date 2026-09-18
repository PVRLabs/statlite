package main

import (
	"fmt"
	"log"
	"net/http"
	"time"
)

func main() {
	processStartedAt := time.Now()
	recorder := newStatLiteRecorder(processStartedAt, "UP")

	mux := http.NewServeMux()
	mux.HandleFunc(statLiteMetricsPath, recorder.metricsHandler)
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/" {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = fmt.Fprintln(w, "hello")
	})
	mux.HandleFunc("/implicit", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("/client-error", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "controlled client error", http.StatusTeapot)
	})
	mux.HandleFunc("/failure", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "controlled server error", http.StatusInternalServerError)
	})

	server := &http.Server{
		Addr:              "127.0.0.1:8080",
		Handler:           recorder.middleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("Go net/http demo listening on http://%s", server.Addr)
	log.Fatal(server.ListenAndServe())
}
