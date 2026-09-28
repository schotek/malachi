// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageView/MessageViewController.swift
// (show, clear, showEmbedded, render, renderHeaders, renderBody, loading,
// cancelSpinner, showText, setBodyPage, htmlUnavailable,
// renderOutboxBanner, renderRemoteBar, showRemoteBar, renderAttachments,
// setPage) and Windows/EmbeddedWindowController.swift (show, loadImages);
// GTK: ui/internal/window/message_view.go (messageView: showMessage with
// bodyGen, render, renderHeaders, renderBody, loading with
// bodySpinnerDelay, showText, setBarVisible, setBarLoading), remote.go
// (showRemoteBar, renderRemoteBar), outbox.go (renderOutboxBanner),
// attachments.go (renderAttachments) and embedded.go (show, loadImages).
//
// One message display, minus the widgets, which macOS keeps in AppKit
// (docs/windows-port.md §7.4): what the pane, a message window or an
// attached message's window shows, as observable properties the WinUI
// MessageView binds to. It asks the cache for what it shows and renders
// whatever the cache holds; rendering is idempotent, and the
// MessageWindowRegistry re-renders every view showing a message when the
// cache learns something new about it. A reply for a message the pane has
// moved on from is dropped by the generation (GTK bodyGen, macOS
// current?.id). The body area stays blank for SpinnerDelay before the
// spinner shows (a timer on the TimeProvider, its tick posted back to the
// UI thread). Everything shown is server data: the view sets it as plain
// text only, and the HTML goes to the viewer only as the sanitiser's
// output. A body the viewer gave up on (Unavailable after its one reload,
// docs/windows-port.md §6.1) is shown as plain text with the hint, and the
// same body is not loaded again by a later render of the same message.
// UI-thread-affine.

using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using CommunityToolkit.Mvvm.ComponentModel;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Platform;
using Malachi.Core.Text;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Presentation;

/// <summary>What one message view shows (message_view.go <c>messageView</c>).</summary>
public sealed partial class ReaderController : ObservableObject, IDisposable
{
    /// <summary>
    /// How long the body area stays blank before the spinner appears
    /// (message_view.go <c>bodySpinnerDelay</c>): bodies come from the local
    /// store, so most messages render sooner and never show a spinner.
    /// </summary>
    public static readonly TimeSpan SpinnerDelay = TimeSpan.FromMilliseconds(400);

    private readonly IReaderCache cache;
    private readonly IFileTypePolicy? policy;
    private readonly TimeProvider time;
    private readonly SynchronizationContext? context;
    private readonly ILogger logger;

    private ITimer? spinner;
    private int spinnerTicket;
    private int bodyGen;
    private bool closed;
    private bool hasAccounts = true;

    // The body last rendered, for the fallback to its plain text when the
    // viewer cannot show its HTML (macOS renderedBody).
    private MessageBodyResult? renderedBody;

    // The HTML the viewer gave up on for the message on display.
    private string? failedHtml;

    // A plain-text body scrolls to its top the first time it is shown for a
    // message, not on every re-render (macOS scrollToTopPending).
    private bool scrollToTopPending = true;

    // Embedded mode (embedded.go EmbeddedWindow): the part, the result on
    // display and whether its images are on their way.
    private string? part;
    private MessageEmbeddedResult? shown;
    private bool loadingImages;

    /// <summary>A view of <paramref name="mode"/> over <paramref name="cache"/>.</summary>
    /// <param name="mode">Where the view lives.</param>
    /// <param name="cache">The loaded messages.</param>
    /// <param name="policy">What the platform would run (a chip's Open); <see cref="DangerousTypes"/> without one.</param>
    /// <param name="timeProvider">The spinner's clock (the system's when null).</param>
    /// <param name="logger">Kinds and codes only, never mail content.</param>
    public ReaderController(
        ReaderMode mode, IReaderCache cache, IFileTypePolicy? policy = null, TimeProvider? timeProvider = null, ILogger<ReaderController>? logger = null)
    {
        ArgumentNullException.ThrowIfNull(cache);
        Mode = mode;
        this.cache = cache;
        this.policy = policy;
        time = timeProvider ?? TimeProvider.System;
        context = SynchronizationContext.Current;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        Page = mode == ReaderMode.Pane ? ReaderPage.Empty : ReaderPage.Message;
    }

    /// <summary>Called after every render with what was rendered (a window's title and star follow it; macOS <c>onRender</c>).</summary>
    public event EventHandler<ReaderRender>? Rendered;

    /// <summary>The plain-text body should scroll to its top (the first text shown for a message).</summary>
    public event EventHandler? ScrollToTopRequested;

