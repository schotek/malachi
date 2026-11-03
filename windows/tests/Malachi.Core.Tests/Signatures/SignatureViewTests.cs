// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/signature/signature_test.go (TestSeverityIconStyle,
// TestListIcon, TestSignerLabel, TestCleanCapsOnRuneBoundary, TestBadge,
// TestTitlesAndExplanationsCoverEveryStatus, TestNotesAndReasons, TestTiles,
// TestFormatFingerprint, TestDetailsSMIME, TestRevocationValues,
// TestDetailsPGP, TestErrors, TestImportParamsAndText, TestKeyRows,
// TestQuestionsAndPage). The process-wide catalogue is English here, so
// every msgid is its own translation; the msgids themselves are checked
// against po/malachi.pot by Malachi.Conventions.Tests (StringsCheckTests).

using System;
using System.Globalization;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.Signatures;
using Malachi.Core.Tests.Api;
using Malachi.Core.Transport;
using Xunit;

namespace Malachi.Core.Tests.Signatures;

public sealed class SignatureViewTests
{
    private static readonly DateTimeOffset Arrived = new(2026, 11, 2, 8, 0, 0, TimeSpan.Zero);
    private static readonly string Fp40 = "0123456789abcdef0123456789abcdef01234567";
    private static readonly string Fp64 = "ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34";

    private static readonly string[] AllStatuses =
    [
        SignatureStatus.Invalid, SignatureStatus.Revoked, SignatureStatus.Mismatch, SignatureStatus.Untrusted,
        SignatureStatus.UnknownKey, SignatureStatus.Weak, SignatureStatus.Unsupported, SignatureStatus.Error,
        SignatureStatus.Valid, SignatureStatus.ValidSeen,
    ];

    private static string Day(DateTimeOffset t) => t.UtcDateTime.ToString("yyyy-MM-dd", CultureInfo.InvariantCulture);

    private static Attachment Att(string part, string name = "file", string type = "application/octet-stream") =>
        new() { PartId = part, Filename = name, ContentType = type, Size = 1, Inline = false };

    private static SignatureRowKey[] Keys(System.Collections.Generic.IEnumerable<SignatureRow> rows) => [.. rows.Select(r => r.Key)];

    [Fact]
    public void SeverityIconStyle()
    {
        (string Status, SignatureSeverity Severity, string Icon, string Style)[] cases =
        [
            (SignatureStatus.Valid, SignatureSeverity.Good, "security-high-symbolic", "success"),
            (SignatureStatus.ValidSeen, SignatureSeverity.GoodSeen, "security-medium-symbolic", "success"),
            (SignatureStatus.Mismatch, SignatureSeverity.Warning, "dialog-warning-symbolic", "warning"),
            (SignatureStatus.Untrusted, SignatureSeverity.Warning, "dialog-warning-symbolic", "warning"),
            (SignatureStatus.Weak, SignatureSeverity.Warning, "dialog-warning-symbolic", "warning"),
            (SignatureStatus.Invalid, SignatureSeverity.Bad, "dialog-error-symbolic", "error"),
            (SignatureStatus.Revoked, SignatureSeverity.Bad, "dialog-error-symbolic", "error"),
            (SignatureStatus.UnknownKey, SignatureSeverity.Neutral, "security-low-symbolic", "dim-label"),
            (SignatureStatus.Unsupported, SignatureSeverity.Neutral, "security-low-symbolic", "dim-label"),
            (SignatureStatus.Error, SignatureSeverity.Neutral, "security-low-symbolic", "dim-label"),
            ("somethingNew", SignatureSeverity.Neutral, "security-low-symbolic", "dim-label"),
        ];
        foreach (var (status, severity, icon, style) in cases)
        {
            var got = SignatureView.SeverityOf(status);
            Assert.Equal((severity, icon, style), (got, SignatureView.IconOf(got), SignatureView.StyleOf(got)));
        }
        Assert.Equal("", SignatureView.IconOf(SignatureSeverity.None));
        Assert.Equal("", SignatureView.StyleOf(SignatureSeverity.None));
        Assert.Equal("security-high-symbolic", SignatureView.IconPage);
    }

