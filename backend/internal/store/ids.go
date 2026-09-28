// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// newID returns prefix followed by 32 random hex characters.
func newID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return prefix + hex.EncodeToString(b[:])
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
