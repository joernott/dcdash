// SPDX-License-Identifier: GPL-3.0-or-later

// Package totp implements time-based one-time passwords (RFC 6238, HMAC-SHA1,
// 6 digits, 30 seconds), as supported by all common authenticator apps, plus
// the storage of enrolled secrets and recovery codes.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- RFC 6238 default; HMAC-SHA1 is not affected by SHA-1 collisions
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Parameters understood by every authenticator app.
const (
	Digits     = 6
	Period     = 30 * time.Second
	SecretSize = 20 // bytes (160 bits, as recommended by RFC 4226)
	// Skew is the number of time steps accepted before and after the current one.
	Skew = 1
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret returns a random secret.
func NewSecret() []byte {
	b := make([]byte, SecretSize)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error
	return b
}

// EncodeSecret returns the secret in base32, as typed into authenticator apps.
func EncodeSecret(secret []byte) string { return b32.EncodeToString(secret) }

// Step returns the time step for t.
func Step(t time.Time) int64 { return t.Unix() / int64(Period/time.Second) }

// Code returns the code for a time step.
func Code(secret []byte, step int64) string {
	mac := hmac.New(sha1.New, secret)
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step)) // #nosec G115 -- steps are positive
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", Digits, v%1_000_000)
}

// Match returns the time step within the allowed skew around now whose code
// equals code, or 0 if none matches. Only steps after notBefore are
// considered, so that a code cannot be used twice (replay protection).
func Match(secret []byte, code string, now time.Time, notBefore int64) int64 {
	code = strings.TrimSpace(code)
	if len(code) != Digits {
		return 0
	}
	cur := Step(now)
	var found int64
	for s := cur - Skew; s <= cur+Skew; s++ {
		// Compare all candidates to keep the timing independent of the match.
		if subtle.ConstantTimeCompare([]byte(Code(secret, s)), []byte(code)) == 1 && s > notBefore && found == 0 {
			found = s
		}
	}
	return found
}

// URI returns the otpauth:// URI for authenticator apps (usually shown as QR code).
func URI(issuer, account string, secret []byte) string {
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	q := url.Values{}
	q.Set("secret", EncodeSecret(secret))
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(Digits))
	q.Set("period", fmt.Sprint(int(Period/time.Second)))
	return "otpauth://totp/" + label + "?" + q.Encode()
}