    [Fact]
    public void ListIcon()
    {
        Assert.Null(SignatureView.ListIconFor(null));
        string[] shown = [SignatureStatus.Valid, SignatureStatus.ValidSeen, SignatureStatus.Invalid, SignatureStatus.Revoked, SignatureStatus.Mismatch];
        foreach (var status in AllStatuses.Append("somethingNew"))
        {
            var icon = SignatureView.ListIconFor(new MessageSignature { Kind = SignatureKind.Smime, Status = status });
            Assert.Equal(shown.Contains(status), icon is not null);
            if (icon is not null)
            {
                var severity = SignatureView.SeverityOf(status);
                Assert.Equal(
                    (SignatureView.IconOf(severity), SignatureView.StyleOf(severity), SignatureText.StatusTitle(SignatureKind.Smime, status)),
                    (icon.Icon, icon.Style, icon.Tooltip));
            }
        }
    }

    [Fact]
    public void SignerLabel()
    {
        Assert.Equal("", SignatureView.SignerLabel(null));
        Assert.Equal("", SignatureView.SignerLabel(new SignatureSigner()));
        Assert.Equal("Jan Novák <jan@example.org>", SignatureView.SignerLabel(new SignatureSigner { Name = "Jan Novák", Email = "jan@example.org" }));
        Assert.Equal("jan@example.org", SignatureView.SignerLabel(new SignatureSigner { Email = "jan@example.org" }));
        Assert.Equal("Jan Novák", SignatureView.SignerLabel(new SignatureSigner { Name = "Jan Novák" }));
        Assert.Equal("jan@example.org", SignatureView.SignerLabel(new SignatureSigner { Name = "JAN@example.org", Email = "jan@example.org" }));
        // Hostile: bidi override, zero-width, newline, control.
        Assert.Equal(
            "Jan kávoN X <jan@example.org>",
            SignatureView.SignerLabel(new SignatureSigner { Name = "Jan‮ kávoN​\nX\u0007", Email = " jan@example.org " }));
    }

    [Fact]
    public void CleanCapsOnACharacterBoundary()
    {
        var longText = new string('ž', 200); // 400 bytes
        var got = SignatureView.Clean(longText);
        Assert.Equal(256, Encoding.UTF8.GetByteCount(got));
        Assert.StartsWith(got, longText, StringComparison.Ordinal);
        Assert.Equal("a b", SignatureView.Clean(" a b"));
        Assert.Equal("a", SignatureView.Clean("a\ud800")); // a lone surrogate is dropped
    }

    [Fact]
    public void Badge()
    {
        Assert.False(SignatureView.BadgeFor(null).Visible);
        Assert.Equal("", SignatureView.BadgeFor(null).Text);
        var jan = new SignatureSigner { Name = "Jan", Email = "jan@example.org" };
        const string janLabel = "Jan <jan@example.org>";
        (string Kind, string Status, SignatureSigner? Signer, string[] Notes, string Want)[] cases =
        [
            (SignatureKind.Smime, SignatureStatus.Valid, jan, [], $"Signed by {janLabel}"),
            (SignatureKind.Pgp, SignatureStatus.ValidSeen, jan, [], $"Signed by {janLabel}"),
            (SignatureKind.Smime, SignatureStatus.Valid, null, [], "Valid signature"),
            (SignatureKind.Smime, SignatureStatus.Mismatch, jan, [], $"Signed by someone else: {janLabel}"),
            (SignatureKind.Smime, SignatureStatus.Mismatch, null, [], "Signed by someone else"),
            (SignatureKind.Smime, SignatureStatus.Untrusted, jan, [], $"Signed by {janLabel}, but the certificate is not trusted"),
            (SignatureKind.Pgp, SignatureStatus.Untrusted, jan, [], $"Signed by {janLabel}, but the key had expired"),
            (SignatureKind.Pgp, SignatureStatus.Untrusted, null, [], "Untrusted signature"),
            (SignatureKind.Smime, SignatureStatus.Weak, jan, [], $"Signed by {janLabel} with a weak algorithm"),
            (SignatureKind.Smime, SignatureStatus.Weak, null, [], "Weak signature"),
            (SignatureKind.Smime, SignatureStatus.Revoked, jan, [], "Signed with a revoked certificate"),
            (SignatureKind.Pgp, SignatureStatus.Revoked, jan, [], "Signed with a revoked key"),
            (SignatureKind.Smime, SignatureStatus.Invalid, jan, [], "Invalid signature"),
            (SignatureKind.Pgp, SignatureStatus.UnknownKey, null, [], "Signed with an unknown OpenPGP key"),
            (SignatureKind.Pgp, SignatureStatus.Unsupported, null, [SignatureNote.InlinePgp], "Inline OpenPGP signature, not verified"),
            (SignatureKind.Smime, SignatureStatus.Unsupported, null, [], "Signature not verified"),
            (SignatureKind.Smime, SignatureStatus.Error, null, [], "Signature could not be verified"),
            (SignatureKind.Smime, "somethingNew", jan, [], "Signature could not be verified"),
        ];
        foreach (var (kind, status, signer, notes, want) in cases)
        {
            var b = SignatureView.BadgeFor(new SignatureSummary
            {
                Kind = kind,
                Status = status,
                Signer = signer,
                Notes = [.. notes.Select(n => (SignatureNote)n)],
            });
            Assert.True(b.Visible, $"{kind} {status}");
            Assert.Equal(want, b.Text);
            Assert.Equal("Show Signature Details", b.Tooltip);
            Assert.Equal(SignatureView.IconOf(SignatureView.SeverityOf(status)), b.Icon);
        }
    }

