// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CertTrustTests.swift, the counterpart
// of ui/internal/certtrust/certtrust_test.go plus the texts of the trust
// confirmation (accountwizard trust.go), with the cases Go has beyond
// Swift's. Every error here has crossed the wire as JSON, as it does in the
// client (Go's "over JSON" cases are all cases here).

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.Wizard;
using Xunit;
using static Malachi.Core.Tests.Wizard.TlsErrors;

namespace Malachi.Core.Tests.Wizard;

public sealed class CertTrustTests
{
    // Invisible characters of hostile certificates, as escapes so the source
    // stays readable.
    private const string Rlo = "\x202E"; // RIGHT-TO-LEFT OVERRIDE
    private const string Zwsp = "\x200B"; // ZERO WIDTH SPACE
    private const string Lri = "\x2066"; // LEFT-TO-RIGHT ISOLATE
    private const string Lrm = "\x200E"; // LEFT-TO-RIGHT MARK
    private const string Ls = "\x2028"; // LINE SEPARATOR (Zl)
    private const string Ps = "\x2029"; // PARAGRAPH SEPARATOR (Zp)

    private static readonly string SumA = string.Concat(Enumerable.Repeat("ab", 32));
    private static readonly string SumB = string.Concat(Enumerable.Repeat("0f", 32));

    private static readonly DateTimeOffset NotBefore = DateTimeOffset.FromUnixTimeSeconds(1_704_164_645); // 2024-01-02T03:04:05Z
    private static readonly DateTimeOffset NotAfter = DateTimeOffset.FromUnixTimeSeconds(2_335_316_645); // 2044-01-02T03:04:05Z

    private static readonly CertificateInfo BridgeCert = new()
    {
        Sha256 = SumA,
        Subject = "127.0.0.1",
        Issuer = "127.0.0.1",
        IpAddresses = ["127.0.0.1"],
        NotBefore = NotBefore,
        NotAfter = NotAfter,
        SelfSigned = true,
    };

    private static readonly ServerConfig ImapTls = new()
    {
        Host = "bridge.tail.example",
        Port = 1143,
        Security = Security.Starttls,
        Username = "me",
        AuthMethod = AuthMethod.Password,
    };

    private static readonly CultureInfo Posix = CultureInfo.InvariantCulture;

    private static string Groups(string four) => string.Join(" ", Enumerable.Repeat(four, 16));

    [Fact]
    public void Details()
    {
        (string Name, RpcError? E, CertTrust.Problem? Want)[] cases =
        [
            ("nil", null, null),
            ("other code", new RpcError { Code = ErrorCode.NetworkError, Message = "x", Data = Json(new TlsErrorData { Reason = TlsErrorReason.Untrusted }) }, null),
            ("no data", Error((System.Text.Json.JsonElement?)null), null),
            ("empty reason", Error(new TlsErrorData { Reason = "", Certificate = BridgeCert }), null),
            ("map without reason", Error(Raw("{\"certificate\":{\"sha256\":\"" + SumA + "\"}}")), null),
            ("wrong shape", Error(Raw("\"untrusted\"")), null),
            ("untrusted with certificate", Error(new TlsErrorData { Reason = TlsErrorReason.Untrusted, Certificate = BridgeCert }),
                new CertTrust.Problem { Reason = TlsErrorReason.Untrusted, Cert = BridgeCert }),
            ("handshake without certificate", Error(new TlsErrorData { Reason = TlsErrorReason.Handshake }),
                new CertTrust.Problem { Reason = TlsErrorReason.Handshake }),
            ("unknown reason is other", Error(new TlsErrorData { Reason = "quantumDecoherence", Certificate = BridgeCert }),
                new CertTrust.Problem { Reason = TlsErrorReason.Other, Cert = BridgeCert }),
            ("pin mismatch keeps the expected fingerprint",
                Error(new TlsErrorData { Reason = TlsErrorReason.PinMismatch, Certificate = BridgeCert, ExpectedSha256 = SumB.ToUpperInvariant() }),
                new CertTrust.Problem { Reason = TlsErrorReason.PinMismatch, Cert = BridgeCert, Expected = SumB }),
            ("invalid expected fingerprint is dropped", Error(new TlsErrorData { Reason = TlsErrorReason.PinMismatch, ExpectedSha256 = "nope" }),
                new CertTrust.Problem { Reason = TlsErrorReason.PinMismatch }),
            ("certificate with a bad fingerprint is dropped",
                Error(new TlsErrorData { Reason = TlsErrorReason.Untrusted, Certificate = new CertificateInfo { Sha256 = "12:34", Subject = "x" } }),
                new CertTrust.Problem { Reason = TlsErrorReason.Untrusted }),
            ("certificate without a fingerprint is dropped",
                Error(new TlsErrorData { Reason = TlsErrorReason.Expired, Certificate = new CertificateInfo { Sha256 = "", Subject = "x" } }),
                new CertTrust.Problem { Reason = TlsErrorReason.Expired }),
            ("upper-case fingerprint is normalised",
                Error(new TlsErrorData { Reason = TlsErrorReason.Untrusted, Certificate = new CertificateInfo { Sha256 = SumA.ToUpperInvariant() } }),
                new CertTrust.Problem { Reason = TlsErrorReason.Untrusted, Cert = new CertificateInfo { Sha256 = SumA } }),
        ];
        foreach (var (name, e, want) in cases)
        {
            Assert.True(Equals(want, CertTrust.Details(e)), name);
        }
    }

