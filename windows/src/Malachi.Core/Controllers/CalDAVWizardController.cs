// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/CalDAVWizardController.swift;
// Swift-first, no GTK controller yet: its flow, fields, texts and the
// certificate-trust rules are ui/internal/calendar/caldav.go
// (Cal.CalDAVWizard), the confirmation is the account assistant's
// (TrustPrompt, CertTrust).
//
// The Add CalDAV Calendar sheet (docs/calendar.md §5.8): owns one
// Cal.CalDAVWizard and runs its calls through the calendar source —
// Discover Calendars (account.discoverCalDAV, then
// account.listCalDAVCalendars), Add (account.add) — and the certificate
// trust: on a tlsError the wizard offers Trust Certificate…, the
// confirmation (ConfirmTrust) shows the SHA-256 fingerprint, and a
// confirmed certificate runs discovery again with the pin. The mail-account
// popup lists the accounts that can send (compose), for a server that
// leaves the invitations to the client. Nothing typed is logged; the
// password leaves only in the calls' Credentials.
//
// Windows: Swift's onChange is Changed, onDone Done, onConfirmTrust the
// ConfirmTrust hook (as WizardController's). Its mutable struct is a class
// here (Cal.CalDAVWizard): Wizard hands out a copy, so the sheet reads it
// but cannot change it behind the controller's back. Every call goes
// through the controller's ControllerScope (yield-first, docs/windows-port.md
// §7.2) with Swift's op counter; Cancel closes the scope, so discovery not
// yet sent is never sent, while an Add already started is sent all the same
// (PerformPastClose, as Swift's Task), its answer dropped. The trust
// prompt's dates are formatted against the injected TimeProvider and its
// local zone.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Transport;
using Malachi.Core.Wizard;

namespace Malachi.Core.Controllers;

/// <summary>
/// The Add CalDAV Calendar sheet's controller (Swift
/// <c>CalDAVWizardController</c>) over a <see cref="Cal.CalDAVWizard"/> and
/// the calendar source.
/// </summary>
/// <remarks>
/// Create it, and call it, on the UI thread; every event is raised there.
/// Subscribe, then call <see cref="Start"/>; the sheet's Cancel is
/// <see cref="Cancel"/>. After every <see cref="Changed"/> the sheet reads
/// <see cref="Wizard"/> (phase, fields, <see cref="Cal.CalDAVWizard.CanDiscover"/>,
/// <see cref="Cal.CalDAVWizard.CanAdd"/>, <see cref="Cal.CalDAVWizard.Progress"/>,
/// <see cref="Cal.CalDAVWizard.FoundLine"/>, <see cref="Cal.CalDAVWizard.ServerChanged"/>,
/// <see cref="Cal.CalDAVWizard.CertificateText"/>,
/// <see cref="Cal.CalDAVWizard.TrustOffer"/>,
/// <see cref="Cal.CalDAVWizard.ShowsMailAccount"/>), <see cref="ListedCalendars"/>,
/// <see cref="MailAccounts"/> and <see cref="ErrorText"/> again.
/// </remarks>
public sealed partial class CalDAVWizardController : IDisposable
{
    // Technical English of the errors made here (RpcError.Message is never shown).
    private const string PreviewCode = "preview";
    private const string TimeoutCode = "timeout";

    private readonly ControllerScope scope;
    private readonly TimeProvider time;
    private Cal.CalDAVWizard wizard;

    // Bumped per call and by Cancel so stale answers bail out.
    private int op;
    private BoardObserverToken? observer;

    // The user chose in the mail-account popup (None too).
    private bool mailAccountChosen;

    /// <summary>The sheet's controller over <paramref name="source"/>, on the calling (UI) thread.</summary>
    /// <param name="source">The calendar's data and its writes (the daemon's, or the samples', which refuse every call).</param>
    /// <param name="time">The clock of the trust prompt's dates; the system's when null.</param>
    /// <param name="pending">Counts the controller's background work; one of its own when null.</param>
    public CalDAVWizardController(ICalendarDataSource source, TimeProvider? time = null, PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(source);
        Source = source;
        this.time = time ?? TimeProvider.System;
        scope = new ControllerScope(pending);
        wizard = new Cal.CalDAVWizard(FirstOf(MailAccountsOf(source.Snapshot)));
    }

    // Outputs

    /// <summary>What the sheet shows changed: it reads the controller again (Swift <c>onChange</c>).</summary>
    public event EventHandler? Changed;

    /// <summary>The account was added; the sheet closes (Swift <c>onDone</c>).</summary>
    public event EventHandler<AccountId>? Done;

    /// <summary>
    /// Asks "Trust This Certificate?" with the certificate's details and
    /// completes with true when the user confirmed (Swift
    /// <c>onConfirmTrust</c>). Without it, or answering false, nothing is pinned.
    /// </summary>
    public Func<TrustPrompt, Task<bool>>? ConfirmTrust { get; set; }

