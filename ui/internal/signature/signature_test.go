// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package signature

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// ids is the identity translator.
type ids struct{}

func (ids) T(msgid string) string { return msgid }
func (ids) N(msgid, plural string, n int) string {
	if n == 1 {
		return msgid
	}
	return plural
}
func (ids) C(_, msgid string) string { return msgid }

var (
	tr      = ids{}
	arrived = time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	day     = func(t time.Time) string { return t.Format("2006-01-02") }
	fp40    = "0123456789abcdef0123456789abcdef01234567"
	fp64    = "ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34"
)

var allStatuses = []api.SignatureStatus{
	api.SignatureInvalid, api.SignatureRevoked, api.SignatureMismatch, api.SignatureUntrusted,
	api.SignatureUnknownKey, api.SignatureWeak, api.SignatureUnsupported, api.SignatureError,
	api.SignatureValid, api.SignatureValidSeen,
}

func TestSeverityIconStyle(t *testing.T) {
	cases := []struct {
		status api.SignatureStatus
		sev    Severity
		icon   string
		style  string
	}{
		{api.SignatureValid, SeverityGood, "security-high-symbolic", "success"},
		{api.SignatureValidSeen, SeverityGoodSeen, "security-medium-symbolic", "success"},
		{api.SignatureMismatch, SeverityWarning, "dialog-warning-symbolic", "warning"},
		{api.SignatureUntrusted, SeverityWarning, "dialog-warning-symbolic", "warning"},
		{api.SignatureWeak, SeverityWarning, "dialog-warning-symbolic", "warning"},
		{api.SignatureInvalid, SeverityBad, "dialog-error-symbolic", "error"},
		{api.SignatureRevoked, SeverityBad, "dialog-error-symbolic", "error"},
		{api.SignatureUnknownKey, SeverityNeutral, "security-low-symbolic", "dim-label"},
		{api.SignatureUnsupported, SeverityNeutral, "security-low-symbolic", "dim-label"},
		{api.SignatureError, SeverityNeutral, "security-low-symbolic", "dim-label"},
		{"somethingNew", SeverityNeutral, "security-low-symbolic", "dim-label"},
	}
	for _, c := range cases {
		sev := SeverityOf(c.status)
		if sev != c.sev || IconOf(sev) != c.icon || StyleOf(sev) != c.style {
			t.Errorf("%s: %v %q %q, want %v %q %q", c.status, sev, IconOf(sev), StyleOf(sev), c.sev, c.icon, c.style)
		}
	}
	if IconOf(SeverityNone) != "" || StyleOf(SeverityNone) != "" {
		t.Error("SeverityNone has an icon or style")
	}
}

func TestListIcon(t *testing.T) {
	if _, ok := ListIconFor(nil, tr); ok {
		t.Fatal("nil has an icon")
	}
	want := map[api.SignatureStatus]bool{
		api.SignatureValid: true, api.SignatureValidSeen: true, api.SignatureInvalid: true,
		api.SignatureRevoked: true, api.SignatureMismatch: true,
	}
	for _, s := range append(allStatuses, "somethingNew") {
		li, ok := ListIconFor(&api.MessageSignature{Kind: api.SignatureSMIME, Status: s}, tr)
		if ok != want[s] {
			t.Errorf("%s: icon %v, want %v", s, ok, want[s])
			continue
		}
		if ok && (li.Icon != IconOf(SeverityOf(s)) || li.Style != StyleOf(SeverityOf(s)) || li.Tooltip != StatusTitle(api.SignatureSMIME, s, tr)) {
			t.Errorf("%s: %+v", s, li)
		}
	}
}

