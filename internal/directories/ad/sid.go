// SPDX-License-Identifier: GPL-3.0-or-later

package ad

import (
	"encoding/binary"
	"errors"
	"strconv"
	"strings"
)

// parseSID converts a binary security identifier to its string form S-1-5-21-....
func parseSID(b []byte) (string, error) {
	if len(b) < 8 || b[0] != 1 || len(b) != 8+4*int(b[1]) {
		return "", errors.New("invalid SID")
	}
	var auth uint64
	for _, x := range b[2:8] {
		auth = auth<<8 | uint64(x)
	}
	var s strings.Builder
	s.WriteString("S-1-")
	s.WriteString(strconv.FormatUint(auth, 10))
	for i := range int(b[1]) {
		s.WriteByte('-')
		s.WriteString(strconv.FormatUint(uint64(binary.LittleEndian.Uint32(b[8+4*i:])), 10))
	}
	return s.String(), nil
}

// rid returns the relative ID (last part) of a SID, or 0.
func rid(sid string) uint32 {
	i := strings.LastIndexByte(sid, '-')
	if i < 0 {
		return 0
	}
	v, err := strconv.ParseUint(sid[i+1:], 10, 32)
	if err != nil {
		return 0
	}
	return uint32(v)
}

// Well-known groups whose members administer the domain or its controllers. Their members
// and the groups themselves are protected.
var (
	privilegedDomainRIDs = []uint32{512, 518, 519, 520, 526, 527} // Domain, Schema, Enterprise Admins, GPO Creators, Key Admins
	privilegedBuiltin    = []string{"S-1-5-32-544", "S-1-5-32-548", "S-1-5-32-549", "S-1-5-32-550", "S-1-5-32-551"}
)

// privilegedSIDs returns the SIDs of the privileged groups of a domain.
func privilegedSIDs(domainSID string) []string {
	out := append([]string{}, privilegedBuiltin...)
	for _, r := range privilegedDomainRIDs {
		out = append(out, domainSID+"-"+strconv.FormatUint(uint64(r), 10))
	}
	return out
}
