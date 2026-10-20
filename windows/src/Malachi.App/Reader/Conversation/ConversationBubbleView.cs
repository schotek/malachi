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
// and the draft's. The board's conversation block shows its chat in the
// same bubbles (ShowPlain: the sender and date a case's member carries,
// "BoardChatBubble:" and its index).
//
// HTML in the bubble (the preference conversation-chat-html,
// Conversation.BubbleRendersHtml; docs/windows-port.md §11.7 "HTML in the
// chat bubbles"): a body that is BubbleBody.Html shows its sanitized
// fragment in a CardWebView, the timeline card's locked view, where the text
// stands, inside the bubble's background with the same padding (the view
// transparent and its document, ViewerDocument.ChatDocument, without any
// background of the mail and without a frame, so the bubble's own colour,
// the card's or the accent tint of the user's own, is the document's; in
// the dark theme the text and links take the bubbles' text colours, in high
// contrast the system's, the light theme keeps the mail's), while the
// chat gives the bubble a web view (SetLive: near the viewport and within
// the timeline's cap); otherwise, and when the view cannot show the
// document, the message's text as without the preference. Such a bubble is
// as wide as the row allows (no shrink-to-fit) and has no bars and no fold;
// a link goes through the cards' link path (the chat's IChatWebHost), the
// text zoom applies to the document, and the view's height is measured as a
// card's (WebHeightGovernor). The web view takes the presses over its
// document, so a double tap opens the message from the header (the sender,
// the date) and the padding around the document.
//
// Shortened (the preferences conversation-chat-truncate-mine and -others, Shorten My or Others' Messages in
// Chat; Conversation.BubbleShortened, set by the chat with SetShortened): a
// text shows Conversation.ChatPreviewLines lines with an ellipsis, an HTML
// document is clipped to that many lines of the chat font (the view still
// loads and measures it whole) under a fade into the bubble's colour, which
// takes the presses while it shows; "Show More" under the body only when
// something is cut (TextBlock.IsTextTrimmed, the measured document taller
// than the preview). A tap anywhere in a shortened bubble asks the chat to
// expand it (Expand); a double tap still opens the message.

