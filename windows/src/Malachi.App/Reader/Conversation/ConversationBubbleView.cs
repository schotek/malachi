// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageView/ConversationBubbleView.swift
// (ConversationBubbleView, ConversationClipPill); GTK:
// ui/internal/window/conversation_chat_row.go (chatBubble, newChatBubble,
// showMessage, showDraft, showBody, the clip pill). A bubble of the chat
// layout (docs/plans/chat-view.md D3, D6, D14, D15): a message of the
// conversation, or the user's draft, as plain text only. The header has
// the unread dot (only of someone else's message), the sender, a paperclip
// pill (from the first moment when the summary has attachments, with the
// number of the timeline's chips once message.get told it,
// Conversation.BubbleAttachmentBadge), the Draft and Internal badges of a
// draft, the Internal badge, who relayed it ("via …") and Edited of an
// issue's comment, and at the trailing end the date (the list's short form
// in a narrow pane, the full one in the tooltip) or, for a draft, when it
// was saved. Under it the text: the summary's snippet, dimmed, until the
// body is there (Conversation.BubbleBodyFor), then the message's text
// without its quoted history, or the draft's.
//
// Every text is plain (TextBlock.Text) and none can be selected: a double
// tap anywhere in the bubble activates it (Activate: the message in its own
// window, a draft in the compose window, once the press is released; the
// chat sets it), where everything else is. No buttons, no links, no context
// menu, no keyboard stop. The bubble is a card on the pane's grey (a
// hairline, rounded corners); the user's own is tinted with the accent
// colour and has a border of it (ConversationStyles.xaml). It hugs its
// text, never wider than the row allows (MaxWidth, set by the row from
// ConversationLayout.ChatBubbleMax) and never narrower than its header,
// whose sender is cut first when the row is too narrow for it: aligned to
// its side, not stretched, the control is as wide as its content measures.
// The bubble is one automation group named by its sender and date, as a
// card is, and "Has attachments" while the paperclip shows (the pill, a
// Border, is no automation element); its AutomationId is
// "ConversationBubble:" and the message's id, or "ConversationDraftBubble:"
// and the draft's.

using System;
using System.Linq;
using Malachi.App.Controls;
using Malachi.App.Resources;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Shapes;

namespace Malachi.App.Reader.Conversation;

/// <summary>A bubble of the chat layout: a message or a draft as plain text.</summary>
internal sealed partial class ConversationBubbleView : UserControl
{
    /// <summary>The AutomationId's prefix of a message's bubble.</summary>
    public const string MessagePrefix = "ConversationBubble:";

    /// <summary>The AutomationId's prefix of a draft's bubble.</summary>
    public const string DraftPrefix = "ConversationDraftBubble:";

    // The padding inside the bubble from its edge (macOS paddingH, paddingV),
    // the border's pixel included.
    private const double PaddingH = 13;
    private const double PaddingV = 9;

    // The widest the name of who relayed a comment gets (as the event row's
    // sender).
    private const double ViaMaxWidth = 200;

    private readonly Func<string, Style> look;
    private readonly Ellipse unread;
    private readonly TextBlock senderLabel;
    private readonly Border clipPill;
    private readonly TextBlock clipCount;
    private readonly IssuePill draftPill;
    private readonly IssuePill internalPill;
    private readonly TextBlock viaLabel;
    private readonly TextBlock editedLabel;
    private readonly StackPanel badges;
    private readonly TextBlock dateLabel;
    private readonly TextBlock bodyLabel;

    // What the header names for automation and the date it shows; a draft's
    // "Draft saved …" in the date's place.
    private string senderName = "";
    private DateTimeOffset? date;
    private string? saved;
    private bool compactDate;
    private BubbleBody? shownBody;
    private double fontSize;
    private FontFamily? monospaceFamily;
    private bool monospace;

