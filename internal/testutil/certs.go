// SPDX-License-Identifier: GPL-3.0-or-later

// Package testutil contains helpers shared by tests.
package testutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// CertOptions configures WriteCert. Zero values default to a certificate for
// "localhost" and 127.0.0.1, valid from one hour ago for one hour.
type CertOptions struct {
	DNSNames  []string
	NotBefore time.Time
	NotAfter  time.Time
	// Name is the file name prefix (default "cert"): <Name>.pem and <Name>-key.pem.
	Name string
}

// WriteCert writes a self-signed ECDSA certificate and key as PEM files into
// dir and returns their paths.
func WriteCert(t testing.TB, dir string, o CertOptions) (certFile, keyFile string) {
	t.Helper()
	if len(o.DNSNames) == 0 {
		o.DNSNames = []string{"localhost"}
	}
	if o.NotBefore.IsZero() {
		o.NotBefore = time.Now().Add(-time.Hour)
	}
	if o.NotAfter.IsZero() {
		o.NotAfter = time.Now().Add(time.Hour)
	}
	if o.Name == "" {
		o.Name = "cert"
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: o.DNSNames[0]},
		DNSNames:     o.DNSNames,
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    o.NotBefore,
		NotAfter:     o.NotAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile = filepath.Join(dir, o.Name+".pem")
	keyFile = filepath.Join(dir, o.Name+"-key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}
