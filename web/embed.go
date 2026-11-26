package web

import "embed"

//go:embed index.html app.js style.css questions.json
var Files embed.FS