    [Fact]
    public void TitlesAndExplanationsCoverEveryStatus()
    {
        foreach (var kind in new[] { SignatureKind.Smime, SignatureKind.Pgp })
        {
            foreach (var status in AllStatuses)
            {
                Assert.NotEqual("", SignatureText.StatusTitle(kind, status));
                Assert.NotEqual("", SignatureText.Explanation(new SignatureSummary { Kind = kind, Status = status }, ""));
            }
        }
        var m = new SignatureSummary
        {
            Kind = SignatureKind.Smime,
            Status = SignatureStatus.Mismatch,
            Signer = new SignatureSigner { Email = "boss@evil.example" },
        };
        Assert.Equal(
            "The signature is valid, but it belongs to boss@evil.example, not to the sender boss@example.org.",
            SignatureText.Explanation(m, "boss@example.org"));
        Assert.Equal("The signature is valid, but it does not belong to the sender's address.", SignatureText.Explanation(m, ""));
    }

    [Fact]
    public void NotesAndReasons()
    {
        string[] notes =
        [
            SignatureNote.ExpiredSince, SignatureNote.RevocationUnchecked, SignatureNote.RevocationGood,
            SignatureNote.RevokedAfter, SignatureNote.PartialFooter, SignatureNote.ProtectedHeadersDiffer,
            SignatureNote.InlinePgp, SignatureNote.KeyChanged, SignatureNote.Opaque,
        ];
        foreach (var kind in new[] { SignatureKind.Smime, SignatureKind.Pgp })
        {
            foreach (var n in notes)
            {
                Assert.NotEqual("", SignatureText.NoteText(kind, n));
            }
        }
        Assert.NotEqual(
            SignatureText.NoteText(SignatureKind.Pgp, SignatureNote.ExpiredSince),
            SignatureText.NoteText(SignatureKind.Smime, SignatureNote.ExpiredSince));
        Assert.Equal("", SignatureText.NoteText(SignatureKind.Smime, "unknownNote"));
        string[] reasons =
        [
            SignatureReason.DigestMismatch, SignatureReason.BadSignature, SignatureReason.Malformed,
            SignatureReason.Md5, SignatureReason.KeyUsage, SignatureReason.UnknownIssuer,
            SignatureReason.SelfSigned, SignatureReason.NotYetValid, SignatureReason.Expired,
            SignatureReason.Sha1, SignatureReason.ShortKey, SignatureReason.Algorithm,
            SignatureReason.MultipleSigners, SignatureReason.Rebuilt, SignatureReason.Format,
            SignatureReason.Limit,
        ];
        foreach (var r in reasons)
        {
            Assert.NotEqual("", SignatureText.ReasonText(SignatureKind.Smime, r));
        }
        foreach (var r in new SignatureReason?[] { null, "", SignatureReason.Internal, "unknownReason" })
        {
            Assert.Equal("", SignatureText.ReasonText(SignatureKind.Smime, r));
        }
    }

