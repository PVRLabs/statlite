package server

// This file serves embedded static assets used by the dashboard.

import (
	"net/http"

	"github.com/pvrlabs/statlite/internal/dashboard"
)

func (s *Server) handleStatliteIcon(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/static/statlite-icon.png" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=604800")
	w.Write(dashboard.StatliteIconPNG)
}
