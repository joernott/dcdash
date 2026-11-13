// SPDX-License-Identifier: GPL-3.0-or-later

package bind

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/joernott/dcdash/internal/config"
)

// ZoneFiles stores zone files on the primary.
type ZoneFiles interface {
	// Upload writes the file atomically (temporary name, then rename).
	Upload(ctx context.Context, name string, content []byte) error
	// Remove deletes the files; missing files are not an error.
	Remove(ctx context.Context, names ...string) error
	// Check tests the connection and the zone directory.
	Check(ctx context.Context) error
}

// SFTPFiles is ZoneFiles over SFTP with a dedicated key and a verified host
// key.
type SFTPFiles struct {
	addr    string
	dir     string
	cfg     *ssh.ClientConfig
	timeout time.Duration
}

// NewSFTP creates the SFTP client. The private key and the host key (or
// known_hosts file) are read now, so configuration errors show at startup.
func NewSFTP(c config.SFTP, timeout time.Duration) (*SFTPFiles, error) {
	keyPEM, err := os.ReadFile(c.PrivateKeyFile)
	if err != nil {
		return nil, fmt.Errorf("sftp: private key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("sftp: private key %s: %w", c.PrivateKeyFile, err)
	}
	var hostKey ssh.HostKeyCallback
	if c.HostKey != "" {
		pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(c.HostKey))
		if err != nil {
			return nil, fmt.Errorf("sftp: host_key: %w", err)
		}
		hostKey = ssh.FixedHostKey(pk)
	} else {
		hostKey, err = knownhosts.New(c.KnownHostsFile)
		if err != nil {
			return nil, fmt.Errorf("sftp: known_hosts_file: %w", err)
		}
	}
	return &SFTPFiles{
		addr: withDefaultPort(c.Address, "22"),
		dir:  c.ZoneDir,
		cfg: &ssh.ClientConfig{
			User:            c.User,
			Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
			HostKeyCallback: hostKey,
			Timeout:         timeout,
		},
		timeout: timeout,
	}, nil
}

func withDefaultPort(addr, port string) string {
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	return net.JoinHostPort(strings.Trim(addr, "[]"), port)
}

// session opens an SSH connection with an SFTP client; close closes both.
func (s *SFTPFiles) session(ctx context.Context) (*sftp.Client, func(), error) {
	d := net.Dialer{Timeout: s.timeout}
	conn, err := d.DialContext(ctx, "tcp", s.addr)
	if err != nil {
		return nil, nil, fmt.Errorf("sftp %s: %w", s.addr, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(2 * s.timeout))
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, s.addr, s.cfg)
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("sftp %s: %w", s.addr, err)
	}
	client := ssh.NewClient(c, chans, reqs)
	sc, err := sftp.NewClient(client)
	if err != nil {
		_ = client.Close()
		return nil, nil, fmt.Errorf("sftp %s: %w", s.addr, err)
	}
	return sc, func() { _ = sc.Close(); _ = client.Close() }, nil
}

// file returns the path of a zone file; names never contain a slash.
func (s *SFTPFiles) file(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, "/\\") || name == "." || name == ".." {
		return "", fmt.Errorf("sftp: invalid file name %q", name)
	}
	return path.Join(s.dir, name), nil
}

// Upload implements ZoneFiles.
func (s *SFTPFiles) Upload(ctx context.Context, name string, content []byte) error {
	target, err := s.file(name)
	if err != nil {
		return err
	}
	c, done, err := s.session(ctx)
	if err != nil {
		return err
	}
	defer done()
	rnd := make([]byte, 6)
	_, _ = rand.Read(rnd)
	tmp := path.Join(s.dir, "."+name+".tmp-"+hex.EncodeToString(rnd))
	f, err := c.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return fmt.Errorf("sftp: create %s: %w", tmp, err)
	}
	_, werr := f.Write(content)
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		// named needs read access; the directory is shared with its group.
		werr = c.Chmod(tmp, 0o640)
	}
	if werr != nil {
		_ = c.Remove(tmp)
		return fmt.Errorf("sftp: write %s: %w", tmp, werr)
	}
	if err := c.PosixRename(tmp, target); err != nil {
		_ = c.Remove(tmp)
		return fmt.Errorf("sftp: rename to %s: %w", target, err)
	}
	return nil
}

// Remove implements ZoneFiles.
func (s *SFTPFiles) Remove(ctx context.Context, names ...string) error {
	c, done, err := s.session(ctx)
	if err != nil {
		return err
	}
	defer done()
	var errs []error
	for _, n := range names {
		p, err := s.file(n)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := c.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("sftp: remove %s: %w", p, err))
		}
	}
	return errors.Join(errs...)
}

// Check implements ZoneFiles.
func (s *SFTPFiles) Check(ctx context.Context) error {
	c, done, err := s.session(ctx)
	if err != nil {
		return err
	}
	defer done()
	fi, err := c.Stat(s.dir)
	if err != nil {
		return fmt.Errorf("sftp: zone directory %s: %w", s.dir, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("sftp: %s is not a directory", s.dir)
	}
	return nil
}
