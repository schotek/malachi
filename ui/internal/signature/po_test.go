// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package signature

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// msgKey is a msgid of the template with its context and plural.
type msgKey struct{ ctx, msgid, plural string }

// recorder is a translator that notes every msgid asked for.
type recorder map[msgKey]bool

func (r recorder) T(msgid string) string { r[msgKey{msgid: msgid}] = true; return msgid }

func (r recorder) N(msgid, plural string, n int) string {
	r[msgKey{msgid: msgid, plural: plural}] = true
	if n == 1 {
		return msgid
	}
	return plural
}

func (r recorder) C(ctx, msgid string) string { r[msgKey{ctx: ctx, msgid: msgid}] = true; return msgid }

// exercise calls every function of the package that translates, down
// every branch that picks a msgid.
func exercise(tr Translator) {
	at := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	date := func(time.Time) string { return "date" }
	kinds := []api.SignatureKind{api.SignatureSMIME, api.SignaturePGP}
	statuses := []api.SignatureStatus{
		api.SignatureInvalid, api.SignatureRevoked, api.SignatureMismatch, api.SignatureUntrusted,
		api.SignatureUnknownKey, api.SignatureWeak, api.SignatureUnsupported, api.SignatureError,
		api.SignatureValid, api.SignatureValidSeen,
	}
	notes := []api.SignatureNote{
		api.SignatureNoteExpiredSince, api.SignatureNoteRevocationUnchecked, api.SignatureNoteRevocationGood,
		api.SignatureNoteRevokedAfter, api.SignatureNotePartialFooter, api.SignatureNoteProtectedHeadersDiffer,
		api.SignatureNoteInlinePGP, api.SignatureNoteKeyChanged, api.SignatureNoteOpaque,
	}
	reasons := []api.SignatureReason{
		api.SignatureReasonDigestMismatch, api.SignatureReasonBadSignature, api.SignatureReasonMalformed,
		api.SignatureReasonMD5, api.SignatureReasonKeyUsage, api.SignatureReasonUnknownIssuer,
		api.SignatureReasonSelfSigned, api.SignatureReasonNotYetValid, api.SignatureReasonExpired,
		api.SignatureReasonSHA1, api.SignatureReasonShortKey, api.SignatureReasonAlgorithm,
		api.SignatureReasonMultipleSigners, api.SignatureReasonRebuilt, api.SignatureReasonFormat,
		api.SignatureReasonLimit, api.SignatureReasonInternal,
	}
	signer := &api.SignatureSigner{Name: "n", Email: "e@x"}
	for _, k := range kinds {
		for _, s := range statuses {
			for _, sg := range []*api.SignatureSigner{nil, signer} {
				for _, ns := range [][]api.SignatureNote{nil, {api.SignatureNoteInlinePGP}} {
					sum := &api.SignatureSummary{Kind: k, Status: s, Signer: sg, Notes: ns}
					BadgeFor(sum, tr)
					explanation(sum, "f@x", tr)
					ListIconFor(&api.MessageSignature{Kind: k, Status: s}, tr)
					StatusTitle(k, s, tr)
				}
			}
		}
		for _, n := range notes {
			NoteText(k, n, tr)
		}
		for _, r := range reasons {
			ReasonText(k, r, tr)
		}
	}
	for _, st := range []api.RevocationState{api.RevocationUnchecked, api.RevocationGood, api.RevocationRevoked, api.RevocationUnknown} {
		revocationText(api.SMIMERevocation{State: st, CheckedAt: &at, RevokedAt: &at}, date, tr)
		revocationText(api.SMIMERevocation{State: st}, date, tr)
	}
	fp := "0123456789abcdef0123456789abcdef01234567"
	for _, trust := range []api.PGPKeyTrust{api.PGPKeyUnknown, api.PGPKeySeen, api.PGPKeyVerified} {
		for _, src := range []api.PGPKeySource{api.PGPSourceAutocrypt, api.PGPSourceAttached, api.PGPSourceImport} {
			DetailsFor(&api.SignatureDetails{
				SignatureSummary: api.SignatureSummary{Kind: api.SignaturePGP, Status: api.SignatureValidSeen, Signer: signer, Notes: notes},
				MessageID:        "m", Reason: api.SignatureReasonExpired, SignedAt: &at, ArrivedAt: at, Algorithm: "a",
				ExpiredAt: &at, RevokedAt: &at,
				PGP: &api.PGPDetails{KeyTrust: trust, KeyID: "0123456789abcdef", PrimaryFingerprint: fp, UserIDs: []string{"u"},
					KeySource: src, FirstSeen: &at, PreviousFingerprint: fp, Importable: &api.PGPKeyOffer{PartID: "2"}},
			}, "f@x", ActionNone, date, tr)
			KeyRows([]api.PGPKey{{Fingerprint: fp, Trust: trust, Source: src, ExpiresAt: &at, RevokedAt: &at}, {Fingerprint: fp, Trust: trust, Source: src, ExpiresAt: &at}}, at, date, tr)
		}
	}
	for _, busy := range []Action{ActionNone, ActionCheckRevocation} {
		for _, st := range []api.RevocationState{api.RevocationUnchecked, api.RevocationGood} {
			DetailsFor(&api.SignatureDetails{
				SignatureSummary: api.SignatureSummary{Kind: api.SignatureSMIME, Status: api.SignatureValid, Signer: signer},
				MessageID:        "m", ArrivedAt: at,
				ProtectedHeaders: []api.ProtectedHeader{{Name: api.ProtectedSubject, Value: "v"}, {Name: api.ProtectedFrom, Value: "v"},
					{Name: api.ProtectedTo, Value: "v"}, {Name: api.ProtectedCc, Value: "v"}, {Name: api.ProtectedReplyTo, Value: "v"},
					{Name: api.ProtectedDate, Value: "v"}},
				SMIME: &api.SMIMEDetails{Chain: []api.SignatureCertificate{
					{SHA256: fp, Subject: "s", Issuer: "i", Emails: []string{"e"}, NotBefore: at, NotAfter: at},
					{SHA256: fp, Subject: "s", NotBefore: at, NotAfter: at},
					{SHA256: fp, Subject: "s", SystemRoot: true},
				}, Revocation: api.SMIMERevocation{State: st, Checkable: true}},
			}, "f@x", busy, date, tr)
		}
	}
	KeyRows([]api.PGPKey{{Fingerprint: fp, Trust: api.PGPKeyVerified}}, at, date, tr)
	DetailsFor(&api.SignatureDetails{
		SignatureSummary: api.SignatureSummary{Kind: api.SignaturePGP, Status: api.SignatureUnknownKey},
		PGP:              &api.PGPDetails{KeyTrust: api.PGPKeyUnknown, KeyID: "0123456789abcdef"},
	}, "", ActionNone, date, tr)
	DialogTitle(tr)
	BadgeTooltip(tr)
	CloseLabel(tr)
	ConfirmKeyQuestion(tr)
	RemoveKeyQuestion(tr)
	KeyConfirmedText(tr)
	ConfirmationRemovedText(tr)
	KeyRemovedText(tr)
	for _, r := range []api.RevocationFailure{"", api.RevocationFailNetwork, api.RevocationFailTimeout, api.RevocationFailServer, api.RevocationFailResponse, api.RevocationFailAddress} {
		RevocationFailedText(r, tr)
	}
	for _, r := range []api.PGPImportFailure{"", api.PGPImportNoKey, api.PGPImportMalformed, api.PGPImportSecretKey, api.PGPImportTooLarge, api.PGPImportTooMany} {
		ImportFailedText(r, tr)
	}
	for _, r := range []api.PGPKeyImportResult{{Added: 1}, {Updated: 1}, {}} {
		ImportedText(r, tr)
	}
	ImportDialogTexts(tr)
	SecurityPageTexts(tr)
	buttonLabel(ActionUnconfirmKey, false, false, tr)
	buttonLabel(ActionRemoveKey, false, false, tr)
}

