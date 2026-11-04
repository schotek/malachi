// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package update

import (
	"crypto/ed25519"
	"encoding/base64"
)

// Keys are the public keys a manifest may be signed with, by keyId
// (docs/services.md §2.2). They are compiled in and never read from the
// configuration. A new key is added a release before the owner signs with
// it; an old one goes only when no supported version needs it. The test
// key of backend/testdata/update ("test-1") is never here.
var Keys = map[string]ed25519.PublicKey{
	"2026-1": mustKey("RU+SjbfcMnJgjn1SdgqahtuHYGqlemrpKi5siiGDSTM="),
}

func mustKey(b64 string) ed25519.PublicKey {
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(b) != ed25519.PublicKeySize {
		panic("update: bad compiled-in public key")
	}
	return ed25519.PublicKey(b)
}