    // State

    /// <summary>The calendar source.</summary>
    public ICalendarDataSource Source { get; }

    /// <summary>A copy of the sheet's state (its phase, fields, answers, error and texts); changed only through the controller.</summary>
    public Cal.CalDAVWizard Wizard => wizard.Clone();

    /// <summary>The last call failed with the preview's error (the samples cannot add an account).</summary>
    public bool PreviewRefused { get; private set; }

    /// <summary>Whether <see cref="Cancel"/> ran: late answers are dropped.</summary>
    public bool IsClosed => scope.IsClosed;

    /// <summary>The accounts invitations can leave from, in the accounts' order (the popup after None).</summary>
    public IReadOnlyList<MailAccount> MailAccounts => MailAccountsOf(Source.Snapshot);

    /// <summary>The sheet's error line; "" for none.</summary>
    public string ErrorText => PreviewRefused ? Cal.Text.PreviewOnly : wizard.ErrorText;

    /// <summary>The found calendars, their names cleaned, with their checkboxes.</summary>
    public IReadOnlyList<ListedCalendar> ListedCalendars =>
    [
        .. (wizard.Listed?.Calendars ?? []).Select(c => new ListedCalendar
        {
            Href = c.Href,
            Name = Cal.CleanLine(c.Name),
            Color = c.Color ?? "",
            Note = Cal.CalDAVWizard.ListedNote(c),
            Selected = wizard.IsSelected(c.Href),
        }),
    ];

    /// <summary>
    /// The accounts invitations can leave from (Swift's static
    /// <c>mailAccounts(_:)</c>): those that can send (<see cref="Capability.Compose"/>,
    /// a daemon without capabilities read as mail), not CalDAV, in the
    /// accounts' order, their names cleaned.
    /// </summary>
    public static IReadOnlyList<MailAccount> MailAccountsOf(Cal.Snapshot snapshot)
    {
        ArgumentNullException.ThrowIfNull(snapshot);
        return
        [
            .. snapshot.Accounts
                .Where(a => a.Can(Capability.Compose) && a.Config.ProtocolKind != AccountKind.CalDAV)
                .Select(a => new MailAccount(a.Id, Cal.CleanLine(a.Config.Name))),
        ];
    }

    /// <summary>
    /// Follows the source: the mail accounts arrive with its snapshot, and
    /// the first one becomes the default until the user chooses.
    /// </summary>
    public void Start()
    {
        scope.VerifyAccess();
        if (observer is not null || IsClosed)
        {
            return;
        }
        observer = Source.Observe(c =>
        {
            if (c is Cal.SourceChange.SnapshotChanged)
            {
                SnapshotChanged();
            }
        });
        SnapshotChanged();
    }

    /// <summary>The sheet closed (Cancel): nothing is taken any more; an Add already started still reaches the daemon.</summary>
    public void Cancel()
    {
        scope.VerifyAccess();
        op++;
        observer?.Cancel();
        observer = null;
        scope.Close();
    }

    /// <summary>Cancels the controller.</summary>
    public void Dispose() => Cancel();

    // Fields

    /// <summary>
    /// The four fields as typed (<see cref="Cal.CalDAVWizard.Edit"/>): a
    /// change of the server, username or password drops what was found.
    /// Ignored while a call runs.
    /// </summary>
    public void Edit(string name, string server, string username, string password)
    {
        scope.VerifyAccess();
        var before = (wizard.Name, wizard.Server, wizard.Username, wizard.Password, wizard.Pin, wizard.Phase);
        wizard.Edit(name, server, username, password);
        if ((wizard.Name, wizard.Server, wizard.Username, wizard.Password, wizard.Pin, wizard.Phase) != before)
        {
            PreviewRefused = false;
            RaiseChanged();
        }
    }

    /// <summary>A found calendar's checkbox.</summary>
    public void Toggle(string href, bool on)
    {
        scope.VerifyAccess();
        wizard.Toggle(href, on);
        RaiseChanged();
    }

    /// <summary>The mail-account popup; null is None. The choice stays when the accounts change.</summary>
    public void SetMailAccount(AccountId? id)
    {
        scope.VerifyAccess();
        mailAccountChosen = true;
        wizard.SetMailAccount(id);
        RaiseChanged();
    }

    // Calls

    /// <summary>Discover Calendars: account.discoverCalDAV, then account.listCalDAVCalendars.</summary>
    public void Discover()
    {
        scope.VerifyAccess();
        if (IsClosed || !wizard.BeginDiscover())
        {
            return;
        }
        PreviewRefused = false;
        RaiseChanged();
        RunDiscovery();
    }

