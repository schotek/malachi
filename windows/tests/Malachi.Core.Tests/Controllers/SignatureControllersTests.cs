// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first tests of Controllers/SignatureDetailsController and
// Controllers/PgpKeysController (the flows of docs/plans/signatures.md §11.3)
// against a fake daemon: the details that start from the summary and are
// replaced by message.signature, Check Revocation with its busy button and
// the class of a failure, Import Key from the offer (and the failure's
// sentence), Confirm Key, notify.signaturesChanged's Refresh, and the key list
// of Preferences → Security with its import (the dialog's failure text), its
// confirmations and removal.

using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Signatures;
using Malachi.Core.Tests.Api;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Xunit;

namespace Malachi.Core.Tests.Controllers;

public sealed class SignatureControllersTests
{
    private const string Fp40 = "0123456789abcdef0123456789abcdef01234567";
    private static readonly DateTimeOffset Arrived = new(2026, 11, 2, 8, 0, 0, TimeSpan.Zero);

    private static SignatureDetails Smime(string revocation = RevocationState.Unchecked, bool checkable = true) => new()
    {
        Kind = SignatureKind.Smime,
        Status = SignatureStatus.Valid,
        Signer = new SignatureSigner { Name = "Jan", Email = "jan@example.org" },
        MessageId = "m_1",
        ArrivedAt = Arrived,
        Smime = new SmimeDetails
        {
            Chain = [new SignatureCertificate { Sha256 = Fp40 + "01234567890123456789012", NotBefore = Arrived, NotAfter = Arrived.AddYears(1) }],
            Revocation = new SmimeRevocation { State = revocation, Checkable = checkable },
        },
    };

    private static SignatureDetails Pgp(string trust, PgpKeyOffer? offer) => new()
    {
        Kind = SignatureKind.Pgp,
        Status = SignatureStatus.ValidSeen,
        Signer = new SignatureSigner { Email = "jan@example.org", Fingerprint = Fp40 },
        MessageId = "m_2",
        ArrivedAt = Arrived,
        Pgp = new PgpDetails { KeyTrust = trust, PrimaryFingerprint = Fp40, Importable = offer },
    };

    private static PgpKey Key(string trust = PgpKeyTrust.Seen) => new()
    {
        Fingerprint = Fp40,
        UserIds = ["Jan <jan@example.org>"],
        Emails = ["jan@example.org"],
        Trust = trust,
        Source = PgpKeySource.Import,
        FirstSeen = Arrived,
        CreatedAt = Arrived,
    };

    [Fact]
    public async Task DetailsStartFromTheSummaryAndAreReplacedByTheDaemons()
    {
        await using var h = await Harness.StartAsync();
        h.Answer(API.MessageSignature.Name, _ => JsonCoding.EncodeToString(new MessageSignatureResult { Signature = Smime() with { Algorithm = "RSA 2048 / SHA-256" } }));
        var summary = new SignatureSummary { Kind = SignatureKind.Smime, Status = SignatureStatus.Valid, PartId = "2" };
        var (c, changes) = await h.Ui.RunAsync(() =>
        {
            var controller = new SignatureDetailsController(h.Client, "m_1", summary, "jan@example.org", pending: h.Pending);
            var n = new List<int>();
            controller.Changed += (_, _) => n.Add(1);
            Assert.Null(controller.Details.Algorithm);
            Assert.Equal("2", controller.Details.PartId);
            controller.Load();
            return (controller, n);
        });
        await BoardTriageDaemon.UntilAsync(h.Ui, () => c.Details.Algorithm is not null);
        Assert.Equal("RSA 2048 / SHA-256", await h.Ui.RunAsync(() => c.Details.Algorithm));
        Assert.Equal("m_1", JsonCoding.Decode<MessageSignatureParams>(h.Params(API.MessageSignature.Name)).MessageId.Value);
        Assert.NotEmpty(changes);
        await h.Ui.RunAsync(c.Dispose);
    }

