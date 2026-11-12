// SPDX-License-Identifier: GPL-3.0-or-later

// Package version holds build information injected via -ldflags.
package version

import "runtime"

// These variables are set at build time, e.g.
//
//	-ldflags "-X github.com/joernott/dcdash/internal/version.Version=v0.1.0"
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// Info describes the running build.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"goVersion"`
}

// Get returns the build information of the running binary.
func Get() Info {
	return Info{Version: Version, Commit: Commit, Date: Date, GoVersion: runtime.Version()}
}
