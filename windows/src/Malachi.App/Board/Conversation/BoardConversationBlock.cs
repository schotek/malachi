// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardConversationBlock.swift; Go:
// ui/internal/window/board_conversation.go (boardConversation: apply, live,
// scheduleRefresh, refresh, fetchBodies, fetchBody, bodyArrived, loaded,
// userFold, removeAll, close, showHover, setZoom, changingHeight).
//
// The conversation of the board's detail: the heading with Show in Mail, a
// status row (a spinner and the note while the conversation loads, the note
// with Try Again when it could not be loaded), the cards
// (BoardMessageCardView) and the link under the pointer. It lives as long
// as the detail and is never rebuilt with it: a refresh of the board (every
// autosave of the inline reply lists the board again) changes the heading
// and the note in place and touches the cards only when the case or its
// members changed (Board.ConversationCards.Apply, keyed by the case and each
// member's id and excerpt). Another case starts over and lets every web view
// go; a new message adds its card and keeps the others, with their web
// views, as they are.
//
// Which card shows what is Board.ConversationCards's: the newest open, older
// ones folded to a preview; an open card of a message with an id asks for
// its body through Mail's cache (MessageCache.FetchBody, the variant the
// cache entry shows: trimmed of its quoted history unless Mail revealed it,
// as GTK and macOS) and shows the sanitised HTML in a card web view while
// the block is live (attached, loaded and visible), at most
// ConversationCards.MaxLiveWebViews of them; errors, withheld or missing HTML
// and the samples (no ids) keep the excerpt. The block hears the cache's
// MessageLoaded, so a body that changes (remote images loaded in Mail)
// shows here too; links go through the reader's LinkOpener (allow-list,
// masked-link question). Every text is a TextBlock's Text.
//
// Windows: the detail calls Detach when it hides the block without taking
// it out of the tree (the Mail mode, a closed panel), which lets every web
// view go, and Attach when it shows it again; Close ends it for good.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.App.Reader;
using Malachi.App.WebViews;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Malachi.Core.Settings;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Windows.Foundation;
using CoreBoard = Malachi.Core.Boards.Board;

namespace Malachi.App.Board;

/// <summary>The conversation of the board's detail (BoardConversationBlock).</summary>
public sealed partial class BoardConversationBlock : UserControl
{
    // The link label's longest text (the conversation view's StatusMaxChars).
    private const int HoverMaxChars = 512;

    private readonly IBoardConversationHost host;
    private readonly ReaderServices? services;
    private readonly TextBlock heading = new();
    private readonly Button showButton = new();
    private readonly StackPanel statusPanel = new() { Spacing = 4, Visibility = Visibility.Collapsed };
    private readonly StackPanel cardsColumn = new() { Spacing = 8 };
    private readonly TextBlock hover = new();
    private readonly List<BoardMessageCardView> cards = [];
    private readonly HashSet<BoardMessageCardView> pendingHeights = [];
    private CoreBoard.ConversationCards state = new();
    private AccountId? account;
    private Status? status;
    private SettingsChangeToken? zoomToken;
    private bool attached = true;
    private bool closed;
    private bool userChanging;
    private bool refreshPending;

