// SPDX-License-Identifier: GPL-3.0-or-later

package ad

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/joernott/dcdash/internal/config"
)

func TestParseSID(t *testing.T) {
	// S-1-5-21-1004336348-1177238915-682003330-512
	b := []byte{1, 5, 0, 0, 0, 0, 0, 5, 21, 0, 0, 0, 0xdc, 0xf4, 0xdc, 0x3b, 0x83, 0x3d, 0x2b, 0x46, 0x82, 0x8b, 0xa6, 0x28, 0x00, 0x02, 0, 0}
	s, err := parseSID(b)
	if err != nil || s != "S-1-5-21-1004336348-1177238915-682003330-512" {
		t.Fatalf("parseSID = %q, %v", s, err)
	}
	if rid(s) != 512 || rid("x") != 0 {
		t.Errorf("rid(%s) = %d", s, rid(s))
	}
	builtin := []byte{1, 2, 0, 0, 0, 0, 0, 5, 32, 0, 0, 0, 0x20, 0x02, 0, 0}
	if s, _ := parseSID(builtin); s != "S-1-5-32-544" {
		t.Errorf("builtin = %q", s)
	}
	for _, bad := range [][]byte{nil, {2, 0, 0, 0, 0, 0, 0, 5}, {1, 2, 0, 0, 0, 0, 0, 5, 1, 0, 0, 0}} {
		if _, err := parseSID(bad); err == nil {
			t.Errorf("parseSID(%v) accepted", bad)
		}
	}
	p := privilegedSIDs("S-1-5-21-1-2-3")
	if p[0] != "S-1-5-32-544" || p[len(p)-1] != "S-1-5-21-1-2-3-527" {
		t.Errorf("privilegedSIDs = %v", p)
	}
}

func TestGroupType(t *testing.T) {
	for _, c := range []struct {
		scope    string
		security bool
		want     string
	}{
		{ScopeGlobal, true, "-2147483646"},
		{ScopeUniversal, true, "-2147483640"},
		{ScopeDomainLocal, true, "-2147483644"},
		{ScopeGlobal, false, "2"},
		{"", false, "2"},
	} {
		if got, err := groupType(c.scope, c.security); err != nil || got != c.want {
			t.Errorf("groupType(%s, %v) = %s, %v", c.scope, c.security, got, err)
		}
	}
	if _, err := groupType("forest", true); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown scope: %v", err)
	}
}

func TestGroupFrom(t *testing.T) {
	c := &Client{cfg: config.Directory{ManagedOUs: []string{"OU=Lab,DC=ad,DC=test"}, ProtectedGroups: []string{"Backup"}}}
	sid := func(r byte) []byte {
		return []byte{1, 5, 0, 0, 0, 0, 0, 5, 21, 0, 0, 0, 1, 0, 0, 0, 2, 0, 0, 0, 3, 0, 0, 0, r, 4, 0, 0}
	}
	entry := func(dn, name, gt string, s []byte) *ldap.Entry {
		e := ldap.NewEntry(dn, map[string][]string{"sAMAccountName": {name}, "groupType": {gt}})
		e.Attributes = append(e.Attributes, &ldap.EntryAttribute{Name: "objectSid", ByteValues: [][]byte{s}})
		return e
	}
	g := c.groupFrom(entry("CN=lab,OU=Lab,DC=ad,DC=test", "lab", "-2147483640", sid(0x50)), nil)
	if g.Scope != ScopeUniversal || !g.Security || !g.Managed || g.Protected {
		t.Errorf("lab: %+v", g)
	}
	g = c.groupFrom(entry("CN=backup,CN=Users,DC=ad,DC=test", "backup", "2", sid(0x51)), nil)
	if g.Scope != ScopeGlobal || g.Security || g.Managed || !g.Protected {
		t.Errorf("configured protected group: %+v", g)
	}
	g = c.groupFrom(entry("CN=x,OU=Lab,DC=ad,DC=test", "x", "-2147483646", sid(0x52)), []string{"S-1-5-21-1-2-3-1106"})
	if !g.Protected {
		t.Errorf("privileged group not protected")
	}
}

func TestTimes(t *testing.T) {
	if fileTime("0") != nil || fileTime("9223372036854775807") != nil || fileTime("x") != nil {
		t.Error("never values")
	}
	if ft := fileTime("133000000000000000"); ft == nil || ft.Year() != 2022 {
		t.Errorf("fileTime = %v", ft)
	}
	if gt := generalizedTime("20261123143000.0Z"); gt == nil || !gt.Equal(time.Date(2026, 11, 23, 14, 30, 0, 0, time.UTC)) {
		t.Errorf("generalizedTime = %v", gt)
	}
	if interval("-18000000000") != 30*time.Minute || interval("-9223372036854775808") != 0 || interval("0") != 0 {
		t.Error("interval")
	}
}

func TestWrap(t *testing.T) {
	for _, c := range []struct {
		err  error
		want error
	}{
		{ldap.NewError(ldap.LDAPResultNoSuchObject, errors.New("x")), ErrNotFound},
		{ldap.NewError(ldap.LDAPResultEntryAlreadyExists, errors.New("x")), ErrExists},
		{ldap.NewError(ldap.LDAPResultInsufficientAccessRights, errors.New("x")), ErrForbidden},
		{ldap.NewError(ldap.LDAPResultConstraintViolation, errors.New("00000056: Constraint violation - check_password_restrictions: the old password specified doesn't match")), ErrWrongPassword},
		{ldap.NewError(ldap.LDAPResultConstraintViolation, errors.New("0000052D: Constraint violation - the password is too short")), ErrPolicy},
		{ldap.NewError(ldap.LDAPResultUnwillingToPerform, errors.New("0000052D: SvcErr: DSID-031A12D2, problem 5003 (WILL_NOT_PERFORM), data 0")), ErrPolicy},
		{ldap.NewError(ldap.ErrorNetwork, errors.New("x")), ErrUnavailable},
		{ldap.NewError(ldap.LDAPResultOther, errors.New("x")), ErrInvalid},
		{errors.New("dial tcp: refused"), ErrUnavailable},
	} {
		got := wrap("op", c.err)
		if !errors.Is(got, c.want) {
			t.Errorf("wrap(%v) = %v, want %v", c.err, got, c.want)
		}
		if again := wrap("outer", got); again != got { //nolint:errorlint // the same error, not an equal one
			t.Errorf("wrap is not idempotent: %v", again)
		}
	}
	var e *Error
	err := wrap("change password", ldap.NewError(ldap.LDAPResultConstraintViolation, errors.New("0000052D: the password is too short")))
	if !errors.As(err, &e) || e.Message != "0000052D: the password is too short" {
		t.Errorf("message: %v", err)
	}
	if wrap("op", nil) != nil {
		t.Error("nil")
	}
}

func TestEncodePassword(t *testing.T) {
	// "aB1" in double quotes, UTF-16LE.
	if got := fmt.Sprintf("%x", encodePassword("aB1")); got != "22006100420031002200" {
		t.Errorf("encodePassword = %s", got)
	}
}

func TestDNHelpers(t *testing.T) {
	if p := parentDN(`CN=Doe\, John,OU=Lab,DC=ad`); p != "OU=Lab,DC=ad" {
		t.Errorf("parentDN = %q", p)
	}
	if parentDN("DC=ad") != "" {
		t.Error("parentDN of root")
	}
	if reverseDN("OU=a,DC=ad,DC=test") != "dc=test,dc=ad,ou=a" {
		t.Error("reverseDN")
	}
}
