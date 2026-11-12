// SPDX-License-Identifier: GPL-3.0-or-later

package certs

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/go-acme/lego/v5/acme"
	"github.com/go-acme/lego/v5/certcrypto"
	"github.com/go-acme/lego/v5/certificate"
	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/lego"
	legolog "github.com/go-acme/lego/v5/log"
	"github.com/go-acme/lego/v5/registration"
	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/logging"
	"github.com/joernott/dcdash/internal/version"
)

// Files in the ACME storage directory <data_dir>/acme/<ca>/.
const (
	accountKeyFile  = "account.key"
	accountFile     = "account.json"
	certFile        = "certificate.pem"
	certKeyFile     = "certificate.key"
	certMetaFile    = "certificate.json"
	storageDirPerm  = 0o700
	storageFilePerm = 0o600
)

// ACMEManager obtains and renews the server certificate from an ACME CA using
// the DNS-01 challenge. Account and certificate are stored per CA below the
// data directory, so they survive restarts and switching CAs causes a new
// account and certificate.
type ACMEManager struct {
	holder
	cfg config.ACME
	dir string
	log zerolog.Logger
	now func() time.Time

	// newProvider creates the DNS-01 provider; replaceable in tests.
	newProvider func(config.ACMEDNS) (challenge.Provider, error)
	// obtainFn and sleep drive the renewal loop; replaceable in tests.
	obtainFn func(context.Context) error
	sleep    func(context.Context, time.Duration) bool

	mu     sync.Mutex
	client *lego.Client
}

// certMeta is stored next to the certificate to detect configuration changes.
type certMeta struct {
	Domains []string  `json:"domains"`
	KeyType string    `json:"keyType"`
	Issued  time.Time `json:"issued"`
}

