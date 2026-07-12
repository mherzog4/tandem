// Package web embeds the guest client assets so the relay ships as a
// single binary (NFR1).
package web

import "embed"

//go:embed index.html app.js player.html player.js vendor
var Assets embed.FS