    [Fact]
    public async Task ANullAnswerAndAFailureKeepTheSummary()
    {
        await using var h = await Harness.StartAsync();
        h.Answer(API.MessageSignature.Name, _ => """{"signature":null}""");
        var summary = new SignatureSummary { Kind = SignatureKind.Pgp, Status = SignatureStatus.UnknownKey };
        var c = await h.Ui.RunAsync(() =>
        {
            var controller = new SignatureDetailsController(h.Client, "m_1", summary, "", pending: h.Pending);
            controller.Load();
            return controller;
        });
        await BoardTriageDaemon.UntilAsync(h.Ui, () => h.Count(API.MessageSignature.Name) == 1);
        await Quiescence.IdleAsync(h.Ui, h.Pending, h.Fake);
        Assert.Equal(SignatureStatus.UnknownKey, await h.Ui.RunAsync(() => c.Details.Status.Value));

        h.Fail(API.MessageSignature.Name, ErrorCode.NotImplemented);
        await h.Ui.RunAsync(c.Refresh);
        await BoardTriageDaemon.UntilAsync(h.Ui, () => h.Count(API.MessageSignature.Name) == 2);
        await Quiescence.IdleAsync(h.Ui, h.Pending, h.Fake);
        Assert.Equal(SignatureStatus.UnknownKey, await h.Ui.RunAsync(() => c.Details.Status.Value));
        Assert.Equal("", await h.Ui.RunAsync(() => c.Problem));
        await h.Ui.RunAsync(c.Dispose);
    }

    [Fact]
    public async Task CheckRevocationRunsBusyAndStoresTheAnswer()
    {
        await using var h = await Harness.StartAsync();
        var release = new ManualResetEventSlim();
        h.Answer(API.MessageSignatureRevocation.Name, _ =>
        {
            release.Wait(TimeSpan.FromSeconds(10));
            return JsonCoding.EncodeToString(new MessageSignatureResult { Signature = Smime(RevocationState.Good) });
        });
        var c = await h.Ui.RunAsync(() => new SignatureDetailsController(
            h.Client, "m_1", Smime().Let(d => new SignatureSummary { Kind = d.Kind, Status = d.Status, Signer = d.Signer }), "", pending: h.Pending));
        // Without the daemon's details the certificate is not known: nothing to check.
        await h.Ui.RunAsync(() =>
        {
            c.CheckRevocation();
            Assert.Equal(SignatureAction.None, c.Busy);
        });
        Assert.Equal(0, h.Count(API.MessageSignatureRevocation.Name));

        h.Answer(API.MessageSignature.Name, _ => JsonCoding.EncodeToString(new MessageSignatureResult { Signature = Smime() }));
        await h.Ui.RunAsync(c.Load);
        await BoardTriageDaemon.UntilAsync(h.Ui, () => SignatureView.CanCheckRevocation(c.Details));
        await h.Ui.RunAsync(() =>
        {
            c.CheckRevocation();
            Assert.Equal(SignatureAction.CheckRevocation, c.Busy);
            var view = c.View(t => t.ToString("O"));
            Assert.Equal(new SignatureButton(SignatureAction.CheckRevocation, "Checking…", false), view.Buttons[0]);
            c.CheckRevocation(); // one at a time
        });
        release.Set();
        await BoardTriageDaemon.UntilAsync(h.Ui, () => c.Busy == SignatureAction.None && c.Details.Smime!.Revocation.State.Value == RevocationState.Good);
        Assert.Equal(1, h.Count(API.MessageSignatureRevocation.Name));
        Assert.Equal("Check Revocation Again", await h.Ui.RunAsync(() => c.View(t => "").Buttons[0].Label));
        await h.Ui.RunAsync(c.Dispose);
    }

