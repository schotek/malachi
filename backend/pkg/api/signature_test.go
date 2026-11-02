// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// signatureMethods end AllMethods, after the update check's;
// notify.signaturesChanged ends AllNotifications.
var signatureMethods = []string{
	"message.signature", "message.signatureRevocation",
	"pgp.keyImport", "pgp.keyVerify", "pgp.keyList", "pgp.keyRemove",
}

func TestSignatureMethodsOrder(t *testing.T) {
	n := len(AllMethods)
	if !reflect.DeepEqual(AllMethods[n-len(signatureMethods):], signatureMethods) {
		t.Fatalf("AllMethods does not end with the signature methods: %v", AllMethods[n-len(signatureMethods):])
	}
	if AllMethods[n-len(signatureMethods)-1] != MethodUpdateDismissNotice {
		t.Fatal("the signature methods do not follow the update check's")
	}
	if AllNotifications[len(AllNotifications)-1] != NotifySignaturesChanged {
		t.Fatalf("AllNotifications: %v", AllNotifications)
	}
}

// 1510 is the next free code of the content group (docs/plans/signatures.md
// §4).
func TestSignatureErrorCode(t *testing.T) {
	if CodeRevocationFailed != 1510 || CodeRevocationFailed.String() != "revocationFailed" {
		t.Fatalf("%d = %s", int(CodeRevocationFailed), CodeRevocationFailed)
	}
	raw, _ := json.Marshal(RevocationFailedData{Reason: RevocationFailTimeout})
	if string(raw) != `{"reason":"timeout"}` {
		t.Errorf("revocation data %s", raw)
	}
	raw, _ = json.Marshal(PGPImportErrorData{Reason: PGPImportSecretKey})
	if string(raw) != `{"reason":"secretKey"}` {
		t.Errorf("import data %s", raw)
	}
}

// The enums' wire values are those of docs/api.md §4.21; the mirrors in
// Swift and C# are checked against the same strings.
func TestSignatureEnums(t *testing.T) {
	got := []string{
		string(SignatureSMIME), string(SignaturePGP),
		string(SignatureInvalid), string(SignatureRevoked), string(SignatureMismatch),
		string(SignatureUntrusted), string(SignatureUnknownKey), string(SignatureWeak),
		string(SignatureUnsupported), string(SignatureError), string(SignatureValid), string(SignatureValidSeen),
		string(SignatureNoteExpiredSince), string(SignatureNoteRevocationUnchecked), string(SignatureNoteRevocationGood),
		string(SignatureNoteRevokedAfter), string(SignatureNotePartialFooter), string(SignatureNoteProtectedHeadersDiffer),
		string(SignatureNoteInlinePGP), string(SignatureNoteKeyChanged), string(SignatureNoteOpaque),
		string(SignatureReasonDigestMismatch), string(SignatureReasonBadSignature), string(SignatureReasonMalformed),
		string(SignatureReasonMD5), string(SignatureReasonKeyUsage), string(SignatureReasonUnknownIssuer),
		string(SignatureReasonSelfSigned), string(SignatureReasonNotYetValid), string(SignatureReasonExpired),
		string(SignatureReasonSHA1), string(SignatureReasonShortKey), string(SignatureReasonAlgorithm),
		string(SignatureReasonMultipleSigners), string(SignatureReasonRebuilt), string(SignatureReasonFormat),
		string(SignatureReasonLimit), string(SignatureReasonInternal),
		string(RevocationUnchecked), string(RevocationGood), string(RevocationRevoked), string(RevocationUnknown),
		string(RevocationOCSP), string(RevocationCRL),
		string(PGPKeyUnknown), string(PGPKeySeen), string(PGPKeyVerified),
		string(PGPSourceAutocrypt), string(PGPSourceAttached), string(PGPSourceImport),
		string(ProtectedSubject), string(ProtectedFrom), string(ProtectedTo), string(ProtectedCc),
		string(ProtectedReplyTo), string(ProtectedDate),
		string(RevocationFailNetwork), string(RevocationFailTimeout), string(RevocationFailServer),
		string(RevocationFailResponse), string(RevocationFailAddress),
		string(PGPImportNoKey), string(PGPImportMalformed), string(PGPImportSecretKey),
		string(PGPImportTooLarge), string(PGPImportTooMany),
	}
	want := []string{
		"smime", "pgp",
		"invalid", "revoked", "mismatch", "untrusted", "unknownKey", "weak", "unsupported", "error", "valid", "validSeen",
		"expiredSince", "revocationUnchecked", "revocationGood", "revokedAfter", "partialFooter",
		"protectedHeadersDiffer", "inlinePGP", "keyChanged", "opaque",
		"digestMismatch", "badSignature", "malformed", "md5", "keyUsage", "unknownIssuer", "selfSigned",
		"notYetValid", "expired", "sha1", "shortKey", "algorithm", "multipleSigners", "rebuilt", "format",
		"limit", "internal",
		"unchecked", "good", "revoked", "unknown", "ocsp", "crl",
		"unknown", "seen", "verified", "autocrypt", "attached", "import",
		"subject", "from", "to", "cc", "replyTo", "date",
		"network", "timeout", "server", "response", "address",
		"noKey", "malformed", "secretKey", "tooLarge", "tooMany",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("enum values:\n got %v\nwant %v", got, want)
	}
}

