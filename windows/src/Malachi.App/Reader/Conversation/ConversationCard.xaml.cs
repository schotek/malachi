// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/window/conversation_card.go (convCard: update, setFold,
// setFolded, showFoldState, setCompact, refreshActions, toggleDetails,
// renderDetails, render, renderBody, showText, showHTML, setHint,
// renderBars, renderChips, refreshChips, setLive, loadWebView,
// releaseWebView, sizeReported, setWebHeight, setZoom, forwardScroll);
// macOS: MessageView/ConversationCardView.swift. One message of the
// conversation view (ConversationItemKind.Message). Headers are plain text;
// the body's HTML is the sanitiser's output only, in a view of its own sized
// to its document (CardWebView; never one document for the conversation: a
// message's CSS must not reach another's headers). The sender's avatar is
// not the card's: it sits on the timeline beside it (ConversationRow).
//
// A card is cheap: the recipients and the bars are made when first needed
// (x:Load), and the web view exists only while the pane keeps the card live
// (SetLive, near the viewport, from the pane's pool); otherwise the body
// keeps the height it last had, on white. The recipients and the chips are
// the single-message pane's own code (MessageChips over an AddressHeader of
// the card's), the bars the reader's texts (RemoteBar): their buttons act on
// this card's message exactly as they do in the pane.

using System;
using System.Collections.Generic;
using Malachi.App.Resources;
using Malachi.App.WebViews;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.I18n;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Malachi.Core.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;

namespace Malachi.App.Reader.Conversation;

/// <summary>One card of the conversation view.</summary>
internal sealed partial class ConversationCard : UserControl
{
    private readonly ConversationView pane;
    private readonly AddressHeader addresses = new();
    private readonly MessageChips chips;
    private MessageSummary s;
    private bool compact;
    private bool hovering;
    private bool focused;

    // The fold of the card that opened the conversation (SetFold), and the
    // parts of the top the fold hid, shown again when it opens.
    private bool foldable;
    private readonly List<UIElement> foldHidden = [];

    // The body: the web view while the card is live, the height the HTML body
    // last had (kept while no view is live), what governs it, and what it
    // shows.
    private CardWebView? web;
    private WebHeightGovernor gov = new();
    private double webHeight = ConversationLayout.InitialWebHeight;
    private string? webDoc;
    private string html = "";
    private bool reload;
    private IReadOnlyList<Link> links = [];

    // What the body shows, so that a render with the same body leaves it
    // alone.
    private MessageBodyResult? rendered;
    private bool renderedErr;
    private bool renderedAny;

    /// <summary>newConvCard: the card of <paramref name="item"/> in <paramref name="pane"/>.</summary>
    public ConversationCard(ConversationView pane, ConversationItem item, bool compact)
    {
        this.pane = pane;
        this.compact = compact;
        s = item.Message!;
        Id = s.Id;
        Item = item;
        InitializeComponent();
        chips = new MessageChips(pane.Services, this, () => pane.HostWindow, pane.FocusList, pane.Logger);
        addresses.RowChanged += (_, kind) => FillAddressRow(kind);
        FoldGlyph.Glyph = Icons.Glyph("pan-down");
        DisclosureGlyph.Glyph = Icons.Glyph("pan-end");
        ReplyAllGlyph.Glyph = Icons.Glyph("mail-reply-all");
        ForwardGlyph.Glyph = Icons.Glyph("mail-forward");
        HtmlHost.Height = webHeight;
        PointerEntered += (_, _) =>
        {
            hovering = true;
            SetButtonsShown(true);
        };
        PointerExited += (_, _) =>
        {
            hovering = false;
            SetButtonsShown(focused);
        };
        Buttons.GotFocus += (_, _) =>
        {
            focused = true;
            SetButtonsShown(true);
        };
        Buttons.LostFocus += (_, _) =>
        {
            focused = false;
            SetButtonsShown(hovering);
        };
        // forwardScroll: the wheel over the web view of a document that fits
        // (nothing to scroll inside) goes on to the conversation.
        HtmlHost.AddHandler(PointerWheelChangedEvent, new PointerEventHandler(OnHtmlWheel), handledEventsToo: true);
        ApplyBodyFont();
        Update(item);
    }