    [Fact]
    public void DetailsFromAHandWrittenMap()
    {
        // As another daemon version might send it: members missing, one
        // unknown, dates as RFC 3339.
        var e = Error(Raw(
            "{\"reason\":\"pinMismatch\","
            + "\"certificate\":{\"sha256\":\"" + SumB + "\",\"subject\":\"mail\",\"notBefore\":\"2024-01-02T03:04:05Z\",\"notAfter\":\"2044-01-02T03:04:05Z\",\"selfSigned\":true,\"extra\":1},"
            + "\"unknown\":[1,2]}"));
        var p = CertTrust.Details(e);
        Assert.NotNull(p);
        Assert.Equal(TlsErrorReason.PinMismatch, p.Reason);
        var c = p.Cert;
        Assert.NotNull(c);
        Assert.True(c.Sha256 == SumB && c.SelfSigned && c.NotAfter == NotAfter && c.Subject == "mail" && c.DnsNames.Count == 0);
        // Wrong types inside the certificate: no details at all rather than
        // a half-read certificate.
        Assert.Null(CertTrust.Details(Error(Raw("{\"reason\":\"untrusted\",\"certificate\":{\"sha256\":42}}"))));
        // A certificate without dates is Go's zero time, not a failure.
        var bare = CertTrust.Details(Error(Raw("{\"reason\":\"untrusted\",\"certificate\":{\"sha256\":\"" + SumA + "\"}}")));
        Assert.True(bare?.Cert?.NotBefore.IsGoZero == true && bare.Cert.SelfSigned == false);
    }

    // certtrust_test.go TestDetailsOverJSON: the bridge's certificate comes
    // back whole.
    [Fact]
    public void DetailsOverJson()
    {
        var p = CertTrust.Details(Error(new TlsErrorData { Reason = TlsErrorReason.HostnameMismatch, Certificate = BridgeCert }));
        Assert.NotNull(p?.Cert);
        Assert.Equal(TlsErrorReason.HostnameMismatch, p.Reason);
        Assert.Equal(new CertTrust.Problem { Reason = TlsErrorReason.HostnameMismatch, Cert = BridgeCert }, p);
    }