func TestSignerLabel(t *testing.T) {
	cases := []struct {
		s    *api.SignatureSigner
		want string
	}{
		{nil, ""},
		{&api.SignatureSigner{}, ""},
		{&api.SignatureSigner{Name: "Jan Novák", Email: "jan@example.org"}, "Jan Novák <jan@example.org>"},
		{&api.SignatureSigner{Email: "jan@example.org"}, "jan@example.org"},
		{&api.SignatureSigner{Name: "Jan Novák"}, "Jan Novák"},
		{&api.SignatureSigner{Name: "JAN@example.org", Email: "jan@example.org"}, "jan@example.org"},
		// Hostile: bidi override, zero-width, newline, control.
		{&api.SignatureSigner{Name: "Jan‮ kávoN​\nX\x07", Email: " jan@example.org "}, "Jan kávoN X <jan@example.org>"},
	}
	for _, c := range cases {
		if got := SignerLabel(c.s); got != c.want {
			t.Errorf("%+v: %q, want %q", c.s, got, c.want)
		}
	}
}

func TestCleanCapsOnRuneBoundary(t *testing.T) {
	long := strings.Repeat("ž", 200) // 400 bytes
	got := clean(long)
	if len(got) > api.MaxSignatureTextBytes || !strings.HasPrefix(long, got) || len(got) != 256 {
		t.Fatalf("%d bytes: %q", len(got), got)
	}
	if clean(" a b") != "a b" {
		t.Fatalf("separators and private use: %q", clean(" a b"))
	}
}

func TestBadge(t *testing.T) {
	if b := BadgeFor(nil, tr); b.Visible || b.Text != "" {
		t.Fatalf("nil: %+v", b)
	}
	jan := &api.SignatureSigner{Name: "Jan", Email: "jan@example.org"}
	cases := []struct {
		kind   api.SignatureKind
		status api.SignatureStatus
		signer *api.SignatureSigner
		notes  []api.SignatureNote
		want   string
	}{
		{api.SignatureSMIME, api.SignatureValid, jan, nil, "Signed by Jan <jan@example.org>"},
		{api.SignaturePGP, api.SignatureValidSeen, jan, nil, "Signed by Jan <jan@example.org>"},
		{api.SignatureSMIME, api.SignatureValid, nil, nil, "Valid signature"},
		{api.SignatureSMIME, api.SignatureMismatch, jan, nil, "Signed by someone else: Jan <jan@example.org>"},
		{api.SignatureSMIME, api.SignatureMismatch, nil, nil, "Signed by someone else"},
		{api.SignatureSMIME, api.SignatureUntrusted, jan, nil, "Signed by Jan <jan@example.org>, but the certificate is not trusted"},
		{api.SignaturePGP, api.SignatureUntrusted, jan, nil, "Signed by Jan <jan@example.org>, but the key had expired"},
		{api.SignaturePGP, api.SignatureUntrusted, nil, nil, "Untrusted signature"},
		{api.SignatureSMIME, api.SignatureWeak, jan, nil, "Signed by Jan <jan@example.org> with a weak algorithm"},
		{api.SignatureSMIME, api.SignatureWeak, nil, nil, "Weak signature"},
		{api.SignatureSMIME, api.SignatureRevoked, jan, nil, "Signed with a revoked certificate"},
		{api.SignaturePGP, api.SignatureRevoked, jan, nil, "Signed with a revoked key"},
		{api.SignatureSMIME, api.SignatureInvalid, jan, nil, "Invalid signature"},
		{api.SignaturePGP, api.SignatureUnknownKey, nil, nil, "Signed with an unknown OpenPGP key"},
		{api.SignaturePGP, api.SignatureUnsupported, nil, []api.SignatureNote{api.SignatureNoteInlinePGP}, "Inline OpenPGP signature, not verified"},
		{api.SignatureSMIME, api.SignatureUnsupported, nil, nil, "Signature not verified"},
		{api.SignatureSMIME, api.SignatureError, nil, nil, "Signature could not be verified"},
		{api.SignatureSMIME, "somethingNew", jan, nil, "Signature could not be verified"},
	}
	for _, c := range cases {
		b := BadgeFor(&api.SignatureSummary{Kind: c.kind, Status: c.status, Signer: c.signer, Notes: c.notes}, tr)
		if !b.Visible || b.Text != c.want || b.Tooltip != "Show Signature Details" || b.Icon != IconOf(SeverityOf(c.status)) {
			t.Errorf("%s %s: %+v, want %q", c.kind, c.status, b, c.want)
		}
	}
}