    /// <summary>Where the view lives.</summary>
    public ReaderMode Mode { get; }

    /// <summary>
    /// What is on display: the summary <see cref="Show"/> was given, or the
    /// attached message's own summary in embedded mode (it carries the
    /// containing message's id); null on the pane's placeholder pages.
    /// </summary>
    public MessageSummary? Current { get; private set; }

    /// <summary>The message an attached message's view was opened from (embedded mode).</summary>
    public MessageSummary? Containing { get; private set; }

    /// <summary>The links of the body on display, as the daemon listed them, for link activation.</summary>
    public IReadOnlyList<Link> Links { get; private set; } = [];

    /// <summary>The From, To and Cc lines.</summary>
    public AddressHeader Addresses { get; } = new();

    /// <summary>Whether the remote-image bar has Always From This Sender (not in an attached message's window).</summary>
    public bool TrustVisible => Mode != ReaderMode.Embedded;

    /// <summary>Whether a message of the Drafts folder is on display (the pane's draft banner); set by the application.</summary>
    public Func<MessageSummary, bool>? IsDraft { get; set; }

    /// <summary>Shows a message in the view's window (a failed image load of an attached message).</summary>
    public Action<string>? Toast { get; set; }

    /// <summary>The page of the message stack; a window always shows the message.</summary>
    [ObservableProperty]
    public partial ReaderPage Page { get; private set; }

    /// <summary>The subject (message_subject), or "(No subject)".</summary>
    [ObservableProperty]
    public partial string Subject { get; private set; } = "";

    /// <summary>The date (message_date), "" when the message has none.</summary>
    [ObservableProperty]
    public partial string DateText { get; private set; } = "";

    /// <summary>Whether the hint that only the plain text is shown is visible (body_hint).</summary>
    [ObservableProperty]
    public partial bool HintVisible { get; private set; }

    /// <summary>The page of the body area.</summary>
    [ObservableProperty]
    public partial ReaderBodyPage BodyPage { get; private set; }

    /// <summary>The plain text of the body area (message_body).</summary>
    [ObservableProperty]
    public partial string BodyText { get; private set; } = "";

    /// <summary>
    /// The sanitised HTML the viewer shows while <see cref="BodyPage"/> is
    /// <see cref="ReaderBodyPage.Html"/>; null otherwise, which drops the
    /// viewer's document and its pictures.
    /// </summary>
    [ObservableProperty]
    public partial string? Html { get; private set; }

    /// <summary>Whether the remote-image bar is shown.</summary>
    [ObservableProperty]
    public partial bool RemoteBarVisible { get; private set; }

    /// <summary>Whether the images are on their way: the spinner instead of the buttons.</summary>
    [ObservableProperty]
    public partial bool RemoteBarLoading { get; private set; }

    /// <summary>The bar's sentence.</summary>
    [ObservableProperty]
    public partial string RemoteBarText { get; private set; } = "";

    /// <summary>Whether the outbox banner is shown (a message in the outbox, not delivered yet).</summary>
    [ObservableProperty]
    public partial bool OutboxVisible { get; private set; }

    /// <summary>The outbox banner's sentence (plain text: it may carry the daemon's error).</summary>
    [ObservableProperty]
    public partial string OutboxTitle { get; private set; } = "";

    /// <summary>The outbox banner's button (Retry), "" for none.</summary>
    [ObservableProperty]
    public partial string OutboxButton { get; private set; } = "";

    /// <summary>Whether the delivery failed (the banner warns).</summary>
    [ObservableProperty]
    public partial bool OutboxFailed { get; private set; }

    /// <summary>Whether the draft banner is shown (the pane, a message of the Drafts folder).</summary>
    [ObservableProperty]
    public partial bool DraftVisible { get; private set; }

    /// <summary>The attachment chips; empty hides the box.</summary>
    [ObservableProperty]
    public partial IReadOnlyList<AttachmentChip> Chips { get; private set; } = [];

    /// <summary>What Save All saves; empty hides it.</summary>
    [ObservableProperty]
    public partial IReadOnlyList<Attachment> SaveAll { get; private set; } = [];

    // Showing