// template reads po/malachi.pot: every entry, and whether it names a file
// of this package among its references.
func template(t *testing.T) map[msgKey]bool {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "..", "po", "malachi.pot"))
	if err != nil {
		t.Fatalf("open the template: %v", err)
	}
	defer f.Close()
	field := regexp.MustCompile(`^(msgctxt|msgid_plural|msgid|msgstr(?:\[\d\])?) (".*")$`)
	out := map[msgKey]bool{}
	var cur msgKey
	var ours bool
	var last *string
	flush := func() {
		if cur.msgid != "" {
			out[cur] = ours
		}
		cur, ours, last = msgKey{}, false, nil
	}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		switch {
		case strings.TrimSpace(line) == "":
			flush()
		case strings.HasPrefix(line, "#:"):
			ours = ours || strings.Contains(line, "ui/internal/signature/")
		case strings.HasPrefix(line, "\""):
			if last != nil {
				*last += unquote(t, line)
			}
		default:
			m := field.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			last = nil
			switch m[1] {
			case "msgctxt":
				last = &cur.ctx
			case "msgid":
				last = &cur.msgid
			case "msgid_plural":
				last = &cur.plural
			}
			if last != nil {
				*last = unquote(t, m[2])
			}
		}
	}
	flush()
	if err := s.Err(); err != nil {
		t.Fatalf("read the template: %v", err)
	}
	return out
}

func unquote(t *testing.T, s string) string {
	t.Helper()
	u, err := strconv.Unquote(s)
	if err != nil {
		t.Fatalf("template string %s: %v", s, err)
	}
	return u
}

// TestMsgidsInTemplate keeps po/malachi.pot in step with the package on a
// machine without make lint: every msgid it translates (its own and the
// texts it shares with other packages) is in the template with its context and plural,
// and every entry that names a file of the package is one it translates.
func TestMsgidsInTemplate(t *testing.T) {
	used := recorder{}
	exercise(used)
	pot := template(t)
	for k := range used {
		if _, ok := pot[k]; !ok {
			t.Errorf("po/malachi.pot lacks msgid %q (context %q, plural %q)", k.msgid, k.ctx, k.plural)
		}
	}
	own := 0
	for k, ours := range pot {
		if !ours {
			continue
		}
		own++
		if !used[k] {
			t.Errorf("po/malachi.pot names ui/internal/signature for msgid %q (context %q), which the package does not translate", k.msgid, k.ctx)
		}
	}
	if own == 0 {
		t.Error("po/malachi.pot names no file of ui/internal/signature")
	}
}
