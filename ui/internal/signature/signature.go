// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package signature is the view logic of signed mail (phase 1 of
// docs/plans/signatures.md: verifying S/MIME and OpenPGP signatures of
// incoming mail): the small icon of a message row, the badge in a
// message's header (reading pane, message window, conversation card), the
// details dialog with its buttons (Check Revocation, Import Key, Confirm
// Key…), which attachment tiles a verdict hides, and Preferences →
// Security with the stored OpenPGP keys.
//
// The daemon verifies and decides the verdict (message.signature,
// pgp.*, docs/api.md §4.21); this package only turns its API values into
// texts and small view models. It is pure (no GTK, no gettext, no cgo: the
// caller passes a Translator) and the reference that is ported one to one
// to macOS (macos/Sources/MalachiCore/Signatures/) and Windows
// (windows/src/Malachi.Core/Signatures/).
//
// Every string of a certificate or key (names, addresses, distinguished
// names, user IDs, signed header values) is hostile input: the daemon
// cleans it, this package cleans it again, and the clients show it as
// plain text only.
package signature

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/jira"
)

// Translator translates a msgid of the malachi domain (jira.Translator).
type Translator = jira.Translator

// Names that stay as they are in every language.
const (
	SMIMEName   = "S/MIME"
	OpenPGPName = "OpenPGP"
	// ArmorPlaceholder is the placeholder of the import dialog's text
	// area.
	ArmorPlaceholder = "-----BEGIN PGP PUBLIC KEY BLOCK-----"
)

// KindName is the name of a signature format ("" unknown).
func KindName(k api.SignatureKind) string {
	switch k {
	case api.SignatureSMIME:
		return SMIMEName
	case api.SignaturePGP:
		return OpenPGPName
	}
	return ""
}

// ---------------------------------------------------------------------------
// Severity, icon and colour
// ---------------------------------------------------------------------------

// Severity is the colour family of a verdict.
type Severity int

const (
	// SeverityNone: nothing is shown (no verdict).
	SeverityNone Severity = iota
	// SeverityGood: valid, green with the check mark.
	SeverityGood
	// SeverityGoodSeen: validSeen, green without the check mark.
	SeverityGoodSeen
	// SeverityWarning: mismatch, untrusted, weak; yellow.
	SeverityWarning
	// SeverityBad: invalid, revoked; red.
	SeverityBad
	// SeverityNeutral: unknownKey, unsupported, error (and an unknown
	// status); grey.
	SeverityNeutral
)

// SeverityOf is the severity of a status; an unknown one reads as
// api.SignatureError (neutral).
func SeverityOf(s api.SignatureStatus) Severity {
	switch s {
	case api.SignatureValid:
		return SeverityGood
	case api.SignatureValidSeen:
		return SeverityGoodSeen
	case api.SignatureMismatch, api.SignatureUntrusted, api.SignatureWeak:
		return SeverityWarning
	case api.SignatureInvalid, api.SignatureRevoked:
		return SeverityBad
	}
	return SeverityNeutral
}

// Icon names, symbolic icons of the Adwaita theme (the GTK client; the
// Windows client maps them in IconGlyphs, macOS to SF Symbols): a full
// shield for a valid signature, a half one for a key not confirmed, an
// empty one for a signature that was not judged, the warning and error
// signs for the others.
const (
	IconGood     = "security-high-symbolic"
	IconGoodSeen = "security-medium-symbolic"
	IconWarning  = "dialog-warning-symbolic"
	IconBad      = "dialog-error-symbolic"
	IconNeutral  = "security-low-symbolic"
	// IconPage is the icon of Preferences → Security.
	IconPage = "security-high-symbolic"
)

// IconOf is the icon of a severity ("" for SeverityNone).
func IconOf(s Severity) string {
	switch s {
	case SeverityGood:
		return IconGood
	case SeverityGoodSeen:
		return IconGoodSeen
	case SeverityWarning:
		return IconWarning
	case SeverityBad:
		return IconBad
	case SeverityNeutral:
		return IconNeutral
	}
	return ""
}

