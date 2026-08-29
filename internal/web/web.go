// Package web embeds the static frontend (HTML/CSS/JS) served by cmd/serve.
package web

import "embed"

//go:embed static
var FS embed.FS
