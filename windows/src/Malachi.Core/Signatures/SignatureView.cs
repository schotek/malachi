// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/signature/signature.go (KindName, SeverityOf, IconOf,
// StyleOf, ListIconFor, BadgeFor, SignerLabel, HidesPart, TileAttachments,
// DetailsFor, sectionOf, certificateSection, signedHeaderRows,
// CanCheckRevocation, ImportParams, ConfirmParams, RevocationFailureOf,
// ImportFailureOf, PastedParams, FileParams, KeyRows, KeyRowFor,
// FormatFingerprint, clean, cleanList, hasNote); the macOS port is
// macos/Sources/MalachiCore/Signatures/ (written at the same time).
//
// The view logic of signed mail (docs/plans/signatures.md, phase 1:
// verifying S/MIME and OpenPGP signatures of incoming mail): the small icon
// of a message row, the badge in a message's header (reading pane, message
// window, conversation card), the details dialog with its buttons (Check
// Revocation, Import Key, Confirm Key…), which attachment tiles a verdict
// hides, and Preferences → Security with the stored OpenPGP keys.
//
// The daemon verifies and decides the verdict (message.signature, pgp.*,
// docs/api.md §4.22); this only turns its API values into texts and small
// view models. Every string of a certificate or key (names, addresses,
// distinguished names, user IDs, signed header values) is hostile input: the
// daemon cleans it, this cleans it again, and the client shows it as plain
// text only (TextBlock.Text).
//
// Windows: Go's "(value, ok)" results are nullable values; Go's Translator
// is L10n. Go's icon names are Adwaita's; IconGlyphs maps them to glyphs.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Text;
using System.Text.Json;
using Malachi.Core.Api;
using Malachi.Core.Contacts;
using Malachi.Core.Model;
using Malachi.Core.Text;

namespace Malachi.Core.Signatures;

/// <summary>ui/internal/signature (signature.go): the view logic of signed mail.</summary>
public static class SignatureView
{
    /// <summary>S/MIME: a name, never translated.</summary>
    public const string SmimeName = "S/MIME";

    /// <summary>OpenPGP: a name, never translated.</summary>
    public const string OpenPgpName = "OpenPGP";

    /// <summary>The placeholder of the import dialog's text area (not translated).</summary>
    public const string ArmorPlaceholder = "-----BEGIN PGP PUBLIC KEY BLOCK-----";

    /// <summary>signature.IconGood: a full shield for a valid signature.</summary>
    public const string IconGood = "security-high-symbolic";

    /// <summary>signature.IconGoodSeen: a half shield for a key not confirmed.</summary>
    public const string IconGoodSeen = "security-medium-symbolic";

    /// <summary>signature.IconWarning.</summary>
    public const string IconWarning = "dialog-warning-symbolic";

    /// <summary>signature.IconBad.</summary>
    public const string IconBad = "dialog-error-symbolic";

    /// <summary>signature.IconNeutral: an empty shield for a signature that was not judged.</summary>
    public const string IconNeutral = "security-low-symbolic";

    /// <summary>signature.IconPage: the icon of Preferences → Security.</summary>
    public const string IconPage = "security-high-symbolic";

    /// <summary>signature.StyleGood: libadwaita's style class (Windows: SystemFillColorSuccess).</summary>
    public const string StyleGood = "success";

    /// <summary>signature.StyleWarning (Windows: SystemFillColorCaution).</summary>
    public const string StyleWarning = "warning";

    /// <summary>signature.StyleBad (Windows: SystemFillColorCritical).</summary>
    public const string StyleBad = "error";

    /// <summary>signature.StyleNeutral (Windows: TextFillColorSecondary).</summary>
    public const string StyleNeutral = "dim-label";

    /// <summary>signature.KindName: the name of a signature format ("" unknown).</summary>
    public static string KindName(SignatureKind kind) => kind.Value switch
    {
        SignatureKind.Smime => SmimeName,
        SignatureKind.Pgp => OpenPgpName,
        _ => "",
    };

    // ---------------------------------------------------------------------
    // Severity, icon and colour
    // ---------------------------------------------------------------------