    /// <summary>The member the card shows.</summary>
    public MessageId Id { get; }

    /// <summary>The item as the model has it now.</summary>
    public ConversationItem Item { get; private set; }

    /// <summary>The body is HTML (it wants a web view while near).</summary>
    public bool IsHtml { get; private set; }

    /// <summary>Folded to its header (no body, no view).</summary>
    public bool Folded { get; private set; }

    /// <summary>The recipients are open (the card needs message.get).</summary>
    public bool DetailsOpen { get; private set; }

    /// <summary>The card holds a web view (the pane's live window).</summary>
    public bool IsLive { get; private set; }

    /// <summary>update: shows <paramref name="item"/>: the same member, as the model has it now (flags, the issue's badges).</summary>
    public void Update(ConversationItem item)
    {
        Item = item;
        s = item.Message!;
        SenderLabel.Text = item.Sender;
        ToolTipService.SetToolTip(SenderLabel, item.Sender.Length > 0 ? item.Sender : null);
        SenderLabel.Visibility = item.Sender.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        // The disclosure names whose recipients it shows.
        AutomationProperties.SetName(Disclosure, item.Sender);
        UnreadDot.Visibility = item.Unread ? Visibility.Visible : Visibility.Collapsed;
        InternalPill.Text = item.Internal ? item.InternalLabel : "";
        ViaLabel.Text = item.Via;
        ToolTipService.SetToolTip(ViaLabel, item.Via.Length > 0 ? item.Via : null);
        ViaLabel.Visibility = item.Via.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        EditedLabel.Text = item.Edited;
        EditedLabel.Visibility = item.Edited.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        ConversationEventRow.ShowDate(DateLabel, s.Date, compact);
        // The card is read by its sender and date, as the list's row by its own.
        AutomationProperties.SetName(this, item.Sender);
        RefreshActions();
        if (foldable)
        {
            ShowFoldState(); // the snippet may have changed
        }
    }

    /// <summary>
    /// setFold: makes the card the one that opened the conversation
    /// (<paramref name="on"/>), which folds to its header and a preview of its
    /// text (ConversationLayout.DisplayOrder), and folds or opens it; off: an
    /// ordinary card, open.
    /// </summary>
    public void SetFold(bool on, bool folded)
    {
        foldable = on;
        FoldButton.Visibility = on ? Visibility.Visible : Visibility.Collapsed;
        SetFolded(on && folded);
    }

    /// <summary>
    /// setFolded: folds the card to its header and the preview (the
    /// summary's snippet), or opens it: the recipients, the chips, the hint,
    /// the bars and the body come back as they were, and what arrived
    /// meanwhile is shown. A folded card holds no web view and asks for no
    /// body (the pane's UpdateLive).
    /// </summary>
    public void SetFolded(bool folded)
    {
        if (folded == Folded)
        {
            ShowFoldState();
            return;
        }
        Folded = folded;
        if (folded)
        {
            if (Disclosure.IsChecked == true)
            {
                Disclosure.IsChecked = false; // the recipients close with it
                ToggleDetails();
            }
            foreach (var part in new UIElement?[] { Details, Chips, Hint })
            {
                if (part is { Visibility: Visibility.Visible })
                {
                    part.Visibility = Visibility.Collapsed;
                    foldHidden.Add(part);
                }
            }
            SetLive(false);
        }
        else
        {
            foreach (var part in foldHidden)
            {
                part.Visibility = Visibility.Visible;
            }
            foldHidden.Clear();
        }
        ShowFoldState();
        if (!folded)
        {
            Render(pane.Held(Id));
        }
    }

    /// <summary>setCompact: the short date of a narrow pane, or the full one.</summary>
    public void SetCompact(bool compact)
    {
        if (compact == this.compact)
        {
            return;
        }
        this.compact = compact;
        ConversationEventRow.ShowDate(DateLabel, s.Date, compact);
    }