    /// <summary>
    /// A block in <paramref name="host"/>'s detail; <paramref name="services"/>
    /// are Mail's (its cache, its link opener, the text zoom), null where
    /// there is no daemon (the cards keep their excerpts).
    /// </summary>
    public BoardConversationBlock(IBoardConversationHost host, ReaderServices? services)
    {
        ArgumentNullException.ThrowIfNull(host);
        this.host = host;
        this.services = services;
        IsTabStop = false;

        heading.FontWeight = FontWeights.Bold;
        heading.MaxLines = 1;
        heading.TextWrapping = TextWrapping.NoWrap;
        heading.TextTrimming = TextTrimming.CharacterEllipsis;
        heading.VerticalAlignment = VerticalAlignment.Center;
        showButton.Content = CoreBoard.Text.ShowInMail;
        showButton.VerticalAlignment = VerticalAlignment.Center;
        AutomationProperties.SetAutomationId(showButton, "BoardShowInMail");
        showButton.Click += (_, _) => host.ShowInMail();
        var top = new Grid { ColumnSpacing = 8 };
        top.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        top.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        Grid.SetColumn(showButton, 1);
        top.Children.Add(heading);
        top.Children.Add(showButton);

        hover.FontSize = 12;
        hover.Foreground = (Brush)Application.Current.Resources["TextFillColorSecondaryBrush"];
        hover.MaxLines = 1;
        hover.TextWrapping = TextWrapping.NoWrap;
        hover.TextTrimming = TextTrimming.CharacterEllipsis;
        hover.Visibility = Visibility.Collapsed;

        var root = new StackPanel { Spacing = 8 };
        root.Children.Add(top);
        root.Children.Add(statusPanel);
        root.Children.Add(cardsColumn);
        root.Children.Add(hover);
        Content = root;
        AutomationProperties.SetAutomationId(this, "BoardConversation");

        // Moving the detail between List and the panel unloads it briefly:
        // the decision waits, so a view that stays keeps its document.
        Loaded += (_, _) => ScheduleRefresh();
        Unloaded += (_, _) => ScheduleRefresh();
        RegisterPropertyChangedCallback(VisibilityProperty, (_, _) => ScheduleRefresh());
        if (services is not null)
        {
            services.Cache.MessageLoaded += OnMessageLoaded;
            var settings = services.State.Settings;
            zoomToken = settings.OnChange(SettingsKey.TextZoom, () => SetZoom(settings.TextZoom));
        }
    }

    /// <summary>The cards, oldest first (the same indices as the state's members).</summary>
    public IReadOnlyList<BoardMessageCardView> Cards => cards;

    /// <summary>The cards holding a web view now.</summary>
    public int LiveWebViews => cards.Count(c => c.HasWebView);

    /// <summary>The case the block shows; null for none.</summary>
    public BoardCaseId? CaseId => state.CaseId;

    // The block may hold web views now: attached, in the tree, shown, with
    // Mail's cache at hand.
    private bool Live =>
        !closed && attached && services is not null && IsLoaded && XamlRoot is not null
        && Visibility == Visibility.Visible && cardsColumn.Visibility == Visibility.Visible;

    // Applying the detail

    /// <summary>
    /// Brings the block up to <paramref name="d"/> (null: no case; every card
    /// goes). Returns whether anything it shows changed. The same case with
    /// the same members keeps every card, its fold and its web view.
    /// </summary>
    public bool Update(CoreBoard.Detail? d)
    {
        if (closed)
        {
            return false;
        }
        if (d is null)
        {
            var had = cards.Count > 0 || state.CaseId is not null;
            RemoveAll();
            state = new CoreBoard.ConversationCards();
            account = null;
            return had;
        }
        var changed = false;
        account = d.AccountId;
        if (heading.Text != d.ConversationTitle)
        {
            heading.Text = d.ConversationTitle;
            AutomationProperties.SetName(this, d.ConversationTitle);
            changed = true;
        }
        var canShow = host.CanShowInMail(d.Id);
        if (showButton.IsEnabled != canShow)
        {
            showButton.IsEnabled = canShow;
            changed = true;
        }
        var s = new Status(d.MessagesLoading, d.MessagesNote, d.MessagesRetry);
        changed |= ApplyStatus(s.Loading || s.Note.Length > 0 ? s : null);
        if (s.Loading || s.Note.Length > 0)
        {
            // The cards of the same case wait hidden for the conversation;
            // another case's go.
            if (state.CaseId != d.Id)
            {
                changed |= cards.Count > 0;
                RemoveAll();
                state = new CoreBoard.ConversationCards();
            }
            if (cardsColumn.Visibility != Visibility.Collapsed)
            {
                cardsColumn.Visibility = Visibility.Collapsed;
                changed = true;
            }
            Refresh();
            return changed;
        }
        if (cardsColumn.Visibility != Visibility.Visible)
        {
            cardsColumn.Visibility = Visibility.Visible;
            changed = true;
        }
        IReadOnlyList<int> reloads = [];
        switch (state.Apply(CoreBoard.ConversationCards.KeyOf(d)))
        {
            case CoreBoard.ConversationCards.Change.Reset:
                RemoveAll();
                foreach (var m in d.Messages)
                {
                    var card = new BoardMessageCardView(this, m);
                    cards.Add(card);
                    cardsColumn.Children.Add(card);
                }
                changed = true;
                break;
            case CoreBoard.ConversationCards.Change.Members(var kept, var reload):
                var next = new List<BoardMessageCardView>(d.Messages.Count);
                var stays = new HashSet<BoardMessageCardView>();
                for (var i = 0; i < d.Messages.Count; i++)
                {
                    if (kept.TryGetValue(i, out var old))
                    {
                        // The same card, whatever the excerpt now says.
                        cards[old].Update(d.Messages[i]);
                        next.Add(cards[old]);
                        stays.Add(cards[old]);
                    }
                    else
                    {
                        next.Add(new BoardMessageCardView(this, d.Messages[i]));
                    }
                }
                foreach (var card in cards.Where(c => !stays.Contains(c)))
                {
                    card.Close();
                    cardsColumn.Children.Remove(card);
                }
                // In place: a card that stays is moved only when its place
                // changed, so its web view keeps its document.
                for (var i = 0; i < next.Count; i++)
                {
                    var card = next[i];
                    if (i < cardsColumn.Children.Count && ReferenceEquals(cardsColumn.Children[i], card))
                    {
                        continue;
                    }
                    var at = cardsColumn.Children.IndexOf(card);
                    if (at >= 0)
                    {
                        cardsColumn.Children.RemoveAt(at);
                    }
                    cardsColumn.Children.Insert(i, card);
                }
                cards.Clear();
                cards.AddRange(next);
                Hover("");
                reloads = reload;
                changed = true;
                break;
            default:
                // Metadata (the sender, the time, the own-message tint) can
                // change without the key.
                for (var i = 0; i < cards.Count && i < d.Messages.Count; i++)
                {
                    changed |= cards[i].Update(d.Messages[i]);
                }
                break;
        }
        Refresh();
        FetchBodies();
        foreach (var i in reloads)
        {
            FetchBody(i);
        }
        return changed;
    }