func TestTitlesAndExplanationsCoverEveryStatus(t *testing.T) {
	for _, k := range []api.SignatureKind{api.SignatureSMIME, api.SignaturePGP} {
		for _, s := range allStatuses {
			if StatusTitle(k, s, tr) == "" || explanation(&api.SignatureSummary{Kind: k, Status: s}, "", tr) == "" {
				t.Errorf("%s %s has no title or explanation", k, s)
			}
		}
	}
	m := &api.SignatureSummary{Kind: api.SignatureSMIME, Status: api.SignatureMismatch,
		Signer: &api.SignatureSigner{Email: "boss@evil.example"}}
	if got := explanation(m, "boss@example.org", tr); got != "The signature is valid, but it belongs to boss@evil.example, not to the sender boss@example.org." {
		t.Errorf("mismatch: %q", got)
	}
	if got := explanation(m, "", tr); got != "The signature is valid, but it does not belong to the sender's address." {
		t.Errorf("mismatch without From: %q", got)
	}
}

func TestNotesAndReasons(t *testing.T) {
	notes := []api.SignatureNote{
		api.SignatureNoteExpiredSince, api.SignatureNoteRevocationUnchecked, api.SignatureNoteRevocationGood,
		api.SignatureNoteRevokedAfter, api.SignatureNotePartialFooter, api.SignatureNoteProtectedHeadersDiffer,
		api.SignatureNoteInlinePGP, api.SignatureNoteKeyChanged, api.SignatureNoteOpaque,
	}
	for _, k := range []api.SignatureKind{api.SignatureSMIME, api.SignaturePGP} {
		for _, n := range notes {
			if NoteText(k, n, tr) == "" {
				t.Errorf("%s %s has no text", k, n)
			}
		}
	}
	if NoteText(api.SignaturePGP, api.SignatureNoteExpiredSince, tr) == NoteText(api.SignatureSMIME, api.SignatureNoteExpiredSince, tr) {
		t.Error("expiredSince does not tell a key from a certificate")
	}
	if NoteText(api.SignatureSMIME, "unknownNote", tr) != "" {
		t.Error("an unknown note has a text")
	}
	reasons := []api.SignatureReason{
		api.SignatureReasonDigestMismatch, api.SignatureReasonBadSignature, api.SignatureReasonMalformed,
		api.SignatureReasonMD5, api.SignatureReasonKeyUsage, api.SignatureReasonUnknownIssuer,
		api.SignatureReasonSelfSigned, api.SignatureReasonNotYetValid, api.SignatureReasonExpired,
		api.SignatureReasonSHA1, api.SignatureReasonShortKey, api.SignatureReasonAlgorithm,
		api.SignatureReasonMultipleSigners, api.SignatureReasonRebuilt, api.SignatureReasonFormat,
		api.SignatureReasonLimit,
	}
	for _, r := range reasons {
		if ReasonText(api.SignatureSMIME, r, tr) == "" {
			t.Errorf("%s has no text", r)
		}
	}
	for _, r := range []api.SignatureReason{"", api.SignatureReasonInternal, "unknownReason"} {
		if ReasonText(api.SignatureSMIME, r, tr) != "" {
			t.Errorf("%q has a text", r)
		}
	}
}