// The wire form of the signature types: optional fields left out, the
// summary's signature shadowed by message.get's, a message without a
// verdict as "signature": null.
func TestSignatureJSON(t *testing.T) {
	at := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	signer := &SignatureSigner{Name: "Jan Novák", Email: "jan@example.org", Fingerprint: "ab12"}
	var m Message
	m.ID, m.Date = "m_1", at
	m.MessageSummary.Signature = &MessageSignature{Kind: SignatureSMIME, Status: SignatureValid}
	m.Signature = &SignatureSummary{Kind: SignatureSMIME, Status: SignatureValid, Signer: signer,
		Notes: []SignatureNote{SignatureNoteRevocationUnchecked}, PartID: "2"}
	cases := []struct {
		name string
		v    any
		want string
	}{
		{"summary", MessageSummary{ID: "m_1", Date: at, Signature: &MessageSignature{Kind: SignaturePGP, Status: SignatureValidSeen}},
			`{"id":"m_1","accountId":"","folderId":"","from":null,"subject":"","date":"2026-11-02T08:00:00Z","snippet":"","flags":null,"hasAttachments":false,"size":0,"signature":{"kind":"pgp","status":"validSeen"}}`},
		{"message", m,
			`{"id":"m_1","accountId":"","folderId":"","from":null,"subject":"","date":"2026-11-02T08:00:00Z","snippet":"","flags":null,"hasAttachments":false,"size":0,"attachments":null,"signature":{"kind":"smime","status":"valid","signer":{"name":"Jan Novák","email":"jan@example.org","fingerprint":"ab12"},"notes":["revocationUnchecked"],"partId":"2"}}`},
		{"minimal summary", SignatureSummary{Kind: SignaturePGP, Status: SignatureUnknownKey},
			`{"kind":"pgp","status":"unknownKey"}`},
		{"no verdict", MessageSignatureResult{}, `{"signature":null}`},
		{"smime details", MessageSignatureResult{Signature: &SignatureDetails{
			SignatureSummary: SignatureSummary{Kind: SignatureSMIME, Status: SignatureUntrusted, Signer: signer},
			MessageID:        "m_1", Reason: SignatureReasonUnknownIssuer, ArrivedAt: at,
			SMIME: &SMIMEDetails{Chain: []SignatureCertificate{{SHA256: "ab12", NotBefore: at, NotAfter: at}},
				Revocation: SMIMERevocation{State: RevocationUnchecked, Checkable: true}},
		}},
			`{"signature":{"kind":"smime","status":"untrusted","signer":{"name":"Jan Novák","email":"jan@example.org","fingerprint":"ab12"},"messageId":"m_1","reason":"unknownIssuer","arrivedAt":"2026-11-02T08:00:00Z","smime":{"chain":[{"sha256":"ab12","notBefore":"2026-11-02T08:00:00Z","notAfter":"2026-11-02T08:00:00Z"}],"revocation":{"state":"unchecked","checkable":true}}}}`},
		{"pgp details", SignatureDetails{
			SignatureSummary: SignatureSummary{Kind: SignaturePGP, Status: SignatureUnknownKey},
			MessageID:        "m_2", ArrivedAt: at,
			PGP: &PGPDetails{KeyTrust: PGPKeyUnknown, KeyID: "0123456789abcdef",
				Importable: &PGPKeyOffer{Fingerprint: "cd34", Autocrypt: true}},
		},
			`{"kind":"pgp","status":"unknownKey","messageId":"m_2","arrivedAt":"2026-11-02T08:00:00Z","pgp":{"keyTrust":"unknown","keyId":"0123456789abcdef","importable":{"fingerprint":"cd34","autocrypt":true}}}`},
		{"key", PGPKey{Fingerprint: "cd34", UserIDs: []string{"Jan <jan@example.org>"}, Emails: []string{"jan@example.org"},
			Trust: PGPKeySeen, Source: PGPSourceAutocrypt, FirstSeen: at, CreatedAt: at},
			`{"fingerprint":"cd34","userIds":["Jan \u003cjan@example.org\u003e"],"emails":["jan@example.org"],"trust":"seen","source":"autocrypt","firstSeen":"2026-11-02T08:00:00Z","createdAt":"2026-11-02T08:00:00Z"}`},
		{"import by part", PGPKeyImportParams{MessageID: "m_2", PartID: "3"}, `{"messageId":"m_2","partId":"3"}`},
		{"import binary", PGPKeyImportParams{Data: []byte{1, 2}}, `{"data":"AQI="}`},
		{"import result", PGPKeyImportResult{Keys: []PGPKey{}, Added: 1}, `{"keys":[],"added":1,"updated":0}`},
		{"verify", PGPKeyVerifyParams{Fingerprint: "cd34"}, `{"fingerprint":"cd34","verified":false}`},
		{"notification", SignaturesChangedNotification{}, `{}`},
	}
	for _, c := range cases {
		raw, err := json.Marshal(c.v)
		if err != nil || string(raw) != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, raw, c.want)
		}
	}
	// message.get's summary decodes into Message.Signature, not into the
	// embedded MessageSummary.Signature.
	var back Message
	if err := json.Unmarshal([]byte(cases[1].want), &back); err != nil {
		t.Fatal(err)
	}
	if back.Signature == nil || back.Signature.Signer == nil || back.Signature.PartID != "2" || back.MessageSummary.Signature != nil {
		t.Fatalf("decoded %+v / %+v", back.Signature, back.MessageSummary.Signature)
	}
}
