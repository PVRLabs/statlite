package dashboard

// This file embeds the dashboard HTML and static image assets.

import (
	"crypto/sha256"
	"fmt"

	_ "embed"
)

//go:embed static/index.html
var Page string

//go:embed static/dashboard.js
var Script string

// ScriptPath is content-addressed so a binary upgrade can safely use immutable
// browser caching without leaving the dashboard paired with an old script.
func ScriptPath() string {
	return contentPath("/static/dashboard", []byte(Script), ".js")
}

//go:embed static/statlite-icon.png
var StatliteIconPNG []byte

//go:embed static/vendor/chart.4.6.1.min.js
var ChartJS []byte

//go:embed static/fonts/orbitron-2.001-700.woff2
var OrbitronFontWoff2 []byte

const (
	ChartJSPath      = "/static/vendor/chart.4.6.1.min.js"
	OrbitronFontPath = "/static/fonts/orbitron-2.001-700.woff2"
)

func contentPath(prefix string, content []byte, suffix string) string {
	sum := sha256.Sum256(content)
	return fmt.Sprintf("%s.%x%s", prefix, sum[:8], suffix)
}