using System;
using System.Linq;
using Malachi.App.Controls;
using Malachi.App.Resources;
using Malachi.App.WebViews;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Html;
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
    private readonly Grid htmlHost;

    // Never shown: its Foreground is the link colour of the bubbles' text in
    // the theme shown, which Palette reads.
    private readonly TextBlock linkProbe;

    // Shortened: the hint under the body, and the fade over the clipped
    // document (its two layers: the bubble's fill and its tint).
    private readonly TextBlock showMore;
    private readonly Grid fade;
    private readonly RectangleGeometry htmlClip = new();

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

    // The HTML body (BubbleBody.Html): the web view while the chat gives the
    // bubble one, the height the document last had, what governs it, the
    // document loaded, a reload of its pictures asked for, and whether the
    // view could not show this document (the text then, until another one).
    private CardWebView? web;
    private WebHeightGovernor gov = new();
    private double webHeight = ConversationLayout.InitialWebHeight;
    private string? webDoc;
    private bool reload;
    private bool htmlFailed;

    // The system's high-contrast setting (one for the app).
    private static Windows.UI.ViewManagement.AccessibilitySettings? accessibility;

    // The document's height measured while the bubble held a web view,
    // kept as the text's MinHeight once the view goes while the body stays
    // HTML (as a released card keeps its paper's height), so that the row's
    // frame does not change and LiveCards does not flip the bubble between
    // the view and its text; null when nothing was measured, the body is no
    // longer HTML (the preference off, another body) or the view failed.
    private double? frozenHeight;

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

        // The HTML body's slot, where the text stands (the same margin; no
        // paper: the transparent view shows the bubble's background), shown
        // only while the bubble holds a web view.
        htmlHost = new Grid
        {
            Margin = new Thickness(0, 6, 0, 0),
            Height = webHeight,
            Visibility = Visibility.Collapsed,
        };
        linkProbe = new TextBlock { Style = look("ConversationBubbleLinkStyle"), Visibility = Visibility.Collapsed };
        // The fade over a clipped document, on top of the view (the view goes
        // in under it); while it shows it takes the presses the view would.
        fade = new Grid { Visibility = Visibility.Collapsed, Background = new SolidColorBrush(Microsoft.UI.Colors.Transparent) };
        htmlHost.Children.Add(fade);
        htmlHost.Clip = htmlClip;
        htmlHost.SizeChanged += (_, e) => htmlClip.Rect = new Windows.Foundation.Rect(0, 0, e.NewSize.Width, e.NewSize.Height);
        showMore = new TextBlock
        {
            Style = look("ConversationBubbleLinkStyle"),
            FontSize = 12,
            Margin = new Thickness(0, 4, 0, 0),
            Text = Core.Model.Conversation.ChatShowMore(),
            Visibility = Visibility.Collapsed,
        };
        bodyLabel.IsTextTrimmedChanged += (_, _) => UpdateShowMore();
        // The wheel over the web view of a document that fits (nothing to
        // scroll inside) goes on to the chat (the card's forwardScroll).
        htmlHost.AddHandler(PointerWheelChangedEvent, new PointerEventHandler(OnHtmlWheel), handledEventsToo: true);

        var column = new StackPanel();
        column.Children.Add(header);
        column.Children.Add(bodyLabel);
        column.Children.Add(htmlHost);
        column.Children.Add(showMore);
        column.Children.Add(linkProbe);
        // Border is sealed in WinUI, so the bubble hosts its frame; the
        // user's own has the tint as an inner layer over the card's fill.
        var inner = new Border
        {
            Padding = new Thickness(PaddingH - 1, PaddingV - 1, PaddingH - 1, PaddingV - 1),
            Child = column,
        };
        // The others' bubble is a light grey (ConversationOtherTintStyle).
        inner.Style = look(mine ? "ConversationMineTintStyle" : "ConversationOtherTintStyle");
        Content = new Border
        {
            Style = look(mine ? "ConversationMineBubbleStyle" : "ConversationBubbleStyle"),
            Child = inner,
        };
        DoubleTapped += OnDoubleTapped;
        Tapped += OnTapped;
        // An HTML bubble takes the widest the row allows (ApplyWidth).
        RegisterPropertyChangedCallback(MaxWidthProperty, (_, _) => ApplyWidth());
        // The theme changed: the document again with the theme's colours.
        ActualThemeChanged += (_, _) =>
        {
            if (web is not null)
            {
                LoadWebView();
            }
            PaintFade();
        };
    }

    /// <summary>The user's own message or draft (on the right, tinted).</summary>
    public bool Mine { get; }

    /// <summary>A double tap anywhere in the bubble (the chat opens the message or edits the draft).</summary>
    public Action? Activate { get; set; }

    /// <summary>The text shows a body (not the placeholder): a let-go entry keeps it (<see cref="ShowBody"/>).</summary>
    public bool ShowsText { get; private set; }

    /// <summary>The text shows in the monospaced font.</summary>
    public bool IsMonospace => monospace;

    private static Windows.UI.ViewManagement.AccessibilitySettings Accessibility => accessibility ??= new();

    /// <summary>The chat's side of the web view of an HTML body; without it (the board's chat) a bubble never renders HTML.</summary>
    public IChatWebHost? WebHost { get; set; }

    /// <summary>The body is HTML (<see cref="BubbleBody.Html"/>): the bubble is as wide as its row allows.</summary>
    public bool RendersHtml => shownBody is BubbleBody.Html;

    /// <summary>The body is HTML the view has not failed on: the bubble wants a web view while near (the chat's live set).</summary>
    public bool WantsWebView => shownBody is BubbleBody.Html && !htmlFailed && WebHost is not null;

    /// <summary>The bubble holds a web view (the chat's live set, <see cref="SetLive"/>).</summary>
    public bool IsLive { get; private set; }

    /// <summary>The bubble shows the shortened preview (<see cref="SetShortened"/>).</summary>
    public bool Shortened { get; private set; }

    /// <summary>A tap on a shortened bubble: the chat records it as expanded and shows it whole.</summary>
    public Action? Expand { get; set; }

    /// <summary>
    /// Shows the shortened preview (<paramref name="on"/>,
    /// <see cref="Core.Model.Conversation.BubbleShortened"/>) or the whole
    /// message; the chat runs it where heights change.
    /// </summary>
    public void SetShortened(bool on)
    {
        if (on == Shortened)
        {
            return;
        }
        Shortened = on;
        bodyLabel.MaxLines = on ? Core.Model.Conversation.ChatPreviewLines : 0;
        bodyLabel.TextTrimming = on ? TextTrimming.CharacterEllipsis : TextTrimming.None;
        ApplyHtmlHeight();
        ShowPage();
    }

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
    /// The header of a message of the board's conversation
    /// (BoardConversationBlock's chat; GTK board_conversation_chat.go show):
    /// <paramref name="sender"/> (You for the user's own) and
    /// <paramref name="messageDate"/> (Go's zero shows none), no unread dot,
    /// paperclip, badges or relay; its AutomationId is
    /// <paramref name="automationId"/>. The board's members carry no more.
    /// </summary>
    public void ShowPlain(string automationId, string sender, DateTimeOffset messageDate)
    {
        AutomationProperties.SetAutomationId(this, automationId);
        ShowSender(sender);
        unread.Visibility = Visibility.Collapsed;
        ShowClip(new BubbleAttachments.None());
        draftPill.Text = "";
        internalPill.Text = "";
        viaLabel.Visibility = Visibility.Collapsed;
        editedLabel.Visibility = Visibility.Collapsed;
        ShowBadges();
        date = messageDate;
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
    public void ShowBody(BubbleBody body, bool monospaceText) => ShowBody(body, monospaceText, reloadPictures: false);

    /// <summary>
    /// <see cref="ShowBody(BubbleBody, bool)"/>, and for an HTML body whose
    /// pictures were downloaded (<paramref name="reloadPictures"/>,
    /// <see cref="ConversationLayout.PicturesArrived"/>) the document loaded
    /// again, as a card does.
    /// </summary>
    public void ShowBody(BubbleBody body, bool monospaceText, bool reloadPictures)
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
            if (reloadPictures && body is BubbleBody.Html && web is not null)
            {
                reload = true;
                LoadWebView();
            }
            return;
        }
        var wasHtml = shownBody is BubbleBody.Html;
        var wanted = WantsWebView;
        shownBody = body;
        switch (body)
        {
            case BubbleBody.Placeholder p:
                ShowsText = false;
                bodyLabel.Style = look("ConversationBubblePlaceholderStyle");
                bodyLabel.Text = p.Value;
                break;
            case BubbleBody.Text t:
                ShowsText = true;
                bodyLabel.Style = look("ConversationBubbleTextStyle");
                bodyLabel.Text = t.Value;
                break;
            case BubbleBody.Html h:
                ShowsText = true;
                bodyLabel.Style = look("ConversationBubbleTextStyle");
                bodyLabel.Text = h.Fallback;
                if (!string.Equals(webDoc, h.Value, StringComparison.Ordinal))
                {
                    htmlFailed = false;
                }
                reload = reload || reloadPictures;
                break;
        }
        if (body is BubbleBody.Html)
        {
            if (IsLive)
            {
                LoadWebView();
            }
        }
        else
        {
            // Not HTML: no web view, and none until the chat decides anew;
            // the frozen height goes with it.
            ReleaseWebView();
            IsLive = false;
            frozenHeight = null;
        }
        ShowPage();
        if (wasHtml != (body is BubbleBody.Html))
        {
            ApplyWidth();
        }
        if (wanted != WantsWebView)
        {
            WebHost?.LiveChanged();
        }
    }

    /// <summary>
    /// An HTML body reads as its text again (the preference was turned off
    /// while the bubble's entry was let go: no body to read it from anew);
    /// any other body stays.
    /// </summary>
    public void DropHtml()
    {
        if (shownBody is BubbleBody.Html h)
        {
            ShowBody(new BubbleBody.Text(h.Fallback), monospaceText: false);
        }
    }

    /// <summary>
    /// Gives the bubble a web view (the chat's live set: near the viewport,
    /// within the timeline's cap) or takes it: without one an HTML body shows
    /// its text.
    /// </summary>
    public void SetLive(bool on)
    {
        if (on == IsLive)
        {
            return;
        }
        IsLive = on;
        if (!on)
        {
            ReleaseWebView();
        }
        else if (WantsWebView)
        {
            LoadWebView();
        }
        ShowPage();
    }

    /// <summary>The text-zoom setting in percent: the web view's document scaled, its height measured again.</summary>
    public void SetZoom(int percent)
    {
        if (web is not null)
        {
            gov.WidthChanged();
            web.Zoom = percent;
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
        ApplyHtmlHeight();
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

    // The text, or the HTML body's slot while the bubble holds a web view;
    // an empty text hides it.
    private void ShowPage()
    {
        var html = web is not null;
        htmlHost.Visibility = html ? Visibility.Visible : Visibility.Collapsed;
        var frozen = !html && RendersHtml && !htmlFailed ? frozenHeight : null;
        bodyLabel.Visibility = !html && (bodyLabel.Text.Length > 0 || frozen is not null) ? Visibility.Visible : Visibility.Collapsed;
        if (frozen is { } h)
        {
            // A shortened bubble keeps its clipped height.
            bodyLabel.MinHeight = Shortened ? Math.Min(h, PreviewHeight) : h;
        }
        else
        {
            bodyLabel.ClearValue(FrameworkElement.MinHeightProperty);
        }
        UpdateShowMore();
    }

    // The clipped document's height: ChatPreviewLines lines of the chat
    // document's font (the bubbles' size with the text zoom, line-height
    // 1.33 as ViewerDocument.ChatDocument sets it).
    private double PreviewHeight => Math.Ceiling(Core.Model.Conversation.ChatPreviewLines * fontSize * 1.33);

    // The HTML slot's height: the document's, or the preview's while
    // shortened (the view keeps the document's height and is clipped), and
    // the fade over the clipped part.
    private void ApplyHtmlHeight()
    {
        var clipped = Shortened && webHeight > PreviewHeight;
        htmlHost.Height = clipped ? PreviewHeight : webHeight;
        fade.Visibility = clipped && web is not null ? Visibility.Visible : Visibility.Collapsed;
        if (clipped)
        {
            PaintFade();
        }
        UpdateShowMore();
    }

    // "Show More" only while something is cut: the text trimmed, or the
    // document taller than the preview.
    private void UpdateShowMore()
    {
        var cut = Shortened && (web is not null ? webHeight > PreviewHeight : bodyLabel.IsTextTrimmed);
        showMore.Visibility = cut ? Visibility.Visible : Visibility.Collapsed;
    }

    // The fade into the bubble's colour over the lowest part of a clipped
    // document: the bubble's fill and its tint as two gradients from
    // transparent, over an opaque base of the theme (the pane's solid
    // background), so that the document's last line fades out; high contrast
    // keeps no gradient (the fade only takes the presses).
    private void PaintFade()
    {
        fade.Children.Clear();
        if (Accessibility.HighContrast || Content is not Border outer)
        {
            return;
        }
        var dark = ActualTheme == ElementTheme.Dark;
        var under = dark ? Windows.UI.Color.FromArgb(255, 0x20, 0x20, 0x20) : Windows.UI.Color.FromArgb(255, 0xF3, 0xF3, 0xF3);
        var color = under;
        if (outer.Background is SolidColorBrush fill)
        {
            color = Over(color, fill.Color, fill.Opacity);
        }
        if (outer.Child is Border { Background: SolidColorBrush tint })
        {
            color = Over(color, tint.Color, tint.Opacity);
        }
        var brush = new LinearGradientBrush { StartPoint = new Windows.Foundation.Point(0, 0), EndPoint = new Windows.Foundation.Point(0, 1) };
        brush.GradientStops.Add(new GradientStop { Color = Windows.UI.Color.FromArgb(0, color.R, color.G, color.B), Offset = 0.45 });
        brush.GradientStops.Add(new GradientStop { Color = color, Offset = 1 });
        fade.Children.Add(new Microsoft.UI.Xaml.Shapes.Rectangle { Fill = brush, IsHitTestVisible = false });
    }

    // over drawn at opacity over an opaque under.
    private static Windows.UI.Color Over(Windows.UI.Color under, Windows.UI.Color over, double opacity)
    {
        var a = over.A / 255.0 * opacity;
        byte Mix(byte u, byte o) => (byte)Math.Round((o * a) + (u * (1 - a)));
        return Windows.UI.Color.FromArgb(255, Mix(under.R, over.R), Mix(under.G, over.G), Mix(under.B, over.B));
    }

    // A tap on a shortened bubble expands it (the double tap still opens it).
    private void OnTapped(object sender, TappedRoutedEventArgs e)
    {
        if (!Shortened || Expand is not { } expand)
        {
            return;
        }
        e.Handled = true;
        expand();
    }

    // An HTML bubble is as wide as the row allows (MaxWidth, the row's
    // ConversationLayout.ChatBubbleMax); any other hugs its text.
    private void ApplyWidth()
    {
        var max = MaxWidth;
        if (RendersHtml && max > 0 && !double.IsInfinity(max))
        {
            Width = max;
        }
        else
        {
            ClearValue(WidthProperty);
        }
    }

    // A view from the chat's pool (once while live) and the fragment loaded
    // into it, as ConversationCard.LoadWebView.
    private void LoadWebView()
    {
        if (WebHost is not { } host || shownBody is not BubbleBody.Html h || htmlFailed)
        {
            return;
        }
        if (web is null)
        {
            web = host.TakeWebView();
            gov = new WebHeightGovernor(1);
            var links = h.Links;
            web.OnLink = link => host.OpenLink(link, (shownBody as BubbleBody.Html)?.Links ?? links);
            web.OnHover = host.Hover;
            web.OnSize = SizeReported;
            web.OnFailed = HtmlUnavailable;
            web.Height = webHeight;
            htmlHost.Children.Insert(0, web);
            ApplyHtmlHeight();
            webDoc = null;
        }
        if (!string.Equals(webDoc, h.Value, StringComparison.Ordinal) || reload)
        {
            gov.Reset();
        }
        web.Palette = Palette();
        web.Load(h.Value, reload);
        webDoc = h.Value;
        reload = false;
    }

    // The colours the document's text and links take: the system's in high
    // contrast (CSS system colours), the bubbles' text and link colours in
    // the dark theme (the mail's dark text would sit on a dark bubble), none
    // in the light theme (the mail's own).
    private ChatPalette? Palette()
    {
        if (Accessibility.HighContrast)
        {
            return new ChatPalette("CanvasText", "LinkText");
        }
        if (ActualTheme != ElementTheme.Dark)
        {
            return null;
        }
        return CssColor(bodyLabel.Foreground) is { } text && CssColor(linkProbe.Foreground) is { } link ? new ChatPalette(text, link) : null;
    }

    private static string? CssColor(Brush? brush) =>
        brush is SolidColorBrush { Color: var c }
            ? string.Create(System.Globalization.CultureInfo.InvariantCulture, $"rgba({c.R},{c.G},{c.B},{c.A / 255.0:0.###})")
            : null;

    // The view goes back to the chat's pool; the document's height is kept
    // for the next view.
    private void ReleaseWebView()
    {
        if (web is not { } view)
        {
            return;
        }
        web = null;
        webDoc = null;
        htmlHost.Children.Remove(view);
        WebHost?.ReturnWebView(view);
        WebHost?.Hover("");
    }

    // The view could not show the document (it could not be created, or the
    // body failed again after its one reload): the text, as the card's plain
    // text, until another document.
    private void HtmlUnavailable()
    {
        if (shownBody is not BubbleBody.Html h)
        {
            return;
        }
        WebHost?.Reflow(this, () =>
        {
            ReleaseWebView();
            htmlFailed = true;
            frozenHeight = null;
            webDoc = h.Value;
            ShowPage();
        });
        WebHost?.LiveChanged();
    }

    // The document's height from the view; the governor decides the view's
    // height (capped, frozen for a document that grows with the view).
    private void SizeReported(double css, bool viewport, bool relayout)
    {
        if (web is null)
        {
            return;
        }
        if (relayout)
        {
            gov.WidthChanged();
        }
        if (gov.Report(css, viewport) is { } h)
        {
            SetWebHeight(h);
        }
    }

    private void SetWebHeight(double h)
    {
        if (h <= 0 || h == webHeight)
        {
            return;
        }
        void Apply()
        {
            webHeight = h;
            frozenHeight = h;
            if (web is not null)
            {
                web.Height = h;
            }
            ApplyHtmlHeight();
        }
        if (WebHost is { } host)
        {
            host.Reflow(this, Apply);
        }
        else
        {
            Apply();
        }
    }

    // The wheel over the web view of a document that fits goes on to the
    // chat, which the view would otherwise swallow.
    private void OnHtmlWheel(object sender, PointerRoutedEventArgs e)
    {
        if (web is null || !gov.Fits || WebHost is not { } host)
        {
            return;
        }
        var delta = e.GetCurrentPoint(htmlHost).Properties.MouseWheelDelta;
        if (delta == 0)
        {
            return;
        }
        host.ScrollBy(-delta);
        e.Handled = true;
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