    /// <summary>
    /// Trust Certificate…: the confirmation with the offered certificate's
    /// details (<see cref="ConfirmTrust"/>), then discovery again with the
    /// pin. A confirmation that arrives after anything else started is dropped.
    /// </summary>
    public void Trust()
    {
        scope.VerifyAccess();
        if (IsClosed || wizard.TrustOffer is not { Cert: { } cert } offer || ConfirmTrust is not { } confirm)
        {
            return;
        }
        var prompt = TrustPrompt.Create(
            cert, [Cal.CalDAVWizard.Host(wizard.ServerUrl)], time.GetUtcNow(), offer.Category == CertTrust.Category.Changed,
            offer.Expected, timeZone: time.LocalTimeZone);
        var current = op;
        scope.Perform(_ => confirm(prompt), outcome =>
        {
            if (!outcome.TryGetValue(out var ok, out _) || !ok || current != op || !wizard.Trust())
            {
                return;
            }
            RaiseChanged();
            RunDiscovery();
        });
    }

    /// <summary>Add: account.add with the found calendars as checked; <see cref="Done"/> once it is stored.</summary>
    public void Add()
    {
        scope.VerifyAccess();
        if (IsClosed || !wizard.BeginAdd())
        {
            return;
        }
        PreviewRefused = false;
        RaiseChanged();
        op++;
        var current = op;
        var parameters = wizard.AddParams;
        // A mutation: sent even when the sheet is cancelled meanwhile (Swift's Task).
        scope.PerformPastClose(() => Source.AddAccountAsync(parameters), outcome =>
        {
            if (current != op)
            {
                return;
            }
            var error = outcome.Error is { } e ? RpcErrorOf(e) : null;
            var id = error is null ? outcome.Value?.AccountId : null;
            wizard.AddDone(id, error);
            RaiseChanged();
            if (id is { } added && error is null)
            {
                scope.Raise(Done, this, added);
            }
        });
    }

    // The calls of the discovering phase: discovery, then the list. Neither
    // is sent once the sheet was cancelled (the scope's Lifetime).
    private void RunDiscovery()
    {
        op++;
        var current = op;
        var parameters = wizard.DiscoverParams;
        scope.Perform(ct => Read(() => Source.DiscoverCalDAVAsync(parameters), ct), outcome =>
        {
            if (current != op)
            {
                return;
            }
            var error = outcome.Error is { } e ? RpcErrorOf(e) : null;
            wizard.DiscoverDone(error is null ? outcome.Value : null, error);
            RaiseChanged();
            if (error is not null || wizard.Phase != Cal.CalDAVWizard.WizardPhase.Discovering
                || wizard.Step != Cal.CalDAVWizard.WizardStep.List)
            {
                return;
            }
            var list = wizard.ListParams;
            scope.Perform(ct => Read(() => Source.ListCalDAVCalendarsAsync(list), ct), listed =>
            {
                if (current != op)
                {
                    return;
                }
                var listError = listed.Error is { } le ? RpcErrorOf(le) : null;
                wizard.ListDone(listError is null ? listed.Value : null, listError);
                RaiseChanged();
            });
        });
    }

    // A call of the source that is not made once the controller closed (the
    // source's calls take no token).
    private static Task<T> Read<T>(Func<Task<T>> call, CancellationToken lifetime)
    {
        lifetime.ThrowIfCancellationRequested();
        return call();
    }

    // Any error as the wizard takes it (Swift rpcError): the daemon's as it
    // is, the preview's as notImplemented (and PreviewRefused), a timeout as
    // serverTimeout, anything else as networkError.
    private RpcError RpcErrorOf(Exception error)
    {
        switch (error)
        {
            case RpcException e:
                return e.Error;
            case Cal.SourceError { Kind: Cal.SourceErrorKind.Preview }:
                PreviewRefused = true;
                return new RpcError { Code = ErrorCode.NotImplemented, Message = PreviewCode };
            case RpcClientException { Error.Kind: ClientErrorKind.Timeout }:
            case TimeoutException:
                return new RpcError { Code = ErrorCode.ServerTimeout, Message = TimeoutCode };
            default:
                return new RpcError { Code = ErrorCode.NetworkError, Message = error.Message };
        }
    }

    // The first mail account, the popup's default; null for none.
    private static AccountId? FirstOf(IReadOnlyList<MailAccount> accounts) => accounts.Count > 0 ? accounts[0].Id : default(AccountId?);

    // The follow-up of the source's snapshot: the first mail account is the
    // default until the user chose.
    private void SnapshotChanged()
    {
        if (!mailAccountChosen)
        {
            var first = FirstOf(MailAccounts);
            if (wizard.MailAccount != first)
            {
                wizard.SetMailAccount(first);
            }
        }
        RaiseChanged();
    }

    // Raises Changed unless cancelled, every handler guarded (ControllerEvents).
    private void RaiseChanged()
    {
        if (IsClosed || Changed is not { } handlers)
        {
            return;
        }
        foreach (var handler in handlers.GetInvocationList())
        {
            scope.Guard(() => ((EventHandler)handler).Invoke(this, EventArgs.Empty));
        }
    }
}
