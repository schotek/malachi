// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The opt-in suite (MALACHI_DEVMAIL; docs/windows-port.md §12) against
// devmail's seeded mailbox: the Inbox listed, a message in the reader, a
// real double click on a row opening the message's window in front of the
// main window (the owner's finding of 2026-10-17: it came up behind, the
// release of the second press taking the activation back; AfterRelease), a
// reply prepared by the daemon (draft.create) in a composer, a message
// sent from a composer arriving on the server, and an invitation to the
// account itself (submitted raw over SMTP, DevmailSmtp) shown with its
// invitation card and Accept, Tentative and Decline above the header
// (docs/calendar.md §5.7), and a move to Trash taken back by Ctrl+Z and by
// the toast's Undo (Ctrl+Z), the restored row selected, while Ctrl+Z in the
// search box stays the box's own (docs/windows-port.md §11.14). The seeded subjects and addresses are devmail's
// (seed.go), never translated.

using System;
using System.Globalization;
using System.Linq;
using System.Threading;
using System.Windows.Automation;
using Xunit;

namespace Malachi.App.UiTests;

/// <summary>The app against a local mail server.</summary>
[Collection(OneAppAtATime.Name)]
public sealed class DevmailTests(DevmailFixture fixture) : IClassFixture<DevmailFixture>
{
    private const string Lunch = "Lunch tomorrow?";
    private const string LunchSender = "bob.smith@example.org";
    private const byte VkControl = 0x11;
    private const byte VkZ = 0x5A;

    // How long the double click's second press is held: longer than the
    // message window takes to show.
    private static readonly TimeSpan SecondPressHeld = TimeSpan.FromSeconds(1);

    private AppSession App
    {
        get
        {
            Assert.SkipWhen(fixture.SkipReason is not null, fixture.SkipReason ?? "");
            return fixture.Session;
        }
    }

    [Fact]
    public void TheInboxListsTheSeededMessages()
    {
        var list = Uia.Find(App.MainWindow, "MessageList");
        // The fixture waited for some rows; the rest may still be arriving.
        Uia.WaitFor(() => Uia.All(list, ControlType.ListItem).Count > 10, "more than ten rows in the Inbox");
        Assert.NotNull(Row(Lunch));
    }

    [Fact]
    public void SelectingAMessageShowsItInTheReader()
    {
        var main = App.MainWindow;
        Uia.Select(Row(Lunch));
        Uia.WaitFor(() => Uia.TryFind(main, "MessageSubject")?.Current.Name == Lunch, "the reader to show " + Lunch);
        var from = Uia.Find(main, "FromChips");
        Assert.Contains(
            Uia.All(from, ControlType.Button).Cast<AutomationElement>(),
            b => b.Current.Name.Contains(LunchSender, StringComparison.Ordinal));
    }

    [Fact]
    public void ADoubleClickOnARowOpensTheMessageWindowInFront()
    {
        var app = App;
        var main = app.MainWindow;
        var mainHandle = (nint)main.Current.NativeWindowHandle;
        var row = Row(Lunch);
        if (row.TryGetCurrentPattern(ScrollItemPattern.Pattern, out var scroll))
        {
            ((ScrollItemPattern)scroll).ScrollIntoView();
        }
        // The click must land on the row: the main window in front first.
        row.SetFocus();
        Uia.WaitFor(() => NativeMethods.GetForegroundWindow() == mainHandle, "the main window in front");
        // Off the fold arrow at the row's start, on its texts.
        var r = row.Current.BoundingRectangle;
        Uia.DoubleClick(new System.Windows.Point(r.Left + (r.Width * 0.4), r.Top + (r.Height / 2)), SecondPressHeld);

        var window = Uia.Until(
            () => Uia.Windows(app.ProcessId).FirstOrDefault(w => w.Current.NativeWindowHandle != mainHandle
                && Uia.TryFind(w, "MessageSubject")?.Current.Name == Lunch),
            null,
            "the message window of " + Lunch);
        var handle = (nint)window.Current.NativeWindowHandle;
        Uia.WaitFor(() => NativeMethods.GetForegroundWindow() == handle, "the message window in front");
        Assert.True(NativeMethods.IsAbove(handle, mainHandle), "the message window lies above the main window");
        Uia.Close(window);
        Uia.WaitGone(window, what: "the message window");
    }

    [Fact]
    public void ReplyOpensAComposerAddressedToTheSender()
    {
        var main = App.MainWindow;
        Uia.Select(Row(Lunch));
        var reply = Uia.Find(main, "ReplyButton");
        Uia.WaitFor(() => reply.Current.IsEnabled, "Reply to be enabled");
        Uia.Invoke(reply);

        var composer = Uia.WindowWith(App.ProcessId, "ComposeSend");
        Assert.Equal("Re: " + Lunch, Uia.Value(Uia.Find(composer, "ComposeSubject")));
        Assert.Contains(LunchSender, Uia.Value(Uia.Find(composer, "ComposeTo")), StringComparison.Ordinal);
        // An untouched reply closes without a question.
        Uia.Close(composer);
        Uia.WaitGone(composer, what: "the reply's composer");
    }

