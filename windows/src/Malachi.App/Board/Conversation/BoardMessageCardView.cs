// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardMessageCardView.swift; Go:
// ui/internal/window/board_conversation.go (boardConversationCard: makeCard,
// update, apply, showText, loadWeb, releaseWeb, setHeight, forwardScroll).
//
// One message of the board detail's conversation: a card with a hairline
// border (the user's own messages on the secondary card fill), the fold
// arrow, the sender in semibold and the time at the end, then the message.
// The message is the plain-text excerpt board.get gave (a TextBlock, never
// markup), or, while the card is open and message.body answered with HTML,
// that sanitised HTML in a card web view of its own (CardWebView: page
// script off, the CSP, no network, links only through OnLink) on white
// under the header, as tall as its document (WebHeightGovernor: capped at
// 4000, frozen when it grows with the view). What the card shows is the
// conversation block's (Board.ConversationCards.ShowsOf): this view only
// applies it (Apply). A folded card shows a preview of the excerpt (its
// lines joined) of at most three lines, not selectable. Every change of the
// card's height the user did not make (the web view's height, HTML in place
// of the text, a fold by the limit, a rebuilt excerpt) goes through the
// block's ChangingHeight, which keeps what the user sees in place. The wheel
// over a web view whose document fits goes on to the detail's column.

using System;
using System.Collections.Generic;
using Malachi.App.Resources;
using Malachi.App.WebViews;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using Windows.Foundation;
using CoreBoard = Malachi.Core.Boards.Board;

namespace Malachi.App.Boards;

/// <summary>One card of the board detail's conversation (BoardMessageCardView).</summary>
public sealed partial class BoardMessageCardView : UserControl
{
    /// <summary>The lines a folded card keeps.</summary>
    public const int FoldedLines = 3;

    private const double PadH = 13;
    private const double PadV = 9;

    // The paper starts at this height before its document reports one,
    // never less (Swift minimumPaper).
    private const double MinimumPaper = ConversationLayout.InitialWebHeight / 2;

    private readonly BoardConversationBlock block;
    private readonly Border frame = new();
    private readonly Button foldButton = new();
    private readonly FontIcon foldGlyph = new() { FontSize = 12 };
    private readonly TextBlock fromLabel = new();
    private readonly TextBlock whenLabel = new();
    private readonly TextBlock body = new();
    private readonly Grid htmlHost = new();

    private CoreBoard.MessageCard message;
    private bool foldable;
    private bool arrowAlways;

    // Whether the excerpt is longer than FoldedLines at the text's width;
    // null until measured.
    private bool? isLong;
    private double measuredWidth = -1;

    // The HTML body: the sanitiser's output from message.body, the body's
    // links as the daemon listed them, and whether the pictures it shows
    // arrived since it was loaded (the same malachi-cid: URLs have something
    // to serve now).
    private string? html;
    private IReadOnlyList<Link> links = [];
    private bool reload;
    private CardWebView? web;
    private string? webDoc;
    private WebHeightGovernor gov = new(1);
    private double? webHeight;

    /// <summary>The card of <paramref name="message"/> in <paramref name="block"/>.</summary>
    public BoardMessageCardView(BoardConversationBlock block, CoreBoard.MessageCard message)
    {
        ArgumentNullException.ThrowIfNull(block);
        ArgumentNullException.ThrowIfNull(message);
        this.block = block;
        this.message = message;
        IsTabStop = false;

        foldGlyph.Glyph = Icons.Glyph("pan-end");
        foldButton.Content = foldGlyph;
        foldButton.MinWidth = 24;
        foldButton.MinHeight = 20;
        foldButton.Padding = new Thickness(4, 2, 4, 2);
        foldButton.BorderThickness = new Thickness(0);
        foldButton.Background = new SolidColorBrush(Microsoft.UI.Colors.Transparent);
        foldButton.VerticalAlignment = VerticalAlignment.Center;
        foldButton.Visibility = Visibility.Collapsed;
        AutomationProperties.SetAutomationId(foldButton, "BoardConversationFold");
        foldButton.Click += (_, _) => block.UserFolded(this, !Folded);

        fromLabel.FontWeight = FontWeights.SemiBold;
        fromLabel.MaxLines = 1;
        fromLabel.TextWrapping = TextWrapping.NoWrap;
        fromLabel.TextTrimming = TextTrimming.CharacterEllipsis;
        fromLabel.VerticalAlignment = VerticalAlignment.Center;
        fromLabel.IsTextSelectionEnabled = true;
        whenLabel.FontSize = 12;
        whenLabel.MaxLines = 1;
        whenLabel.TextWrapping = TextWrapping.NoWrap;
        whenLabel.VerticalAlignment = VerticalAlignment.Center;
        SetSecondary(whenLabel);

        var head = new Grid { ColumnSpacing = 8, Margin = new Thickness(PadH, PadV, PadH, 4) };
        head.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        head.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        head.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        Grid.SetColumn(fromLabel, 1);
        Grid.SetColumn(whenLabel, 2);
        head.Children.Add(foldButton);
        head.Children.Add(fromLabel);
        head.Children.Add(whenLabel);

        body.TextWrapping = TextWrapping.Wrap;
        body.Margin = new Thickness(PadH, 0, PadH, PadV);
        body.SizeChanged += (_, e) =>
        {
            if (Math.Abs(e.NewSize.Width - e.PreviousSize.Width) >= 0.5)
            {
                Measure();
            }
        };

        htmlHost.Background = new SolidColorBrush(Microsoft.UI.Colors.White);
        htmlHost.Visibility = Visibility.Collapsed;
        // forwardScroll: the wheel over the web view of a document that fits
        // (nothing to scroll inside) goes on to the detail's column.
        htmlHost.AddHandler(PointerWheelChangedEvent, new PointerEventHandler(OnHtmlWheel), handledEventsToo: true);

        var column = new StackPanel();
        column.Children.Add(head);
        column.Children.Add(body);
        column.Children.Add(htmlHost);
        frame.Child = column;
        frame.BorderThickness = new Thickness(1);
        frame.CornerRadius = new CornerRadius(10);
        frame.BorderBrush = Brush("CardStrokeColorDefaultBrush");
        Content = frame;
        Show(message);
        ShowText();
    }