    /// <summary>signature.SeverityOf: the severity of a status; an unknown one reads as error (neutral).</summary>
    public static SignatureSeverity SeverityOf(SignatureStatus status) => status.Value switch
    {
        SignatureStatus.Valid => SignatureSeverity.Good,
        SignatureStatus.ValidSeen => SignatureSeverity.GoodSeen,
        SignatureStatus.Mismatch or SignatureStatus.Untrusted or SignatureStatus.Weak => SignatureSeverity.Warning,
        SignatureStatus.Invalid or SignatureStatus.Revoked => SignatureSeverity.Bad,
        _ => SignatureSeverity.Neutral,
    };

    /// <summary>signature.IconOf: the icon of a severity ("" for none).</summary>
    public static string IconOf(SignatureSeverity severity) => severity switch
    {
        SignatureSeverity.Good => IconGood,
        SignatureSeverity.GoodSeen => IconGoodSeen,
        SignatureSeverity.Warning => IconWarning,
        SignatureSeverity.Bad => IconBad,
        SignatureSeverity.Neutral => IconNeutral,
        _ => "",
    };

    /// <summary>signature.StyleOf: the style class of a severity ("" for none).</summary>
    public static string StyleOf(SignatureSeverity severity) => severity switch
    {
        SignatureSeverity.Good or SignatureSeverity.GoodSeen => StyleGood,
        SignatureSeverity.Warning => StyleWarning,
        SignatureSeverity.Bad => StyleBad,
        SignatureSeverity.Neutral => StyleNeutral,
        _ => "",
    };

    // ---------------------------------------------------------------------
    // The message list
    // ---------------------------------------------------------------------

    /// <summary>
    /// signature.ListIconFor: the icon of a row's verdict: only valid,
    /// validSeen, invalid, revoked and mismatch get one (plan §6); the grey
    /// verdicts, untrusted and weak show nothing in the list, only in the
    /// header. A collapsed thread row passes its latest member's signature.
    /// Null for none.
    /// </summary>
    public static SignatureListIcon? ListIconFor(MessageSignature? signature)
    {
        if (signature is null)
        {
            return null;
        }
        switch (signature.Status.Value)
        {
            case SignatureStatus.Valid or SignatureStatus.ValidSeen or SignatureStatus.Invalid
                or SignatureStatus.Revoked or SignatureStatus.Mismatch:
                var severity = SeverityOf(signature.Status);
                return new SignatureListIcon(IconOf(severity), StyleOf(severity), SignatureText.StatusTitle(signature.Kind, signature.Status));
            default:
                return null;
        }
    }

    // ---------------------------------------------------------------------
    // The header's badge
    // ---------------------------------------------------------------------

    /// <summary>
    /// signature.BadgeFor: the badge of <c>message.get</c>'s signature (null:
    /// no badge). The reading pane, the message window and a conversation
    /// card show it; a chat bubble and an attached message
    /// (<c>message.embedded</c>) do not.
    /// </summary>
    public static SignatureBadge BadgeFor(SignatureSummary? s)
    {
        if (s is null)
        {
            return new SignatureBadge();
        }
        var severity = SeverityOf(s.Status);
        return new SignatureBadge
        {
            Visible = true,
            Severity = severity,
            Icon = IconOf(severity),
            Style = StyleOf(severity),
            Text = SignatureText.BadgeText(s, SignerLabel(s.Signer)),
            Tooltip = SignatureText.BadgeTooltip(),
        };
    }

    /// <summary>signature.SignerLabel: "Name &lt;address&gt;", the address alone or the name alone ("" for none), cleaned.</summary>
    public static string SignerLabel(SignatureSigner? s)
    {
        if (s is null)
        {
            return "";
        }
        var name = Clean(s.Name);
        var email = Clean(s.Email);
        if (name.Length > 0 && email.Length > 0 && !CodePoints.EqualFold(name, email))
        {
            return name + " <" + email + ">";
        }
        return email.Length > 0 ? email : name;
    }

    // ---------------------------------------------------------------------
    // Attachment tiles (plan S15)
    // ---------------------------------------------------------------------

    /// <summary>
    /// signature.HidesPart: the attachment's tile is not shown: it is the
    /// detached signature part of a message with a verdict. A message without
    /// a verdict (<paramref name="signature"/> null) shows its signature parts
    /// like any file.
    /// </summary>
    public static bool HidesPart(Attachment a, SignatureSummary? signature)
    {
        ArgumentNullException.ThrowIfNull(a);
        return signature is { PartId: { Length: > 0 } part } && string.Equals(a.PartId, part, StringComparison.Ordinal);
    }

