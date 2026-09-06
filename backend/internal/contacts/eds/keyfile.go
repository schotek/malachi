// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package eds

import "strings"

// keyFile is a parsed GKeyFile (the format of an ESource's Data property):
// group → key → value, with GKeyFile's escapes decoded. A key with a locale
// suffix (Key[cs]) is kept under that spelling and simply never asked for.
type keyFile map[string]map[string]string

// parseKeyFile reads data leniently: lines before the first group, lines
// without '=', and malformed group headers are skipped rather than
// rejected, so one odd line in a source file cannot hide a whole address
// book.
func parseKeyFile(data string) keyFile {
	kf := keyFile{}
	group := ""
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimRight(line, "\r")
		t := strings.TrimSpace(line)
		switch {
		case t == "" || strings.HasPrefix(t, "#"):
			continue
		case strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]"):
			group = t[1 : len(t)-1]
			if kf[group] == nil {
				kf[group] = map[string]string{}
			}
		default:
			if group == "" {
				continue
			}
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			kf[group][strings.TrimSpace(k)] = unescapeKeyFile(strings.TrimSpace(v))
		}
	}
	return kf
}

// get returns a key's value or "" when the group or key is absent.
func (kf keyFile) get(group, key string) string {
	return kf[group][key]
}

// has reports whether the group is present, even when empty.
func (kf keyFile) has(group string) bool {
	_, ok := kf[group]
	return ok
}

// isFalse reads a GKeyFile boolean: only an explicit "false" is false, so a
// missing key keeps the format's default of true where that is the
// default (Enabled, IncludeMe).
func (kf keyFile) isFalse(group, key string) bool {
	return strings.EqualFold(kf.get(group, key), "false")
}

// unescapeKeyFile decodes \s \n \t \r and \\; an unknown escape is kept
// as written.
func unescapeKeyFile(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 's':
			b.WriteByte(' ')
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '\\':
			b.WriteByte('\\')
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
