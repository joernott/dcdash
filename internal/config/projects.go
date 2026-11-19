// SPDX-License-Identifier: GPL-3.0-or-later

package config

import "time"

// Projects configures the project files (see docs/adr/0007-projects.md).
type Projects struct {
	// Dir holds one YAML file per project. A missing directory means no
	// projects.
	Dir string `yaml:"dir"`
	// ReloadInterval is how often the directory is read again (0 disables
	// reloading).
	ReloadInterval Duration `yaml:"reload_interval"`
}

func defaultProjects() Projects {
	return Projects{Dir: "/etc/dcdash/projects", ReloadInterval: Duration(time.Minute)}
}

func (p Projects) validate(add func(string, ...any)) {
	if p.ReloadInterval < 0 {
		add("projects.reload_interval: must not be negative")
	}
}