    /// <summary>
    /// signature.TileAttachments: the attachments without the part
    /// <see cref="HidesPart"/> hides; the other rules of the tiles (inline
    /// pictures, the invitation's calendar part) apply as before.
    /// </summary>
    public static IReadOnlyList<Attachment> TileAttachments(IReadOnlyList<Attachment> attachments, SignatureSummary? signature)
    {
        ArgumentNullException.ThrowIfNull(attachments);
        return [.. attachments.Where(a => !HidesPart(a, signature))];
    }

    // ---------------------------------------------------------------------
    // The details dialog
    // ---------------------------------------------------------------------

    /// <summary>
    /// signature.DetailsFor: the dialog of <c>message.signature</c>'s details
    /// (null: an empty model, nothing to show). <paramref name="from"/> is the
    /// message's From address, <paramref name="date"/> formats a date and time
    /// like the client's message headers, and <paramref name="busy"/> is the
    /// action that runs (its button says so and is disabled).
    /// </summary>
    public static SignatureDetailsView DetailsFor(SignatureDetails? d, string from, SignatureAction busy, Func<DateTimeOffset, string> date)
    {
        ArgumentNullException.ThrowIfNull(date);
        if (d is null)
        {
            return new SignatureDetailsView();
        }
        var severity = SeverityOf(d.Status);
        var rows = new List<SignatureRow>();
        void Add(SignatureRowKey key, string value, bool mono = false)
        {
            if (value.Length > 0)
            {
                rows.Add(new SignatureRow(key, SignatureText.RowLabel(key), value, mono));
            }
        }

        Add(SignatureRowKey.Signer, SignerLabel(d.Signer));
        Add(SignatureRowKey.Kind, KindName(d.Kind));
        Add(SignatureRowKey.Reason, SignatureText.ReasonText(d.Kind, d.Reason));
        if (d.SignedAt is { } signedAt)
        {
            Add(SignatureRowKey.SignedAt, date(signedAt));
        }
        if (d.ArrivedAt != default)
        {
            Add(SignatureRowKey.ArrivedAt, date(d.ArrivedAt));
        }
        Add(SignatureRowKey.Algorithm, Clean(d.Algorithm));
        var smime = d.Kind.Value == SignatureKind.Smime ? d.Smime : null;
        if (smime is not null)
        {
            Add(SignatureRowKey.Revocation, SignatureText.RevocationText(smime.Revocation, date));
        }
        if (d.Kind.Value == SignatureKind.Pgp && d.Pgp is { } p)
        {
            Add(SignatureRowKey.KeyTrust, SignatureText.KeyTrustText(p.KeyTrust));
            var fp = p.PrimaryFingerprint ?? "";
            if (fp.Length == 0 && d.Signer?.Fingerprint is { } signerFp)
            {
                fp = signerFp;
            }
            Add(SignatureRowKey.KeyFingerprint, FormatFingerprint(fp), mono: true);
            if (FormatFingerprint(fp).Length == 0)
            {
                Add(SignatureRowKey.KeyId, FormatFingerprint(p.KeyId), mono: true);
            }
            Add(SignatureRowKey.KeySource, SignatureText.KeySourceText(p.KeySource));
            if (p.FirstSeen is { } firstSeen)
            {
                Add(SignatureRowKey.FirstSeen, date(firstSeen));
            }
            Add(SignatureRowKey.UserIds, string.Join('\n', CleanList(p.UserIds, API.Limits.MaxSignatureUserIDs)));
            Add(SignatureRowKey.PreviousKey, FormatFingerprint(p.PreviousFingerprint), mono: true);
        }
        if (d.ExpiredAt is { } expiredAt)
        {
            Add(SignatureRowKey.Expired, date(expiredAt));
        }
        if (d.RevokedAt is { } revokedAt && d.Kind.Value == SignatureKind.Pgp)
        {
            Add(SignatureRowKey.Revoked, date(revokedAt));
        }

        var notes = new List<string>();
        foreach (var n in d.Notes)
        {
            if (n.Value is SignatureNote.RevocationUnchecked or SignatureNote.RevocationGood)
            {
                continue;
            }
            if (SignatureText.NoteText(d.Kind, n) is { Length: > 0 } text)
            {
                notes.Add(text);
            }
        }

        var sections = new List<SignatureSection>();
        if (smime is not null)
        {
            IReadOnlyList<SignatureCertificate> chain =
                smime.Chain.Count > API.Limits.MaxSignatureChain ? smime.Chain.Take(API.Limits.MaxSignatureChain).ToList() : smime.Chain;
            for (var i = 0; i < chain.Count; i++)
            {
                sections.Add(CertificateSection(chain[i], SectionOf(i, chain.Count, chain[i]), date));
            }
        }
        var headers = SignedHeaderRows(d.ProtectedHeaders);
        if (headers.Count > 0)
        {
            sections.Add(new SignatureSection(SignatureSectionKey.SignedHeaders, SignatureText.SectionTitle(SignatureSectionKey.SignedHeaders), headers));
        }

        var buttons = new List<SignatureButton>();
        void Button(SignatureAction action, bool again)
        {
            buttons.Add(new SignatureButton(action, SignatureText.ButtonLabel(action, again, busy == action), busy != action));
        }
        var caption = "";
        if (CanCheckRevocation(d))
        {
            Button(SignatureAction.CheckRevocation, smime!.Revocation.State.Value != RevocationState.Unchecked);
            caption = SignatureText.RevocationCaption();
        }
        if (ImportParams(d) is not null)
        {
            Button(SignatureAction.ImportKey, false);
        }
        var keyFingerprint = "";
        if (ConfirmParams(d) is { } confirm)
        {
            Button(SignatureAction.ConfirmKey, false);
            keyFingerprint = FormatFingerprint(confirm.Fingerprint);
        }

        return new SignatureDetailsView
        {
            Severity = severity,
            Icon = IconOf(severity),
            Style = StyleOf(severity),
            Title = SignatureText.StatusTitle(d.Kind, d.Status),
            Explanation = SignatureText.Explanation(d, from),
            Rows = rows,
            Notes = notes,
            Sections = sections,
            Caveat = (d.Status.Value is SignatureStatus.Valid or SignatureStatus.ValidSeen) ? SignatureText.Caveat() : "",
            Buttons = buttons,
            RevocationCaption = caption,
            KeyFingerprint = keyFingerprint,
        };
    }