    [Fact]
    public void DetailsCleanHostileCertificates()
    {
        var longText = string.Concat(Enumerable.Repeat("ž", 100)); // 200 bytes
        var hostile = new CertificateInfo
        {
            Sha256 = SumA,
            Subject = "  evil" + Rlo + Zwsp + "moc.knab\x0000\x001B[31m\n ",
            Issuer = "CA\r\nInjected: yes" + Lri + Ls + Ps,
            DnsNames = ["a.example", "", Lrm, "b\texample", "c", "d", "e", "f", "g", "h", "i", "j"],
            IpAddresses = ["127.0.0.1\x0085", longText],
            NotBefore = NotBefore,
            NotAfter = NotAfter,
        };
        var p = CertTrust.Details(Error(new TlsErrorData { Reason = TlsErrorReason.Other, Certificate = hostile }));
        var c = p?.Cert;
        Assert.NotNull(c);
        Assert.Equal("evilmoc.knab[31m", c.Subject);
        Assert.Equal("CAInjected: yes", c.Issuer);
        Assert.Equal(["a.example", "bexample", "c", "d", "e", "f", "g", "h"], c.DnsNames);
        Assert.True(c.IpAddresses.Count == 2 && c.IpAddresses[0] == "127.0.0.1");
        var capped = c.IpAddresses[^1];
        Assert.True(Encoding.UTF8.GetByteCount(capped) == 128 && longText.StartsWith(capped, StringComparison.Ordinal));
        HashSet<int> hidden = [0x200B, 0x200E, 0x202E, 0x2066, 0x2028, 0x2029];
        foreach (var s in new[] { c.Subject ?? "", c.Issuer ?? "" }.Concat(c.DnsNames).Concat(c.IpAddresses))
        {
            Assert.False(
                s.EnumerateRunes().Any(r => r.Value < 0x20 || r.Value == 0x7F || (r.Value >= 0x80 && r.Value < 0xA0) || hidden.Contains(r.Value)),
                $"hidden character left in {s}");
        }
        // The caller's data is not modified.
        Assert.NotEqual(hostile.Subject, c.Subject);
    }

    [Fact]
    public void CleanTextCapsOnACharacterBoundary()
    {
        var s = "a" + string.Concat(Enumerable.Repeat("€", 60)); // 1 + 180 bytes
        var got = CertTrust.CleanText(s);
        Assert.True(Encoding.UTF8.GetByteCount(got) == 127 && s.StartsWith(got, StringComparison.Ordinal));
        // What invalid UTF-8 or a lone surrogate became goes.
        Assert.Equal("badutf8", CertTrust.CleanText("bad\xFFFDutf8"));
        Assert.Equal("badutf8", CertTrust.CleanText("bad" + (char)0xD800 + "utf8"));
        // Trimmed again after the cut.
        Assert.Equal(new string('x', 127), CertTrust.CleanText(new string('x', 127) + " y"));
        // Line and paragraph separators (Zl, Zp) go too.
        Assert.Equal("linepara", CertTrust.CleanText("line" + Ls + "para" + Ps));
    }

    [Fact]
    public void Category()
    {
        var cases = new Dictionary<string, CertTrust.Category>
        {
            [TlsErrorReason.Untrusted] = CertTrust.Category.Certificate,
            [TlsErrorReason.HostnameMismatch] = CertTrust.Category.Certificate,
            [TlsErrorReason.Expired] = CertTrust.Category.Certificate,
            [TlsErrorReason.NotYetValid] = CertTrust.Category.Certificate,
            [TlsErrorReason.Invalid] = CertTrust.Category.Certificate,
            [TlsErrorReason.Other] = CertTrust.Category.Certificate,
            ["somethingNew"] = CertTrust.Category.Certificate,
            [""] = CertTrust.Category.Certificate,
            [TlsErrorReason.PinMismatch] = CertTrust.Category.Changed,
            [TlsErrorReason.Handshake] = CertTrust.Category.Connection,
            [TlsErrorReason.StarttlsUnavailable] = CertTrust.Category.Connection,
            [TlsErrorReason.TlsRequired] = CertTrust.Category.Connection,
        };
        foreach (var (r, want) in cases)
        {
            Assert.True(CertTrust.CategoryOf(r) == want, r);
        }
        Assert.Equal(CertTrust.Category.Certificate, CertTrust.CategoryOf(default));
        Assert.True(CertTrust.NormalizeReason("somethingNew") == TlsErrorReason.Other && CertTrust.NormalizeReason(TlsErrorReason.Expired) == TlsErrorReason.Expired);
    }