// Style classes of libadwaita that colour the icon and the badge's text
// (macOS: systemGreen, systemOrange, systemRed, secondaryLabelColor;
// Windows: SystemFillColorSuccess, …Caution, …Critical, TextFillColorSecondary).
const (
	StyleGood    = "success"
	StyleWarning = "warning"
	StyleBad     = "error"
	StyleNeutral = "dim-label"
)

// StyleOf is the style class of a severity ("" for SeverityNone).
func StyleOf(s Severity) string {
	switch s {
	case SeverityGood, SeverityGoodSeen:
		return StyleGood
	case SeverityWarning:
		return StyleWarning
	case SeverityBad:
		return StyleBad
	case SeverityNeutral:
		return StyleNeutral
	}
	return ""
}

// ---------------------------------------------------------------------------
// The message list
// ---------------------------------------------------------------------------

// ListIcon is the small icon of a message row, next to the paperclip.
type ListIcon struct {
	Icon, Style string
	// Tooltip is StatusTitle.
	Tooltip string
}

// ListIconFor is the icon of a row's verdict: only valid, validSeen,
// invalid, revoked and mismatch get one (plan §6); the grey verdicts,
// untrusted and weak show nothing in the list, only in the header. A
// collapsed thread row passes its latest member's signature.
func ListIconFor(sig *api.MessageSignature, tr Translator) (ListIcon, bool) {
	if sig == nil {
		return ListIcon{}, false
	}
	switch sig.Status {
	case api.SignatureValid, api.SignatureValidSeen, api.SignatureInvalid, api.SignatureRevoked, api.SignatureMismatch:
	default:
		return ListIcon{}, false
	}
	sev := SeverityOf(sig.Status)
	return ListIcon{Icon: IconOf(sev), Style: StyleOf(sev), Tooltip: StatusTitle(sig.Kind, sig.Status, tr)}, true
}

// ---------------------------------------------------------------------------
// The header's badge
// ---------------------------------------------------------------------------

// Badge is the button in a message's header that opens the details.
type Badge struct {
	// Visible: the message has a verdict; nothing else is set otherwise.
	Visible  bool
	Severity Severity
	Icon     string
	Style    string
	// Text is the badge's sentence, plain text; Tooltip opens the details.
	Text, Tooltip string
}

// BadgeFor is the badge of message.get's signature (nil: no badge). The
// reading pane, the message window and a conversation card show it; a
// chat bubble and an attached message (message.embedded) do not.
func BadgeFor(s *api.SignatureSummary, tr Translator) Badge {
	if s == nil {
		return Badge{}
	}
	sev := SeverityOf(s.Status)
	return Badge{
		Visible:  true,
		Severity: sev,
		Icon:     IconOf(sev),
		Style:    StyleOf(sev),
		Text:     badgeText(s, SignerLabel(s.Signer), tr),
		Tooltip:  BadgeTooltip(tr),
	}
}

// SignerLabel is "Name <address>", the address alone or the name alone
// ("" for nil or empty), cleaned.
func SignerLabel(s *api.SignatureSigner) string {
	if s == nil {
		return ""
	}
	name, email := clean(s.Name), clean(s.Email)
	switch {
	case name != "" && email != "" && !strings.EqualFold(name, email):
		return name + " <" + email + ">"
	case email != "":
		return email
	}
	return name
}

// ---------------------------------------------------------------------------
// Attachment tiles (plan S15)
// ---------------------------------------------------------------------------

// HidesPart reports that a's tile is not shown: it is the detached
// signature part of a message with a verdict. A message without a verdict
// (sig nil) shows its signature parts like any file.
func HidesPart(a api.Attachment, sig *api.SignatureSummary) bool {
	return sig != nil && sig.PartID != "" && a.PartID == sig.PartID
}