    // signature.sectionOf: the section of the i-th of n certificates: the
    // signer's first, a root last (a system root, or self-signed after the
    // signer's), intermediates between.
    private static SignatureSectionKey SectionOf(int i, int n, SignatureCertificate c)
    {
        if (i == 0)
        {
            return SignatureSectionKey.SignerCertificate;
        }
        return i == n - 1 && (c.SystemRoot || c.SelfSigned) ? SignatureSectionKey.Root : SignatureSectionKey.Intermediate;
    }

    private static SignatureSection CertificateSection(SignatureCertificate c, SignatureSectionKey key, Func<DateTimeOffset, string> date)
    {
        var rows = new List<SignatureRow>();
        void Add(SignatureRowKey rk, string value, bool mono = false)
        {
            if (value.Length > 0)
            {
                rows.Add(new SignatureRow(rk, SignatureText.RowLabel(rk), value, mono));
            }
        }
        Add(SignatureRowKey.IssuedTo, Clean(c.Subject));
        Add(SignatureRowKey.Emails, string.Join('\n', CleanList(c.Emails, API.Limits.MaxSignatureEmails)));
        Add(SignatureRowKey.IssuedBy, Clean(c.Issuer));
        if (c.NotBefore != default && c.NotAfter != default)
        {
            Add(SignatureRowKey.Validity, SignatureText.Validity(date(c.NotBefore), date(c.NotAfter)));
        }
        Add(SignatureRowKey.CertFingerprint, FormatFingerprint(c.Sha256), mono: true);
        return new SignatureSection(key, SignatureText.SectionTitle(key), rows);
    }

    private static List<SignatureRow> SignedHeaderRows(IReadOnlyList<ProtectedHeader> headers)
    {
        var rows = new List<SignatureRow>();
        foreach (var h in headers)
        {
            if (rows.Count == API.Limits.MaxProtectedHeaders)
            {
                break;
            }
            var label = SignatureText.HeaderName(h.Name);
            if (label.Length == 0)
            {
                continue;
            }
            rows.Add(new SignatureRow(SignatureRowKey.SignedHeader, label, Clean(h.Value)));
        }
        return rows;
    }

