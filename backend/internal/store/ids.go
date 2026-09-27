// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"
)

// idRandomBytes is how much randomness an id carries: 32 hex characters.
const idRandomBytes = 16

// messageIDPrefix begins every message id, and so the name of every raw
// message file (MessageRawPath).
const messageIDPrefix = "m_"

// newID returns prefix followed by 32 random hex characters.
func newID(prefix string) string {
	var b [idRandomBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return prefix + hex.EncodeToString(b[:])
}

// isMessageID reports whether s has the shape of the message ids newID
// makes: messageIDPrefix and 32 lowercase hex characters.
func isMessageID(s string) bool {
	digits, ok := strings.CutPrefix(s, messageIDPrefix)
	if !ok || len(digits) != 2*idRandomBytes {
		return false
	}
	for _, c := range []byte(digits) {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}

// nowStamp formats the current time the way the migrations' strftime
// defaults do, so ordering by the column stays consistent.
func nowStamp() string {
	return time.Now().UTC().Format(timeLayout)
}

func parseStamp(s string) time.Time {
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