    /// <summary>
    /// A bubble, the user's own (<paramref name="mine"/>) or not, styled from
    /// <paramref name="look"/>; its text in <paramref name="fontSize"/> and,
    /// where it is monospaced, in <paramref name="monospaceFamily"/>.
    /// </summary>
    public ConversationBubbleView(bool mine, Func<string, Style> look, double fontSize, FontFamily? monospaceFamily)
    {
        ArgumentNullException.ThrowIfNull(look);
        Mine = mine;
        this.look = look;
        this.fontSize = fontSize;
        this.monospaceFamily = monospaceFamily;
        IsTabStop = false;

        unread = new Ellipse
        {
            Style = look("ConversationUnreadStyle"),
            Margin = new Thickness(0, 0, 6, 0),
            Visibility = Visibility.Collapsed,
        };
        senderLabel = new TextBlock { Style = look("ConversationSenderStyle") };

        // The paperclip pill: the icon alone, or with the number.
        clipCount = new TextBlock { Style = look("ConversationClipCountStyle"), Visibility = Visibility.Collapsed };
        var clipRow = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 2 };
        clipRow.Children.Add(new FontIcon { Style = look("ConversationClipIconStyle"), Glyph = Icons.Glyph("mail-attachment") });
        clipRow.Children.Add(clipCount);
        clipPill = new Border { Style = look("ConversationClipPillStyle"), Child = clipRow, Visibility = Visibility.Collapsed };
        // A Border is no automation element: the bubble's name says it
        // (RenderDate).
        ToolTipService.SetToolTip(clipPill, L10n.T("Has attachments"));

        draftPill = new IssuePill { IsDraft = true, Text = "" };
        internalPill = new IssuePill { IsInternal = true };
        viaLabel = new TextBlock
        {
            Style = look("ConversationCaptionStyle"),
            VerticalAlignment = VerticalAlignment.Center,
            MaxWidth = ViaMaxWidth,
            MaxLines = 1,
            TextTrimming = TextTrimming.CharacterEllipsis,
            TextWrapping = TextWrapping.NoWrap,
            Visibility = Visibility.Collapsed,
        };
        editedLabel = new TextBlock
        {
            Style = look("ConversationCaptionStyle"),
            VerticalAlignment = VerticalAlignment.Center,
            Visibility = Visibility.Collapsed,
        };
        badges = new StackPanel
        {
            Orientation = Orientation.Horizontal,
            Spacing = 6,
            Margin = new Thickness(6, 0, 0, 0),
            VerticalAlignment = VerticalAlignment.Center,
        };
        badges.Children.Add(clipPill);
        badges.Children.Add(draftPill);
        badges.Children.Add(internalPill);
        badges.Children.Add(viaLabel);
        badges.Children.Add(editedLabel);

        // The dot, the sender (which gives way first) and the badges right
        // after it, however wide the bubble is; the date at the trailing
        // end.
        var lead = new Grid { HorizontalAlignment = HorizontalAlignment.Left };
        lead.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        lead.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        lead.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        Grid.SetColumn(senderLabel, 1);
        Grid.SetColumn(badges, 2);
        lead.Children.Add(unread);
        lead.Children.Add(senderLabel);
        lead.Children.Add(badges);
        dateLabel = new TextBlock
        {
            Style = look("ConversationCaptionStyle"),
            Margin = new Thickness(12, 0, 0, 0),
            VerticalAlignment = VerticalAlignment.Center,
            MaxLines = 1,
            TextWrapping = TextWrapping.NoWrap,
        };
        var header = new Grid();
        header.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        header.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        Grid.SetColumn(dateLabel, 1);
        header.Children.Add(lead);
        header.Children.Add(dateLabel);

        bodyLabel = new TextBlock
        {
            Style = look("ConversationBubblePlaceholderStyle"),
            Margin = new Thickness(0, 6, 0, 0),
            Visibility = Visibility.Collapsed,
        };
        ApplyFont();