    [Fact]
    public async Task ARevocationFailureIsToldByItsClass()
    {
        await using var h = await Harness.StartAsync();
        h.Answer(API.MessageSignature.Name, _ => JsonCoding.EncodeToString(new MessageSignatureResult { Signature = Smime() }));
        var c = await h.Ui.RunAsync(() =>
        {
            var controller = new SignatureDetailsController(
                h.Client, "m_1", new SignatureSummary { Kind = SignatureKind.Smime, Status = SignatureStatus.Valid }, "", pending: h.Pending);
            controller.Load();
            return controller;
        });
        await BoardTriageDaemon.UntilAsync(h.Ui, () => SignatureView.CanCheckRevocation(c.Details));
        h.Fail(API.MessageSignatureRevocation.Name, ErrorCode.RevocationFailed, """{"reason":"timeout"}""");
        await h.Ui.RunAsync(c.CheckRevocation);
        await BoardTriageDaemon.UntilAsync(h.Ui, () => c.Problem.Length > 0);
        Assert.Equal("The certificate's issuer did not answer in time.", await h.Ui.RunAsync(() => c.Problem));
        Assert.Equal(SignatureAction.None, await h.Ui.RunAsync(() => c.Busy));
        // The button is back (Check Revocation, not Again: nothing was checked).
        Assert.Equal("Check Revocation", await h.Ui.RunAsync(() => c.View(t => "").Buttons[0].Label));
        await h.Ui.RunAsync(c.Dispose);
    }

    [Fact]
    public async Task ImportAndConfirmKeyUseTheOfferAndTheStoredKey()
    {
        await using var h = await Harness.StartAsync();
        var details = Pgp(PgpKeyTrust.Unknown, new PgpKeyOffer { Fingerprint = Fp40, Autocrypt = true });
        h.Answer(API.MessageSignature.Name, _ => JsonCoding.EncodeToString(new MessageSignatureResult { Signature = details }));
        h.Answer(API.PgpKeyImport.Name, _ =>
        {
            details = Pgp(PgpKeyTrust.Seen, null);
            return JsonCoding.EncodeToString(new PgpKeyImportResult { Keys = [Key()], Added = 1 });
        });
        h.Answer(API.PgpKeyVerify.Name, _ =>
        {
            details = Pgp(PgpKeyTrust.Verified, null);
            return JsonCoding.EncodeToString(new PgpKeyVerifyResult { Key = Key(PgpKeyTrust.Verified) });
        });
        var toasts = new List<string>();
        var c = await h.Ui.RunAsync(() =>
        {
            var controller = new SignatureDetailsController(
                h.Client, "m_2", new SignatureSummary { Kind = SignatureKind.Pgp, Status = SignatureStatus.ValidSeen }, "", pending: h.Pending);
            controller.ToastRequested += (_, text) => toasts.Add(text);
            controller.Load();
            return controller;
        });
        await BoardTriageDaemon.UntilAsync(h.Ui, () => SignatureView.ImportParams(c.Details) is not null);
        await h.Ui.RunAsync(() =>
        {
            c.ConfirmKey(); // an unknown key cannot be confirmed
            c.ImportKey();
            Assert.Equal(SignatureAction.ImportKey, c.Busy);
        });
        await BoardTriageDaemon.UntilAsync(h.Ui, () => c.Busy == SignatureAction.None && SignatureView.ConfirmParams(c.Details) is not null);
        var import = JsonCoding.Decode<PgpKeyImportParams>(h.Params(API.PgpKeyImport.Name));
        Assert.Equal(("m_2", true), (import.MessageId!.Value.Value, import.Autocrypt == true));
        Assert.Equal(["1 key imported"], toasts);
        Assert.Equal(0, h.Count(API.PgpKeyVerify.Name));

        await h.Ui.RunAsync(c.ConfirmKey);
        await BoardTriageDaemon.UntilAsync(h.Ui, () => c.Details.Pgp!.KeyTrust.Value == PgpKeyTrust.Verified);
        var verify = JsonCoding.Decode<PgpKeyVerifyParams>(h.Params(API.PgpKeyVerify.Name));
        Assert.Equal((Fp40, true), (verify.Fingerprint, verify.Verified));
        Assert.Equal(["1 key imported", "Key confirmed"], toasts);
        await h.Ui.RunAsync(c.Dispose);
    }

