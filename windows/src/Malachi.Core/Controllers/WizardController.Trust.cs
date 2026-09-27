// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/WizardController.swift (the
// pinned certificates: setFields' pins, applyPins, fieldsConfig,
// pinnedFingerprints, forgetPin, trustCertificate); GTK:
// ui/internal/accountwizard/trust.go (offerFor, onTrust, onForget) and
// wizard.go (serverRows.apply, read) over ui/internal/certtrust.
//
// A pin rides in the endpoint's fields while their host and port are the
// ones it was trusted for (CertTrust.KeepPin); nothing is pinned without
// the explicit confirmation of ConfirmTrust, with the fingerprint in the
// prompt, and a confirmation that arrives after anything else started is
// dropped. The prompt's dates are formatted against the controller's clock
// and time zone.

using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Wizard;
using Microsoft.Extensions.Logging;

namespace Malachi.Core.Controllers;

public sealed partial class WizardController
{
    /// <summary>
    /// The Servers page's Forget (certtrust: Forget clears <c>pinned</c>): the
    /// endpoint verifies the server's certificate again; the next test shows
    /// whether it passes.
    /// </summary>
    public void ForgetPin(Endpoint endpoint)
    {
        scope.VerifyAccess();
        pinned.Remove(endpoint);
        ApplyPins();
    }

    /// <summary>
    /// A results row's "Trust Certificate…" (trust.go <c>onTrust</c>): asks for
    /// the confirmation with the certificate's details and, once confirmed,
    /// pins it to the endpoint it was tested with and tests again. When the
    /// other endpoint presented the same certificate (a mail bridge serving
    /// IMAP and SMTP), one confirmation names and pins both. Nothing is
    /// pinned without <see cref="ConfirmTrust"/> answering true, and a
    /// confirmation that arrives after anything else started is dropped.
    /// </summary>
    public void TrustCertificate(Endpoint endpoint)
    {
        scope.VerifyAccess();
        if (IsClosed || !trustOffers.TryGetValue(endpoint, out var offer) || offer.Problem.Cert is not { } cert
            || ConfirmTrust is not { } confirm)
        {
            return;
        }
        List<Endpoint> targets = [endpoint];
        var other = endpoint == Endpoint.Imap ? Endpoint.Smtp : Endpoint.Imap;
        if (trustOffers.TryGetValue(other, out var o) && CertTrust.SameCertificate(cert, o.Problem.Cert))
        {
            targets = [Endpoint.Imap, Endpoint.Smtp];
        }
        var offered = targets.Where(trustOffers.ContainsKey).Select(t => trustOffers[t]).ToList();
        var servers = offered.Select(t => CertTrust.ServerName(t.Server)).ToList();
        // A pinned certificate that changed gets its own warning, with the
        // fingerprint trusted before.
        var changed = offered.Any(t => t.Problem.Category == CertTrust.Category.Changed);
        var previous = changed ? offered.Select(t => t.Problem.Expected).FirstOrDefault(e => e.Length > 0) ?? "" : "";
        var prompt = TrustPrompt.Create(cert, servers, time.GetUtcNow(), changed, previous, timeZone: time.LocalTimeZone);
        var op = Op;
        scope.Perform(_ => confirm(prompt), outcome =>
        {
            if (!outcome.TryGetValue(out var confirmed, out _) || !confirmed || op != Op)
            {
                return;
            }
            foreach (var t in targets)
            {
                if (!trustOffers.TryGetValue(t, out var of) || of.Problem.Cert is not { } c)
                {
                    continue;
                }
                pinned[t] = of.Server with { CertificateSha256 = c.Sha256 };
            }
            LogTrusted(logger, targets.Count);
            ApplyPins();
            RunTest();
        });
    }

    // Stores the endpoint rows of a configuration (wizard.go
    // serverRows.apply): its pin, if any, becomes the endpoint's pinned
    // certificate and any earlier one is dropped.
    private void SetFields(AccountConfig cfg)
    {
        AccountName = cfg.Name;
        if (cfg.Imap is { } imap)
        {
            Imap = new ServerFields { Host = imap.Host, Port = imap.Port, Security = imap.Security, Username = imap.Username };
            SetPinned(Endpoint.Imap, imap);
        }
        if (cfg.Smtp is { } smtp)
        {
            Smtp = new ServerFields { Host = smtp.Host, Port = smtp.Port, Security = smtp.Security, Username = smtp.Username };
            SetPinned(Endpoint.Smtp, smtp);
        }
        ApplyPins();
    }

    private void SetPinned(Endpoint endpoint, ServerConfig server)
    {
        if (string.IsNullOrEmpty(server.CertificateSha256))
        {
            pinned.Remove(endpoint);
        }
        else
        {
            pinned[endpoint] = server;
        }
    }

    // Gives each endpoint the pin trusted for its host and port
    // (CertTrust.KeepPin) and reports a change.
    private void ApplyPins()
    {
        Imap = Imap with { CertificateSha256 = pinned.TryGetValue(Endpoint.Imap, out var i) ? CertTrust.KeepPin(i, FieldsConfig(Imap)) : "" };
        Smtp = Smtp with { CertificateSha256 = pinned.TryGetValue(Endpoint.Smtp, out var s) ? CertTrust.KeepPin(s, FieldsConfig(Smtp)) : "" };
        var now = (CertTrust.FormatFingerprint(Imap.CertificateSha256), CertTrust.FormatFingerprint(Smtp.CertificateSha256));
        if (now == PinnedFingerprints)
        {
            return;
        }
        PinnedFingerprints = now;
        PinsChanged?.Invoke(this, now);
    }

    // The configuration the rows of one endpoint stand for (without a pin:
    // KeepPin decides it).
    private static ServerConfig FieldsConfig(ServerFields f) => new()
    {
        Host = f.Host,
        Port = f.Port,
        Security = f.Security,
        Username = f.Username,
        AuthMethod = AuthMethod.Password,
    };

    [LoggerMessage(Level = LogLevel.Information, Message = "certificate trusted, endpoints {Count}")]
    private static partial void LogTrusted(ILogger logger, int count);
}