    /// <summary>
    /// signature.CanCheckRevocation: Check Revocation is offered: an S/MIME
    /// verdict whose signer's certificate names an OCSP responder or CRL the
    /// daemon may ask.
    /// </summary>
    public static bool CanCheckRevocation(SignatureDetails? d) =>
        d is { Kind.Value: SignatureKind.Smime, Smime: { Chain.Count: > 0, Revocation.Checkable: true } };

    /// <summary>
    /// signature.ImportParams: the params of Import Key: the key the message
    /// offers (<see cref="PgpDetails.Importable"/>), from its part or its
    /// Autocrypt header; null when there is none.
    /// </summary>
    public static PgpKeyImportParams? ImportParams(SignatureDetails? d)
    {
        if (d is not { Kind.Value: SignatureKind.Pgp, Pgp.Importable: { } offer } || d.MessageId.Value.Length == 0)
        {
            return null;
        }
        var hasPart = !string.IsNullOrEmpty(offer.PartId);
        if (hasPart && !offer.Autocrypt)
        {
            return new PgpKeyImportParams { MessageId = d.MessageId, PartId = offer.PartId };
        }
        if (!hasPart && offer.Autocrypt)
        {
            return new PgpKeyImportParams { MessageId = d.MessageId, Autocrypt = true };
        }
        return null;
    }

    /// <summary>
    /// signature.ConfirmParams: the params of Confirm Key…: a stored key that
    /// is only seen, of a verdict that is not invalid or revoked; null when
    /// there is none.
    /// </summary>
    public static PgpKeyVerifyParams? ConfirmParams(SignatureDetails? d)
    {
        if (d is not { Kind.Value: SignatureKind.Pgp, Pgp: { } p }
            || p.KeyTrust.Value != PgpKeyTrust.Seen || FormatFingerprint(p.PrimaryFingerprint).Length == 0)
        {
            return null;
        }
        if (d.Status.Value is SignatureStatus.Invalid or SignatureStatus.Revoked)
        {
            return null;
        }
        return new PgpKeyVerifyParams { Fingerprint = p.PrimaryFingerprint!, Verified = true };
    }

    // ---------------------------------------------------------------------
    // Errors of the methods
    // ---------------------------------------------------------------------

    /// <summary>
    /// signature.RevocationFailureOf: the class of a
    /// <c>message.signatureRevocation</c> error (null for another error or
    /// data without a class); <see cref="SignatureText.RevocationFailedText"/>
    /// turns it into the sentence.
    /// </summary>
    public static RevocationFailure? RevocationFailureOf(Exception? error) => RevocationFailureOf(RpcErrorText.DaemonError(error));

    /// <summary><see cref="RevocationFailureOf(Exception?)"/> of the daemon's error object.</summary>
    public static RevocationFailure? RevocationFailureOf(RpcError? e)
    {
        if (e is null || e.Code.Value != ErrorCode.RevocationFailed || Data(e, ApiJsonContext.Wire.RevocationFailedData) is not { } d)
        {
            return null;
        }
        return string.IsNullOrEmpty(d.Reason.Value) ? (RevocationFailure?)null : d.Reason;
    }

    /// <summary>
    /// signature.ImportFailureOf: the class of a <c>pgp.keyImport</c> error
    /// (null for another error); <see cref="SignatureText.ImportFailedText"/>
    /// turns it into the sentence.
    /// </summary>
    public static PgpImportFailure? ImportFailureOf(Exception? error) => ImportFailureOf(RpcErrorText.DaemonError(error));

    /// <summary><see cref="ImportFailureOf(Exception?)"/> of the daemon's error object.</summary>
    public static PgpImportFailure? ImportFailureOf(RpcError? e)
    {
        if (e is null || e.Code.Value != ErrorCode.InvalidArgument || Data(e, ApiJsonContext.Wire.PgpImportErrorData) is not { } d)
        {
            return null;
        }
        return string.IsNullOrEmpty(d.Reason.Value) ? (PgpImportFailure?)null : d.Reason;
    }

    // An error's data as T; null without data or with data of another shape.
    private static T? Data<T>(RpcError e, System.Text.Json.Serialization.Metadata.JsonTypeInfo<T> info)
        where T : class
    {
        if (e.Data is not { ValueKind: JsonValueKind.Object } data)
        {
            return null;
        }
        try
        {
            return data.Deserialize(info);
        }
        catch (JsonException)
        {
            return null;
        }
    }

