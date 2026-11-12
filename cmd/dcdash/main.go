// SPDX-License-Identifier: GPL-3.0-or-later

// Command dcdash is a dashboard for managing central data center services.
package main

import (
	"fmt"

	"github.com/joernott/dcdash/internal/version"
)

func main() {
	fmt.Println("dcdash", version.Get().Version)
}
