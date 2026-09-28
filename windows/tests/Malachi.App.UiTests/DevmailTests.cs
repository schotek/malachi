// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The opt-in suite (MALACHI_DEVMAIL; docs/windows-port.md §12) against
// devmail's seeded mailbox: the Inbox listed, a message in the reader, a
// reply prepared by the daemon (draft.create) in a composer, and a message
// sent from a composer arriving on the server. The seeded subjects and
// addresses are devmail's (seed.go), never translated.

using System;
using System.Linq;
using System.Windows.Automation;
using Xunit;

namespace Malachi.App.UiTests;

/// <summary>The app against a local mail server.</summary>
[Collection(OneAppAtATime.Name)]
public sealed class DevmailTests(DevmailFixture fixture) : IClassFixture<DevmailFixture>
{
    private const string Lunch = "Lunch tomorrow?";
    private const string LunchSender = "bob.smith@example.org";

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
        Assert.True(Uia.All(list, ControlType.ListItem).Count > 10);
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