    /// <summary>
    /// The detail hides the block without taking it out of the tree (the
    /// Mail mode, a closed panel): every web view goes, nothing is asked for.
    /// The cards keep their folds and what they know.
    /// </summary>
    public void Detach()
    {
        if (!attached)
        {
            return;
        }
        attached = false;
        Refresh();
    }

    /// <summary>The detail shows the block again after <see cref="Detach"/>: the open cards show their HTML again.</summary>
    public void Attach()
    {
        if (attached || closed)
        {
            return;
        }
        attached = true;
        Refresh();
        FetchBodies();
    }

    /// <summary>The block goes for good (the window closes): its web views and its subscriptions go.</summary>
    public void Close()
    {
        if (closed)
        {
            return;
        }
        closed = true;
        RemoveAll();
        if (services is not null)
        {
            services.Cache.MessageLoaded -= OnMessageLoaded;
        }
        zoomToken?.Cancel();
        zoomToken = null;
    }

    // For the cards

    /// <summary>A card's arrow: the state decides, the limit may fold another.</summary>
    internal void UserFolded(BoardMessageCardView card, bool folded)
    {
        var i = cards.IndexOf(card);
        if (i < 0 || closed)
        {
            return;
        }
        // The user's own fold moves the viewport as it will; the cards the
        // limit folds and the late heights compensate on their own.
        userChanging = true;
        try
        {
            state.SetFolded(i, folded);
            ApplyCard(i, Live, services is not null);
        }
        finally
        {
            userChanging = false;
        }
        Refresh();
        FetchBodies();
    }

    /// <summary>A card measured whether its excerpt is longer than the preview: its arrow and its preview follow.</summary>
    internal void LengthMeasured(BoardMessageCardView card)
    {
        if (cards.Contains(card))
        {
            Refresh();
        }
    }

    /// <summary>A card's web view cannot show its document: the card counts as text from now on.</summary>
    internal void HtmlUnavailable(BoardMessageCardView card)
    {
        var i = cards.IndexOf(card);
        if (i < 0)
        {
            return;
        }
        state.Answered(i, html: false);
        Refresh();
    }

    /// <summary>A card's web view: the viewer's profile and gate, Mail's parts, the text zoom.</summary>
    internal CardWebView MakeWebView()
    {
        var web = new CardWebView { Zoom = services?.State.Settings.TextZoom ?? 100 };
        if (services?.Cache is { } cache)
        {
            web.Parts = (part, token) => cache.FetchPartAsync(part.AccountId, part.MessageId, part.PartId, token);
        }
        return web;
    }