    /// <summary>
    /// Displays message <paramref name="s"/>: the summary headers at once,
    /// the rest (and the outbox banner, for a queued message) when the cache
    /// has it (message_view.go <c>showMessage</c>, message_window.go
    /// <c>show</c>). A reply for an earlier message is dropped.
    /// </summary>
    public void Show(MessageSummary s)
    {
        ArgumentNullException.ThrowIfNull(s);
        if (Mode == ReaderMode.Embedded || closed)
        {
            return;
        }
        if (Current?.Id != s.Id)
        {
            scrollToTopPending = true;
            failedHtml = null;
        }
        Current = s;
        var gen = ++bodyGen;
        Page = ReaderPage.Message;
        if (Mode == ReaderMode.Pane)
        {
            DraftVisible = IsDraft?.Invoke(s) == true;
        }
        if (cache.Loaded(s.Id) is { Complete: true } complete)
        {
            Render(s, complete);
            return;
        }
        // The fetch first: it clears a stale body error before its retry, so
        // the render below shows the wait rather than the old error.
        cache.Fetch(s, lm =>
        {
            if (closed || gen != bodyGen || Current?.Id != s.Id)
            {
                return; // the view moved on
            }
            Render(s, lm);
        });
        if (gen == bodyGen)
        {
            Render(s, cache.Loaded(s.Id));
        }
    }

    /// <summary>
    /// The pane's placeholder: No Message Selected, or No Accounts while the
    /// daemon has none (window.go <c>onMessageRowSelected</c> with no row,
    /// <c>emptyPageName</c>); windows never clear.
    /// </summary>
    public void Clear()
    {
        if (Mode != ReaderMode.Pane)
        {
            return;
        }
        bodyGen++;
        Current = null;
        Links = [];
        renderedBody = null;
        failedHtml = null;
        scrollToTopPending = true;
        CancelSpinner();
        OutboxVisible = false;
        DraftVisible = false;
        SetBarVisible(false);
        Html = null; // drop the pictures of the message before
        Page = hasAccounts ? ReaderPage.Empty : ReaderPage.NoAccounts;
    }

    /// <summary>
    /// <c>account.list</c> answered (folders.go <c>loadAccounts</c>): the
    /// placeholder becomes No Accounts while there is none, unless a message
    /// is on display.
    /// </summary>
    public void SetHasAccounts(bool has)
    {
        hasAccounts = has;
        if (Mode == ReaderMode.Pane && Page != ReaderPage.Message)
        {
            Page = has ? ReaderPage.Empty : ReaderPage.NoAccounts;
        }
    }

    /// <summary>
    /// Renders an attached message (embedded.go <c>show</c>): its own headers
    /// and body. <paramref name="containing"/> is the message it was attached
    /// to, <paramref name="partId"/> the part.
    /// </summary>
    public void ShowEmbedded(MessageSummary containing, string partId, MessageEmbeddedResult result)
    {
        ArgumentNullException.ThrowIfNull(containing);
        ArgumentNullException.ThrowIfNull(partId);
        ArgumentNullException.ThrowIfNull(result);
        if (Mode != ReaderMode.Embedded || closed)
        {
            return;
        }
        Containing = containing;
        part = partId;
        shown = result;
        var s = result.Message.Summary;
        Current = s;
        scrollToTopPending = true;
        failedHtml = null;
        Render(s, new LoadedMessage { Msg = result.Message, Body = result.Body });
    }

    /// <summary>
    /// The bar's Load Images in an attached message's view (embedded.go
    /// <c>loadImages</c>): <c>message.embedded</c> again with remote images
    /// allowed for this one call, shown in place of what is on display; a
    /// failure is a toast and the bar offers the images again.
    /// </summary>
    public async Task LoadEmbeddedImagesAsync()
    {
        if (Mode != ReaderMode.Embedded || loadingImages || closed || Containing is not { } c || part is not { } p || shown is not { } before)
        {
            return;
        }
        loadingImages = true;
        ShowRemoteBar(new RemoteBarState(Visible: true, Loading: true));
        MessageEmbeddedResult? result = null;
        Exception? error = null;
        try
        {
            result = await cache.FetchEmbeddedAsync(c.AccountId, c.Id, p, RemoteContentPolicy.Allow);
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            error = e;
        }
        loadingImages = false;
        if (closed)
        {
            return;
        }
        if (result is null)
        {
            LogEmbeddedImagesFailed(logger, error?.GetType().Name ?? "");
            Toast?.Invoke(RpcErrorText.Text(L10n.T("Loading the images"), error));
            // The bar offers the images again.
            RenderRemoteBar(new LoadedMessage { Body = before.Body });
            return;
        }
        ShowEmbedded(c, p, result);
    }

    // Rendering

