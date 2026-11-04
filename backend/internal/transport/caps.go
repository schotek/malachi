// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package transport

import "sort"

// CleanCapabilities scrubs a server's capability list before it is shown
// to anyone: printable ASCII only, no surrounding spaces, no duplicates,
// each at most MaxCapabilityLen bytes, at most MaxCapabilities entries,
// sorted so the output is deterministic.
func CleanCapabilities(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !printableASCII(s) {
			continue
		}
		if len(s) > MaxCapabilityLen {
			s = s[:MaxCapabilityLen]
		}
		if s == "" || s[0] == ' ' || s[len(s)-1] == ' ' {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	if len(out) > MaxCapabilities {
		out = out[:MaxCapabilities]
	}
	return out
}

func printableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}
