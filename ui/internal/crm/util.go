// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package crm

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
)

// apiError is the daemon's error in err; nil for any other.
func apiError(err error) *api.Error {
	var e *api.Error
	if errors.As(err, &e) {
		return e
	}
	return nil
}

// decodeData decodes an error's data (a value of the daemon's type, or
// the JSON object the RPC client decoded into a map) into out.
func decodeData(data any, out any) bool {
	if data == nil {
		return false
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return false
	}
	return json.Unmarshal(raw, out) == nil
}

// ellipsis ends a text cut by capBytes.
const ellipsis = "…"

// capBytes caps s at n bytes on a character boundary, with an ellipsis
// when cut.
func capBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimRight(s[:cut], " ") + ellipsis
}