    /// <summary>refreshActions: the hover buttons the account allows for this member; Reply is Comment on an issue.</summary>
    public void RefreshActions()
    {
        var a = pane.Actions(s);
        var reply = Jira.ReplyLabel(a.Comment);
        AutomationProperties.SetName(ReplyButton, reply);
        ToolTipService.SetToolTip(ReplyButton, reply);
        ReplyGlyph.Glyph = Icons.Glyph(a.Comment ? "chat-message-new" : "mail-reply-sender");
        ReplyButton.Visibility = a.Reply ? Visibility.Visible : Visibility.Collapsed;
        ReplyAllButton.Visibility = a.ReplyAll ? Visibility.Visible : Visibility.Collapsed;
        ForwardButton.Visibility = a.Forward ? Visibility.Visible : Visibility.Collapsed;
    }

    /// <summary>
    /// render: shows whatever <paramref name="lm"/> holds (null: nothing asked
    /// for yet, or let go by the pane): the recipients, the body, the bars and
    /// the chips. A body already shown stays when there is none. A folded
    /// card shows none of it (SetFolded renders once it opens).
    /// </summary>
    public void Render(LoadedMessage? lm)
    {
        if (Folded)
        {
            return;
        }
        RenderDetails(lm?.Msg);
        RenderBody(lm);
        RenderBars(lm);
        RenderChips(lm);
    }

    /// <summary>renderBars: redraws the remote-image and pictures bars of an HTML body and leaves the body alone; the bars are made when first shown.</summary>
    public void RenderBars(LoadedMessage? lm)
    {
        if (!IsHtml || lm is null)
        {
            if (RemoteBar is not null)
            {
                RemoteBar.Visibility = Visibility.Collapsed;
            }
            if (PicturesBar is not null)
            {
                PicturesBar.Visibility = Visibility.Collapsed;
            }
            return;
        }
        var remote = Core.Model.RemoteBar.RemoteBarStateFor(lm);
        if (remote.Visible || RemoteBar is not null)
        {
            ShowRemoteBar(remote);
        }
        var pictures = Core.Model.RemoteBar.PicturesBarStateFor(lm);
        if (pictures.Visible || PicturesBar is not null)
        {
            ShowPicturesBar(pictures);
        }
    }

    /// <summary>refreshChips: redraws the chips (a download started or ended): from <paramref name="lm"/>, or from the entry they last showed.</summary>
    public void RefreshChips(LoadedMessage? lm)
    {
        if (Folded)
        {
            return; // shown once the card opens (SetFolded renders)
        }
        RenderChips(lm ?? pane.Held(Id));
    }

