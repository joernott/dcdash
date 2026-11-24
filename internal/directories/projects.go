// SPDX-License-Identifier: GPL-3.0-or-later

package directories

import (
	"context"
	"errors"

	"github.com/joernott/dcdash/internal/directories/ad"
)

// ErrUnknownDirectory is returned for a project group in a directory that isn't configured.
var ErrUnknownDirectory = errors.New("unknown directory")

// ProjectGroup returns a group of a project with its direct members (project.GroupSource).
// The caller checks the permission.
func (s *Service) ProjectGroup(ctx context.Context, directory, name string) (ad.Group, error) {
	b := s.dirs[directory]
	if b == nil {
		return ad.Group{}, ErrUnknownDirectory
	}
	return b.Group(ctx, name)
}