    /// <summary>A link the user activated in a card, with the body's links as the daemon listed them.</summary>
    internal void OpenLink(ActivatedLink link, IReadOnlyList<Link> links)
    {
        if (services is not null && !closed)
        {
            _ = services.Links.OpenAsync(link, links, host.HostWindow);
        }
    }

    /// <summary>showHover: the link under the pointer of any card ("" hides it), plain text, capped.</summary>
    internal void Hover(string link)
    {
        var text = ConversationLayout.ClampText(link ?? "", HoverMaxChars);
        hover.Text = text;
        hover.Visibility = text.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
    }

    /// <summary>
    /// scrollBy: scrolls the detail's column by what a card whose document
    /// fits handed on, in the wheel's units (120 a step; the conversation
    /// view's step).
    /// </summary>
    internal void ScrollBy(double wheelDelta)
    {
        if (host.Scroller is not { } scroller)
        {
            return;
        }
        var step = Math.Pow(Math.Max(scroller.ViewportHeight, 1), 2.0 / 3.0);
        var to = scroller.VerticalOffset + (wheelDelta / 120.0 * step);
        scroller.ChangeView(null, Math.Clamp(to, 0, Math.Max(0, scroller.ScrollableHeight)), null);
    }

    /// <summary>
    /// changingHeight: runs a change of <paramref name="card"/>'s height
    /// that the user did not make; when the card ended above the viewport,
    /// the column moves by as much once the layout settled
    /// (Board.ConversationCards.CompensatedTop), so what the user reads stays
    /// in place. Changes of one card before the layout settled count as one.
    /// </summary>
    internal void ChangingHeight(BoardMessageCardView card, Action change)
    {
        var scroller = host.Scroller;
        if (userChanging || closed || pendingHeights.Contains(card) || scroller?.Content is not UIElement content
            || !card.IsLoaded || card.ActualHeight <= 0)
        {
            change();
            return;
        }
        double cardTop;
        try
        {
            cardTop = card.TransformToVisual(content).TransformPoint(new Point(0, 0)).Y;
        }
        catch (ArgumentException)
        {
            change();
            return;
        }
        var top = scroller.VerticalOffset;
        var maxY = cardTop + card.ActualHeight;
        var before = card.ActualHeight;
        pendingHeights.Add(card);
        change();
        DispatcherQueue.TryEnqueue(DispatcherQueuePriority.Low, () =>
        {
            if (!pendingHeights.Remove(card) || closed || !card.IsLoaded)
            {
                return;
            }
            scroller.UpdateLayout();
            var delta = card.ActualHeight - before;
            if (Math.Abs(delta) < 0.5)
            {
                return;
            }
            var compensated = CoreBoard.ConversationCards.CompensatedTop(top, maxY, delta, scroller.ExtentHeight, scroller.ViewportHeight);
            // Keeps whatever the user scrolled while the layout was pending.
            var to = scroller.VerticalOffset + compensated - top;
            if (Math.Abs(to - scroller.VerticalOffset) >= 0.5)
            {
                scroller.ChangeView(null, Math.Clamp(to, 0, Math.Max(0, scroller.ScrollableHeight)), null, disableAnimation: true);
            }
        });
    }

    // What the cards show