    [Fact]
    public void SendingDeliversTheMessageToTheServer()
    {
        var app = App;
        var server = fixture.Server;
        var inbox = server.Total("INBOX");
        var sent = server.Total("Sent");
        var subject = "UI test " + Guid.NewGuid().ToString("N")[..8];

        Uia.Invoke(Uia.Find(app.MainWindow, "NewMessageButton"));
        var composer = Uia.WindowWith(app.ProcessId, "ComposeSend");
        Uia.SetValue(Uia.Find(composer, "ComposeTo"), DevmailServer.User);
        Uia.SetValue(Uia.Find(composer, "ComposeSubject"), subject);
        Uia.Invoke(Uia.Find(composer, "ComposeSend"));
        // Queued: the composer closes (draft.save, message.send).
        Uia.WaitGone(composer, TimeSpan.FromSeconds(30), "the composer after Send");

        // Delivered to the seeded user over SMTP, and the Sent copy appended.
        Uia.WaitFor(() => server.Total("INBOX") == inbox + 1, "the message in the server's INBOX", TimeSpan.FromSeconds(60));
        Uia.WaitFor(() => server.Total("Sent") == sent + 1, "the copy in the server's Sent", TimeSpan.FromSeconds(60));
        Assert.Equal(inbox + 1, server.Total("INBOX"));
    }

    [Fact]
    public void AnInvitationShowsItsCardWithAccept()
    {
        var app = App;
        var server = fixture.Server;
        var inbox = server.Total("INBOX");
        var id = Guid.NewGuid().ToString("N")[..8];
        var subject = "Planning " + id;
        DevmailSmtp.Submit(server, Invitation(subject, id, DateTimeOffset.UtcNow.AddDays(7)));
        Uia.WaitFor(() => server.Total("INBOX") == inbox + 1, "the invitation in the server's INBOX", TimeSpan.FromSeconds(60));

        // The sync brings it into the Inbox (IDLE, else the next poll).
        var main = app.MainWindow;
        var list = Uia.Find(main, "MessageList");
        var row = Uia.Until(
            () => Uia.All(list, ControlType.ListItem).Cast<AutomationElement>()
                .FirstOrDefault(r => r.Current.Name.Contains(subject, StringComparison.Ordinal)),
            TimeSpan.FromSeconds(120),
            "the invitation's row");
        Uia.Select(row);
        Uia.WaitFor(() => Uia.TryFind(main, "MessageSubject")?.Current.Name == subject, "the reader to show the invitation");

        // The card above the header, with its pill and the answers.
        var card = Uia.Find(main, "InvitationCard", TimeSpan.FromSeconds(30));
        Assert.NotNull(Uia.Find(card, "InvitationPill"));
        var accept = Uia.Find(card, "InvitationAccept");
        Uia.WaitFor(() => accept.Current.IsEnabled, "Accept to be enabled");
        Assert.NotNull(Uia.Find(card, "InvitationTentative"));
        Assert.NotNull(Uia.Find(card, "InvitationDecline"));
    }

    [Fact]
    public void CtrlZAndTheToastsButtonTakeBackAMoveToTrash()
    {
        var app = App;
        var server = fixture.Server;
        var main = app.MainWindow;
        var list = Uia.Find(main, "MessageList");
        // A seeded message the other tests do not read.
        var row = Uia.Until(
            () => Uia.All(list, ControlType.ListItem).Cast<AutomationElement>()
                .LastOrDefault(r => !r.Current.Name.Contains(Lunch, StringComparison.Ordinal)),
            null,
            "a row other than " + Lunch);
        Uia.Select(row);
        var subject = Uia.Until(
            () => Uia.TryFind(main, "MessageSubject")?.Current.Name is { Length: > 0 } s && row.Current.Name.Contains(s, StringComparison.Ordinal) ? s : null,
            null,
            "the reader to show the chosen row");
        var inbox = server.Total("INBOX");

        // Move to Trash: the row goes and the toast offers Undo (Ctrl+Z).
        Trash(main);
        Uia.WaitFor(() => RowOf(list, subject) is null, "the row to leave the Inbox");
        var button = Uia.Find(main, "ToastButton", TimeSpan.FromSeconds(10));
        Assert.Contains("Ctrl+Z", button.Current.Name, StringComparison.Ordinal);
        Uia.WaitFor(() => server.Total("INBOX") == inbox - 1, "the move on the server", TimeSpan.FromSeconds(60));

        // Ctrl+Z in the search box is the box's own undo: the message stays in Trash.
        Uia.PressKey(Uia.Find(main, "SearchBox"), app.ProcessId, VkZ, VkControl);
        Thread.Sleep(TimeSpan.FromSeconds(2));
        Assert.Null(RowOf(list, subject));

        // Ctrl+Z on the list takes it back, selected.
        Uia.PressKey(list, app.ProcessId, VkZ, VkControl);
        var back = Uia.Until(() => RowOf(list, subject), TimeSpan.FromSeconds(30), "the row back in the Inbox");
        Uia.WaitFor(() => IsSelected(back), "the restored row selected");
        Uia.WaitFor(() => server.Total("INBOX") == inbox, "the message back on the server", TimeSpan.FromSeconds(60));

        // The toast's button does the same.
        Uia.Select(back);
        Trash(main);
        Uia.WaitFor(() => RowOf(list, subject) is null, "the row to leave the Inbox again");
        Uia.Invoke(Uia.Find(main, "ToastButton", TimeSpan.FromSeconds(10)));
        back = Uia.Until(() => RowOf(list, subject), TimeSpan.FromSeconds(30), "the row back after the toast's button");
        Uia.WaitFor(() => IsSelected(back), "the restored row selected after the toast's button");
        Uia.WaitFor(() => server.Total("INBOX") == inbox, "the message back on the server again", TimeSpan.FromSeconds(60));
    }