    [Fact]
    public void Tiles()
    {
        Attachment[] atts = [Att("1.2", "a.pdf"), Att("2", "smime.p7s", "application/pkcs7-signature")];
        Assert.Equal(atts, SignatureView.TileAttachments(atts, null));
        var sig = new SignatureSummary { Kind = SignatureKind.Smime, Status = SignatureStatus.Valid, PartId = "2" };
        Assert.Equal(["1.2"], SignatureView.TileAttachments(atts, sig).Select(a => a.PartId));
        // Opaque S/MIME and inline OpenPGP have no part: nothing is hidden,
        // and a part without an id is never taken for it.
        Attachment[] more = [.. atts, Att("")];
        Assert.Equal(3, SignatureView.TileAttachments(more, new SignatureSummary { Kind = SignatureKind.Smime, Status = SignatureStatus.Valid }).Count);
        // A signature part of another verdict's message (a forwarded signed message) stays.
        Assert.False(SignatureView.HidesPart(Att("3.2", "signature.asc", "application/pgp-signature"), sig));
    }

    [Fact]
    public void FormatFingerprint()
    {
        Assert.Equal("", SignatureView.FormatFingerprint(""));
        Assert.Equal("", SignatureView.FormatFingerprint(null));
        Assert.Equal("0123 4567 89AB CDEF", SignatureView.FormatFingerprint("0123456789abcdef"));
        Assert.Equal("0123 4567 89AB CDEF 0123 4567 89AB CDEF 0123 4567", SignatureView.FormatFingerprint(Fp40));
        Assert.Equal(
            "AB12 CD34 AB12 CD34 AB12 CD34 AB12 CD34 AB12 CD34 AB12 CD34 AB12 CD34 AB12 CD34",
            SignatureView.FormatFingerprint(Fp64));
        Assert.Equal("", SignatureView.FormatFingerprint("0123456789abcdeg"));
        Assert.Equal("", SignatureView.FormatFingerprint("0123"));
        Assert.Equal("", SignatureView.FormatFingerprint("0123456789ABCDEF0123456789ABCDEF0123456"));
    }