    /// <summary>
    /// Shows whatever <paramref name="lm"/> holds so far (message_view.go
    /// <c>render</c>): the full headers once <c>message.get</c> answered, the
    /// body once <c>message.body</c> did, the chips from both; null shows the
    /// summary and the wait.
    /// </summary>
    public void Render(MessageSummary s, LoadedMessage? lm)
    {
        ArgumentNullException.ThrowIfNull(s);
        if (closed)
        {
            return;
        }
        RenderHeaders(s, lm?.Msg);
        if (lm is { BodySettled: true })
        {
            RenderBody(lm);
        }
        else
        {
            Loading();
        }
        RenderAttachments(s, lm);
        if (Mode != ReaderMode.Embedded)
        {
            RenderOutboxBanner(lm?.Msg);
        }
        Rendered?.Invoke(this, new ReaderRender(s, lm));
    }

    /// <summary>Redraws the remote-image bar for <paramref name="lm"/> and leaves the body alone (remote.go <c>refreshRemoteBar</c>).</summary>
    public void RefreshRemoteBar(LoadedMessage lm)
    {
        ArgumentNullException.ThrowIfNull(lm);
        if (!closed)
        {
            RenderRemoteBar(lm);
        }
    }

    /// <summary>
    /// The delivery state of <paramref name="m"/> (null while
    /// <c>message.get</c> has not answered) on the banner (outbox.go
    /// <c>renderOutboxBanner</c>). The title may carry the daemon's error
    /// message: plain text.
    /// </summary>
    public void RenderOutboxBanner(Message? m)
    {
        if (Mode == ReaderMode.Embedded)
        {
            return;
        }
        var (title, button, visible) = Outbox.OutboxBannerText(m?.Summary.Outbox);
        OutboxFailed = m?.Summary.Outbox?.State == OutboxState.Failed;
        OutboxTitle = title;
        OutboxButton = button;
        OutboxVisible = visible;
    }

    /// <summary>Puts the remote-image bar in state <paramref name="st"/> (remote.go <c>showRemoteBar</c>).</summary>
    public void ShowRemoteBar(RemoteBarState st)
    {
        if (st.Loading)
        {
            RemoteBarText = L10n.T("Loading remote images…");
        }
        else if (st.Blocked > 0)
        {
            // TRANSLATORS: %d is the number of remote images the message tried to load.
            RemoteBarText = L10n.N("%d remote image was blocked", "%d remote images were blocked", st.Blocked);
        }
        RemoteBarLoading = st.Loading;
        RemoteBarVisible = st.Visible;
    }

    /// <summary>
    /// The viewer will show nothing (it could not be created, its protections
    /// could not be applied, or the body failed again after its one reload):
    /// the plain text with the hint instead, as for HTML the daemon withheld
    /// (MessageViewController.swift <c>htmlUnavailable</c>). The same body is
    /// not loaded again; the next one tries the viewer again.
    /// </summary>
    public void HtmlUnavailable()
    {
        if (closed || renderedBody is not { } b || !LoadedMessageText.ShowsHtml(b))
        {
            return;
        }
        LogHtmlUnavailable(logger);
        failedHtml = b.Html;
        ShowPlainInsteadOfHtml(b);
    }

    /// <summary>The owning window closed: late replies and the spinner's timer are dropped.</summary>
    public void Close()
    {
        closed = true;
        CancelSpinner();
    }

    /// <inheritdoc/>
    public void Dispose() => Close();

    // renderHeaders: from the summary alone, or from the full message when m
    // is not null (recipients with Cc).
    private void RenderHeaders(MessageSummary s, Message? m)
    {
        var from = s.From;
        var to = s.To;
        IReadOnlyList<Address>? cc = null;
        var date = s.Date;
        var subject = s.Subject;
        if (m is not null)
        {
            from = m.Summary.From;
            to = m.Summary.To;
            cc = m.Cc;
            date = m.Summary.Date;
            subject = m.Summary.Subject;
        }
        Subject = LoadedMessageText.SubjectText(subject);
        Addresses.Show(s.Id, s.AccountId, from, to, cc);
        DateText = date.IsGoZero ? "" : Format.FormatDateTime(date);
    }

    // renderBody: the sanitised HTML in the viewer when there is one, the
    // plain text otherwise, with the hint when the HTML was withheld and the
    // bar when remote images were removed; or the error that prevented it.
    private void RenderBody(LoadedMessage lm)
    {
        CancelSpinner();
        Links = [];
        renderedBody = lm.Body;
        if (lm.Err is { } err)
        {
            HintVisible = false;
            SetBarVisible(false);
            ShowText(RpcErrorText.Text(L10n.T("Loading the message"), err));
            return;
        }
        var b = lm.Body;
        if (LoadedMessageText.ShowsHtml(b))
        {
            if (string.Equals(b!.Html, failedHtml, StringComparison.Ordinal))
            {
                ShowPlainInsteadOfHtml(b);
                return;
            }
            Links = b.Links;
            HintVisible = false;
            Html = b.Html;
            SetBodyPage(ReaderBodyPage.Html);
            RenderRemoteBar(lm);
            return;
        }
        HintVisible = b?.HtmlWithheld == true;
        SetBarVisible(false);
        ShowText(LoadedMessageText.BodyText(b));
    }