    /// <summary>
    /// setLive: gives the card a web view (the pane's live window,
    /// ConversationLayout.MaxLiveWebViews at most) or takes it; the body
    /// keeps its last height meanwhile.
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
            return;
        }
        if (IsHtml)
        {
            LoadWebView();
        }
    }

    /// <summary>setZoom: scales the web view's document; its height is measured again.</summary>
    public void SetZoom(int percent)
    {
        if (web is not null)
        {
            gov.WidthChanged();
            web.Zoom = percent;
        }
    }

    /// <summary>The text-zoom and monospace settings of the plain-text body (style.go .message-body).</summary>
    public void ApplyBodyFont()
    {
        TextBody.FontSize = pane.BodyFontSize;
        if (pane.BodyFontFamily is { } mono)
        {
            TextBody.FontFamily = mono;
        }
        else
        {
            TextBody.ClearValue(TextBlock.FontFamilyProperty);
        }
    }

    /// <summary>The pane lets go of the card for good: its web view goes back to the pool.</summary>
    public void Detach() => SetLive(false);

    // The hover buttons show while the pointer is over the card or one of
    // them has the focus.
    private void SetButtonsShown(bool on)
    {
        Buttons.Opacity = on ? 1 : 0;
        Buttons.IsHitTestVisible = on;
    }

    // showFoldState: the fold's arrow, the preview and the body as the card
    // is folded or not.
    private void ShowFoldState()
    {
        var tip = Folded ? L10n.T("Expand") : L10n.T("Collapse");
        FoldGlyph.Glyph = Icons.Glyph(Folded ? "pan-end" : "pan-down");
        ToolTipService.SetToolTip(FoldButton, tip);
        AutomationProperties.SetName(FoldButton, tip);
        var snippet = s.Snippet.Trim();
        Preview.Text = snippet;
        Preview.Visibility = Folded && snippet.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        Disclosure.Visibility = Folded ? Visibility.Collapsed : Visibility.Visible;
        Bars.Visibility = Folded ? Visibility.Collapsed : Visibility.Visible;
        Body.Visibility = Folded ? Visibility.Collapsed : Visibility.Visible;
    }

    private void OnFoldClick(object sender, RoutedEventArgs e) => pane.SetCardFolded(this, !Folded);

    private void OnPreviewTapped(object sender, TappedRoutedEventArgs e)
    {
        if (Folded)
        {
            pane.SetCardFolded(this, false);
        }
    }

    private void OnDisclosureClick(object sender, RoutedEventArgs e) => ToggleDetails();

    // toggleDetails: opens or closes the recipients: the summary's, then the
    // full message's (Cc) once message.get answered.
    private void ToggleDetails()
    {
        DetailsOpen = Disclosure.IsChecked == true;
        DisclosureGlyph.Glyph = Icons.Glyph(DetailsOpen ? "pan-down" : "pan-end");
        if (!DetailsOpen)
        {
            if (Details is not null)
            {
                Details.Visibility = Visibility.Collapsed;
            }
            return;
        }
        FindName(nameof(Details)); // made the first time
        Details!.Visibility = Visibility.Visible;
        RenderDetails((pane.Held(Id) ?? pane.Services.Cache.Loaded(Id))?.Msg);
        pane.NeedsBody(Id, details: true);
    }

    // renderDetails: the recipients while they are open: from m, the full
    // message, when there is one, else from the summary.
    private void RenderDetails(Message? m)
    {
        if (!DetailsOpen || Details is null)
        {
            return;
        }
        IReadOnlyList<Address>? from = s.From;
        IReadOnlyList<Address>? to = s.To;
        IReadOnlyList<Address>? cc = null;
        if (m is not null)
        {
            from = m.Summary.From;
            to = m.Summary.To;
            cc = m.Cc;
        }
        addresses.Show(Id, s.AccountId, from, to, cc);
    }

    private (TextBlock Label, WrapBox Box) RowOf(AddressRowKind kind) => kind switch
    {
        AddressRowKind.From => (FromLabel, FromChips),
        AddressRowKind.To => (ToLabel, ToChips),
        _ => (CcLabel, CcChips),
    };

    private void FillAddressRow(AddressRowKind kind)
    {
        if (Details is null)
        {
            return;
        }
        var (label, box) = RowOf(kind);
        chips.FillAddressRow(addresses.Row(kind), label, box, addresses.Expand, () => RowOf(kind).Box);
    }

    // renderBody: the body, its error, or the wait; the same body again
    // changes nothing (a render must not reload the web view or lay a long
    // text out anew).
    private void RenderBody(LoadedMessage? lm)
    {
        if (lm is not { BodySettled: true })
        {
            if (!renderedAny)
            {
                ShowPage(WaitLabel);
            }
            return;
        }
        var before = rendered;
        if (renderedAny && ReferenceEquals(lm.Body, rendered) && (lm.Err is not null) == renderedErr)
        {
            return;
        }
        rendered = lm.Body;
        renderedErr = lm.Err is not null;
        renderedAny = true;
        if (lm.Err is { } err)
        {
            links = [];
            SetHint(false);
            ShowText(RpcErrorText.Text(L10n.T("Loading the message"), err));
            return;
        }
        var b = lm.Body;
        if (LoadedMessageText.ShowsHtml(b))
        {
            links = b!.Links;
            SetHint(false);
            ShowHtml(b.Html!, ConversationLayout.PicturesArrived(before, b));
            return;
        }
        links = [];
        SetHint(b?.HtmlWithheld == true);
        ShowText(LoadedMessageText.BodyText(b));
    }

    // showText: plain text in the body; a web view goes.
    private void ShowText(string text)
    {
        var wasHtml = IsHtml;
        ReleaseWebView();
        IsHtml = false;
        html = "";
        TextBody.Text = text;
        ShowPage(TextBody);
        if (wasHtml)
        {
            pane.ScheduleLiveUpdate();
        }
    }

    // showHTML: the sanitised body in the card's web view while the card is
    // live, and its last height on white otherwise.
    private void ShowHtml(string body, bool reloadPictures)
    {
        var was = IsHtml;
        IsHtml = true;
        html = body;
        reload = reload || reloadPictures;
        HtmlHost.Height = webHeight;
        ShowPage(HtmlHost);
        if (IsLive)
        {
            LoadWebView();
        }
        if (!was)
        {
            pane.ScheduleLiveUpdate(); // an HTML card wants a view
        }
    }

    private void ShowPage(FrameworkElement page)
    {
        WaitLabel.Visibility = ReferenceEquals(page, WaitLabel) ? Visibility.Visible : Visibility.Collapsed;
        TextBody.Visibility = ReferenceEquals(page, TextBody) ? Visibility.Visible : Visibility.Collapsed;
        HtmlHost.Visibility = ReferenceEquals(page, HtmlHost) ? Visibility.Visible : Visibility.Collapsed;
    }

    // setHint: the note that only the plain text is shown.
    private void SetHint(bool on) => Hint.Visibility = on ? Visibility.Visible : Visibility.Collapsed;

    // remote.go showRemoteBar over the card's own bar.
    private void ShowRemoteBar(RemoteBarState st)
    {
        FindName(nameof(RemoteBar));
        if (st.Loading)
        {
            RemoteLabel.Text = L10n.T("Loading remote images…");
        }
        else if (st.Blocked > 0)
        {
            // TRANSLATORS: %d is the number of remote images the message tried to load.
            RemoteLabel.Text = L10n.N("%d remote image was blocked", "%d remote images were blocked", st.Blocked);
        }
        AutomationProperties.SetName(RemoteSpinner, RemoteLabel.Text);
        RemoteSpinner.IsActive = st.Loading;
        RemoteSpinner.Visibility = st.Loading ? Visibility.Visible : Visibility.Collapsed;
        RemoteLoad.Visibility = st.Loading ? Visibility.Collapsed : Visibility.Visible;
        RemoteTrust.Visibility = st.Loading ? Visibility.Collapsed : Visibility.Visible;
        RemoteBar!.Visibility = st.Visible ? Visibility.Visible : Visibility.Collapsed;
    }

    // remote.go showPicturesBar over the card's own bar.
    private void ShowPicturesBar(PicturesBarState st)
    {
        FindName(nameof(PicturesBar));
        if (st.Loading)
        {
            PicturesLabel.Text = L10n.T("Downloading pictures…");
        }
        else if (st.Remote > 0)
        {
            // TRANSLATORS: %d is the number of pictures of the message kept on the mail server only.
            PicturesLabel.Text = L10n.N(
                "%d picture of this message is on the server only", "%d pictures of this message are on the server only", st.Remote);
        }
        AutomationProperties.SetName(PicturesSpinner, PicturesLabel.Text);
        PicturesSpinner.IsActive = st.Loading;
        PicturesSpinner.Visibility = st.Loading ? Visibility.Visible : Visibility.Collapsed;
        PicturesDownload.Visibility = st.Loading ? Visibility.Collapsed : Visibility.Visible;
        PicturesBar!.Visibility = st.Visible ? Visibility.Visible : Visibility.Collapsed;
    }

    private void OnLoadImagesClick(object sender, RoutedEventArgs e) => pane.Services.Router.LoadImages(Id);

    private void OnTrustSenderClick(object sender, RoutedEventArgs e) => pane.Services.Router.TrustSender(Id);

    private void OnDownloadPicturesClick(object sender, RoutedEventArgs e)
    {
        var window = pane.HostWindow;
        pane.Services.Router.DownloadPictures(Id, text => pane.Services.ToastIn(window, text));
    }

    // renderChips: the attachment chips for what lm holds (the pane's
    // renderAttachments); nothing until message.get answered.
    private void RenderChips(LoadedMessage? lm)
    {
        if (Chips.Children.Count == 0 && (lm?.Msg is not { } m || AttachmentChips.ChipAttachments(m.Attachments, lm.Body).Count == 0))
        {
            return;
        }
        var (list, saveAll) = AttachmentChip.For(s, lm, nestedView: false, pane.Services.FileTypes, downloading: pane.Services.Cache.ShowsDownload(Id));
        chips.FillAttachments(Chips, list, saveAll, AttachmentChips.AnyRemote(saveAll, lm?.Body), s);
    }

    /// <summary>AttachmentOpener's Save All run of a message began or ended.</summary>
    public void SavingAllChanged(MessageId id) => chips.SavingAllChanged(id);

    // loadWebView: a view from the pane's pool (once while live), and the
    // body loaded into it.
    private void LoadWebView()
    {
        if (web is null)
        {
            web = pane.TakeWebView();
            gov = new WebHeightGovernor(1);
            web.OnLink = link => _ = pane.Services.Links.OpenAsync(link, links, pane.HostWindow);
            web.OnHover = pane.Hover;
            web.OnSize = SizeReported;
            web.OnFailed = HtmlUnavailable;
            web.Height = webHeight;
            HtmlHost.Children.Add(web);
            webDoc = null;
        }
        if (!string.Equals(webDoc, html, StringComparison.Ordinal) || reload)
        {
            gov.Reset();
        }
        web.Load(html, reload);
        webDoc = html;
        reload = false;
    }

    // releaseWebView: the view goes back to the pane's pool; the body keeps
    // its height.
    private void ReleaseWebView()
    {
        if (web is not { } view)
        {
            return;
        }
        web = null;
        HtmlHost.Children.Remove(view);
        pane.ReturnWebView(view);
        pane.Hover("");
    }

    // The view could not show the document (it could not be created, or the
    // body failed again after its one reload): the plain text with the hint
    // instead, as the reader does (HtmlUnavailable).
    private void HtmlUnavailable()
    {
        if (rendered is not { } b)
        {
            return;
        }
        links = [];
        SetHint(true);
        ShowText(LoadedMessageText.BodyText(b));
    }

    // sizeReported: the document's height from the view; the governor
    // decides the view's height (capped, frozen for a document that grows
    // with the view), which the body keeps when the view goes.
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

    // setWebHeight: the HTML body's height.
    private void SetWebHeight(double h)
    {
        if (h <= 0 || h == webHeight)
        {
            return;
        }
        webHeight = h;
        HtmlHost.Height = h;
        if (web is not null)
        {
            web.Height = h;
        }
    }

    // forwardScroll: the wheel over the web view of a document that fits
    // goes on to the conversation, which the view would otherwise swallow.
    private void OnHtmlWheel(object sender, PointerRoutedEventArgs e)
    {
        if (web is null || !gov.Fits)
        {
            return;
        }
        var delta = e.GetCurrentPoint(HtmlHost).Properties.MouseWheelDelta;
        if (delta == 0)
        {
            return;
        }
        pane.ScrollBy(-delta);
        e.Handled = true;
    }

    private void OnReplyClick(object sender, RoutedEventArgs e) => pane.Services.Router.Reply(Id);

    private void OnReplyAllClick(object sender, RoutedEventArgs e) => pane.Services.Router.ReplyAll(Id);

    private void OnForwardClick(object sender, RoutedEventArgs e) => pane.Services.Router.Forward(Id, pane.HostWindow);
}
