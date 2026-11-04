// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package update

import (
	"strconv"
	"strings"
)

// Version is a plain MAJOR.MINOR.PATCH.
type Version [3]uint32

// ParseVersion accepts three decimal numbers without leading zeros
// separated by dots, nothing else: no "v", no suffix such as the
// "-3-gabc1234" of a development build.
func ParseVersion(s string) (Version, bool) {
	var v Version
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		if p == "" || len(p) > 9 || (len(p) > 1 && p[0] == '0') {
			return v, false
		}
		for _, c := range []byte(p) {
			if c < '0' || c > '9' {
				return v, false
			}
		}
		n, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			return v, false
		}
		v[i] = uint32(n)
	}
	return v, true
}

// Compare is -1, 0 or 1 as v is lower than, equal to or higher than w.
func (v Version) Compare(w Version) int {
	for i := range v {
		switch {
		case v[i] < w[i]:
			return -1
		case v[i] > w[i]:
			return 1
		}
	}
	return 0
}

func (v Version) String() string {
	return strconv.FormatUint(uint64(v[0]), 10) + "." + strconv.FormatUint(uint64(v[1]), 10) + "." +
		strconv.FormatUint(uint64(v[2]), 10)
}
