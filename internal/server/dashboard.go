package server

// This file serves the dashboard HTML entry point.

import (
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/pvrlabs/statlite/internal/dashboard"
	"github.com/pvrlabs/statlite/internal/version"
)

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page := strings.ReplaceAll(dashboard.Page, "{{dashboard_script_url}}", dashboard.ScriptPath())
	page = strings.ReplaceAll(page, "{{dashboard_version}}", html.EscapeString(version.Version))
	attentionMarkup := ""
	if s.attention.IsEnabled() {
		attentionMarkup = dashboard.AttentionMarkup
	}
	page = strings.Replace(page, "{{dashboard_attention}}", attentionMarkup, 1)
	fmt.Fprint(w, page)
}
