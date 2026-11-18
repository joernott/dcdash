// SPDX-License-Identifier: GPL-3.0-or-later

// Package docs embeds the Markdown documentation, so that dcdash can show it
// in the web interface (see internal/help).
package docs

import "embed"

// FS contains the Markdown files of this directory.
//
//go:embed *.md
var FS embed.FS