    [Fact]
    public void DetailsOfSmime()
    {
        var signed = Arrived.AddHours(-1);
        var expired = Arrived.AddHours(24);
        var d = new SignatureDetails
        {
            Kind = SignatureKind.Smime,
            Status = SignatureStatus.Valid,
            Signer = new SignatureSigner { Name = "Jan", Email = "jan@example.org", Fingerprint = Fp64 },
            Notes = [SignatureNote.RevocationUnchecked, SignatureNote.ExpiredSince, "unknownNote"],
            PartId = "2",
            MessageId = "m_1",
            SignedAt = signed,
            ArrivedAt = Arrived,
            Algorithm = "RSA 2048 / SHA-256",
            ExpiredAt = expired,
            ProtectedHeaders =
            [
                new ProtectedHeader { Name = ProtectedHeaderName.Subject, Value = "Real‮ subject" },
                new ProtectedHeader { Name = "unknown", Value = "x" },
            ],
            Smime = new SmimeDetails
            {
                Chain =
                [
                    new SignatureCertificate { Sha256 = Fp64, Subject = "CN=Jan", Issuer = "CN=CA 2", Emails = ["jan@example.org"], NotBefore = Arrived, NotAfter = expired },
                    new SignatureCertificate { Sha256 = Fp64, Subject = "CN=CA 2", Issuer = "CN=Root", NotBefore = Arrived, NotAfter = expired },
                    new SignatureCertificate { Sha256 = Fp64, Subject = "CN=Root", Issuer = "CN=Root", NotBefore = Arrived, NotAfter = expired, SystemRoot = true, SelfSigned = true },
                ],
                Revocation = new SmimeRevocation { State = RevocationState.Unchecked, Checkable = true },
            },
        };
        var got = SignatureView.DetailsFor(d, "jan@example.org", SignatureAction.None, Day);
        Assert.Equal(("Valid signature", SignatureSeverity.Good), (got.Title, got.Severity));
        Assert.NotEqual("", got.Caveat);
        Assert.Equal(
            [SignatureRowKey.Signer, SignatureRowKey.Kind, SignatureRowKey.SignedAt, SignatureRowKey.ArrivedAt, SignatureRowKey.Algorithm, SignatureRowKey.Revocation, SignatureRowKey.Expired],
            Keys(got.Rows));
        Assert.Equal(("Jan <jan@example.org>", "S/MIME", "Not checked"), (got.Rows[0].Value, got.Rows[1].Value, got.Rows[5].Value));
        Assert.Equal(["The certificate has expired since the message arrived."], got.Notes);
        Assert.Equal(
            [SignatureSectionKey.SignerCertificate, SignatureSectionKey.Intermediate, SignatureSectionKey.Root, SignatureSectionKey.SignedHeaders],
            got.Sections.Select(s => s.Key));
        var signer = got.Sections[0];
        Assert.Equal(
            [SignatureRowKey.IssuedTo, SignatureRowKey.Emails, SignatureRowKey.IssuedBy, SignatureRowKey.Validity, SignatureRowKey.CertFingerprint],
            Keys(signer.Rows));
        Assert.Equal("2026-11-02 to 2026-11-03", signer.Rows[3].Value);
        Assert.True(signer.Rows[4].Monospace);
        var headers = got.Sections[3].Rows;
        Assert.Single(headers);
        Assert.Equal(("Subject", "Real subject"), (headers[0].Label, headers[0].Value));
        Assert.Equal([new SignatureButton(SignatureAction.CheckRevocation, "Check Revocation", true)], got.Buttons);
        Assert.NotEqual("", got.RevocationCaption);

        // While it runs, and once checked.
        var busy = SignatureView.DetailsFor(d, "", SignatureAction.CheckRevocation, Day);
        Assert.Equal(new SignatureButton(SignatureAction.CheckRevocation, "Checking…", false), busy.Buttons[0]);
        var checkedAt = Arrived.AddHours(1);
        var again = d with { Smime = d.Smime! with { Revocation = new SmimeRevocation { State = RevocationState.Good, CheckedAt = checkedAt, Checkable = true } } };
        var rechecked = SignatureView.DetailsFor(again, "", SignatureAction.None, Day);
        Assert.Equal("Check Revocation Again", rechecked.Buttons[0].Label);
        Assert.Equal("Not revoked (checked 2026-11-02)", rechecked.Rows[5].Value);

        // Without a responder, or without the certificate, no button.
        var notCheckable = again with { Smime = again.Smime! with { Revocation = again.Smime!.Revocation with { Checkable = false } } };
        Assert.Empty(SignatureView.DetailsFor(notCheckable, "", SignatureAction.None, Day).Buttons);
        var noChain = again with { Smime = again.Smime! with { Chain = [] } };
        var none = SignatureView.DetailsFor(noChain, "", SignatureAction.None, Day);
        Assert.Empty(none.Buttons);
        Assert.Single(none.Sections);
    }

    [Fact]
    public void RevocationValues()
    {
        var at = Arrived;
        (SmimeRevocation R, string Want)[] cases =
        [
            (new() { State = RevocationState.Unchecked }, "Not checked"),
            (new() { State = RevocationState.Good, CheckedAt = at }, "Not revoked (checked 2026-11-02)"),
            (new() { State = RevocationState.Good }, "Not checked"),
            (new() { State = RevocationState.Revoked, RevokedAt = at }, "Revoked on 2026-11-02"),
            (new() { State = RevocationState.Revoked }, "Revoked"),
            (new() { State = RevocationState.Unknown, CheckedAt = at }, "Unknown to the issuer (checked 2026-11-02)"),
            (new() { State = "new" }, "Not checked"),
        ];
        foreach (var (r, want) in cases)
        {
            Assert.Equal(want, SignatureText.RevocationText(r, Day));
        }
    }