// TileAttachments is atts without the part HidesPart hides; the other
// rules of the tiles (inline pictures, the invitation's calendar part)
// apply as before.
func TileAttachments(atts []api.Attachment, sig *api.SignatureSummary) []api.Attachment {
	out := make([]api.Attachment, 0, len(atts))
	for _, a := range atts {
		if !HidesPart(a, sig) {
			out = append(out, a)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// The details dialog
// ---------------------------------------------------------------------------

// RowKey names a row of the details.
type RowKey int

const (
	RowSigner RowKey = iota
	RowKind
	RowReason
	RowSignedAt
	RowArrivedAt
	RowAlgorithm
	RowRevocation
	RowKeyTrust
	RowKeyFingerprint
	RowKeyID
	RowKeySource
	RowFirstSeen
	RowUserIDs
	RowPreviousKey
	RowExpired
	RowRevoked
	// Rows of a certificate section.
	RowIssuedTo
	RowEmails
	RowIssuedBy
	RowValidity
	RowCertFingerprint
	// A row of the signed headers' section (its label is the header's).
	RowSignedHeader
)

// Row is one line of the details: a label and a plain-text value.
type Row struct {
	Key   RowKey
	Label string
	Value string
	// Monospace: a fingerprint or key ID. Every value is selectable.
	Monospace bool
}

// SectionKey names a section of the details.
type SectionKey int

const (
	SectionSignerCertificate SectionKey = iota
	SectionIntermediate
	SectionRoot
	SectionSignedHeaders
)

// Section is a titled group of rows under the main rows.
type Section struct {
	Key   SectionKey
	Title string
	Rows  []Row
}

// Action is a button of the details or a key row.
type Action int

const (
	ActionNone Action = iota
	// ActionCheckRevocation calls message.signatureRevocation.
	ActionCheckRevocation
	// ActionImportKey calls pgp.keyImport with ImportParams.
	ActionImportKey
	// ActionConfirmKey asks ConfirmKeyQuestion, then pgp.keyVerify
	// {verified: true}.
	ActionConfirmKey
	// ActionUnconfirmKey calls pgp.keyVerify {verified: false} (key rows).
	ActionUnconfirmKey
	// ActionRemoveKey asks RemoveKeyQuestion, then pgp.keyRemove (key rows).
	ActionRemoveKey
)

// Button is a button of the details.
type Button struct {
	Action  Action
	Label   string
	Enabled bool
}

// Question is a confirmation dialog.
type Question struct {
	Heading, Body string
	// Cancel and Accept are button labels with mnemonics (order by the
	// platform's convention); Destructive styles Accept.
	Cancel, Accept string
	Destructive    bool
}

// Details is the view model of the details dialog: heading with icon,
// explanation, rows, notes, sections, the caveat and buttons.
type Details struct {
	Severity Severity
	Icon     string
	Style    string
	// Title is StatusTitle; Explanation the paragraph under it.
	Title, Explanation string
	Rows               []Row
	// Notes are the sentences of the summary's notes, without those the
	// Revocation row already says (revocationUnchecked, revocationGood).
	Notes []string
	// Sections: the S/MIME chain (signer first), then the signed headers.
	Sections []Section
	// Caveat is the line under a valid verdict ("" otherwise).
	Caveat  string
	Buttons []Button
	// RevocationCaption is the line under Check Revocation ("" without
	// that button).
	RevocationCaption string
	// KeyFingerprint is the formatted fingerprint ConfirmKeyQuestion
	// shows ("" without Confirm Key…).
	KeyFingerprint string
}

// DetailsFor is the dialog of message.signature's details (nil: an empty
// model, nothing to show). from is the message's From address, date
// formats a date and time like the client's message headers, and busy is
// the action that runs (its button says so and is disabled).
func DetailsFor(d *api.SignatureDetails, from string, busy Action, date func(time.Time) string, tr Translator) Details {
	if d == nil {
		return Details{}
	}
	s := &d.SignatureSummary
	sev := SeverityOf(s.Status)
	out := Details{
		Severity:    sev,
		Icon:        IconOf(sev),
		Style:       StyleOf(sev),
		Title:       StatusTitle(s.Kind, s.Status, tr),
		Explanation: explanation(s, from, tr),
	}
	add := func(k RowKey, value string, mono bool) {
		if value != "" {
			out.Rows = append(out.Rows, Row{Key: k, Label: rowLabel(k, tr), Value: value, Monospace: mono})
		}
	}
	add(RowSigner, SignerLabel(s.Signer), false)
	add(RowKind, KindName(s.Kind), false)
	add(RowReason, ReasonText(s.Kind, d.Reason, tr), false)
	if d.SignedAt != nil {
		add(RowSignedAt, date(*d.SignedAt), false)
	}
	if !d.ArrivedAt.IsZero() {
		add(RowArrivedAt, date(d.ArrivedAt), false)
	}
	add(RowAlgorithm, clean(d.Algorithm), false)
	if s.Kind == api.SignatureSMIME && d.SMIME != nil {
		add(RowRevocation, revocationText(d.SMIME.Revocation, date, tr), false)
	}
	if s.Kind == api.SignaturePGP && d.PGP != nil {
		p := d.PGP
		add(RowKeyTrust, keyTrustText(p.KeyTrust, tr), false)
		fp := p.PrimaryFingerprint
		if fp == "" && s.Signer != nil {
			fp = s.Signer.Fingerprint
		}
		add(RowKeyFingerprint, FormatFingerprint(fp), true)
		if FormatFingerprint(fp) == "" {
			add(RowKeyID, FormatFingerprint(p.KeyID), true)
		}
		add(RowKeySource, keySourceText(p.KeySource, tr), false)
		if p.FirstSeen != nil {
			add(RowFirstSeen, date(*p.FirstSeen), false)
		}
		add(RowUserIDs, strings.Join(cleanList(p.UserIDs, api.MaxSignatureUserIDs), "\n"), false)
		add(RowPreviousKey, FormatFingerprint(p.PreviousFingerprint), true)
	}
	if d.ExpiredAt != nil {
		add(RowExpired, date(*d.ExpiredAt), false)
	}
	if d.RevokedAt != nil && s.Kind == api.SignaturePGP {
		add(RowRevoked, date(*d.RevokedAt), false)
	}

	for _, n := range s.Notes {
		if n == api.SignatureNoteRevocationUnchecked || n == api.SignatureNoteRevocationGood {
			continue
		}
		if t := NoteText(s.Kind, n, tr); t != "" {
			out.Notes = append(out.Notes, t)
		}
	}

	if s.Kind == api.SignatureSMIME && d.SMIME != nil {
		chain := d.SMIME.Chain
		if len(chain) > api.MaxSignatureChain {
			chain = chain[:api.MaxSignatureChain]
		}
		for i, c := range chain {
			out.Sections = append(out.Sections, certificateSection(c, sectionOf(i, len(chain), c), date, tr))
		}
	}
	if hs := signedHeaderRows(d.ProtectedHeaders, tr); len(hs) > 0 {
		out.Sections = append(out.Sections, Section{Key: SectionSignedHeaders, Title: sectionTitle(SectionSignedHeaders, tr), Rows: hs})
	}

	if s.Status == api.SignatureValid || s.Status == api.SignatureValidSeen {
		out.Caveat = caveat(tr)
	}

	button := func(a Action, again bool) {
		out.Buttons = append(out.Buttons, Button{Action: a, Label: buttonLabel(a, again, busy == a, tr), Enabled: busy != a})
	}
	if CanCheckRevocation(d) {
		button(ActionCheckRevocation, d.SMIME.Revocation.State != api.RevocationUnchecked)
		out.RevocationCaption = RevocationCaption(tr)
	}
	if _, ok := ImportParams(d); ok {
		button(ActionImportKey, false)
	}
	if p, ok := ConfirmParams(d); ok {
		button(ActionConfirmKey, false)
		out.KeyFingerprint = FormatFingerprint(p.Fingerprint)
	}
	return out
}

// sectionOf is the section of the i-th of n certificates: the signer's
// first, a root last (a system root, or self-signed after the signer's),
// intermediates between.
func sectionOf(i, n int, c api.SignatureCertificate) SectionKey {
	switch {
	case i == 0:
		return SectionSignerCertificate
	case i == n-1 && (c.SystemRoot || c.SelfSigned):
		return SectionRoot
	}
	return SectionIntermediate
}

func certificateSection(c api.SignatureCertificate, k SectionKey, date func(time.Time) string, tr Translator) Section {
	sec := Section{Key: k, Title: sectionTitle(k, tr)}
	add := func(rk RowKey, value string, mono bool) {
		if value != "" {
			sec.Rows = append(sec.Rows, Row{Key: rk, Label: rowLabel(rk, tr), Value: value, Monospace: mono})
		}
	}
	add(RowIssuedTo, clean(c.Subject), false)
	add(RowEmails, strings.Join(cleanList(c.Emails, api.MaxSignatureEmails), "\n"), false)
	add(RowIssuedBy, clean(c.Issuer), false)
	if !c.NotBefore.IsZero() && !c.NotAfter.IsZero() {
		add(RowValidity, validity(date(c.NotBefore), date(c.NotAfter), tr), false)
	}
	add(RowCertFingerprint, FormatFingerprint(c.SHA256), true)
	return sec
}

func signedHeaderRows(hs []api.ProtectedHeader, tr Translator) []Row {
	var out []Row
	for _, h := range hs {
		if len(out) == api.MaxProtectedHeaders {
			break
		}
		label := headerName(h.Name, tr)
		if label == "" {
			continue
		}
		out = append(out, Row{Key: RowSignedHeader, Label: label, Value: clean(h.Value)})
	}
	return out
}

// CanCheckRevocation reports Check Revocation: an S/MIME verdict whose
// signer's certificate names an OCSP responder or CRL the daemon may ask.
func CanCheckRevocation(d *api.SignatureDetails) bool {
	return d != nil && d.Kind == api.SignatureSMIME && d.SMIME != nil &&
		len(d.SMIME.Chain) > 0 && d.SMIME.Revocation.Checkable
}

// ImportParams are the params of Import Key: the key the message offers
// (PGPDetails.Importable), from its part or its Autocrypt header.
func ImportParams(d *api.SignatureDetails) (api.PGPKeyImportParams, bool) {
	if d == nil || d.Kind != api.SignaturePGP || d.PGP == nil || d.PGP.Importable == nil || d.MessageID == "" {
		return api.PGPKeyImportParams{}, false
	}
	o := d.PGP.Importable
	switch {
	case o.PartID != "" && !o.Autocrypt:
		return api.PGPKeyImportParams{MessageID: d.MessageID, PartID: o.PartID}, true
	case o.PartID == "" && o.Autocrypt:
		return api.PGPKeyImportParams{MessageID: d.MessageID, Autocrypt: true}, true
	}
	return api.PGPKeyImportParams{}, false
}

// ConfirmParams are the params of Confirm Key…: a stored key that is only
// seen, of a verdict that is not invalid or revoked.
func ConfirmParams(d *api.SignatureDetails) (api.PGPKeyVerifyParams, bool) {
	if d == nil || d.Kind != api.SignaturePGP || d.PGP == nil ||
		d.PGP.KeyTrust != api.PGPKeySeen || FormatFingerprint(d.PGP.PrimaryFingerprint) == "" {
		return api.PGPKeyVerifyParams{}, false
	}
	if d.Status == api.SignatureInvalid || d.Status == api.SignatureRevoked {
		return api.PGPKeyVerifyParams{}, false
	}
	return api.PGPKeyVerifyParams{Fingerprint: d.PGP.PrimaryFingerprint, Verified: true}, true
}

// ---------------------------------------------------------------------------
// Errors of the methods
// ---------------------------------------------------------------------------

// RevocationFailureOf is the class of a message.signatureRevocation error
// ("" for another error or data without a class); RevocationFailedText
// turns it into the sentence.
func RevocationFailureOf(err error) api.RevocationFailure {
	e, ok := err.(*api.Error)
	if !ok || e.Code != api.CodeRevocationFailed {
		return ""
	}
	var d api.RevocationFailedData
	if !decodeData(e.Data, &d) {
		return ""
	}
	return d.Reason
}

// ImportFailureOf is the class of a pgp.keyImport error ("" for another
// error); ImportFailedText turns it into the sentence.
func ImportFailureOf(err error) api.PGPImportFailure {
	e, ok := err.(*api.Error)
	if !ok || e.Code != api.CodeInvalidArgument {
		return ""
	}
	var d api.PGPImportErrorData
	if !decodeData(e.Data, &d) {
		return ""
	}
	return d.Reason
}

// decodeData reads Error.Data, which is the struct itself in the daemon
// and a decoded JSON value in a client.
func decodeData(data, into any) bool {
	if data == nil {
		return false
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return false
	}
	return json.Unmarshal(raw, into) == nil
}

// ---------------------------------------------------------------------------
// Import
// ---------------------------------------------------------------------------

// PastedParams are the params of a key pasted in the import dialog; false
// when there is nothing to import or it is over api.MaxPGPKeyBytes (the
// Import button is disabled then).
func PastedParams(text string) (api.PGPKeyImportParams, bool) {
	t := strings.TrimSpace(text)
	if t == "" || len(t) > api.MaxPGPKeyBytes {
		return api.PGPKeyImportParams{}, false
	}
	return api.PGPKeyImportParams{Armored: t}, true
}

// FileParams are the params of a key file: ASCII armour as Armored,
// anything else as binary Data; false for an empty file or one over
// api.MaxPGPKeyBytes (clients read at most MaxPGPKeyBytes + 1 bytes and
// show ImportFailedText(api.PGPImportTooLarge)).
func FileParams(data []byte) (api.PGPKeyImportParams, bool) {
	if len(data) == 0 || len(data) > api.MaxPGPKeyBytes {
		return api.PGPKeyImportParams{}, false
	}
	if t := strings.TrimSpace(string(data)); strings.HasPrefix(t, "-----BEGIN PGP") && utf8.ValidString(t) {
		return api.PGPKeyImportParams{Armored: t}, true
	}
	return api.PGPKeyImportParams{Data: data}, true
}

// ---------------------------------------------------------------------------
// Preferences → Security: the stored keys
// ---------------------------------------------------------------------------

// KeyRow is a row of the OpenPGP key list.
type KeyRow struct {
	// Fingerprint is the key's, raw: what pgp.keyVerify and pgp.keyRemove
	// take.
	Fingerprint string
	// Title is the first user ID (or address, or the fingerprint);
	// Subtitle the formatted fingerprint (monospace); Origin where and
	// when it came from.
	Title, Subtitle, Origin string
	// TrustPill is "Confirmed" or "Not confirmed"; Verified styles it as
	// success. StatePill is "Revoked", "Expired" or "".
	TrustPill string
	Verified  bool
	StatePill string
	// Actions are the row's menu: Confirm Key… or Remove Confirmation,
	// then Remove Key….
	Actions []Button
}

// KeyRows are the rows of pgp.keyList's keys, sorted by title (ignoring
// case), then fingerprint. now decides what has expired; date formats
// the day a key was first seen.
func KeyRows(keys []api.PGPKey, now time.Time, date func(time.Time) string, tr Translator) []KeyRow {
	rows := make([]KeyRow, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, KeyRowFor(k, now, date, tr))
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := strings.ToLower(rows[i].Title), strings.ToLower(rows[j].Title)
		if a != b {
			return a < b
		}
		return rows[i].Fingerprint < rows[j].Fingerprint
	})
	return rows
}

// KeyRowFor is the row of one key.
func KeyRowFor(k api.PGPKey, now time.Time, date func(time.Time) string, tr Translator) KeyRow {
	fp := FormatFingerprint(k.Fingerprint)
	title := ""
	if ids := cleanList(k.UserIDs, 1); len(ids) > 0 {
		title = ids[0]
	} else if es := cleanList(k.Emails, 1); len(es) > 0 {
		title = es[0]
	} else {
		title = fp
	}
	revoked := k.RevokedAt != nil && !k.RevokedAt.After(now)
	expired := k.ExpiresAt != nil && !k.ExpiresAt.After(now)
	r := KeyRow{
		Fingerprint: k.Fingerprint,
		Title:       title,
		Subtitle:    fp,
		Origin:      keyOrigin(k.Source, date(k.FirstSeen), tr),
		TrustPill:   keyTrustPill(k.Trust, tr),
		Verified:    k.Trust == api.PGPKeyVerified,
		StatePill:   keyStatePill(revoked, expired, tr),
	}
	switch {
	case r.Verified:
		r.Actions = append(r.Actions, Button{Action: ActionUnconfirmKey, Label: buttonLabel(ActionUnconfirmKey, false, false, tr), Enabled: true})
	case !revoked:
		r.Actions = append(r.Actions, Button{Action: ActionConfirmKey, Label: buttonLabel(ActionConfirmKey, false, false, tr), Enabled: true})
	}
	r.Actions = append(r.Actions, Button{Action: ActionRemoveKey, Label: buttonLabel(ActionRemoveKey, false, false, tr), Enabled: true})
	return r
}

// ---------------------------------------------------------------------------
// Text helpers
// ---------------------------------------------------------------------------

// FormatFingerprint is a fingerprint or key ID in upper-case groups of
// four ("AB12 CD34 …"); "" when hex is not 16, 40 or 64 hex digits.
func FormatFingerprint(hex string) string {
	h := strings.ToUpper(strings.TrimSpace(hex))
	switch len(h) {
	case 16, 40, 64:
	default:
		return ""
	}
	for _, r := range h {
		if !(r >= '0' && r <= '9' || r >= 'A' && r <= 'F') {
			return ""
		}
	}
	var b strings.Builder
	for i := 0; i < len(h); i += 4 {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(h[i : i+4])
	}
	return b.String()
}

// clean is s on one line, without control, format (bidi, zero-width) and
// private-use characters, trimmed and capped at api.MaxSignatureTextBytes
// on a rune boundary: the daemon cleaned it already, this is the client's
// own guard.
func clean(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == utf8.RuneError, unicode.Is(unicode.Co, r), unicode.Is(unicode.Cf, r):
			continue
		case unicode.IsControl(r), r == ' ', r == ' ':
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(r)
	}
	t := strings.Join(strings.Fields(b.String()), " ")
	if len(t) > api.MaxSignatureTextBytes {
		cut := api.MaxSignatureTextBytes
		for cut > 0 && !utf8.RuneStart(t[cut]) {
			cut--
		}
		t = strings.TrimSpace(t[:cut])
	}
	return t
}

// cleanList is every non-empty cleaned entry, at most limit.
func cleanList(list []string, limit int) []string {
	var out []string
	for _, s := range list {
		if len(out) == limit {
			break
		}
		if c := clean(s); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// hasNote reports n among notes.
func hasNote(notes []api.SignatureNote, n api.SignatureNote) bool {
	for _, x := range notes {
		if x == n {
			return true
		}
	}
	return false
}