    // ---------------------------------------------------------------------
    // Import
    // ---------------------------------------------------------------------

    /// <summary>
    /// signature.PastedParams: the params of a key pasted in the import
    /// dialog; null when there is nothing to import or it is over
    /// <see cref="API.Limits.MaxPGPKeyBytes"/> (the Import button is disabled
    /// then).
    /// </summary>
    public static PgpKeyImportParams? PastedParams(string? text)
    {
        var t = ContactBook.TrimSpace(text);
        if (t.Length == 0 || Encoding.UTF8.GetByteCount(t) > API.Limits.MaxPGPKeyBytes)
        {
            return null;
        }
        return new PgpKeyImportParams { Armored = t };
    }

    /// <summary>
    /// signature.FileParams: the params of a key file: ASCII armour as
    /// <see cref="PgpKeyImportParams.Armored"/>, anything else as binary
    /// <see cref="PgpKeyImportParams.Data"/>; null for an empty file or one
    /// over <see cref="API.Limits.MaxPGPKeyBytes"/> (clients read at most
    /// MaxPGPKeyBytes + 1 bytes and show
    /// <c>ImportFailedText(PgpImportFailure.TooLarge)</c>).
    /// </summary>
    public static PgpKeyImportParams? FileParams(byte[]? data)
    {
        if (data is null || data.Length == 0 || data.Length > API.Limits.MaxPGPKeyBytes)
        {
            return null;
        }
        string? text;
        try
        {
            text = new UTF8Encoding(encoderShouldEmitUTF8Identifier: false, throwOnInvalidBytes: true).GetString(data);
        }
        catch (DecoderFallbackException)
        {
            text = null;
        }
        if (text is not null && ContactBook.TrimSpace(text) is var t && t.StartsWith("-----BEGIN PGP", StringComparison.Ordinal))
        {
            return new PgpKeyImportParams { Armored = t };
        }
        return new PgpKeyImportParams { Data = data };
    }

    // ---------------------------------------------------------------------
    // Preferences → Security: the stored keys
    // ---------------------------------------------------------------------

    /// <summary>
    /// signature.KeyRows: the rows of <c>pgp.keyList</c>'s keys, sorted by
    /// title (ignoring case), then fingerprint. <paramref name="now"/> decides
    /// what has expired; <paramref name="date"/> formats the day a key was
    /// first seen.
    /// </summary>
    public static IReadOnlyList<SignatureKeyRow> KeyRows(IReadOnlyList<PgpKey> keys, DateTimeOffset now, Func<DateTimeOffset, string> date)
    {
        ArgumentNullException.ThrowIfNull(keys);
        var rows = keys.Select(k => KeyRowFor(k, now, date)).ToList();
        // Stable, as Go's sort.SliceStable.
        return [.. rows
            .Select((row, index) => (row, index))
            .OrderBy(x => x.row, KeyRowComparer.Instance)
            .ThenBy(x => x.index)
            .Select(x => x.row)];
    }

    /// <summary>signature.KeyRowFor: the row of one key.</summary>
    public static SignatureKeyRow KeyRowFor(PgpKey k, DateTimeOffset now, Func<DateTimeOffset, string> date)
    {
        ArgumentNullException.ThrowIfNull(k);
        ArgumentNullException.ThrowIfNull(date);
        var fp = FormatFingerprint(k.Fingerprint);
        string title;
        if (CleanList(k.UserIds, 1) is { Count: > 0 } ids)
        {
            title = ids[0];
        }
        else if (CleanList(k.Emails, 1) is { Count: > 0 } emails)
        {
            title = emails[0];
        }
        else
        {
            title = fp;
        }
        var revoked = k.RevokedAt is { } r && r <= now;
        var expired = k.ExpiresAt is { } x && x <= now;
        var verified = k.Trust.Value == PgpKeyTrust.Verified;
        var actions = new List<SignatureButton>();
        if (verified)
        {
            actions.Add(new SignatureButton(SignatureAction.UnconfirmKey, SignatureText.ButtonLabel(SignatureAction.UnconfirmKey, false, false), true));
        }
        else if (!revoked)
        {
            actions.Add(new SignatureButton(SignatureAction.ConfirmKey, SignatureText.ButtonLabel(SignatureAction.ConfirmKey, false, false), true));
        }
        actions.Add(new SignatureButton(SignatureAction.RemoveKey, SignatureText.ButtonLabel(SignatureAction.RemoveKey, false, false), true));
        return new SignatureKeyRow(
            k.Fingerprint,
            title,
            fp,
            SignatureText.KeyOrigin(k.Source, date(k.FirstSeen)),
            SignatureText.KeyTrustPill(k.Trust),
            verified,
            SignatureText.KeyStatePill(revoked, expired),
            actions);
    }