    /// <summary>The message the card shows.</summary>
    public CoreBoard.MessageCard Message => message;

    /// <summary>The card is folded to its preview.</summary>
    public bool Folded { get; private set; }

    /// <summary>What the card shows now (<see cref="Apply"/>).</summary>
    public CoreBoard.ConversationCards.Shows Shows { get; private set; } = CoreBoard.ConversationCards.Shows.Text;

    /// <summary>Whether the excerpt is longer than the folded preview (null until measured).</summary>
    public bool? IsLong => isLong;

    /// <summary>The body message.body last gave for the card (its pictures arriving reload the document).</summary>
    public MessageBodyResult? LastBody { get; set; }

    /// <summary>The card holds a web view now.</summary>
    public bool HasWebView => web is not null;

    /// <summary>
    /// update: the message was rebuilt in place (same id, other excerpt) or
    /// its sender, time or tint changed: the same card, with its fold and its
    /// web view. True when anything shown changed.
    /// </summary>
    public bool Update(CoreBoard.MessageCard m)
    {
        ArgumentNullException.ThrowIfNull(m);
        if (m == message)
        {
            return false;
        }
        block.ChangingHeight(this, () =>
        {
            if (m.Text != message.Text)
            {
                isLong = null;
                measuredWidth = -1;
            }
            Show(m);
            if (web is null)
            {
                ShowText();
            }
            Measure();
        });
        return true;
    }

    /// <summary>
    /// The card's arrow: a card that folds (an older message) shows it when
    /// <paramref name="arrow"/> (Board.ConversationCards.Arrow) says so.
    /// </summary>
    public void Configure(bool foldable, bool arrow)
    {
        this.foldable = foldable;
        arrowAlways = arrow;
        if (!foldable)
        {
            Folded = false;
        }
        ShowArrow();
    }

    /// <summary>
    /// The sanitised HTML that message.body gave for the card's message, and
    /// its links; null drops it (the excerpt stays). <paramref name="reloadPictures"/>:
    /// the pictures it shows arrived since it was loaded. A web view showing
    /// the card loads the new document.
    /// </summary>
    public void SetHtml(string? html, IReadOnlyList<Link> links, bool reloadPictures)
    {
        this.html = html;
        this.links = html is null ? [] : links ?? [];
        reload = reload || reloadPictures;
        if (html is not null && web is not null)
        {
            LoadWebView();
        }
    }

    /// <summary>
    /// Shows what the block decided: the preview, the whole excerpt, or the
    /// HTML in a web view (<paramref name="live"/>: one may be made now).
    /// Web without HTML shows the excerpt.
    /// </summary>
    public void Apply(CoreBoard.ConversationCards.Shows shows, bool folded, bool live)
    {
        var target = shows == CoreBoard.ConversationCards.Shows.Web && (html is null || !live)
            ? CoreBoard.ConversationCards.Shows.Text
            : shows;
        var nowFolded = foldable && folded;
        if (target == Shows && nowFolded == Folded)
        {
            if (target == CoreBoard.ConversationCards.Shows.Web)
            {
                LoadWebView();
                ShowArrow();
            }
            else if (Folded)
            {
                // The measured length may cut the preview now, or not.
                block.ChangingHeight(this, ShowText);
            }
            else
            {
                ShowArrow();
            }
            return;
        }
        block.ChangingHeight(this, () =>
        {
            Folded = nowFolded;
            Shows = target;
            if (target == CoreBoard.ConversationCards.Shows.Web)
            {
                ShowWeb();
            }
            else
            {
                ReleaseWebView();
                ShowText();
            }
        });
    }

    /// <summary>The text-zoom setting changed: the web view's document is scaled and measured again.</summary>
    public void SetZoom(int percent)
    {
        if (web is not null)
        {
            gov.WidthChanged();
            web.Zoom = percent;
        }
    }

    /// <summary>The card goes for good: its web view is released.</summary>
    public void Close()
    {
        ReleaseWebView();
    }

