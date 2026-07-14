package server

// This file serves the dashboard HTML entry point.

import (
	"fmt"
	"net/http"

	"github.com/pvrlabs/statlite/internal/dashboard"
)

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, dashboard.Page)
}