    [Fact]
    public void FormatFingerprint()
    {
        var want = Groups("ABAB");
        foreach (var input in new[] { SumA, SumA.ToUpperInvariant(), "AB:" + string.Concat(Enumerable.Repeat("AB:", 30)) + "AB" })
        {
            Assert.True(CertTrust.FormatFingerprint(input) == want, input);
        }
        var groups = CertTrust.FormatFingerprint("0123456789abcdef" + new string('0', 48));
        Assert.True(groups.StartsWith("0123 4567 89AB CDEF 0000", StringComparison.Ordinal) && groups.Length == 64 + 15);
        foreach (var input in new[] { "", "abc", SumA + "00", string.Concat(Enumerable.Repeat("zz", 32)), Rlo + SumA })
        {
            Assert.True(CertTrust.FormatFingerprint(input).Length == 0, $"{input} accepted");
        }
    }

    [Fact]
    public void Trustable()
    {
        static EndpointTestResult Failed(RpcError? e) => new() { Ok = false, Error = e, LatencyMs = 0 };
        static ServerConfig With(Func<ServerConfig, ServerConfig> change) => change(ImapTls);
        static EndpointTestResult Reason(TlsErrorReason r) => Failed(Error(new TlsErrorData { Reason = r, Certificate = BridgeCert }));
        var untrusted = Reason(TlsErrorReason.Untrusted);
        (string Name, EndpointTestResult? Res, ServerConfig Sc, bool Want)[] cases =
        [
            ("untrusted, starttls, password", untrusted, ImapTls, true),
            ("implicit TLS", untrusted, With(s => s with { Security = Security.Tls }), true),
            ("hostname mismatch", Reason(TlsErrorReason.HostnameMismatch), ImapTls, true),
            ("expired", Reason(TlsErrorReason.Expired), ImapTls, true),
            ("not yet valid", Reason(TlsErrorReason.NotYetValid), ImapTls, true),
            ("invalid", Reason(TlsErrorReason.Invalid), ImapTls, true),
            ("other (macOS not standards compliant)", Reason(TlsErrorReason.Other), ImapTls, true),
            ("unknown reason", Reason("fancy"), ImapTls, true),
            ("pin mismatch: trust the new certificate",
                Failed(Error(new TlsErrorData { Reason = TlsErrorReason.PinMismatch, Certificate = BridgeCert, ExpectedSha256 = SumB })),
                With(s => s with { CertificateSha256 = SumB }), true),
            ("already pinned to this certificate", untrusted, With(s => s with { CertificateSha256 = SumA.ToUpperInvariant() }), false),
            ("nil result", null, ImapTls, false),
            ("ok result", new EndpointTestResult { Ok = true, Error = untrusted.Error, LatencyMs = 1 }, ImapTls, false),
            ("no error", Failed(null), ImapTls, false),
            ("other error code", Failed(new RpcError { Code = ErrorCode.NetworkError, Message = "x", Data = Json(new TlsErrorData { Reason = TlsErrorReason.Untrusted, Certificate = BridgeCert }) }), ImapTls, false),
            ("tlsError without details", Failed(Error((System.Text.Json.JsonElement?)null)), ImapTls, false),
            ("no certificate", Failed(Error(new TlsErrorData { Reason = TlsErrorReason.Untrusted })), ImapTls, false),
            ("certificate with a bad fingerprint",
                Failed(Error(new TlsErrorData { Reason = TlsErrorReason.Untrusted, Certificate = new CertificateInfo { Sha256 = "ab" } })), ImapTls, false),
            ("handshake", Reason(TlsErrorReason.Handshake), ImapTls, false),
            ("starttls unavailable", Reason(TlsErrorReason.StarttlsUnavailable), ImapTls, false),
            ("tls required", Reason(TlsErrorReason.TlsRequired), ImapTls, false),
            ("security none", untrusted, With(s => s with { Security = Security.None }), false),
            ("security empty", untrusted, With(s => s with { Security = "" }), false),
            ("oauth2", untrusted, With(s => s with { AuthMethod = AuthMethod.OAuth2 }), false),
            ("auth method empty", untrusted, With(s => s with { AuthMethod = "" }), false),
        ];
        foreach (var (name, res, sc, want) in cases)
        {
            var p = CertTrust.Trustable(res, sc);
            Assert.True((p is not null) == want, name);
            if (p is not null)
            {
                Assert.True(p.Cert?.Sha256 == SumA, name);
            }
        }
    }

