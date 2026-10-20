// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The chat layout of the conversation view (docs/plans/chat-view.md) in the
// opt-in suite (MALACHI_DEVMAIL; docs/windows-port.md §12): the folded row
// of a two-message conversation shows the chat (the setting
// conversation-chat-layout is on), with the other person's bubble on the
// left and the user's on the right, and the strip's toggle switches to the
// timeline. The conversation is ConversationChatFixture's.
//
// The quick-reply bar under the chat (docs/plans/chat-view.md §10, §11), on
// the fixture's second conversation: shown under the chat and not under the
// timeline, Send enabled once something is typed, Open in a Window moving
// the text into a compose window that asks before it closes, and Ctrl+Enter
// sending a reply that devmail delivers and stores in Sent, the field
// emptied and the reply's bubble added to the chat.

using System;
using System.Linq;
using System.Windows.Automation;
using Xunit;

namespace Malachi.App.UiTests;

/// <summary>The chat layout against a local mail server.</summary>
[Collection(OneAppAtATime.Name)]
public sealed class ConversationChatSmokeTests(ConversationChatFixture fixture) : IClassFixture<ConversationChatFixture>
{
    private const byte VkReturn = 0x0D;
    private const byte VkControl = 0x11;

    private AppSession App
    {
        get
        {
            Assert.SkipWhen(fixture.SkipReason is not null, fixture.SkipReason ?? "");
            return fixture.Session;
        }
    }

    [Fact]
    public void AConversationShowsAsAChatAndTheToggleShowsTheTimeline()
    {
        var main = App.MainWindow;
        var list = Uia.Find(main, "MessageList");
        // The sync brings the conversation into the Inbox: one folded row.
        var row = Uia.Until(
            () => Uia.All(list, ControlType.ListItem).Cast<AutomationElement>()
                .FirstOrDefault(r => r.Current.Name.Contains(fixture.Subject, StringComparison.Ordinal)),
            TimeSpan.FromSeconds(120),
            "the conversation's row");
        Uia.Select(row);

        var chat = Uia.Find(main, "ConversationChat", TimeSpan.FromSeconds(30));
        var toggle = Uia.Find(main, "ConversationChatToggle");
        Assert.Equal(ToggleState.On, ((TogglePattern)toggle.GetCurrentPattern(TogglePattern.Pattern)).Current.ToggleState);

        // Two bubbles: Ada's on the left of the chat, the user's on the right.
        var bubbles = Uia.Until(
            () =>
            {
                var found = Bubbles(chat);
                return found.Length == 2 ? found : null;
            },
            TimeSpan.FromSeconds(30),
            "the conversation's two bubbles");
        var middle = chat.Current.BoundingRectangle.Left + (chat.Current.BoundingRectangle.Width / 2);
        double Centre(AutomationElement b) => b.Current.BoundingRectangle.Left + (b.Current.BoundingRectangle.Width / 2);
        Assert.Single(bubbles, b => Centre(b) < middle);
        Assert.Single(bubbles, b => Centre(b) > middle);
        Assert.Null(Uia.TryFind(main, "ConversationTimeline"));

        // The toggle writes the setting; the timeline follows it.
        Uia.Toggle(toggle);
        Uia.WaitFor(() => Uia.TryFind(main, "ConversationTimeline") is not null, "the timeline after the toggle");
        Uia.WaitFor(() => Uia.TryFind(main, "ConversationChat") is null, "the chat to go after the toggle");
        Assert.Equal(ToggleState.Off, ((TogglePattern)toggle.GetCurrentPattern(TogglePattern.Pattern)).Current.ToggleState);
    }