    [Fact]
    public void DetailsOfPgp()
    {
        var seen = Arrived.AddHours(-48);
        var d = new SignatureDetails
        {
            Kind = SignatureKind.Pgp,
            Status = SignatureStatus.ValidSeen,
            Signer = new SignatureSigner { Name = "Jan", Email = "jan@example.org", Fingerprint = Fp40 },
            Notes = [SignatureNote.KeyChanged],
            MessageId = "m_2",
            ArrivedAt = Arrived,
            Pgp = new PgpDetails
            {
                KeyTrust = PgpKeyTrust.Seen,
                KeyId = "89abcdef01234567",
                PrimaryFingerprint = Fp40,
                UserIds = ["Jan <jan@example.org>", "", "Jan (work) <jan@work.example>"],
                KeySource = PgpKeySource.Autocrypt,
                FirstSeen = seen,
                PreviousFingerprint = string.Concat(Enumerable.Repeat("ef", 20)),
                Importable = new PgpKeyOffer { Fingerprint = Fp40, Autocrypt = true },
            },
        };
        var got = SignatureView.DetailsFor(d, "", SignatureAction.None, Day);
        Assert.Equal(
            [
                SignatureRowKey.Signer, SignatureRowKey.Kind, SignatureRowKey.ArrivedAt, SignatureRowKey.KeyTrust,
                SignatureRowKey.KeyFingerprint, SignatureRowKey.KeySource, SignatureRowKey.FirstSeen, SignatureRowKey.UserIds,
                SignatureRowKey.PreviousKey,
            ],
            Keys(got.Rows));
        Assert.Equal("Found automatically, not confirmed", got.Rows[3].Value);
        Assert.Equal("Jan <jan@example.org>\nJan (work) <jan@work.example>", got.Rows[7].Value);
        Assert.Equal("Autocrypt header of a message", got.Rows[5].Value);
        Assert.Equal(["This sender used a different key before."], got.Notes);
        Assert.Equal([SignatureAction.ImportKey, SignatureAction.ConfirmKey], got.Buttons.Select(b => b.Action));
        Assert.Equal(SignatureView.FormatFingerprint(Fp40), got.KeyFingerprint);
        Assert.Equal("", got.RevocationCaption);
        var import = SignatureView.ImportParams(d);
        Assert.NotNull(import);
        Assert.Equal<MessageId?>("m_2", import.MessageId);
        Assert.True(import.Autocrypt);
        Assert.Null(import.PartId);
        var confirm = SignatureView.ConfirmParams(d);
        Assert.NotNull(confirm);
        Assert.Equal((Fp40, true), (confirm.Fingerprint, confirm.Verified));

        // An unknown key: the key ID, Import Key from an attachment, no Confirm.
        var unknown = new SignatureDetails
        {
            Kind = SignatureKind.Pgp,
            Status = SignatureStatus.UnknownKey,
            MessageId = "m_3",
            ArrivedAt = Arrived,
            Pgp = new PgpDetails
            {
                KeyTrust = PgpKeyTrust.Unknown,
                KeyId = "89abcdef01234567",
                Importable = new PgpKeyOffer { Fingerprint = Fp40, PartId = "3" },
            },
        };
        got = SignatureView.DetailsFor(unknown, "", SignatureAction.ImportKey, Day);
        Assert.Equal([SignatureRowKey.Kind, SignatureRowKey.ArrivedAt, SignatureRowKey.KeyTrust, SignatureRowKey.KeyId], Keys(got.Rows));
        Assert.Equal("Not in Malachi Mail", got.Rows[2].Value);
        Assert.Equal([new SignatureButton(SignatureAction.ImportKey, "Import Key", false)], got.Buttons);
        Assert.Equal("", got.Caveat);
        var fromPart = SignatureView.ImportParams(unknown);
        Assert.Equal("3", fromPart!.PartId);
        Assert.Null(fromPart.Autocrypt);

        // A confirmed key, a revoked one, an offer naming both sources.
        Assert.Null(SignatureView.ConfirmParams(d with { Pgp = d.Pgp! with { KeyTrust = PgpKeyTrust.Verified } }));
        var revokedKey = d with { Status = SignatureStatus.Revoked };
        Assert.Null(SignatureView.ConfirmParams(revokedKey));
        var both = revokedKey with { Pgp = revokedKey.Pgp! with { Importable = new PgpKeyOffer { Fingerprint = Fp40, PartId = "3", Autocrypt = true } } };
        Assert.Null(SignatureView.ImportParams(both));
        var withRevoked = both with { RevokedAt = Arrived.AddHours(-1) };
        Assert.Equal(SignatureRowKey.Revoked, SignatureView.DetailsFor(withRevoked, "", SignatureAction.None, Day).Rows[^1].Key);
        var empty = SignatureView.DetailsFor(null, "", SignatureAction.None, Day);
        Assert.Equal((SignatureSeverity.None, "", ""), (empty.Severity, empty.Title, empty.Explanation));
        Assert.Empty(empty.Rows);
        Assert.Empty(empty.Buttons);
    }