// NewACMEManager prepares the storage directory and loads a previously
// obtained certificate if it matches the configuration. It does not contact
// the CA; that happens in Run.
func NewACMEManager(cfg config.ACME, dataDir string, log zerolog.Logger) (*ACMEManager, error) {
	log = log.With().Str("component", "acme").Logger()
	legolog.SetDefault(slog.New(logging.NewSlogHandler(log)))

	dir, err := storageDir(dataDir, cfg.DirectoryURL)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, storageDirPerm); err != nil {
		return nil, fmt.Errorf("create ACME storage: %w", err)
	}
	configureResolvers(cfg.DNS)

	m := &ACMEManager{cfg: cfg, dir: dir, log: log, now: time.Now, newProvider: newDNSProvider, sleep: sleepCtx}
	m.obtainFn = m.obtain
	if err := m.loadStored(); err != nil {
		m.log.Warn().Err(err).Msg("ignoring stored certificate")
	}
	return m, nil
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9.-]+`)

// storageDir returns <dataDir>/acme/<host-and-path of the directory URL>.
func storageDir(dataDir, directoryURL string) (string, error) {
	u, err := url.Parse(directoryURL)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid ACME directory URL %q", directoryURL)
	}
	name := unsafeChars.ReplaceAllString(u.Host+u.Path, "_")
	return filepath.Join(dataDir, "acme", name), nil
}

// Check implements Manager.
func (m *ACMEManager) Check(context.Context) (string, error) { return m.status(m.now()) }

// --- stored certificate ------------------------------------------------------

func sameDomains(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// loadStored loads the stored certificate if it was issued for the configured
// domains and key type. A missing certificate is not an error.
func (m *ACMEManager) loadStored() error {
	metaData, err := os.ReadFile(filepath.Join(m.dir, certMetaFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var meta certMeta
	if err := json.Unmarshal(metaData, &meta); err != nil {
		return fmt.Errorf("parse %s: %w", certMetaFile, err)
	}
	if !sameDomains(meta.Domains, m.cfg.Domains) || meta.KeyType != m.cfg.KeyType {
		m.log.Info().Strs("stored", meta.Domains).Strs("configured", m.cfg.Domains).
			Msg("configuration changed, a new certificate will be requested")
		return nil
	}
	c, err := tls.LoadX509KeyPair(filepath.Join(m.dir, certFile), filepath.Join(m.dir, certKeyFile))
	if err != nil {
		return err
	}
	if err := m.set(&c); err != nil {
		return err
	}
	m.log.Info().Strs("domains", meta.Domains).Time("not_after", c.Leaf.NotAfter).Msg("loaded stored certificate")
	return nil
}

func (m *ACMEManager) store(res *certificate.Resource) (*tls.Certificate, error) {
	c, err := tls.X509KeyPair(res.Certificate, res.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("issued certificate is invalid: %w", err)
	}
	meta, err := json.MarshalIndent(certMeta{Domains: m.cfg.Domains, KeyType: m.cfg.KeyType, Issued: m.now().UTC()}, "", "  ")
	if err != nil {
		return nil, err
	}
	// Key first, metadata last: an interrupted write never pairs metadata with a mismatching key.
	for _, f := range []struct {
		name string
		data []byte
	}{{certKeyFile, res.PrivateKey}, {certFile, res.Certificate}, {certMetaFile, meta}} {
		if err := writeFileAtomic(filepath.Join(m.dir, f.name), f.data); err != nil {
			return nil, err
		}
	}
	return &c, nil
}

// writeFileAtomic writes data to a temporary file and renames it into place.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after successful rename
	if err := tmp.Chmod(storageFilePerm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// --- account -----------------------------------------------------------------

type acmeUser struct {
	email string
	key   crypto.Signer
	reg   *acme.ExtendedAccount
}

func (u *acmeUser) GetEmail() string                        { return u.email }
func (u *acmeUser) GetRegistration() *acme.ExtendedAccount  { return u.reg }
func (u *acmeUser) GetPrivateKey() crypto.Signer            { return u.key }
func (u *acmeUser) setRegistration(r *acme.ExtendedAccount) { u.reg = r }

var _ registration.User = (*acmeUser)(nil)

// loadAccount loads the account key (creating it on first use) and the
// registration, if the account was registered before.
func (m *ACMEManager) loadAccount() (*acmeUser, error) {
	u := &acmeUser{email: m.cfg.Email}

	keyPath := filepath.Join(m.dir, accountKeyFile)
	keyPEM, err := os.ReadFile(keyPath) // #nosec G304 -- path below the data directory
	switch {
	case errors.Is(err, fs.ErrNotExist):
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		der, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			return nil, err
		}
		if err := writeFileAtomic(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})); err != nil {
			return nil, fmt.Errorf("store account key: %w", err)
		}
		u.key = key
	case err != nil:
		return nil, fmt.Errorf("read account key: %w", err)
	default:
		key, err := certcrypto.ParsePEMPrivateKey(keyPEM)
		if err != nil {
			return nil, fmt.Errorf("parse account key: %w", err)
		}
		u.key = key
	}

	regData, err := os.ReadFile(filepath.Join(m.dir, accountFile))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("read account: %w", err)
	default:
		var reg acme.ExtendedAccount
		if err := json.Unmarshal(regData, &reg); err != nil {
			return nil, fmt.Errorf("parse account: %w", err)
		}
		u.reg = &reg
	}
	return u, nil
}

// register creates the ACME account (with external account binding if configured).
func (m *ACMEManager) register(ctx context.Context, client *lego.Client, u *acmeUser) error {
	var (
		reg *acme.ExtendedAccount
		err error
	)
	if m.cfg.EAB.KeyID != "" {
		reg, err = client.Registration.RegisterWithExternalAccountBinding(ctx, registration.RegisterEABOptions{
			TermsOfServiceAgreed: m.cfg.AcceptTOS,
			Kid:                  m.cfg.EAB.KeyID,
			HmacEncoded:          m.cfg.EAB.HMACKey.Value(),
		})
	} else {
		reg, err = client.Registration.Register(ctx, registration.RegisterOptions{TermsOfServiceAgreed: m.cfg.AcceptTOS})
	}
	if err != nil {
		return fmt.Errorf("register ACME account: %w", err)
	}
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(m.dir, accountFile), data); err != nil {
		return fmt.Errorf("store account: %w", err)
	}
	u.setRegistration(reg)
	m.log.Info().Str("account", reg.Location).Msg("registered ACME account")
	return nil
}

// --- client and issuance -----------------------------------------------------

// httpClient returns a client trusting the system roots plus ca_file, or nil
// to use lego's default client when no ca_file is configured.
func (m *ACMEManager) httpClient() (*http.Client, error) {
	if m.cfg.CAFile == "" {
		return nil, nil
	}
	pool, err := lego.CreateCertPool([]string{m.cfg.CAFile}, true)
	if err != nil {
		return nil, fmt.Errorf("load ca_file: %w", err)
	}
	return &http.Client{
		Timeout: 2 * time.Minute,
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool},
			TLSHandshakeTimeout: 15 * time.Second,
		},
	}, nil
}

// legoClient returns the (cached) ACME client, registering the account on first use.
func (m *ACMEManager) legoClient(ctx context.Context) (*lego.Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.client != nil {
		return m.client, nil
	}

	u, err := m.loadAccount()
	if err != nil {
		return nil, err
	}
	lc := lego.NewConfig(u)
	lc.CADirURL = m.cfg.DirectoryURL
	lc.UserAgent = "dcdash/" + version.Version
	hc, err := m.httpClient()
	if err != nil {
		return nil, err
	}
	if hc != nil {
		lc.HTTPClient = hc
	}
	client, err := lego.NewClient(lc)
	if err != nil {
		return nil, fmt.Errorf("create ACME client for %s: %w", m.cfg.DirectoryURL, err)
	}
	provider, err := m.newProvider(m.cfg.DNS)
	if err != nil {
		return nil, err
	}
	if err := client.Challenge.SetDNS01Provider(provider, challengeOptions(m.cfg.DNS)...); err != nil {
		return nil, fmt.Errorf("set DNS-01 provider: %w", err)
	}
	if u.reg == nil {
		if err := m.register(ctx, client, u); err != nil {
			return nil, err
		}
	}
	m.client = client
	return client, nil
}

// obtain requests a new certificate for the configured domains, stores it and
// makes it the current certificate.
func (m *ACMEManager) obtain(ctx context.Context) error {
	client, err := m.legoClient(ctx)
	if err != nil {
		return err
	}
	m.log.Info().Strs("domains", m.cfg.Domains).Str("ca", m.cfg.DirectoryURL).Msg("requesting certificate")
	res, err := client.Certificate.Obtain(ctx, certificate.ObtainRequest{
		Domains: m.cfg.Domains,
		Bundle:  true,
		KeyType: certcrypto.KeyType(m.cfg.KeyType),
	})
	if err != nil {
		return fmt.Errorf("obtain certificate: %w", err)
	}
	c, err := m.store(res)
	if err != nil {
		return err
	}
	if err := m.set(c); err != nil {
		return err
	}
	m.log.Info().Strs("domains", c.Leaf.DNSNames).Time("not_after", c.Leaf.NotAfter).Msg("certificate obtained")
	return nil
}