        var column = new StackPanel();
        column.Children.Add(header);
        column.Children.Add(bodyLabel);
        // Border is sealed in WinUI, so the bubble hosts its frame; the
        // user's own has the tint as an inner layer over the card's fill.
        var inner = new Border
        {
            Padding = new Thickness(PaddingH - 1, PaddingV - 1, PaddingH - 1, PaddingV - 1),
            Child = column,
        };
        if (mine)
        {
            inner.Style = look("ConversationMineTintStyle");
        }
        Content = new Border
        {
            Style = look(mine ? "ConversationMineBubbleStyle" : "ConversationBubbleStyle"),
            Child = inner,
        };
        DoubleTapped += OnDoubleTapped;
    }

    /// <summary>The user's own message or draft (on the right, tinted).</summary>
    public bool Mine { get; }

    /// <summary>A double tap anywhere in the bubble (the chat opens the message or edits the draft).</summary>
    public Action? Activate { get; set; }

    /// <summary>The text shows a body (not the placeholder): a let-go entry keeps it (<see cref="ShowBody"/>).</summary>
    public bool ShowsText { get; private set; }

    /// <summary>The text shows in the monospaced font.</summary>
    public bool IsMonospace => monospace;

    /// <summary>The short date of the list instead of the full date and time (a narrow column, <see cref="ConversationLayout.ChatCompactDates"/>).</summary>
    public bool CompactDate
    {
        get => compactDate;
        set
        {
            if (value != compactDate)
            {
                compactDate = value;
                RenderDate();
            }
        }
    }

    /// <summary>
    /// The header of a message (<paramref name="item"/>, as the model has it
    /// now); <paramref name="attachments"/> is its paperclip pill
    /// (<see cref="Core.Model.Conversation.BubbleAttachmentBadge"/>).
    /// </summary>
    public void ShowMessage(ConversationItem item, BubbleAttachments attachments)
    {
        ArgumentNullException.ThrowIfNull(item);
        AutomationProperties.SetAutomationId(this, MessagePrefix + (item.Message?.Id.ToString() ?? ""));
        ShowSender(item.Sender);
        unread.Visibility = item.Unread && !Mine ? Visibility.Visible : Visibility.Collapsed;
        ShowClip(attachments);
        draftPill.Text = "";
        internalPill.Text = item.Internal ? item.InternalLabel : "";
        viaLabel.Text = item.Via;
        ToolTipService.SetToolTip(viaLabel, item.Via.Length > 0 ? item.Via : null);
        viaLabel.Visibility = item.Via.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        editedLabel.Text = item.Edited;
        editedLabel.Visibility = item.Edited.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        ShowBadges();
        date = item.Message?.Date;
        saved = null;
        RenderDate();
    }

    /// <summary>
    /// A draft of the user's (D14): its header with the Draft badge, the
    /// Internal one of an internal comment and when it was saved, and its
    /// text, monospaced with the setting for plain text
    /// (<paramref name="monospaceText"/>), as the timeline's draft card.
    /// </summary>
    public void ShowDraft(ConversationDraftCard card, bool monospaceText)
    {
        ArgumentNullException.ThrowIfNull(card);
        AutomationProperties.SetAutomationId(this, DraftPrefix + (card.Draft.Id?.ToString() ?? ""));
        ShowSender(card.Sender);
        unread.Visibility = Visibility.Collapsed;
        ShowClip(new BubbleAttachments.None());
        draftPill.Text = Core.Model.Conversation.DraftBadge();
        internalPill.Text = card.Internal ? card.InternalLabel : "";
        viaLabel.Visibility = Visibility.Collapsed;
        editedLabel.Visibility = Visibility.Collapsed;
        ShowBadges();
        date = null;
        saved = card.Draft.UpdatedAt.IsGoZero ? "" : Core.Model.Conversation.DraftSaved(Format.FormatTime(card.Draft.UpdatedAt));
        RenderDate();
        ShowBody(new BubbleBody.Text(card.Text), monospaceText);
    }

    /// <summary>
    /// The text under the header: a placeholder (dimmed), or the text; an
    /// empty one hides it. A placeholder does not replace a text shown
    /// already (the controller let go of the body, or fetches it again: the
    /// bubble keeps what it read). <paramref name="monospaceText"/> picks the
    /// monospaced font for the text (never for a placeholder).
    /// </summary>
    public void ShowBody(BubbleBody body, bool monospaceText)
    {
        ArgumentNullException.ThrowIfNull(body);
        if (body is BubbleBody.Placeholder && ShowsText)
        {
            return;
        }
        var mono = monospaceText && body is BubbleBody.Text;
        if (mono != monospace)
        {
            monospace = mono;
            ApplyFont();
        }
        if (body == shownBody)
        {
            return;
        }
        shownBody = body;
        switch (body)
        {
            case BubbleBody.Placeholder p:
                ShowsText = false;
                bodyLabel.Style = look("ConversationBubblePlaceholderStyle");
                bodyLabel.Text = p.Value;
                bodyLabel.Visibility = p.Value.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
                break;
            case BubbleBody.Text t:
                ShowsText = true;
                bodyLabel.Style = look("ConversationBubbleTextStyle");
                bodyLabel.Text = t.Value;
                bodyLabel.Visibility = t.Value.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
                break;
        }
    }

    /// <summary>The monospace setting changed: the text in the font it asks for (never a placeholder's).</summary>
    public void SetMonospace(bool on)
    {
        var mono = on && ShowsText;
        if (mono == monospace)
        {
            return;
        }
        monospace = mono;
        ApplyFont();
    }

    /// <summary>The text-zoom setting changed (or the monospaced family): the text's size and families anew.</summary>
    public void SetFonts(double size, FontFamily? monospaceFont)
    {
        fontSize = size;
        monospaceFamily = monospaceFont;
        ApplyFont();
    }

    /// <inheritdoc/>
    protected override AutomationPeer OnCreateAutomationPeer() => new ConversationGroupPeer(this, nameof(ConversationBubbleView), AutomationControlType.Group);

    private void ShowSender(string name)
    {
        senderName = name;
        senderLabel.Text = name;
        ToolTipService.SetToolTip(senderLabel, name.Length > 0 ? name : null);
    }

    // The paperclip pill: hidden, the icon alone, or the icon and the number.
    private void ShowClip(BubbleAttachments badge)
    {
        switch (badge)
        {
            case BubbleAttachments.Count n:
                clipCount.Text = n.Value.ToString(System.Globalization.CultureInfo.CurrentCulture);
                clipCount.Visibility = Visibility.Visible;
                clipPill.Visibility = Visibility.Visible;
                break;
            case BubbleAttachments.Some:
                clipCount.Visibility = Visibility.Collapsed;
                clipPill.Visibility = Visibility.Visible;
                break;
            default:
                clipPill.Visibility = Visibility.Collapsed;
                break;
        }
    }

    // The badges' box takes room (its margin) only while one shows.
    private void ShowBadges() =>
        badges.Visibility = badges.Children.Any(c => c.Visibility == Visibility.Visible) ? Visibility.Visible : Visibility.Collapsed;

    // The date (or a draft's saved time) at the header's end, and the
    // automation name: the sender, the full date and, while the paperclip
    // shows, "Has attachments" (a draft's: the sender, the badge and when it
    // was saved).
    private void RenderDate()
    {
        if (saved is not null)
        {
            dateLabel.Text = saved;
            ToolTipService.SetToolTip(dateLabel, null);
            dateLabel.Visibility = saved.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
            AutomationProperties.SetName(this, string.Join(", ", new[] { senderName, Core.Model.Conversation.DraftBadge(), saved }.Where(t => t.Length > 0)));
            return;
        }
        ConversationEventRow.ShowDate(dateLabel, date, compactDate);
        var full = date is { } d && !d.IsGoZero ? Format.FormatDateTime(d) : "";
        var attachments = clipPill.Visibility == Visibility.Visible ? L10n.T("Has attachments") : "";
        AutomationProperties.SetName(this, string.Join(", ", new[] { senderName, full, attachments }.Where(t => t.Length > 0)));
    }

    private void ApplyFont()
    {
        bodyLabel.FontSize = fontSize;
        if (monospace && monospaceFamily is { } mono)
        {
            bodyLabel.FontFamily = mono;
        }
        else
        {
            bodyLabel.ClearValue(TextBlock.FontFamilyProperty);
        }
    }

    // A double tap anywhere in the bubble activates it (the chat waits for
    // the release of the second press before it opens a window).
    private void OnDoubleTapped(object sender, DoubleTappedRoutedEventArgs e)
    {
        if (Activate is not { } activate)
        {
            return;
        }
        e.Handled = true;
        activate();
    }
}