    [Fact]
    public void SameCertificate()
    {
        var b = BridgeCert with { Sha256 = SumA.ToUpperInvariant() };
        var other = BridgeCert with { Sha256 = SumB };
        var invalid = new CertificateInfo { Sha256 = "x" };
        var empty = new CertificateInfo { Sha256 = "" };
        (string Name, CertificateInfo? X, CertificateInfo? Y, bool Want)[] cases =
        [
            ("same", BridgeCert, b, true),
            ("itself", BridgeCert, BridgeCert, true),
            ("different", BridgeCert, other, false),
            ("nil", BridgeCert, null, false),
            ("both nil", null, null, false),
            ("both invalid", invalid, invalid, false),
            ("both empty", empty, empty, false),
        ];
        foreach (var (name, x, y, want) in cases)
        {
            Assert.True(CertTrust.SameCertificate(x, y) == want, name);
        }
    }

    [Fact]
    public void FromSyncState()
    {
        var untrusted = Error(new TlsErrorData { Reason = TlsErrorReason.Untrusted, Certificate = BridgeCert });
        static SyncState St(SyncStatus status, RpcError? e) => new() { AccountId = "a", Status = status, Error = e };
        (string Name, SyncState S, CertTrust.Category? Want)[] cases =
        [
            ("offline untrusted", St(SyncStatus.Offline, untrusted), CertTrust.Category.Certificate),
            ("error status counts too", St(SyncStatus.Error, untrusted), CertTrust.Category.Certificate),
            ("without certificate", St(SyncStatus.Offline, Error(new TlsErrorData { Reason = TlsErrorReason.Expired })), CertTrust.Category.Certificate),
            ("unknown reason", St(SyncStatus.Offline, Error(new TlsErrorData { Reason = "later" })), CertTrust.Category.Certificate),
            ("pin mismatch", St(SyncStatus.Offline, Error(new TlsErrorData { Reason = TlsErrorReason.PinMismatch, Certificate = BridgeCert, ExpectedSha256 = SumB })), CertTrust.Category.Changed),
            ("handshake is offline", St(SyncStatus.Offline, Error(new TlsErrorData { Reason = TlsErrorReason.Handshake })), null),
            ("starttls unavailable is offline", St(SyncStatus.Offline, Error(new TlsErrorData { Reason = TlsErrorReason.StarttlsUnavailable })), null),
            ("tls required is offline", St(SyncStatus.Offline, Error(new TlsErrorData { Reason = TlsErrorReason.TlsRequired })), null),
            ("tlsError without details", St(SyncStatus.Offline, Error((System.Text.Json.JsonElement?)null)), null),
            ("network error", St(SyncStatus.Offline, new RpcError { Code = ErrorCode.NetworkError, Message = "x" }), null),
            ("no error", St(SyncStatus.Offline, null), null),
            // Connected again: the pass runs while error still holds the
            // last failure.
            ("syncing", St(SyncStatus.Syncing, untrusted), null),
            ("idle", St(SyncStatus.Idle, untrusted), null),
            ("disabled", St(SyncStatus.Disabled, untrusted), null),
            ("auth required", St(SyncStatus.AuthRequired, untrusted), null),
        ];
        foreach (var (name, s, want) in cases)
        {
            Assert.True(CertTrust.FromSyncState(s)?.Category == want, name);
        }
    }

