// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The chat suite's world (MALACHI_DEVMAIL; docs/plans/chat-view.md):
// devmail on ports of its own with a two-message conversation submitted
// before the account is added (a message from someone else, and the user's
// reply to it in the Inbox as well, so that the conversation has a member
// on either side without a send), the app started with the list grouped by
// conversation and the conversation shown as a chat
// (group-by-conversation, conversation-chat-layout), and the devmail
// account added as DevmailFixture adds it. A second conversation of the same
// shape is the quick-reply bar's (ReplySubject), so that its sent reply
// does not add a bubble to the first one: its first message, from Ada, has
// Reply-To the user, so the quick reply goes to the seeded mailbox and
// devmail delivers it as it delivers the suite's other sends. A lone message
// of the same kind (SingleSubject) is the bar's under a single message
// (docs/plans/chat-view.md §12): a plain row until its quick reply lands in
// Sent and makes it a conversation.

using System;
using System.Globalization;
using System.IO;
using System.Linq;
using System.Threading.Tasks;
using System.Windows.Automation;
using Microsoft.Win32;
using Xunit;

namespace Malachi.App.UiTests;

/// <summary>devmail with a conversation, the app showing conversations as a chat.</summary>
public sealed class ConversationChatFixture : IAsyncLifetime
{
    private DevmailServer? server;
    private AppSession? session;

    /// <summary>Why the suite does not run; null when it does.</summary>
    public string? SkipReason { get; private set; }

    /// <summary>The conversation's subject (its first message's; the reply's has "Re: ").</summary>
    public string Subject { get; } = "Chat " + Guid.NewGuid().ToString("N")[..8];

    /// <summary>The quick-reply bar's conversation's subject (its first message's).</summary>
    public string ReplySubject { get; } = "Quick " + Guid.NewGuid().ToString("N")[..8];

    /// <summary>The lone message's subject: the quick-reply bar under a single message.</summary>
    public string SingleSubject { get; } = "Single " + Guid.NewGuid().ToString("N")[..8];

    /// <summary>devmail (the quick-reply test counts the server's Sent).</summary>
    internal DevmailServer Server => server ?? throw new InvalidOperationException(SkipReason);

    /// <summary>The app.</summary>
    internal AppSession Session => session ?? throw new InvalidOperationException(SkipReason);

    /// <inheritdoc/>
    public async ValueTask InitializeAsync()
    {
        SkipReason = UiEnvironment.SkipReason;
        if (SkipReason is null)
        {
            var exe = DevmailServer.Executable;
            if (exe is null)
            {
                SkipReason = $"opt-in: set {DevmailServer.FolderEnv} to a folder with devmail.exe to run the tests against a local mail server";
            }
            else if (!File.Exists(exe))
            {
                SkipReason = $"{DevmailServer.FolderEnv} names no devmail.exe: {exe}";
            }
        }
        if (SkipReason is not null)
        {
            return;
        }
        try
        {
            var devmail = server = DevmailServer.Start(Path.Combine(DevmailFixture.RepositoryRoot, "backend", "testdata", "mime"));
            var inbox = devmail.Total("INBOX");
            var id = Subject[5..];
            var first = "<chat-" + id + "@uitests.invalid>";
            DevmailSmtp.Submit(devmail, Message(first, null, "Ada Example <ada@example.org>", "Test User <" + DevmailServer.User + ">", Subject, "Shall we meet on Friday?", DateTimeOffset.UtcNow.AddMinutes(-10)));
            DevmailSmtp.Submit(devmail, Message("<chat-reply-" + id + "@uitests.invalid>", first, "Test User <" + DevmailServer.User + ">", "Ada Example <ada@example.org>, Test User <" + DevmailServer.User + ">", "Re: " + Subject, "Friday works for me.", DateTimeOffset.UtcNow.AddMinutes(-5)));
            var quick = ReplySubject[6..];
            var quickFirst = "<quick-" + quick + "@uitests.invalid>";
            DevmailSmtp.Submit(devmail, Message(quickFirst, null, "Ada Example <ada@example.org>", "Test User <" + DevmailServer.User + ">", ReplySubject, "Can you send me the agenda?", DateTimeOffset.UtcNow.AddMinutes(-9), replyTo: "Test User <" + DevmailServer.User + ">"));
            DevmailSmtp.Submit(devmail, Message("<quick-reply-" + quick + "@uitests.invalid>", quickFirst, "Test User <" + DevmailServer.User + ">", "Ada Example <ada@example.org>, Test User <" + DevmailServer.User + ">", "Re: " + ReplySubject, "Let me look for it.", DateTimeOffset.UtcNow.AddMinutes(-8)));
            var single = SingleSubject[7..];
            DevmailSmtp.Submit(devmail, Message("<single-" + single + "@uitests.invalid>", null, "Ada Example <ada@example.org>", "Test User <" + DevmailServer.User + ">", SingleSubject, "Are you coming tomorrow?", DateTimeOffset.UtcNow.AddMinutes(-7), replyTo: "Test User <" + DevmailServer.User + ">"));
            Uia.WaitFor(() => devmail.Total("INBOX") == inbox + 5, "the conversations in the server's INBOX", TimeSpan.FromSeconds(60));

            session = AppSession.Start(preferences: k =>
            {
                k.SetValue("group-by-conversation", 1, RegistryValueKind.DWord);
                k.SetValue("conversation-chat-layout", 1, RegistryValueKind.DWord);
            });
            session.WaitForDaemon();
            await DevmailFixture.AddAccountAsync(session.Socket, devmail);
            var list = Uia.Find(session.MainWindow, "MessageList", AppSession.StartTimeout);
            Uia.WaitFor(() => Uia.All(list, ControlType.ListItem).Count > 5, "the Inbox's messages", AppSession.StartTimeout);
        }
        catch
        {
            await DisposeAsync();
            session = null;
            server = null;
            throw;
        }
    }

    /// <inheritdoc/>
    public ValueTask DisposeAsync()
    {
        session?.Dispose();
        server?.Dispose();
        return ValueTask.CompletedTask;
    }

    // A plain-text message; a reply names the message it answers, and a
    // message may name where replies go.
    private static string Message(string messageId, string? inReplyTo, string from, string to, string subject, string text, DateTimeOffset date, string? replyTo = null)
    {
        var lines = new[]
        {
            "Message-ID: " + messageId,
            "Date: " + date.ToString("r", CultureInfo.InvariantCulture),
            "From: " + from,
            "To: " + to,
            "Subject: " + subject,
        }.ToList();
        if (replyTo is not null)
        {
            lines.Add("Reply-To: " + replyTo);
        }
        if (inReplyTo is not null)
        {
            lines.Add("In-Reply-To: " + inReplyTo);
            lines.Add("References: " + inReplyTo);
        }
        lines.AddRange(["MIME-Version: 1.0", "Content-Type: text/plain; charset=UTF-8", "", text, ""]);
        return string.Join("\r\n", lines);
    }
}