    [Fact]
    public async Task AnImportFailureIsToldByItsReason()
    {
        await using var h = await Harness.StartAsync();
        h.Answer(API.MessageSignature.Name, _ => JsonCoding.EncodeToString(new MessageSignatureResult { Signature = Pgp(PgpKeyTrust.Unknown, new PgpKeyOffer { Fingerprint = Fp40, PartId = "3" }) }));
        h.Fail(API.PgpKeyImport.Name, ErrorCode.InvalidArgument, """{"reason":"secretKey"}""");
        var c = await h.Ui.RunAsync(() =>
        {
            var controller = new SignatureDetailsController(
                h.Client, "m_2", new SignatureSummary { Kind = SignatureKind.Pgp, Status = SignatureStatus.UnknownKey }, "", pending: h.Pending);
            controller.Load();
            return controller;
        });
        await BoardTriageDaemon.UntilAsync(h.Ui, () => SignatureView.ImportParams(c.Details) is not null);
        await h.Ui.RunAsync(c.ImportKey);
        await BoardTriageDaemon.UntilAsync(h.Ui, () => c.Problem.Length > 0);
        Assert.StartsWith("This is a secret key.", await h.Ui.RunAsync(() => c.Problem), StringComparison.Ordinal);
        Assert.Equal("3", JsonCoding.Decode<PgpKeyImportParams>(h.Params(API.PgpKeyImport.Name)).PartId);
        await h.Ui.RunAsync(c.Dispose);
    }

    [Fact]
    public async Task TheKeyListReadsImportsAndChanges()
    {
        await using var h = await Harness.StartAsync();
        var stored = new List<PgpKey>();
        h.Answer(API.PgpKeyList.Name, _ => JsonCoding.EncodeToString(new PgpKeyListResult { Keys = [.. stored] }));
        h.Answer(API.PgpKeyImport.Name, _ =>
        {
            stored.Add(Key());
            return JsonCoding.EncodeToString(new PgpKeyImportResult { Keys = [Key()], Added = 1 });
        });
        h.Answer(API.PgpKeyVerify.Name, p =>
        {
            var verified = JsonCoding.Decode<PgpKeyVerifyParams>(p).Verified;
            stored[0] = Key(verified ? PgpKeyTrust.Verified : PgpKeyTrust.Seen);
            return JsonCoding.EncodeToString(new PgpKeyVerifyResult { Key = stored[0] });
        });
        h.Answer(API.PgpKeyRemove.Name, _ =>
        {
            stored.Clear();
            return "{}";
        });
        var toasts = new List<string>();
        var k = await h.Ui.RunAsync(() =>
        {
            var controller = new PgpKeysController(h.Client, pending: h.Pending);
            controller.ToastRequested += (_, text) => toasts.Add(text);
            controller.Refresh();
            return controller;
        });
        await BoardTriageDaemon.UntilAsync(h.Ui, () => k.Loaded);
        Assert.Empty(await h.Ui.RunAsync(() => k.Keys));

        var failure = "none";
        await h.Ui.RunAsync(() => k.Import(new PgpKeyImportParams { Armored = SignatureView.ArmorPlaceholder }, text => failure = text));
        await BoardTriageDaemon.UntilAsync(h.Ui, () => failure != "none" && k.Keys.Count == 1);
        Assert.Equal("", failure);
        Assert.Equal(["1 key imported"], toasts);
        Assert.Equal("Not confirmed", (await h.Ui.RunAsync(() => k.Rows(Arrived, t => "d")))[0].TrustPill);

        await h.Ui.RunAsync(() => k.Confirm(Fp40));
        await BoardTriageDaemon.UntilAsync(h.Ui, () => k.Keys.Count == 1 && k.Keys[0].Trust.Value == PgpKeyTrust.Verified);
        Assert.True(JsonCoding.Decode<PgpKeyVerifyParams>(h.Params(API.PgpKeyVerify.Name)).Verified);
        await h.Ui.RunAsync(() => k.Unconfirm(Fp40));
        await BoardTriageDaemon.UntilAsync(h.Ui, () => k.Keys.Count == 1 && k.Keys[0].Trust.Value == PgpKeyTrust.Seen && h.Count(API.PgpKeyVerify.Name) == 2);
        await h.Ui.RunAsync(() => k.Remove(Fp40));
        await BoardTriageDaemon.UntilAsync(h.Ui, () => k.Keys.Count == 0 && h.Count(API.PgpKeyRemove.Name) == 1);
        Assert.Equal(["1 key imported", "Key confirmed", "Confirmation removed", "Key removed"], toasts);
        await h.Ui.RunAsync(k.Dispose);
    }