func TestTiles(t *testing.T) {
	atts := []api.Attachment{{PartID: "1.2", Filename: "a.pdf"}, {PartID: "2", Filename: "smime.p7s", ContentType: "application/pkcs7-signature"}}
	if got := TileAttachments(atts, nil); !reflect.DeepEqual(got, atts) {
		t.Errorf("no verdict: %v", got)
	}
	sig := &api.SignatureSummary{Kind: api.SignatureSMIME, Status: api.SignatureValid, PartID: "2"}
	if got := TileAttachments(atts, sig); len(got) != 1 || got[0].PartID != "1.2" {
		t.Errorf("verdict: %v", got)
	}
	// Opaque S/MIME and inline OpenPGP have no part: nothing is hidden,
	// and a part without an id is never taken for it.
	if got := TileAttachments(append(atts, api.Attachment{}), &api.SignatureSummary{Status: api.SignatureValid}); len(got) != 3 {
		t.Errorf("no part: %v", got)
	}
	// A signature part of another verdict's message (a forwarded signed
	// message) stays.
	if HidesPart(api.Attachment{PartID: "3.2", ContentType: "application/pgp-signature"}, sig) {
		t.Error("hid another signature part")
	}
}

func TestFormatFingerprint(t *testing.T) {
	cases := map[string]string{
		"":                 "",
		"0123456789abcdef": "0123 4567 89AB CDEF",
		fp40:               "0123 4567 89AB CDEF 0123 4567 89AB CDEF 0123 4567",
		fp64:               "AB12 CD34 AB12 CD34 AB12 CD34 AB12 CD34 AB12 CD34 AB12 CD34 AB12 CD34 AB12 CD34",
		"0123456789abcdeg": "",
		"0123":             "",
		"0123456789ABCDEF0123456789ABCDEF0123456": "",
	}
	for in, want := range cases {
		if got := FormatFingerprint(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func rowKeys(rows []Row) []RowKey {
	var out []RowKey
	for _, r := range rows {
		out = append(out, r.Key)
	}
	return out
}

func TestDetailsSMIME(t *testing.T) {
	signed := arrived.Add(-time.Hour)
	expired := arrived.Add(24 * time.Hour)
	d := &api.SignatureDetails{
		SignatureSummary: api.SignatureSummary{Kind: api.SignatureSMIME, Status: api.SignatureValid,
			Signer: &api.SignatureSigner{Name: "Jan", Email: "jan@example.org", Fingerprint: fp64},
			Notes:  []api.SignatureNote{api.SignatureNoteRevocationUnchecked, api.SignatureNoteExpiredSince, "unknownNote"},
			PartID: "2"},
		MessageID: "m_1", SignedAt: &signed, ArrivedAt: arrived, Algorithm: "RSA 2048 / SHA-256", ExpiredAt: &expired,
		ProtectedHeaders: []api.ProtectedHeader{{Name: api.ProtectedSubject, Value: "Real‮ subject"}, {Name: "unknown", Value: "x"}},
		SMIME: &api.SMIMEDetails{
			Chain: []api.SignatureCertificate{
				{SHA256: fp64, Subject: "CN=Jan", Issuer: "CN=CA 2", Emails: []string{"jan@example.org"}, NotBefore: arrived, NotAfter: expired},
				{SHA256: fp64, Subject: "CN=CA 2", Issuer: "CN=Root", NotBefore: arrived, NotAfter: expired},
				{SHA256: fp64, Subject: "CN=Root", Issuer: "CN=Root", NotBefore: arrived, NotAfter: expired, SystemRoot: true, SelfSigned: true},
			},
			Revocation: api.SMIMERevocation{State: api.RevocationUnchecked, Checkable: true},
		},
	}
	got := DetailsFor(d, "jan@example.org", ActionNone, day, tr)
	if got.Title != "Valid signature" || got.Severity != SeverityGood || got.Caveat == "" {
		t.Fatalf("heading: %+v", got)
	}
	want := []RowKey{RowSigner, RowKind, RowSignedAt, RowArrivedAt, RowAlgorithm, RowRevocation, RowExpired}
	if !reflect.DeepEqual(rowKeys(got.Rows), want) {
		t.Fatalf("rows %v, want %v", rowKeys(got.Rows), want)
	}
	if got.Rows[0].Value != "Jan <jan@example.org>" || got.Rows[1].Value != "S/MIME" || got.Rows[5].Value != "Not checked" {
		t.Errorf("values: %+v", got.Rows)
	}
	if !reflect.DeepEqual(got.Notes, []string{"The certificate has expired since the message arrived."}) {
		t.Errorf("notes: %q", got.Notes)
	}
	var secs []SectionKey
	for _, s := range got.Sections {
		secs = append(secs, s.Key)
	}
	if !reflect.DeepEqual(secs, []SectionKey{SectionSignerCertificate, SectionIntermediate, SectionRoot, SectionSignedHeaders}) {
		t.Fatalf("sections %v", secs)
	}
	signer := got.Sections[0]
	if !reflect.DeepEqual(rowKeys(signer.Rows), []RowKey{RowIssuedTo, RowEmails, RowIssuedBy, RowValidity, RowCertFingerprint}) ||
		signer.Rows[3].Value != "2026-11-02 to 2026-11-03" || !signer.Rows[4].Monospace {
		t.Errorf("signer's certificate: %+v", signer)
	}
	hs := got.Sections[3].Rows
	if len(hs) != 1 || hs[0].Label != "Subject" || hs[0].Value != "Real subject" {
		t.Errorf("signed headers: %+v", hs)
	}
	if len(got.Buttons) != 1 || got.Buttons[0] != (Button{ActionCheckRevocation, "Check Revocation", true}) || got.RevocationCaption == "" {
		t.Errorf("buttons: %+v", got.Buttons)
	}
	// While it runs, and once checked.
	busy := DetailsFor(d, "", ActionCheckRevocation, day, tr)
	if busy.Buttons[0] != (Button{ActionCheckRevocation, "Checking…", false}) {
		t.Errorf("busy: %+v", busy.Buttons)
	}
	checked := arrived.Add(time.Hour)
	d.SMIME.Revocation = api.SMIMERevocation{State: api.RevocationGood, CheckedAt: &checked, Checkable: true}
	again := DetailsFor(d, "", ActionNone, day, tr)
	if again.Buttons[0].Label != "Check Revocation Again" || again.Rows[5].Value != "Not revoked (checked 2026-11-02)" {
		t.Errorf("checked: %+v %+v", again.Buttons, again.Rows[5])
	}
	// Without a responder, or without the certificate, no button.
	d.SMIME.Revocation.Checkable = false
	if b := DetailsFor(d, "", ActionNone, day, tr).Buttons; len(b) != 0 {
		t.Errorf("not checkable: %+v", b)
	}
	d.SMIME.Revocation.Checkable, d.SMIME.Chain = true, nil
	if b := DetailsFor(d, "", ActionNone, day, tr); len(b.Buttons) != 0 || len(b.Sections) != 1 {
		t.Errorf("no chain: %+v", b)
	}
}

func TestRevocationValues(t *testing.T) {
	at := arrived
	cases := []struct {
		r    api.SMIMERevocation
		want string
	}{
		{api.SMIMERevocation{State: api.RevocationUnchecked}, "Not checked"},
		{api.SMIMERevocation{State: api.RevocationGood, CheckedAt: &at}, "Not revoked (checked 2026-11-02)"},
		{api.SMIMERevocation{State: api.RevocationGood}, "Not checked"},
		{api.SMIMERevocation{State: api.RevocationRevoked, RevokedAt: &at}, "Revoked on 2026-11-02"},
		{api.SMIMERevocation{State: api.RevocationRevoked}, "Revoked"},
		{api.SMIMERevocation{State: api.RevocationUnknown, CheckedAt: &at}, "Unknown to the issuer (checked 2026-11-02)"},
		{api.SMIMERevocation{State: "new"}, "Not checked"},
	}
	for _, c := range cases {
		if got := revocationText(c.r, day, tr); got != c.want {
			t.Errorf("%+v: %q, want %q", c.r, got, c.want)
		}
	}
}

func TestDetailsPGP(t *testing.T) {
	seen := arrived.Add(-48 * time.Hour)
	d := &api.SignatureDetails{
		SignatureSummary: api.SignatureSummary{Kind: api.SignaturePGP, Status: api.SignatureValidSeen,
			Signer: &api.SignatureSigner{Name: "Jan", Email: "jan@example.org", Fingerprint: fp40},
			Notes:  []api.SignatureNote{api.SignatureNoteKeyChanged}},
		MessageID: "m_2", ArrivedAt: arrived,
		PGP: &api.PGPDetails{KeyTrust: api.PGPKeySeen, KeyID: "89abcdef01234567", PrimaryFingerprint: fp40,
			UserIDs: []string{"Jan <jan@example.org>", "", "Jan (work) <jan@work.example>"}, KeySource: api.PGPSourceAutocrypt,
			FirstSeen: &seen, PreviousFingerprint: strings.Repeat("ef", 20),
			Importable: &api.PGPKeyOffer{Fingerprint: fp40, Autocrypt: true}},
	}
	got := DetailsFor(d, "", ActionNone, day, tr)
	want := []RowKey{RowSigner, RowKind, RowArrivedAt, RowKeyTrust, RowKeyFingerprint, RowKeySource, RowFirstSeen, RowUserIDs, RowPreviousKey}
	if !reflect.DeepEqual(rowKeys(got.Rows), want) {
		t.Fatalf("rows %v, want %v", rowKeys(got.Rows), want)
	}
	if got.Rows[3].Value != "Found automatically, not confirmed" || got.Rows[7].Value != "Jan <jan@example.org>\nJan (work) <jan@work.example>" {
		t.Errorf("values: %+v", got.Rows)
	}
	if got.Rows[5].Value != "Autocrypt header of a message" {
		t.Errorf("source: %q", got.Rows[5].Value)
	}
	if !reflect.DeepEqual(got.Notes, []string{"This sender used a different key before."}) {
		t.Errorf("notes: %q", got.Notes)
	}
	if len(got.Buttons) != 2 || got.Buttons[0].Action != ActionImportKey || got.Buttons[1].Action != ActionConfirmKey ||
		got.KeyFingerprint != FormatFingerprint(fp40) || got.RevocationCaption != "" {
		t.Errorf("buttons: %+v %q", got.Buttons, got.KeyFingerprint)
	}
	if p, ok := ImportParams(d); !ok || !reflect.DeepEqual(p, api.PGPKeyImportParams{MessageID: "m_2", Autocrypt: true}) {
		t.Errorf("import %+v %v", p, ok)
	}
	if p, ok := ConfirmParams(d); !ok || p != (api.PGPKeyVerifyParams{Fingerprint: fp40, Verified: true}) {
		t.Errorf("confirm %+v %v", p, ok)
	}

	// An unknown key: the key ID, Import Key from an attachment, no Confirm.
	unknown := &api.SignatureDetails{
		SignatureSummary: api.SignatureSummary{Kind: api.SignaturePGP, Status: api.SignatureUnknownKey},
		MessageID:        "m_3", ArrivedAt: arrived,
		PGP: &api.PGPDetails{KeyTrust: api.PGPKeyUnknown, KeyID: "89abcdef01234567",
			Importable: &api.PGPKeyOffer{Fingerprint: fp40, PartID: "3"}},
	}
	got = DetailsFor(unknown, "", ActionImportKey, day, tr)
	if !reflect.DeepEqual(rowKeys(got.Rows), []RowKey{RowKind, RowArrivedAt, RowKeyTrust, RowKeyID}) || got.Rows[2].Value != "Not in Malachi Mail" {
		t.Fatalf("unknown rows %+v", got.Rows)
	}
	if len(got.Buttons) != 1 || got.Buttons[0] != (Button{ActionImportKey, "Import Key", false}) || got.Caveat != "" {
		t.Errorf("unknown buttons %+v", got.Buttons)
	}
	if p, _ := ImportParams(unknown); !reflect.DeepEqual(p, api.PGPKeyImportParams{MessageID: "m_3", PartID: "3"}) {
		t.Errorf("import from part %+v", p)
	}

	// A confirmed key, a revoked one, an offer naming both sources.
	d.PGP.KeyTrust = api.PGPKeyVerified
	if _, ok := ConfirmParams(d); ok {
		t.Error("confirm a verified key")
	}
	d.PGP.KeyTrust, d.Status = api.PGPKeySeen, api.SignatureRevoked
	if _, ok := ConfirmParams(d); ok {
		t.Error("confirm a revoked key")
	}
	d.PGP.Importable = &api.PGPKeyOffer{PartID: "3", Autocrypt: true}
	if _, ok := ImportParams(d); ok {
		t.Error("import from two sources")
	}
	revoked := arrived.Add(-time.Hour)
	d.RevokedAt = &revoked
	if keys := rowKeys(DetailsFor(d, "", ActionNone, day, tr).Rows); keys[len(keys)-1] != RowRevoked {
		t.Errorf("revoked row: %v", keys)
	}
	if got := DetailsFor(nil, "", ActionNone, day, tr); !reflect.DeepEqual(got, Details{}) {
		t.Errorf("nil: %+v", got)
	}
}

func TestErrors(t *testing.T) {
	// Over the wire Data is a decoded JSON object, in the daemon the struct.
	wire := &api.Error{Code: api.CodeRevocationFailed, Data: map[string]any{"reason": "timeout"}}
	if RevocationFailureOf(wire) != api.RevocationFailTimeout {
		t.Error("wire data")
	}
	if RevocationFailureOf(&api.Error{Code: api.CodeRevocationFailed, Data: api.RevocationFailedData{Reason: api.RevocationFailAddress}}) != api.RevocationFailAddress {
		t.Error("struct data")
	}
	for _, e := range []error{nil, &api.Error{Code: api.CodeOffline}, &api.Error{Code: api.CodeRevocationFailed}, &api.Error{Code: api.CodeRevocationFailed, Data: "x"}} {
		if RevocationFailureOf(e) != "" {
			t.Errorf("%v has a class", e)
		}
	}
	if ImportFailureOf(&api.Error{Code: api.CodeInvalidArgument, Data: map[string]any{"reason": "secretKey"}}) != api.PGPImportSecretKey {
		t.Error("import class")
	}
	if ImportFailureOf(&api.Error{Code: api.CodeMessageNotFound, Data: map[string]any{"reason": "secretKey"}}) != "" {
		t.Error("import class of another code")
	}
	for _, r := range []api.RevocationFailure{"", api.RevocationFailNetwork, api.RevocationFailTimeout, api.RevocationFailServer, api.RevocationFailResponse, api.RevocationFailAddress} {
		if RevocationFailedText(r, tr) == "" {
			t.Errorf("revocation %q", r)
		}
	}
	for _, r := range []api.PGPImportFailure{"", api.PGPImportNoKey, api.PGPImportMalformed, api.PGPImportSecretKey, api.PGPImportTooLarge, api.PGPImportTooMany} {
		if ImportFailedText(r, tr) == "" {
			t.Errorf("import %q", r)
		}
	}
}

func TestImportParamsAndText(t *testing.T) {
	if _, ok := PastedParams("  \n"); ok {
		t.Error("blank paste")
	}
	if p, ok := PastedParams("\n" + ArmorPlaceholder + "\nabc\n"); !ok || p.Armored != ArmorPlaceholder+"\nabc" {
		t.Errorf("paste %+v", p)
	}
	if _, ok := PastedParams(strings.Repeat("a", api.MaxPGPKeyBytes+1)); ok {
		t.Error("oversized paste")
	}
	if p, ok := FileParams([]byte(ArmorPlaceholder + "\n")); !ok || p.Armored == "" || p.Data != nil {
		t.Errorf("armoured file %+v", p)
	}
	if p, ok := FileParams([]byte{0x99, 0x01}); !ok || p.Armored != "" || len(p.Data) != 2 {
		t.Errorf("binary file %+v", p)
	}
	if _, ok := FileParams(nil); ok {
		t.Error("empty file")
	}
	if _, ok := FileParams(make([]byte, api.MaxPGPKeyBytes+1)); ok {
		t.Error("oversized file")
	}
	cases := []struct {
		r    api.PGPKeyImportResult
		want string
	}{
		{api.PGPKeyImportResult{Added: 1}, "1 key imported"},
		{api.PGPKeyImportResult{Added: 3, Updated: 1}, "3 keys imported"},
		{api.PGPKeyImportResult{Updated: 1}, "1 key updated"},
		{api.PGPKeyImportResult{Updated: 2}, "2 keys updated"},
		{api.PGPKeyImportResult{}, "The key was already imported."},
	}
	for _, c := range cases {
		if got := ImportedText(c.r, tr); got != c.want {
			t.Errorf("%+v: %q, want %q", c.r, got, c.want)
		}
	}
}

func TestKeyRows(t *testing.T) {
	now := arrived
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	keys := []api.PGPKey{
		{Fingerprint: fp64, UserIDs: []string{"zoe <zoe@example.org>"}, Trust: api.PGPKeyVerified, Source: api.PGPSourceImport, FirstSeen: now},
		{Fingerprint: fp40, UserIDs: nil, Emails: []string{"Adam@example.org"}, Trust: api.PGPKeySeen, Source: api.PGPSourceAutocrypt, FirstSeen: now, ExpiresAt: &past},
		{Fingerprint: strings.Repeat("ab", 20), Trust: api.PGPKeySeen, Source: api.PGPSourceAttached, FirstSeen: now, RevokedAt: &past, ExpiresAt: &future},
	}
	rows := KeyRows(keys, now, day, tr)
	var titles []string
	for _, r := range rows {
		titles = append(titles, r.Title)
	}
	wantTitles := []string{"ABAB ABAB ABAB ABAB ABAB ABAB ABAB ABAB ABAB ABAB", "Adam@example.org", "zoe <zoe@example.org>"}
	if !reflect.DeepEqual(titles, wantTitles) {
		t.Fatalf("titles %q", titles)
	}
	revoked, adam, zoe := rows[0], rows[1], rows[2]
	if zoe.TrustPill != "Confirmed" || !zoe.Verified || zoe.StatePill != "" || zoe.Origin != "Imported on 2026-11-02" ||
		len(zoe.Actions) != 2 || zoe.Actions[0].Action != ActionUnconfirmKey || zoe.Actions[1].Action != ActionRemoveKey {
		t.Errorf("zoe %+v", zoe)
	}
	if adam.TrustPill != "Not confirmed" || adam.StatePill != "Expired" || adam.Origin != "Found in a message header on 2026-11-02" ||
		adam.Actions[0].Action != ActionConfirmKey || adam.Subtitle != FormatFingerprint(fp40) || adam.Fingerprint != fp40 {
		t.Errorf("adam %+v", adam)
	}
	if revoked.StatePill != "Revoked" || revoked.Origin != "Imported from a message on 2026-11-02" ||
		len(revoked.Actions) != 1 || revoked.Actions[0].Action != ActionRemoveKey {
		t.Errorf("revoked %+v", revoked)
	}
}

func TestQuestionsAndPage(t *testing.T) {
	c := ConfirmKeyQuestion(tr)
	if c.Accept != "_Confirm Key" || c.Cancel != "_Cancel" || c.Destructive {
		t.Errorf("confirm %+v", c)
	}
	r := RemoveKeyQuestion(tr)
	if r.Accept != "_Remove" || !r.Destructive {
		t.Errorf("remove %+v", r)
	}
	p := SecurityPageTexts(tr)
	if p.Title != "Security" || p.Group != "OpenPGP Keys" || p.Import != "Import Key…" {
		t.Errorf("page %+v", p)
	}
	if d := ImportDialogTexts(tr); d.Import != "_Import" || d.ChooseFile != "Choose File…" {
		t.Errorf("import dialog %+v", d)
	}
}