    private bool ApplyStatus(Status? s)
    {
        if (s == status)
        {
            return false;
        }
        status = s;
        statusPanel.Children.Clear();
        statusPanel.Visibility = s is null ? Visibility.Collapsed : Visibility.Visible;
        if (s is null)
        {
            return true;
        }
        var row = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 6 };
        if (s.Loading)
        {
            row.Children.Add(new ProgressRing { IsActive = true, Width = 16, Height = 16, VerticalAlignment = VerticalAlignment.Center });
        }
        var note = new TextBlock
        {
            Text = s.Note,
            TextWrapping = TextWrapping.Wrap,
            VerticalAlignment = VerticalAlignment.Center,
            Foreground = (Brush)Application.Current.Resources["TextFillColorSecondaryBrush"],
        };
        row.Children.Add(note);
        statusPanel.Children.Add(row);
        if (s.Retry)
        {
            var retry = new HyperlinkButton { Content = CoreBoard.Text.TryAgain, Padding = new Thickness(0) };
            AutomationProperties.SetAutomationId(retry, "BoardConversationRetry");
            retry.Click += (_, _) => host.RetryMessages();
            statusPanel.Children.Add(retry);
        }
        return true;
    }

    // Applies the state to every card: the budget's losers let their web
    // views go before a newly opened card makes one.
    private void Refresh()
    {
        if (closed)
        {
            return;
        }
        var live = Live;
        var canFetch = services is not null;
        for (var i = 0; i < cards.Count; i++)
        {
            if (state.ShowsOf(i, live) != CoreBoard.ConversationCards.Shows.Web)
            {
                ApplyCard(i, live, canFetch);
            }
        }
        for (var i = 0; i < cards.Count; i++)
        {
            if (state.ShowsOf(i, live) == CoreBoard.ConversationCards.Shows.Web)
            {
                ApplyCard(i, live, canFetch);
            }
        }
    }

    private void ApplyCard(int i, bool live, bool canFetch)
    {
        var card = cards[i];
        card.Configure(state.Foldable(i), state.Arrow(i, card.IsLong, canFetch));
        card.Apply(state.ShowsOf(i, live), state.IsFolded(i), live);
    }

    private void ScheduleRefresh()
    {
        if (refreshPending || closed)
        {
            return;
        }
        refreshPending = true;
        DispatcherQueue.TryEnqueue(DispatcherQueuePriority.Low, () =>
        {
            refreshPending = false;
            Refresh();
            FetchBodies();
        });
    }

    // Asks for the bodies the open cards need, while the block is live.
    private void FetchBodies()
    {
        if (!Live)
        {
            return;
        }
        for (var i = 0; i < cards.Count; i++)
        {
            if (state.NeedsBody(i))
            {
                state.Asked(i);
                FetchBody(i);
            }
        }
    }

    // message.body for card i through the cache; the answer shows in place.
    // Also the reload of an open card whose message the daemon rebuilt.
    private void FetchBody(int i)
    {
        if (!Live || services is null || account is not { } acct || i < 0 || i >= state.Members.Count
            || state.Members[i].Id is not { } id)
        {
            return;
        }
        var member = state.Members[i];
        var caseId = state.CaseId;
        services.Cache.FetchBody(Summary(id, acct), lm =>
        {
            if (closed || account != acct || state.CaseId != caseId)
            {
                return;
            }
            BodyArrived(member, lm);
        });
    }

    // The summary message.body needs: the ids. Nothing else of it is read
    // (MessageCache.FetchBody).
    private static MessageSummary Summary(MessageId id, AccountId account) => new()
    {
        Id = id,
        AccountId = account,
        FolderId = new FolderId(""),
        Subject = "",
        Date = default,
        Snippet = "",
        HasAttachments = false,
        Size = 0,
    };

    // The cache's news about any message: a card that asked for its body
    // shows what the cache holds now (remote images loaded in Mail, the
    // other variant of the quoted history).
    private void OnMessageLoaded(object? sender, MessageCacheEntry e)
    {
        if (closed || state.IndexOf(e.Id) is not { } i || state.BodyOf(i) == CoreBoard.ConversationCards.Body.Unknown)
        {
            return;
        }
        BodyArrived(state.Members[i], e.Loaded);
    }

    // What the cache holds for member now: HTML for the web view, or
    // anything else for the excerpt.
    private void BodyArrived(CoreBoard.ConversationCards.Member member, LoadedMessage? lm)
    {
        if (closed || lm is null || !lm.BodySettled || lm.Fetching)
        {
            return;
        }
        var i = -1;
        for (var k = 0; k < state.Members.Count; k++)
        {
            if (state.Members[k] == member)
            {
                i = k;
                break;
            }
        }
        if (i < 0 || i >= cards.Count)
        {
            return;
        }
        var card = cards[i];
        var b = lm.Body;
        var html = lm.Err is null && LoadedMessageText.ShowsHtml(b) && b!.HtmlWithheld != true ? b.Html : null;
        var reload = ConversationLayout.PicturesArrived(card.LastBody, b);
        card.LastBody = b;
        card.SetHtml(html, html is null ? [] : b!.Links, reload);
        state.Answered(i, html is not null);
        Refresh();
    }

    private void RemoveAll()
    {
        foreach (var card in cards)
        {
            card.Close();
        }
        cards.Clear();
        cardsColumn.Children.Clear();
        pendingHeights.Clear();
        Hover("");
    }

    private void SetZoom(int percent)
    {
        foreach (var card in cards)
        {
            card.SetZoom(percent);
        }
    }

    // What the status row shows.
    private sealed record Status(bool Loading, string Note, bool Retry);
}
