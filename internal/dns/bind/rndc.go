// SPDX-License-Identifier: GPL-3.0-or-later

package bind

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/joernott/dcdash/internal/config"
)

// RNDC runs the rndc binary for server operations. Go has no maintained rndc
// library; the arguments are passed without a shell.
type RNDC struct {
	binary  string
	args    []string
	timeout time.Duration
	// run executes the command (replaced in tests).
	run func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// NewRNDC creates the client.
func NewRNDC(c config.RNDC, timeout time.Duration) *RNDC {
	return &RNDC{
		binary:  c.Binary,
		args:    []string{"-s", c.Server, "-p", strconv.Itoa(c.Port), "-k", c.KeyFile},
		timeout: timeout,
		run:     runCommand,
	}
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- binary from the config, no shell
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.Bytes(), err
}

// RNDCError is a failed rndc command with its output.
type RNDCError struct {
	Command string
	Output  string
	Err     error
}

func (e *RNDCError) Error() string {
	return fmt.Sprintf("rndc %s: %v: %s", e.Command, e.Err, e.Output)
}

func (e *RNDCError) Unwrap() error { return e.Err }

// cmd runs one rndc command and returns its output.
func (r *RNDC) cmd(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	out, err := r.run(ctx, r.binary, append(append([]string{}, r.args...), args...)...)
	s := strings.TrimSpace(string(out))
	if err != nil {
		return "", &RNDCError{Command: args[0], Output: s, Err: err}
	}
	return s, nil
}

// Status returns the output of "rndc status".
func (r *RNDC) Status(ctx context.Context) (string, error) { return r.cmd(ctx, "status") }

// Reload reloads one zone ("rndc reload <zone>").
func (r *RNDC) Reload(ctx context.Context, zone string) (string, error) {
	return r.cmd(ctx, "reload", zone)
}

// AddZone adds a primary zone with the given file and options.
func (r *RNDC) AddZone(ctx context.Context, zone, file, options string) error {
	conf := fmt.Sprintf(`{ type primary; file %q; %s };`, file, options)
	_, err := r.cmd(ctx, "addzone", zone, conf)
	return err
}

// DelZone removes a zone added with AddZone.
func (r *RNDC) DelZone(ctx context.Context, zone string) error {
	_, err := r.cmd(ctx, "delzone", zone)
	return err
}
