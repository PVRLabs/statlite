package dashboard

// This file embeds the dashboard HTML and static image assets.

import _ "embed"

//go:embed static/index.html
var Page string

//go:embed static/statlite-icon.png
var StatliteIconPNG []byte