    private static Brush Brush(string key) => (Brush)Application.Current.Resources[key];

    private static void SetSecondary(TextBlock t) => t.Foreground = Brush("TextFillColorSecondaryBrush");

    private void Show(CoreBoard.MessageCard m)
    {
        message = m;
        fromLabel.Text = m.From;
        whenLabel.Text = m.When;
        frame.Background = Brush(m.Mine ? "CardBackgroundFillColorSecondaryBrush" : "CardBackgroundFillColorDefaultBrush");
        // A group named after the sender.
        AutomationProperties.SetName(this, m.From);
    }

    // The whole excerpt or the preview, as Folded and the measured length say.
    private void ShowText()
    {
        var cut = Folded && isLong != false;
        var text = cut ? string.Join(' ', message.Text.Split(['\r', '\n'], StringSplitOptions.RemoveEmptyEntries)) : message.Text;
        // The same text again keeps the selection.
        if (!string.Equals(body.Text, text, StringComparison.Ordinal))
        {
            body.Text = text;
        }
        body.MaxLines = cut ? FoldedLines : 0;
        body.TextTrimming = cut ? TextTrimming.CharacterEllipsis : TextTrimming.None;
        body.IsTextSelectionEnabled = !cut;
        body.Visibility = Visibility.Visible;
        htmlHost.Visibility = Visibility.Collapsed;
        ShowArrow();
    }

    private void ShowArrow()
    {
        var arrow = foldable && arrowAlways;
        foldButton.Visibility = arrow ? Visibility.Visible : Visibility.Collapsed;
        var tip = Folded ? L10n.T("Expand") : L10n.T("Collapse");
        foldGlyph.Glyph = Icons.Glyph(Folded ? "pan-end" : "pan-down");
        ToolTipService.SetToolTip(foldButton, tip);
        AutomationProperties.SetName(foldButton, tip);
    }

    // The HTML: the white host under the header at the height last known
    // (the text's before the document reports one), the web view in it.
    private void ShowWeb()
    {
        var start = webHeight ?? Math.Max(body.Visibility == Visibility.Visible ? body.ActualHeight : 0, MinimumPaper);
        body.Visibility = Visibility.Collapsed;
        htmlHost.Height = start;
        htmlHost.Visibility = Visibility.Visible;
        ShowArrow();
        LoadWebView();
    }

    private void LoadWebView()
    {
        if (html is null)
        {
            return;
        }
        if (web is null)
        {
            web = block.MakeWebView();
            gov = new WebHeightGovernor(1);
            web.OnLink = link => block.OpenLink(link, links);
            web.OnHover = block.Hover;
            web.OnSize = SizeReported;
            web.OnFailed = HtmlUnavailable;
            web.Height = htmlHost.Height;
            htmlHost.Children.Add(web);
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

    private void ReleaseWebView()
    {
        if (web is not { } view)
        {
            return;
        }
        web = null;
        webDoc = null;
        htmlHost.Children.Remove(view);
        view.Release();
        block.Hover("");
    }

    // The view could not show the document: the excerpt, as for HTML the
    // daemon withheld; the block takes the card out of the count.
    private void HtmlUnavailable()
    {
        html = null;
        links = [];
        block.ChangingHeight(this, () =>
        {
            ReleaseWebView();
            Shows = CoreBoard.ConversationCards.Shows.Text;
            ShowText();
        });
        block.HtmlUnavailable(this);
    }

    // sizeReported: the governor decides the view's height (capped, frozen
    // for a document that grows with the view).
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
        block.ChangingHeight(this, () =>
        {
            webHeight = h;
            htmlHost.Height = h;
            if (web is not null)
            {
                web.Height = h;
            }
        });
    }

    private void OnHtmlWheel(object sender, PointerRoutedEventArgs e)
    {
        if (web is null || !gov.Fits)
        {
            return;
        }
        var delta = e.GetCurrentPoint(htmlHost).Properties.MouseWheelDelta;
        if (delta == 0)
        {
            return;
        }
        block.ScrollBy(-delta);
        e.Handled = true;
    }

    // Whether the whole excerpt is longer than FoldedLines at the text's
    // width; the arrow and the preview follow when that changes.
    private void Measure()
    {
        var width = body.ActualWidth;
        if (width <= 0 || Math.Abs(width - measuredWidth) < 0.5)
        {
            return;
        }
        measuredWidth = width;
        var whole = new TextBlock { Text = message.Text, TextWrapping = TextWrapping.Wrap, FontSize = body.FontSize, FontFamily = body.FontFamily };
        whole.Measure(new Size(width, double.PositiveInfinity));
        var line = new TextBlock { FontSize = body.FontSize, FontFamily = body.FontFamily };
        // Windows-only string: the probe of one line's height, never shown.
        line.Text = "X";
        line.Measure(new Size(double.PositiveInfinity, double.PositiveInfinity));
        var longer = whole.DesiredSize.Height > (line.DesiredSize.Height * FoldedLines) + 1;
        if (longer == isLong)
        {
            return;
        }
        isLong = longer;
        block.LengthMeasured(this);
    }
}