    [Fact]
    public void KeepPin()
    {
        var old = ImapTls with { CertificateSha256 = SumA };
        static ServerConfig With(Func<ServerConfig, ServerConfig> change) => change(ImapTls);
        (string Name, ServerConfig Old, ServerConfig Cur, string Want)[] cases =
        [
            ("unchanged", old, ImapTls, SumA),
            ("user name changed", old, With(s => s with { Username = "other" }), SumA),
            ("security tls", old, With(s => s with { Security = Security.Tls }), SumA),
            ("host case and spaces", old, With(s => s with { Host = "  Bridge.Tail.EXAMPLE " }), SumA),
            ("new config already has another pin", old, With(s => s with { CertificateSha256 = SumB }), SumA),
            ("upper-case pin is normalised", With(s => s with { CertificateSha256 = SumA.ToUpperInvariant() }), ImapTls, SumA),
            ("host changed", old, With(s => s with { Host = "bridge2.tail.example" }), ""),
            ("port changed", old, With(s => s with { Port = 993 }), ""),
            ("security none", old, With(s => s with { Security = Security.None }), ""),
            ("oauth2", old, With(s => s with { AuthMethod = AuthMethod.OAuth2 }), ""),
            ("no pin", ImapTls, ImapTls, ""),
            ("invalid pin", With(s => s with { CertificateSha256 = "abc" }), ImapTls, ""),
            // Compared as Go compares: a decomposed name is another name.
            ("composed vs decomposed host", With(s => s with { Host = "caf\x00E9.example", CertificateSha256 = SumA }),
                With(s => s with { Host = "cafe\x0301.example" }), ""),
            ("same composed host, other case", With(s => s with { Host = "caf\x00E9.example", CertificateSha256 = SumA }),
                With(s => s with { Host = "CAF\x00C9.EXAMPLE" }), SumA),
            // Windows: Go's EqualFold says no here too (U+0130 has no simple fold).
            ("dotted capital I is not i", With(s => s with { Host = "\x0130map.example", CertificateSha256 = SumA }),
                With(s => s with { Host = "imap.example" }), ""),
            // Windows: stricter than Go, whose EqualFold equates the long s
            // with s and keeps this pin (CertTrust.cs header): the user is
            // asked again, and the pin still accepts one certificate only.
            ("long s is not s", With(s => s with { Host = "\x017Fmtp.example", CertificateSha256 = SumA }),
                With(s => s with { Host = "smtp.example" }), ""),
        ];
        foreach (var (name, o, cur, want) in cases)
        {
            Assert.True(CertTrust.KeepPin(o, cur) == want, name);
        }
    }

    [Fact]
    public void ServerName()
    {
        static ServerConfig Sc(string host, int port) => new() { Host = host, Port = port, Security = Security.Tls, Username = "", AuthMethod = AuthMethod.Password };
        Assert.Equal("imap.example:993", CertTrust.ServerName(Sc("imap.example", 993)));
        Assert.Equal("100.64.0.1:1143", CertTrust.ServerName(Sc(" 100.64.0.1 ", 1143)));
        Assert.Equal("[fd7a:115c::1]:1025", CertTrust.ServerName(Sc("fd7a:115c::1", 1025)));
    }

    [Fact]
    public void IssuedTo()
    {
        static CertificateInfo C(string? subject = null, string[]? dns = null, string[]? ips = null) =>
            new() { Sha256 = SumA, Subject = subject, DnsNames = dns ?? [], IpAddresses = ips ?? [] };
        (string Name, CertificateInfo Cert, string Want)[] cases =
        [
            ("bridge: subject is the only name", BridgeCert, "127.0.0.1"),
            ("names after the subject", C("mail.example", ["MAIL.example", "imap.example", "imap.example"], ["10.0.0.1"]),
                "mail.example\nimap.example, 10.0.0.1"),
            ("no subject", C(dns: ["a.example", "b.example"]), "a.example, b.example"),
            ("nothing", C(), ""),
            ("hostile", C("x" + Rlo + "\ny", [Zwsp, "z\x0000"]), "xy\nz"),
            // Duplicates are found as Go finds them: literally, ignoring case.
            ("composed and decomposed names both stay", C("caf\x00E9", ["cafe\x0301", "CAF\x00C9"]), "caf\x00E9\ncafe\x0301"),
        ];
        foreach (var (name, cert, want) in cases)
        {
            Assert.True(CertTrust.IssuedTo(cert) == want, name);
        }
    }

    // The confirmation (accountwizard trust.go).

