// Package web owns the embedded dashboard assets. The express server imports
// this package and serves the files at /dashboard. Keeping the embed here
// (at repo root, next to the web/ directory) avoids the "go:embed cannot
// use .." limitation that would apply from internal/express.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dashboard
var DashboardFS embed.FS

// Dashboard returns the embedded dashboard subtree as an fs.FS.
func Dashboard() (fs.FS, error) {
	return fs.Sub(DashboardFS, "dashboard")
}
