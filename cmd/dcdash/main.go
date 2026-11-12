// SPDX-License-Identifier: GPL-3.0-or-later

// Command dcdash is a dashboard for managing central data center services.
package main

import (
	"os"

	"github.com/joernott/dcdash/internal/cli"
)

func main() {
	os.Exit(cli.Execute(cli.Deps{Stdout: os.Stdout, Stderr: os.Stderr}))
}