    [Fact]
    public void TrustPromptNamesTheServers()
    {
        var one = TrustPrompt.Create(BridgeCert, ["bridge.tail.example:1143"], NotBefore);
        Assert.Equal("Trust This Certificate?", one.Heading);
        Assert.Equal("_Trust", one.ConfirmLabel);
        Assert.Contains("accept exactly this certificate for bridge.tail.example:1143 and no other", one.Body, StringComparison.Ordinal);
        var two = TrustPrompt.Create(BridgeCert, ["h:1143", "h:1025"], NotBefore);
        Assert.Contains("for h:1143 and h:1025 and no other", two.Body, StringComparison.Ordinal);
    }

    // A pinned certificate that changed: its own heading and warning, the
    // fingerprint trusted before right after the new one.
    [Fact]
    public void TrustPromptForAChangedCertificate()
    {
        var p = TrustPrompt.Create(BridgeCert, ["h:1143", "h:1025"], NotBefore, changed: true, previous: SumB);
        Assert.Equal("Trust the New Certificate?", p.Heading);
        Assert.Equal("_Trust", p.ConfirmLabel);
        Assert.Equal(
            "The certificate of h:1143 and h:1025 has changed since you trusted it. If you did not renew it yourself, someone may be intercepting the connection. Trust the new certificate only if you know why it changed.",
            p.Body);
        Assert.Equal(
            [
                new CertificateDetail("SHA-256 fingerprint", Groups("ABAB"), Monospaced: true),
                new CertificateDetail("Previously trusted", Groups("0F0F"), Monospaced: true),
            ],
            p.Details.Take(2));
        // Without a known earlier fingerprint the line is left out.
        var unknown = TrustPrompt.Create(BridgeCert, ["h:1143"], NotBefore, changed: true, previous: "");
        Assert.Equal("Trust the New Certificate?", unknown.Heading);
        Assert.DoesNotContain(unknown.Details, d => d.Label == "Previously trusted");
    }

    [Fact]
    public void CertificateDetailsInOrder()
    {
        var now = DateTimeOffset.FromUnixTimeSeconds(1_790_000_000); // 2026
        // Noon UTC, shown in UTC: the dates are the machine's zone's elsewhere.
        var noon = BridgeCert with
        {
            NotBefore = DateTimeOffset.FromUnixTimeSeconds(1_704_196_800), // 2024-01-02T12:00:00Z
            NotAfter = DateTimeOffset.FromUnixTimeSeconds(2_335_348_800), // 2044-01-02T12:00:00Z
        };
        // The fingerprint first: nothing the certificate says can pose as it.
        var d = TrustPrompt.CertificateDetails(noon, now, culture: Posix, timeZone: TimeZoneInfo.Utc);
        Assert.Equal(["SHA-256 fingerprint", "Issued to", "Issued by", "Valid", "Self-signed"], [.. d.Select(x => x.Label)]);
        Assert.True(d[0].Value == Groups("ABAB") && d[0].Monospaced);
        Assert.Equal("127.0.0.1", d[1].Value);
        Assert.Equal("127.0.0.1", d[2].Value);
        Assert.Equal("2024-01-02 to 2044-01-02", d[3].Value);
        Assert.Empty(d[4].Value);
        Assert.Single(d, x => x.Monospaced);
        var changed = TrustPrompt.CertificateDetails(noon, now, SumB.ToUpperInvariant(), Posix, TimeZoneInfo.Utc);
        Assert.Equal(["SHA-256 fingerprint", "Previously trusted", "Issued to", "Issued by", "Valid", "Self-signed"], [.. changed.Select(x => x.Label)]);
        Assert.True(changed[1].Monospaced && changed[1].Value == Groups("0F0F"));
        Assert.True(TrustPrompt.CertificateDetails(noon, now, "nope", Posix, TimeZoneInfo.Utc).Count == 5, "an invalid earlier fingerprint is left out");

        // Empty values and unset dates are left out; not self-signed, no line.
        var bare = new CertificateInfo { Sha256 = SumB, NotAfter = NotAfter };
        Assert.Equal(["SHA-256 fingerprint"], [.. TrustPrompt.CertificateDetails(bare, now).Select(x => x.Label)]);
    }
}