    [Fact]
    public void TheQuickReplyBarSendsAndOpensTheComposeWindow()
    {
        var app = App;
        var main = app.MainWindow;
        var server = fixture.Server;
        var list = Uia.Find(main, "MessageList");
        var row = Uia.Until(
            () => Uia.All(list, ControlType.ListItem).Cast<AutomationElement>()
                .FirstOrDefault(r => r.Current.Name.Contains(fixture.ReplySubject, StringComparison.Ordinal)),
            TimeSpan.FromSeconds(120),
            "the quick-reply conversation's row");
        Uia.Select(row);

        // The chat shows (another test of the class may have left the timeline).
        var toggle = Uia.Find(main, "ConversationChatToggle", TimeSpan.FromSeconds(30));
        var togglePattern = (TogglePattern)toggle.GetCurrentPattern(TogglePattern.Pattern);
        if (togglePattern.Current.ToggleState != ToggleState.On)
        {
            Uia.Toggle(toggle);
        }
        var chat = Uia.Find(main, "ConversationChat", TimeSpan.FromSeconds(30));
        Uia.Until(() => Bubbles(chat) is { Length: 2 } found ? found : null, TimeSpan.FromSeconds(30), "the conversation's two bubbles");

        // The bar under the chat: Send off with nothing typed, on with text.
        var bar = Uia.Find(main, "QuickReplyBar", TimeSpan.FromSeconds(30));
        var field = Uia.Find(bar, "QuickReplyText");
        var send = Uia.Find(bar, "QuickReplySend");
        Assert.NotNull(Uia.TryFind(bar, "QuickReplyMode"));
        Assert.NotNull(Uia.TryFind(bar, "QuickReplyOpen"));
        Assert.False(send.Current.IsEnabled);
        Uia.SetValue(field, "Moved into a window");
        Uia.WaitFor(() => send.Current.IsEnabled, "Send to be enabled after typing");

        // Open in a Window: the compose window of the reply opens with the
        // text, the field empties, and closing the window asks (the text is
        // the user's edit); Discard lets it go.
        var mainHandle = main.Current.NativeWindowHandle;
        Uia.Invoke(Uia.Find(bar, "QuickReplyOpen"));
        var composer = Uia.Until(
            () => Uia.Windows(app.ProcessId).FirstOrDefault(w => w.Current.NativeWindowHandle != mainHandle
                && Uia.TryFind(w, "ComposeSend") is not null),
            TimeSpan.FromSeconds(30),
            "the compose window of the quick reply");
        Assert.Equal("Re: " + fixture.ReplySubject, Uia.Value(Uia.Find(composer, "ComposeSubject")));
        Uia.WaitFor(() => Uia.Value(field).Length == 0, "the bar's field to empty once the window opened");
        Uia.Close(composer);
        // "Save changes to this draft?": ContentDialog's Discard.
        Uia.Invoke(Uia.Find(composer, "SecondaryButton", TimeSpan.FromSeconds(10)));
        Uia.WaitGone(composer, what: "the compose window after Discard");

        // Ctrl+Enter sends: the field empties, devmail delivers the reply
        // (Reply-To is the seeded user) and keeps the Sent copy, and the
        // reply's bubble joins the chat.
        var inbox = server.Total("INBOX");
        var sent = server.Total("Sent");
        Uia.SetValue(field, "Quick reply " + Guid.NewGuid().ToString("N")[..8]);
        Uia.WaitFor(() => send.Current.IsEnabled, "Send to be enabled before Ctrl+Enter");
        Uia.PressKey(field, app.ProcessId, VkReturn, VkControl);
        Uia.WaitFor(() => Uia.Value(field).Length == 0, "the bar's field to empty after the send", TimeSpan.FromSeconds(30));
        Uia.WaitFor(() => server.Total("Sent") == sent + 1, "the reply's copy in the server's Sent", TimeSpan.FromSeconds(60));
        Uia.WaitFor(() => server.Total("INBOX") == inbox + 1, "the reply in the server's INBOX", TimeSpan.FromSeconds(60));
        Uia.WaitFor(() => Bubbles(chat).Length >= 3, "the reply's bubble in the chat", TimeSpan.FromSeconds(120));

        // The timeline has no bar; the chat has it again.
        Uia.Toggle(toggle);
        Uia.WaitFor(() => Uia.TryFind(main, "ConversationTimeline") is not null, "the timeline after the toggle");
        Uia.WaitFor(() => Uia.TryFind(main, "QuickReplyBar") is null, "the bar to go with the chat");
        Uia.Toggle(toggle);
        Uia.WaitFor(() => Uia.TryFind(main, "QuickReplyBar") is not null, "the bar with the chat again");
    }

    // The chat's message bubbles (ConversationBubble:<message id>).
    private static AutomationElement[] Bubbles(AutomationElement chat) =>
        [.. chat.FindAll(TreeScope.Descendants, Condition.TrueCondition)
            .Cast<AutomationElement>()
            .Where(b => b.Current.AutomationId.StartsWith("ConversationBubble:", StringComparison.Ordinal))];
}