    private void ShowPlainInsteadOfHtml(MessageBodyResult b)
    {
        Links = [];
        HintVisible = true;
        SetBarVisible(false);
        ShowText(LoadedMessageText.BodyText(b));
    }

    // loading: blank at once (which also drops the pictures of the message
    // before), the spinner after SpinnerDelay if the body is still missing.
    private void Loading()
    {
        CancelSpinner();
        Links = [];
        renderedBody = null;
        HintVisible = false;
        SetBarVisible(false);
        ShowText("");
        var ticket = spinnerTicket;
        spinner = time.CreateTimer(_ => Tick(ticket), null, SpinnerDelay, Timeout.InfiniteTimeSpan);
    }

    private void Tick(int ticket)
    {
        if (context is null)
        {
            Expire(ticket);
        }
        else
        {
            context.Post(_ => Expire(ticket), null);
        }
    }

    // A tick for a spinner that was cancelled on its way does nothing.
    private void Expire(int ticket)
    {
        if (ticket != spinnerTicket || closed)
        {
            return;
        }
        CancelSpinner();
        SetBodyPage(ReaderBodyPage.Loading);
    }

    // cancelSpinner: disarms the pending spinner; safe to call twice.
    private void CancelSpinner()
    {
        spinnerTicket++;
        spinner?.Dispose();
        spinner = null;
    }

    private void ShowText(string text)
    {
        BodyText = text;
        SetBodyPage(ReaderBodyPage.Text);
        Html = null; // drop the pictures of the previous message
    }

    private void SetBodyPage(ReaderBodyPage page)
    {
        BodyPage = page;
        if (page == ReaderBodyPage.Text && scrollToTopPending)
        {
            scrollToTopPending = false;
            ScrollToTopRequested?.Invoke(this, EventArgs.Empty);
        }
    }

    private void RenderRemoteBar(LoadedMessage lm) => ShowRemoteBar(RemoteBar.RemoteBarStateFor(lm));

    private void SetBarVisible(bool visible) => RemoteBarVisible = visible;

    // renderAttachments: the chips are rebuilt only when what they show
    // changed, so that a re-render (message.get answering after the body)
    // leaves a menu open on a chip alone.
    private void RenderAttachments(MessageSummary s, LoadedMessage? lm)
    {
        var (chips, saveAll) = AttachmentChip.For(s, lm, Mode == ReaderMode.Embedded, policy);
        if (!SameChips(Chips, chips))
        {
            Chips = chips;
        }
        if (!SameAttachments(SaveAll, saveAll))
        {
            SaveAll = saveAll;
        }
    }

    private static bool SameChips(IReadOnlyList<AttachmentChip> a, IReadOnlyList<AttachmentChip> b)
    {
        if (a.Count != b.Count)
        {
            return false;
        }
        for (var i = 0; i < a.Count; i++)
        {
            var x = a[i];
            var y = b[i];
            if (x.Attachment != y.Attachment || x.Message.Id != y.Message.Id || x.Message.AccountId != y.Message.AccountId
                || x.Label != y.Label || x.SizeText != y.SizeText || x.Tooltip != y.Tooltip || x.ArrowTooltip != y.ArrowTooltip
                || x.Available != y.Available || x.Nested != y.Nested || x.CanOpen != y.CanOpen)
            {
                return false;
            }
        }
        return true;
    }

    private static bool SameAttachments(IReadOnlyList<Attachment> a, IReadOnlyList<Attachment> b)
    {
        if (a.Count != b.Count)
        {
            return false;
        }
        for (var i = 0; i < a.Count; i++)
        {
            if (a[i] != b[i])
            {
                return false;
            }
        }
        return true;
    }

    [LoggerMessage(Level = LogLevel.Error, Message = "HTML body not shown: the viewer is unavailable")]
    private static partial void LogHtmlUnavailable(ILogger logger);

    [LoggerMessage(Level = LogLevel.Warning, Message = "message.embedded (allow) failed: {Kind}")]
    private static partial void LogEmbeddedImagesFailed(ILogger logger, string kind);
}