    // ---------------------------------------------------------------------
    // Text helpers
    // ---------------------------------------------------------------------

    /// <summary>
    /// signature.FormatFingerprint: a fingerprint or key ID in upper-case
    /// groups of four ("AB12 CD34 …"); "" when it is not 16, 40 or 64 hex digits.
    /// </summary>
    public static string FormatFingerprint(string? hex)
    {
        var h = ContactBook.TrimSpace(hex).ToUpperInvariant();
        if (h.Length is not (16 or 40 or 64))
        {
            return "";
        }
        foreach (var c in h)
        {
            if (!(c is >= '0' and <= '9' or >= 'A' and <= 'F'))
            {
                return "";
            }
        }
        var b = new StringBuilder(h.Length + (h.Length / 4));
        for (var i = 0; i < h.Length; i += 4)
        {
            if (i > 0)
            {
                b.Append(' ');
            }
            b.Append(h, i, 4);
        }
        return b.ToString();
    }

    /// <summary>
    /// signature.clean: <paramref name="s"/> on one line, without control,
    /// format (bidi, zero-width) and private-use characters, trimmed and
    /// capped at <see cref="API.Limits.MaxSignatureTextBytes"/> on a
    /// character boundary: the daemon cleaned it already, this is the
    /// client's own guard. A lone surrogate reads as U+FFFD and is dropped,
    /// as Go drops utf8.RuneError.
    /// </summary>
    public static string Clean(string? s)
    {
        var b = new StringBuilder((s ?? "").Length);
        foreach (var r in (s ?? "").EnumerateRunes())
        {
            var category = Rune.GetUnicodeCategory(r);
            if (r == Rune.ReplacementChar || category is UnicodeCategory.PrivateUse or UnicodeCategory.Format)
            {
                continue;
            }
            if (category == UnicodeCategory.Control || r.Value is 0x2028 or 0x2029)
            {
                b.Append(' ');
                continue;
            }
            b.Append(r.ToString());
        }
        var t = string.Join(' ', ContactBook.Fields(b.ToString()));
        if (Encoding.UTF8.GetByteCount(t) <= API.Limits.MaxSignatureTextBytes)
        {
            return t;
        }
        var cut = new StringBuilder();
        var bytes = 0;
        foreach (var r in t.EnumerateRunes())
        {
            if (bytes + r.Utf8SequenceLength > API.Limits.MaxSignatureTextBytes)
            {
                break;
            }
            bytes += r.Utf8SequenceLength;
            cut.Append(r.ToString());
        }
        return ContactBook.TrimSpace(cut.ToString());
    }

    // signature.cleanList: every non-empty cleaned entry, at most limit.
    private static List<string> CleanList(IReadOnlyList<string> list, int limit)
    {
        var output = new List<string>();
        foreach (var s in list)
        {
            if (output.Count == limit)
            {
                break;
            }
            if (Clean(s) is { Length: > 0 } c)
            {
                output.Add(c);
            }
        }
        return output;
    }

    // signature.hasNote: n among notes.
    internal static bool HasNote(IReadOnlyList<SignatureNote> notes, string n) => notes.Any(x => x.Value == n);

    // Orders key rows by title ignoring case, then fingerprint, as Go's
    // strings.ToLower and < do (code points).
    private sealed class KeyRowComparer : IComparer<SignatureKeyRow>
    {
        public static KeyRowComparer Instance { get; } = new();

        public int Compare(SignatureKeyRow? x, SignatureKeyRow? y)
        {
            ArgumentNullException.ThrowIfNull(x);
            ArgumentNullException.ThrowIfNull(y);
            var c = CodePoints.Compare(CodePoints.ToLower(x.Title), CodePoints.ToLower(y.Title));
            return c != 0 ? c : CodePoints.Compare(x.Fingerprint, y.Fingerprint);
        }
    }
}