    // Move to Trash from the command bar, answering "Move to Trash?" when asked.
    private static void Trash(AutomationElement main)
    {
        var trash = Uia.Find(main, "TrashButton");
        Uia.WaitFor(() => trash.Current.IsEnabled, "Move to Trash to be enabled");
        Uia.Invoke(trash);
        var clock = System.Diagnostics.Stopwatch.StartNew();
        while (clock.Elapsed < TimeSpan.FromSeconds(5))
        {
            if (Uia.TryFind(main, "PrimaryButton") is { } confirm)
            {
                Uia.Invoke(confirm);
                return;
            }
            Thread.Sleep(100);
        }
    }

    private static AutomationElement? RowOf(AutomationElement list, string subject) =>
        Uia.All(list, ControlType.ListItem).Cast<AutomationElement>()
            .FirstOrDefault(r => r.Current.Name.Contains(subject, StringComparison.Ordinal));

    private static bool IsSelected(AutomationElement row) =>
        row.TryGetCurrentPattern(SelectionItemPattern.Pattern, out var p) && ((SelectionItemPattern)p).Current.IsSelected;

    // A REQUEST from an outside organiser to the seeded user, an hour long
    // at start (UTC), as a calendar client sends it: a text part and the
    // text/calendar part with method=REQUEST.
    private static string Invitation(string subject, string id, DateTimeOffset start)
    {
        static string Utc(DateTimeOffset t) => t.UtcDateTime.ToString("yyyyMMdd'T'HHmmss'Z'", CultureInfo.InvariantCulture);
        var at = new DateTimeOffset(start.Year, start.Month, start.Day, 10, 0, 0, TimeSpan.Zero);
        return string.Join("\r\n",
            "Message-ID: <invite-" + id + "@uitests.invalid>",
            "Date: " + DateTimeOffset.UtcNow.ToString("r", CultureInfo.InvariantCulture),
            "From: Ada Example <ada@example.org>",
            "To: Test User <" + DevmailServer.User + ">",
            "Subject: " + subject,
            "MIME-Version: 1.0",
            "Content-Type: multipart/alternative; boundary=\"b-" + id + "\"",
            "",
            "--b-" + id,
            "Content-Type: text/plain; charset=UTF-8",
            "",
            "You have been invited to " + subject + ".",
            "--b-" + id,
            "Content-Type: text/calendar; charset=UTF-8; method=REQUEST",
            "",
            "BEGIN:VCALENDAR",
            "VERSION:2.0",
            "PRODID:-//Malachi UI tests//EN",
            "METHOD:REQUEST",
            "BEGIN:VEVENT",
            "UID:" + id + "@uitests.invalid",
            "DTSTAMP:" + Utc(DateTimeOffset.UtcNow),
            "DTSTART:" + Utc(at),
            "DTEND:" + Utc(at.AddHours(1)),
            "SUMMARY:" + subject,
            "SEQUENCE:0",
            "ORGANIZER;CN=Ada Example:mailto:ada@example.org",
            "ATTENDEE;ROLE=REQ-PARTICIPANT;PARTSTAT=NEEDS-ACTION;RSVP=TRUE;CN=Test",
            " User:mailto:" + DevmailServer.User,
            "END:VEVENT",
            "END:VCALENDAR",
            "--b-" + id + "--",
            "");
    }

    // The Inbox row whose accessible name carries the subject.
    private AutomationElement Row(string subject)
    {
        var list = Uia.Find(App.MainWindow, "MessageList");
        return Uia.Until(
            () => Uia.All(list, ControlType.ListItem).Cast<AutomationElement>()
                .FirstOrDefault(r => r.Current.Name.Contains(subject, StringComparison.Ordinal)),
            null,
            "the row of " + subject);
    }
}