    private static RpcError Error(int code, string? data = null) =>
        new() { Code = code, Message = "x", Data = data is null ? null : ApiJson.Parse(data) };

    [Fact]
    public void Errors()
    {
        Assert.Equal<RevocationFailure?>(RevocationFailure.Timeout, SignatureView.RevocationFailureOf(Error(ErrorCode.RevocationFailed, """{"reason":"timeout"}""")));
        foreach (var e in new[]
        {
            Error(ErrorCode.Offline),
            Error(ErrorCode.RevocationFailed),
            Error(ErrorCode.RevocationFailed, "\"x\""),
        })
        {
            Assert.Null(SignatureView.RevocationFailureOf(e));
        }
        Assert.Null(SignatureView.RevocationFailureOf((Exception?)null));
        // A failed call carries the daemon's error.
        Assert.Equal<RevocationFailure?>(
            RevocationFailure.Address,
            SignatureView.RevocationFailureOf(new RpcException(Error(ErrorCode.RevocationFailed, """{"reason":"address"}"""))));
        Assert.Equal<PgpImportFailure?>(PgpImportFailure.SecretKey, SignatureView.ImportFailureOf(Error(ErrorCode.InvalidArgument, """{"reason":"secretKey"}""")));
        Assert.Null(SignatureView.ImportFailureOf(Error(ErrorCode.MessageNotFound, """{"reason":"secretKey"}""")));
        foreach (var r in new RevocationFailure?[]
        {
            null, RevocationFailure.Network, RevocationFailure.Timeout, RevocationFailure.Server, RevocationFailure.Response, RevocationFailure.Address,
        })
        {
            Assert.NotEqual("", SignatureText.RevocationFailedText(r));
        }
        foreach (var r in new PgpImportFailure?[]
        {
            null, PgpImportFailure.NoKey, PgpImportFailure.Malformed, PgpImportFailure.SecretKey, PgpImportFailure.TooLarge, PgpImportFailure.TooMany,
        })
        {
            Assert.NotEqual("", SignatureText.ImportFailedText(r));
        }
    }

    [Fact]
    public void ImportParamsAndText()
    {
        Assert.Null(SignatureView.PastedParams("  \n"));
        Assert.Null(SignatureView.PastedParams(null));
        Assert.Equal(SignatureView.ArmorPlaceholder + "\nabc", SignatureView.PastedParams("\n" + SignatureView.ArmorPlaceholder + "\nabc\n")!.Armored);
        Assert.Null(SignatureView.PastedParams(new string('a', API.Limits.MaxPGPKeyBytes + 1)));
        var armoured = SignatureView.FileParams(Encoding.UTF8.GetBytes(SignatureView.ArmorPlaceholder + "\n"));
        Assert.NotNull(armoured);
        Assert.False(string.IsNullOrEmpty(armoured.Armored));
        Assert.Null(armoured.Data);
        var binary = SignatureView.FileParams([0x99, 0x01]);
        Assert.NotNull(binary);
        Assert.Null(binary.Armored);
        Assert.Equal(2, binary.Data!.Length);
        Assert.Null(SignatureView.FileParams(null));
        Assert.Null(SignatureView.FileParams([]));
        Assert.Null(SignatureView.FileParams(new byte[API.Limits.MaxPGPKeyBytes + 1]));
        (PgpKeyImportResult R, string Want)[] cases =
        [
            (new() { Added = 1 }, "1 key imported"),
            (new() { Added = 3, Updated = 1 }, "3 keys imported"),
            (new() { Updated = 1 }, "1 key updated"),
            (new() { Updated = 2 }, "2 keys updated"),
            (new(), "The key was already imported."),
        ];
        foreach (var (r, want) in cases)
        {
            Assert.Equal(want, SignatureText.ImportedText(r));
        }
    }

