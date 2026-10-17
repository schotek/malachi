// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the Integration extension of
// macos/Sources/MalachiMail/App/Integration+Calendar.swift (joinVideoCall;
// alertAnswered is Platform/CalendarAlertNotifier.Answer) and of
// MainWindow/MainWindowController+Calendar.swift (calendarSignIn); GTK: the
// calendar's window code follows (docs/calendar.md §6.4).
//
// The calendar's ways into the shell that need the main window's
// controllers: Join of an alert asks event.get for the event's video call
// (the link is never in the toast) over the main window's calendar source,
// started for it as macOS does, and opens it through the reader's link
// confirmation as an unlisted link ("Open This Link?"); a failure is a toast
// over the main window. Sign In… of a calendar account's state row asks
// account.oauthStart {accountId, calendar: true} (through
// CalendarAccountController.RequestSignInUrlAsync, the Core half the
// Preferences use too) and opens the page in the browser, as the mail's
// re-sign-in does (Main/AccountRepair.cs); the daemon completes the sign-in
// by itself, and its notify.accountsChanged and notify.calendarState reach
// the sources through the hub (AppState.Calendar.cs). The samples answer
// "The preview cannot do that."

using System;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Transport;

namespace Malachi.App.Shell;

/// <summary>The calendar of the Integration.</summary>
public sealed partial class Integration
{
    /// <summary>
    /// Join of an event's alert (macOS <c>joinVideoCall</c>): the event's
    /// video call through the link confirmation of the main window.
    /// </summary>
    public void JoinVideoCall(CalendarEventId eventId, DateOrTime? recurrenceId) => _ = JoinVideoCallAsync(eventId, recurrenceId);

    /// <summary>
    /// Sign In… of a calendar account whose state needs a new sign-in (the
    /// calendar controller's SignInRequested; macOS <c>calendarSignIn</c>):
    /// the re-sign-in page with the calendar scopes, in the browser.
    /// </summary>
    public void CalendarSignIn(AccountId account) => _ = CalendarSignInAsync(account);

    private async Task JoinVideoCallAsync(CalendarEventId eventId, DateOrTime? recurrenceId)
    {
        mainWindow.StartCalendar();
        var source = state.CalendarSourceFor(mainWindow);
        CalendarEvent e;
        try
        {
            e = await source.EventAsync(eventId, recurrenceId);
        }
        catch (Exception error) when (error is RpcException or RpcClientException or TimeoutException or OperationCanceledException or Cal.SourceError)
        {
            mainWindow.Toasts.Show(Cal.ErrorMessage(error));
            return;
        }
        if (e.VideoCallLink?.Url is not { Length: > 0 } url || Reader is not { } reader || state.IsStopping)
        {
            return;
        }
        state.ShowMainWindow();
        await reader.Services.Links.OpenAsync(new ActivatedLink(url, url), [], mainWindow);
    }

    private async Task CalendarSignInAsync(AccountId account)
    {
        SignInUrl outcome;
        using (var c = new CalendarAccountController(state.CalendarSourceFor(mainWindow), account, state.CalendarSamples ? null : state.Client))
        {
            outcome = await c.RequestSignInUrlAsync();
        }
        switch (outcome)
        {
            case SignInUrl.Open open:
                try
                {
                    await state.Launcher.OpenUrlAsync(open.Url, WindowPresenter.Handle(mainWindow));
                }
                catch (Exception e) when (e is ArgumentException or InvalidOperationException or System.ComponentModel.Win32Exception)
                {
                    var detail = e is ArgumentException ? "not an https address" : e.Message; // Windows-only string (the technical detail, as in GTK)
                    // TRANSLATORS: %s is a technical error message.
                    mainWindow.Toasts.Show(L10n.T("The link could not be opened: %s", detail));
                }
                break;
            case SignInUrl.Failed failed:
                mainWindow.Toasts.Show(failed.Text);
                break;
        }
    }
}
