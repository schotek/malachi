// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The chat layout of the conversation view (docs/plans/chat-view.md) in the
// opt-in suite (MALACHI_DEVMAIL; docs/windows-port.md §12): the folded row
// of a two-message conversation shows the chat (the setting
// conversation-chat-layout is on), with the other person's bubble on the
// left and the user's on the right, and the strip's toggle switches to the
// timeline. The conversation is ConversationChatFixture's.

using System;
using System.Linq;
using System.Windows.Automation;
using Xunit;

namespace Malachi.App.UiTests;

/// <summary>The chat layout against a local mail server.</summary>
[Collection(OneAppAtATime.Name)]
public sealed class ConversationChatSmokeTests(ConversationChatFixture fixture) : IClassFixture<ConversationChatFixture>
{
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

    // The chat's message bubbles (ConversationBubble:<message id>).
    private static AutomationElement[] Bubbles(AutomationElement chat) =>
        [.. chat.FindAll(TreeScope.Descendants, Condition.TrueCondition)
            .Cast<AutomationElement>()
            .Where(b => b.Current.AutomationId.StartsWith("ConversationBubble:", StringComparison.Ordinal))];
}