    [Fact]
    public void KeyRows()
    {
        var now = Arrived;
        var past = now.AddHours(-1);
        var future = now.AddHours(1);
        PgpKey[] keys =
        [
            new() { Fingerprint = Fp64, UserIds = ["zoe <zoe@example.org>"], Trust = PgpKeyTrust.Verified, Source = PgpKeySource.Import, FirstSeen = now },
            new() { Fingerprint = Fp40, Emails = ["Adam@example.org"], Trust = PgpKeyTrust.Seen, Source = PgpKeySource.Autocrypt, FirstSeen = now, ExpiresAt = past },
            new()
            {
                Fingerprint = string.Concat(Enumerable.Repeat("ab", 20)),
                Trust = PgpKeyTrust.Seen,
                Source = PgpKeySource.Attached,
                FirstSeen = now,
                RevokedAt = past,
                ExpiresAt = future,
            },
        ];
        var rows = SignatureView.KeyRows(keys, now, Day);
        Assert.Equal(
            ["ABAB ABAB ABAB ABAB ABAB ABAB ABAB ABAB ABAB ABAB", "Adam@example.org", "zoe <zoe@example.org>"],
            rows.Select(r => r.Title));
        var (revoked, adam, zoe) = (rows[0], rows[1], rows[2]);
        Assert.Equal(("Confirmed", true, "", "Imported on 2026-11-02"), (zoe.TrustPill, zoe.Verified, zoe.StatePill, zoe.Origin));
        Assert.Equal([SignatureAction.UnconfirmKey, SignatureAction.RemoveKey], zoe.Actions.Select(a => a.Action));
        Assert.Equal(("Not confirmed", "Expired", "Found in a message header on 2026-11-02"), (adam.TrustPill, adam.StatePill, adam.Origin));
        Assert.Equal(SignatureAction.ConfirmKey, adam.Actions[0].Action);
        Assert.Equal((SignatureView.FormatFingerprint(Fp40), Fp40), (adam.Subtitle, adam.Fingerprint));
        Assert.Equal(("Revoked", "Imported from a message on 2026-11-02"), (revoked.StatePill, revoked.Origin));
        Assert.Equal([SignatureAction.RemoveKey], revoked.Actions.Select(a => a.Action));
    }

    [Fact]
    public void QuestionsAndPage()
    {
        var c = SignatureText.ConfirmKeyQuestion();
        Assert.Equal(("_Confirm Key", "_Cancel", false), (c.Accept, c.Cancel, c.Destructive));
        var r = SignatureText.RemoveKeyQuestion();
        Assert.Equal(("_Remove", true), (r.Accept, r.Destructive));
        var p = SignatureText.SecurityPageTexts();
        Assert.Equal(("Security", "OpenPGP Keys", "Import Key…"), (p.Title, p.Group, p.Import));
        var d = SignatureText.ImportDialogTexts();
        Assert.Equal(("_Import", "Choose File…"), (d.Import, d.ChooseFile));
        Assert.Equal("Signature Details", SignatureText.DialogTitle());
        Assert.Equal("Close", SignatureText.CloseLabel());
        Assert.Equal("Key confirmed", SignatureText.KeyConfirmedText());
        Assert.Equal("Confirmation removed", SignatureText.ConfirmationRemovedText());
        Assert.Equal("Key removed", SignatureText.KeyRemovedText());
        Assert.Equal("The key could not be confirmed.", SignatureText.KeyVerifyFailedText(verified: true));
        Assert.Equal("The confirmation could not be removed.", SignatureText.KeyVerifyFailedText(verified: false));
        Assert.Equal("The key could not be removed.", SignatureText.KeyRemoveFailedText());
    }
}