    [Fact]
    public async Task AFailedListAndAFailedImportSayWhy()
    {
        await using var h = await Harness.StartAsync();
        h.Fail(API.PgpKeyList.Name, ErrorCode.Unavailable);
        h.Fail(API.PgpKeyImport.Name, ErrorCode.InvalidArgument, """{"reason":"noKey"}""");
        var k = await h.Ui.RunAsync(() =>
        {
            var controller = new PgpKeysController(h.Client, pending: h.Pending);
            controller.Refresh();
            return controller;
        });
        await BoardTriageDaemon.UntilAsync(h.Ui, () => k.Loaded);
        Assert.True(await h.Ui.RunAsync(() => k.LoadFailed));
        string? text = null;
        await h.Ui.RunAsync(() => k.Import(new PgpKeyImportParams { Armored = "x" }, t => text = t));
        await BoardTriageDaemon.UntilAsync(h.Ui, () => text is not null);
        Assert.Equal("This is not an OpenPGP public key.", text);
        await h.Ui.RunAsync(k.Dispose);
    }

    private sealed class Harness : IAsyncDisposable
    {
        private readonly Lock gate = new();
        private readonly Dictionary<string, (int Count, string Params)> seen = new(StringComparer.Ordinal);
        private readonly Dictionary<string, Func<string, string>> answers = new(StringComparer.Ordinal);
        private readonly Dictionary<string, RpcException> failures = new(StringComparer.Ordinal);

        private Harness()
        {
            foreach (var method in new[]
            {
                API.MessageSignature.Name, API.MessageSignatureRevocation.Name, API.PgpKeyImport.Name,
                API.PgpKeyVerify.Name, API.PgpKeyList.Name, API.PgpKeyRemove.Name,
            })
            {
                Fake.On(method, p =>
                {
                    Func<string, string>? answer;
                    RpcException? failure;
                    lock (gate)
                    {
                        seen[method] = (seen.GetValueOrDefault(method).Count + 1, p);
                        answers.TryGetValue(method, out answer);
                        failures.TryGetValue(method, out failure);
                    }
                    return failure is not null ? throw failure : (answer ?? (_ => "{}"))(p);
                });
            }
        }

        public FakeDaemon Fake { get; } = new();

        public RpcClient Client { get; private set; } = null!;

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public static async Task<Harness> StartAsync()
        {
            var h = new Harness();
            await h.Fake.StartAsync();
            h.Client = new RpcClient(h.Fake.Path, PortableKeyFilePolicy.Instance);
            await h.Client.ConnectAsync(TestContext.Current.CancellationToken);
            return h;
        }

        public void Answer(string method, Func<string, string> answer)
        {
            lock (gate)
            {
                answers[method] = answer;
                failures.Remove(method);
            }
        }

        public void Fail(string method, int code, string? data = null)
        {
            lock (gate)
            {
                failures[method] = new RpcException(new RpcError { Code = code, Message = "x", Data = data is null ? null : ApiJson.Parse(data) });
            }
        }

        public int Count(string method)
        {
            lock (gate)
            {
                return seen.GetValueOrDefault(method).Count;
            }
        }

        public string Params(string method)
        {
            lock (gate)
            {
                return seen[method].Params;
            }
        }

        public async ValueTask DisposeAsync()
        {
            try
            {
                await Quiescence.IdleAsync(Ui, Pending, Fake);
            }
            catch (Exception e) when (e is TimeoutException or AggregateException)
            {
                // A test's own failure, reported there.
            }
            Client.Dispose();
            await Fake.DisposeAsync();
            Ui.Dispose();
        }
    }
}

internal static class SignatureTestExtensions
{
    // A value through a function, for a one-line conversion in a test.
    public static TResult Let<T, TResult>(this T value, Func<T, TResult> convert) => convert(value);
}
