// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Compose/ComposePane.swift (Options, End,
// init, finish, cleanup, buildInlineContent, footerRow, editorReported,
// applyEditorHeight, wireRows, wireDraft, updateTitle, ComposeForm,
// ComposeWindowHandle's edits and setAccounts, the compose actions); GTK:
// ui/internal/compose/pane.go (Pane, NewPane, setAccounts, account,
// wireRows, updateTitle, recipients, setSendEnabled, closeForm,
// handleSent), pane_board.go (Settle, Finish, Close, Abandon,
// HasUnsavedText, IsSending, IsLost, ReplyTitle, FocusEditorStart,
// EditorHasFocus, SetDialogParent as the host's HostWindow) and
// pane_layout.go (buildInlineFooter, wireInlineShortcuts, wireSizedEditor,
// SetVisibleHeight, applyEditorHeight).
//
// The content of a compose window, and of the board's inline reply: the
// header fields (or the comment's card), the formatting bar, the editor,
// the attachment chips, the status line, and the draft behind them. The
// draft's lifecycle is Core's ComposeDraftController, which reads the pane
// through IComposeForm; the attachments are Core's
// ComposeAttachmentsController, the recipient completion Core's
// SuggestionsController, the header's rules ComposeHeaderRules. The pane
// talks back to what hosts it only through IComposePaneHost.
//
// Two layouts (Options.Layout): Window is the compose window's content as
// it always was (the window keeps its title bar, the Draft Menu, the close
// question, Escape, Quit, the assistant's rewrite and its commands' keys,
// and runs the pane's Send, Save Draft, Attach, Insert Image and Discard);
// Inline is a reply edited in place in the board's case detail: no From row
// (the reply is from-locked; the detail shows the account, and the account
// is always the params' own), the editor in its sized mode (EditorChannel
// sized: as tall as its document between EditorHeight's minimum and its
// cap of the host's visible height, then it scrolls inside), and a footer
// row of its own with Attach, the status line, Discard and Send, the toasts
// going to the host.
//
// Windows specifics: the inline Send has no key of its own (a button's
// accelerator would fire from anywhere in the main window); Ctrl+Enter and
// Ctrl+S are KeyboardAccelerators of the pane's root scoped to it, so they
// run only while XAML's keyboard focus is inside the pane. While the
// editor's WebView2 has the keyboard no XAML key event fires: the host
// window's router decides there, and runs the pane's Send and SaveDraft
// when EditorHasFocus (as the compose window's router runs its commands).
// TextBox.TextChanged also comes for the pane's own prefill, after the
// setter returned: the Subject counts as edited when its text differs from
// the text last seen in it.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.App.Shell;
using Malachi.App.WebViews;
using Malachi.Core.Api;
using Malachi.Core.Board;
using Malachi.Core.Compose;
using Malachi.Core.Controllers;
using Malachi.Core.Html;
using Malachi.Core.I18n;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Presentation;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using WinUIKey = Windows.System.VirtualKey;
using WinUIModifiers = Windows.System.VirtualKeyModifiers;

namespace Malachi.App.Compose;

/// <summary>The content of a compose window or of the board's inline reply (ComposePane, compose.Pane).</summary>
[System.Diagnostics.CodeAnalysis.SuppressMessage("Design", "CA1001", Justification = "The draft, attachment and suggestion controllers are closed by Close, FinishAsync or Abandon, which every host calls.")]
public sealed partial class ComposePane : UserControl, IComposeForm
{
    private readonly AppState state;
    private readonly ComposeController compose;
    private readonly ComposeParams parameters;
    private readonly ComposeDraftController draft;
    private readonly ComposeAttachmentsController attachments;
    private readonly ComposeWebView editor;
    private readonly List<RecipientSuggestions> suggestions = [];
    private readonly Dictionary<TextBox, string> seen = [];
    private readonly ILogger logger;

    // The identities of the From row (accounts).
    private IReadOnlyList<Account> accounts = [];

    // The identity the user picked in From (chosenAccount); until then the
    // account the pane was opened for.
    private AccountId? chosenAccount;

    // compose.send's enabled state (setSendEnabled).
    private bool sendEnabled = true;

    // The draft controller's confirmation of a send, for End.Sent.
    private string? sentText;

    // The host was told the pane ended (once).
    private bool ended;

    // The widgets were released (Close, FinishAsync, Abandon).
    private bool tornDown;

    // Inline: the document height the editor last reported (null until it
    // did), the host's visible height, and the editor's applied height.
    private double? contentHeight;
    private double visibleHeight;
    private double appliedHeight = double.NaN;

    /// <summary>
    /// NewPane: builds a pane prefilled from <paramref name="p"/>. The host
    /// is set afterwards (<see cref="Host"/>); nothing is reported to it
    /// before.
    /// </summary>
    public ComposePane(AppState state, ComposeController compose, ComposeParams p, Options options)
    {
        ArgumentNullException.ThrowIfNull(state);
        ArgumentNullException.ThrowIfNull(compose);
        ArgumentNullException.ThrowIfNull(p);
        ArgumentNullException.ThrowIfNull(options);
        this.state = state;
        this.compose = compose;
        parameters = p;
        PaneOptions = options;
        logger = state.Logs.CreateLogger<ComposePane>();
        InitializeComponent();

        draft = new ComposeDraftController(
            state.Client, state.Settings, () => compose.Placeholder, CidRegistry.Shared,
            logger: state.Logs.CreateLogger<ComposeDraftController>(), owner: options.Owner)
        {
            Form = this,
        };
        attachments = new ComposeAttachmentsController(
            state.Client, () => CallAccount, CidRegistry.Shared, logger: state.Logs.CreateLogger<ComposeAttachmentsController>());
        editor = new ComposeWebView(sized: IsInline);
        try
        {
            if (IsInline)
            {
                BuildInline();
            }

            // The editor's callbacks first, as in compose.go: Ready and the
            // state may follow the load at any time.
            WireEditor();
            WireAttachments();

            // Prefill before the change handlers so it does not count as an edit.
            Header.To.Text = AddressList.Format(p.To);
            Header.Cc.Text = AddressList.Format(p.Cc);
            Header.Bcc.Text = AddressList.Format(p.Bcc);
            Prefill(Header.Subject, p.Subject);
            Header.SetCcBccVisible(cc: p.Cc.Count > 0, bcc: p.Bcc.Count > 0);
            UpdateTitle();
            draft.SetOriginal(p.InReplyTo, p.Forwarding, p.Comment);
            // A draft opened from the Drafts folder is the user's already: its id
            // and version make the saves updates, and closing never deletes it.
            draft.SetOpened(p.DraftId, p.Version, p.Replaces, fromDrafts: p.Kind == ComposeKind.Edit);
            editor.Load(p.BodyHtml);
            SetAccounts(compose.Accounts, compose.Placeholder);
            // What the backend imported for the template (a quoted original's
            // pictures, a forwarded message's files): listed and shown now,
            // bound by the first save.
            attachments.Set(p.Attachments);

            WireDraft();
            WireToolbar();
            WireRows();
            // Text-only phase (draft.go richText): no formatting to offer, no
            // inline images, and the user is told what will go out.
            FormatBar.Visibility = ComposeDraftController.RichText ? Visibility.Visible : Visibility.Collapsed;
            PlainTextHint.Visibility = ComposeDraftController.RichText ? Visibility.Collapsed : Visibility.Visible;
            ApplyCommentMode();
        }
        catch
        {
            // A pane that could not be built holds no browser and no timers.
            TearDown();
            draft.Cleanup();
            throw;
        }
    }

    /// <summary>The pane's layout (ComposePane.Options.Layout).</summary>
    public enum Layout
    {
        /// <summary>The compose window's content.</summary>
        Window,

        /// <summary>Inline in the board's case detail: sized editor, footer row, no From row.</summary>
        Inline,
    }

    /// <summary>How the pane ended (<see cref="IComposePaneHost.Ended"/>).</summary>
    public enum EndKind
    {
        /// <summary>The message (or comment) was queued; <see cref="SentText"/> is the confirmation.</summary>
        Sent,

        /// <summary>The user discarded it.</summary>
        Discarded,

        /// <summary>It closed otherwise (nothing at stake, or after the close question).</summary>
        Closed,

        /// <summary><see cref="DraftOwner.Board"/>: the draft was deleted elsewhere.</summary>
        Lost,
    }

    /// <summary>How a pane is built (ComposePane.Options, compose.PaneOptions).</summary>
    public sealed record Options
    {
        /// <summary>The window's content, or the board's inline reply.</summary>
        public Layout Layout { get; init; } = Layout.Window;

        /// <summary>Who keeps the draft (ComposeDraftController.Owner).</summary>
        public DraftOwner Owner { get; init; } = DraftOwner.Window;
    }

    /// <summary>The bridge's Escape, while the editor has the keyboard (the window's close request).</summary>
    public event EventHandler? EscapePressed;

    /// <summary>
    /// A send failed and Send is back (after the pane's toast saying why);
    /// not when the draft was lost (ComposeDraftController.OnSendFailed).
    /// </summary>
    public event EventHandler? SendFailed;

    /// <summary>What hosts the pane; set once, right after it is made.</summary>
    public IComposePaneHost? Host { get; set; }

    /// <summary>How the pane is laid out and who keeps its draft.</summary>
    public Options PaneOptions { get; }

    /// <summary>What the pane was opened with.</summary>
    public ComposeParams Parameters => parameters;

    /// <summary>The formatted-text editor (the window's rewrite works on it).</summary>
    public ComposeWebView Editor => editor;

    /// <summary>The draft behind the pane.</summary>
    public ComposeDraftController DraftController => draft;

    /// <summary>
    /// The window layout's toast overlay over the content (the window gives
    /// it to the shell); inline the host shows the toasts.
    /// </summary>
    public IToasts Toasts => PaneToasts;

    /// <summary>The title: the subject, or "New Message"; a comment names its issue (ReplyTitle).</summary>
    public string TitleText { get; private set; } = "";

    /// <summary>The confirmation of the send, once the draft was queued (End.Sent).</summary>
    public string? SentText => sentText;

    /// <summary>compose.send's enabled state.</summary>
    public bool SendEnabled => sendEnabled;

    /// <summary>comment.go <c>isComment</c>: the pane writes a comment on an issue.</summary>
    public bool IsComment => parameters.Comment is not null;

    /// <summary>
    /// Whether a popup of the pane is open: Escape and Ctrl+W close it, not
    /// the window (macOS popupsHidden).
    /// </summary>
    public bool PopupOpen => suggestions.Any(s => s.IsVisible) || FormatBar.IsPopupOpen || Header.IsFromOpen;

    /// <summary>closeRequest's first branch: nothing at stake.</summary>
    public bool CanCloseWithoutAsking => draft.CanCloseWithoutAsking;

    /// <summary>HasUnsavedText: edits not yet saved, or a save under way.</summary>
    public bool HasUnsavedText => draft.Draft.Dirty || draft.Draft.Saving;

    /// <summary>IsSending: Send was pressed and has not answered yet.</summary>
    public bool IsSending => draft.Draft.Sending;

    /// <summary>IsLost: the draft was deleted elsewhere.</summary>
    public bool IsLost => draft.Lost;

    /// <summary>The current height of the inline editor (NaN until applied, and in the window layout).</summary>
    public double EditorFrameHeight => appliedHeight;

    /// <summary>The inline editor's content is taller than its height: it scrolls inside.</summary>
    public bool EditorScrolls { get; private set; }

    /// <inheritdoc/>
    Account IComposeForm.Account => Account;

    /// <inheritdoc/>
    string IComposeForm.Subject => Header.Subject.Text;

    /// <inheritdoc/>
    IReadOnlyList<DraftAttachment> IComposeForm.Attachments => attachments.Attachments;

    private bool IsInline => PaneOptions.Layout == Layout.Inline;

    // The draft is cleaned up or the widgets are released: late callbacks do nothing.
    private bool Gone => tornDown || draft.Draft.Closed;

    /// <summary>
    /// compose.go <c>account</c>: the selected identity; a comment's is the
    /// issue's account (ComposeController.CommentAccount), which writes no
    /// mail and is not in From. Inline (the board's draft) there is no From
    /// row: the account is the params' own, never the placeholder or the
    /// first entry.
    /// </summary>
    private Account Account
    {
        get
        {
            if (IsComment && parameters.AccountId is { } issueAccount)
            {
                return compose.CommentAccount(issueAccount);
            }
            if (IsInline && parameters.AccountId is { } own)
            {
                return accounts.FirstOrDefault(a => a.Id == own)
                    ?? compose.KnownAccounts.FirstOrDefault(a => a.Id == own)
                    ?? new Account
                    {
                        Id = own,
                        Config = new AccountConfig { Name = "", Email = "" },
                        Enabled = true,
                        State = new SyncState { AccountId = own, Status = SyncStatus.Idle },
                    };
            }
            var i = Header.SelectedAccountIndex;
            if (i < accounts.Count)
            {
                return accounts[i];
            }
            return accounts.Count > 0 ? accounts[0] : ComposeController.PlaceholderAccounts[0];
        }
    }

    // The identity for the pane's own calls (imports, the template's
    // pictures, the address books): while the From row lists the
    // placeholder, because the account list is on its way, the account the
    // pane was opened for, whose store holds its template's files.
    private AccountId CallAccount => compose.Placeholder && parameters.AccountId is { } opened ? opened : Account.Id;

    private nint Handle => Host?.HostWindow is { } w ? WindowPresenter.Handle(w) : 0;

    /// <inheritdoc/>
    public (IReadOnlyList<Address> To, IReadOnlyList<Address> Cc, IReadOnlyList<Address> Bcc, bool Ok) Recipients()
    {
        var ok = true;
        IReadOnlyList<Address> Parse(RecipientTokenBox row)
        {
            // What the row holds, as its model resolves it: parsing its text
            // again would read some entries differently.
            var (addresses, invalid) = row.Resolved();
            if (invalid.Count > 0)
            {
                ok = false;
            }
            return addresses;
        }
        var to = Parse(Header.To);
        var cc = Parse(Header.Cc);
        var bcc = Parse(Header.Bcc);
        return (to, cc, bcc, ok);
    }

    /// <inheritdoc/>
    public string EditorHtml() => editor.Html;

    /// <inheritdoc/>
    public string EditorText() => editor.Text;

    /// <inheritdoc/>
    public void FlushEditor(Action done) => editor.Flush(done);

    /// <inheritdoc/>
    public void SetAttachments(IReadOnlyList<DraftAttachment> attachments) => this.attachments.Set(attachments);

    /// <inheritdoc/>
    public void SetStatus(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        StatusText.Text = text;
        // The text block's peer kept its first text as its name (measured
        // with UIA): the name follows the status explicitly.
        AutomationProperties.SetName(StatusText, text);
        ToolTipService.SetToolTip(StatusText, text.Length == 0 ? null : text);
    }

    /// <summary>A toast in the host (the window's overlay, the board's page).</summary>
    public void Toast(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        if (Host is { } host)
        {
            host.Toast(text);
        }
        else if (!IsInline)
        {
            PaneToasts.Show(text);
        }
    }

    /// <inheritdoc/>
    public void SetSendEnabled(bool enabled)
    {
        sendEnabled = enabled;
        FooterSend.IsEnabled = enabled;
        Host?.SendEnabledChanged(this, enabled);
    }

    /// <summary>The draft controller decided the pane goes: sent, discarded, or closed.</summary>
    public void CloseWindow()
    {
        if (sentText is not null)
        {
            EndWith(EndKind.Sent);
        }
        else if (draft.Draft.Discard)
        {
            EndWith(EndKind.Discarded);
        }
        else
        {
            EndWith(EndKind.Closed);
        }
    }

    /// <summary>
    /// compose.go <c>setAccounts</c>: fills the From row, keeping the
    /// identity the user picked while it is listed; until they pick, the
    /// account the pane was opened for. The row takes a choice only with
    /// more than one identity, and never for a reply or a forward.
    /// </summary>
    public void SetAccounts(IReadOnlyList<Account> accounts, bool placeholder)
    {
        ArgumentNullException.ThrowIfNull(accounts);
        if (Gone)
        {
            return;
        }
        this.accounts = accounts;
        var (index, found) = ComposeHeaderRules.FromSelection(accounts, chosenAccount, parameters.AccountId);
        Header.SetAccounts(
            [.. accounts.Select(ComposeHeaderRules.FromLabel)],
            index,
            ComposeHeaderRules.FromEnabled(accounts.Count, ComposeHeaderRules.FromLocked(parameters), found));
        if (placeholder && !IsComment)
        {
            SetStatus(L10n.T("Using placeholder account"));
        }
        else
        {
            // The real accounts replaced the placeholder: the status line no
            // longer says it uses one (GTK and macOS leave it until the next
            // edit).
            draft.RefreshStatus();
        }
    }

    /// <summary>
    /// manager.go <c>FindDraft</c>'s comparison: the same saved draft, or the
    /// same Drafts message taken over.
    /// </summary>
    public bool Edits(Draft draft)
    {
        ArgumentNullException.ThrowIfNull(draft);
        return (draft.Id is { } id && this.draft.Draft.DraftId == id) || (draft.Replaces is { } replaces && parameters.Replaces == replaces);
    }

    /// <summary>compose.send: queues the draft, while Send is enabled.</summary>
    public void Send()
    {
        if (Gone || !sendEnabled)
        {
            return;
        }
        draft.Send();
    }

    /// <summary>compose.save: saves the draft now; not in comment mode, which no Drafts folder keeps.</summary>
    public void SaveDraft()
    {
        if (Gone || IsComment)
        {
            return;
        }
        draft.Save(SaveReason.Explicit);
    }

    /// <summary>compose.discard: the draft controller's Discard (it may ask).</summary>
    public void Discard()
    {
        if (Gone)
        {
            return;
        }
        draft.Discard();
    }

    /// <summary>
    /// The keyboard where the pane starts: To, or the editor of a comment
    /// (compose.blp focus-widget; a reply's editor takes it at the start of
    /// the body once it is ready).
    /// </summary>
    public void FocusInitial()
    {
        if (IsComment)
        {
            FocusEditor();
            return;
        }
        Header.FocusTo();
    }

    /// <summary>editor.GrabFocus: the keyboard back to the page.</summary>
    public void FocusEditor() => editor.FocusPage();

    /// <summary>
    /// FocusEditorStart: the keyboard to the editor with the caret at the
    /// start once its bridge runs; false asks the host to try again once
    /// the editor is ready.
    /// </summary>
    public bool FocusEditorStart()
    {
        if (Gone)
        {
            return true;
        }
        editor.FocusStart();
        return editor.IsEditorReady;
    }

    /// <summary>EditorHasFocus: the editor's page has the keyboard (typing, not a row or a button).</summary>
    public bool EditorHasFocus()
    {
        if (XamlRoot is not { } root)
        {
            return false;
        }
        var focused = FocusManager.GetFocusedElement(root);
        return focused is not null && (ReferenceEquals(focused, editor.Web) || ReferenceEquals(focused, editor));
    }

    /// <summary>
    /// SetVisibleHeight: the height of the host's visible area, for the
    /// inline editor's cap (EditorHeight.Cap); nothing in the window layout.
    /// </summary>
    public void SetVisibleHeight(double height)
    {
        if (!IsInline)
        {
            return;
        }
        visibleHeight = height;
        ApplyEditorHeight();
    }

    /// <summary>The draft's close question (Escape, Ctrl+W, the caption's button): true when the pane may go.</summary>
    public Task<bool> CloseRequestAsync() => draft.CloseRequestAsync();

    /// <summary>Quit: saves the draft without asking; true when nothing unsaved is left.</summary>
    public Task<bool> SaveForQuitAsync() => Gone ? Task.FromResult(true) : draft.SaveForQuitAsync();

    /// <summary>
    /// <see cref="DraftOwner.Board"/>: Discard deletes the stored draft
    /// through this (ComposeDraftController.DiscardStored).
    /// </summary>
    public Func<AccountId, DraftId, Task>? DiscardStored
    {
        get => draft.DiscardStored;
        set => draft.DiscardStored = value;
    }

    /// <summary>
    /// Settle: saves everything typed and leaves the pane open; true when
    /// nothing typed is left unsaved (ComposeDraftController.SettleAsync).
    /// </summary>
    public Task<bool> SettleAsync() => draft.SettleAsync();

    /// <summary>
    /// Finish: Settle, then the draft's cleanup when it succeeded, and the
    /// widgets let go once the draft closed. True when nothing typed was
    /// lost; on false the pane stays usable, so the host may keep it and
    /// call again. The host bounds the wait.
    /// </summary>
    public async Task<bool> FinishAsync()
    {
        var ok = await draft.FinishAsync();
        if (draft.Draft.Closed)
        {
            TearDown();
        }
        return ok;
    }

    /// <summary>
    /// Close: the pane goes for good (the window really closed, or the host
    /// drops it once nothing is at stake): the draft's cleanup, never its
    /// finish, and never a question; the widgets let go. Idempotent.
    /// </summary>
    public void Close()
    {
        // The recipient rows are frozen first: closing takes the keyboard
        // from the typed text, which a row would commit, and that commit
        // must not mark the cleaned-up draft dirty (and so arm its autosave)
        // again. The draft reads the attachments, so it runs before they
        // close.
        Header.FreezeRecipients();
        draft.Cleanup();
        TearDown();
    }

    /// <summary>Abandon: forgets the pane without saving; its draft is gone. Idempotent.</summary>
    public void Abandon()
    {
        Header.FreezeRecipients();
        draft.Abandon();
        TearDown();
    }

    /// <summary>
    /// The draft's cleanup alone (the window's cleanupDraft before it closes,
    /// the widgets still showing).
    /// </summary>
    public void CleanupDraft()
    {
        Header.FreezeRecipients();
        draft.Cleanup();
    }

    // The widgets let go: the pending recipient searches end, the popups
    // go, the attachment imports end, and the editor lets go of its browser
    // at once (a case detail whose panes come and go must not pile up
    // browser processes). Idempotent.
    private void TearDown()
    {
        if (tornDown)
        {
            return;
        }
        tornDown = true;
        Header.FreezeRecipients();
        foreach (var s in suggestions)
        {
            s.Dispose();
        }
        FormatBar.HidePopups();
        attachments.Dispose();
        editor.Close();
    }

    // The inline layout (buildInlineContent): the header without From, no
    // margins at the sides, the editor its own height, the footer in place
    // of the status line, the toasts the host's, and the pane's own keys.
    private void BuildInline()
    {
        Header.ShowsFrom = false;
        Header.Margin = new Thickness(0, 0, 0, 6);
        CommentHeader.Margin = new Thickness(0, 0, 0, 6);
        FormatBar.Margin = new Thickness(0);
        PlainTextHint.Margin = new Thickness(0, 4, 0, 4);
        Chips.Margin = new Thickness(0, 6, 0, 0);
        EditorRow.Height = GridLength.Auto;
        PaneToasts.Visibility = Visibility.Collapsed;

        PaneRoot.Children.Remove(StatusText);
        StatusText.Margin = new Thickness(0);
        FooterStatusSlot.Child = StatusText;
        FooterDiscard.Content = Mnemonic.Strip(L10n.T("_Discard"));
        AutomationProperties.SetName(FooterDiscard, Mnemonic.Strip(L10n.T("_Discard")));
        var send = Mnemonic.Strip(L10n.T("_Send"));
        FooterSendLabel.Text = send;
        AutomationProperties.SetName(FooterSend, send);
        Footer.Visibility = Visibility.Visible;

        ApplyEditorHeight();
        WireInlineShortcuts();
    }

    // wireInlineShortcuts: Ctrl+Enter (Send) and Ctrl+S (Save Draft, nothing
    // in comment mode), only while the keyboard is inside the pane.
    private void WireInlineShortcuts()
    {
        PaneRoot.KeyboardAcceleratorPlacementMode = KeyboardAcceleratorPlacementMode.Hidden;
        AddShortcut(WinUIKey.Enter, Send);
        AddShortcut(WinUIKey.S, SaveDraft);
    }

    private void AddShortcut(WinUIKey key, Action run)
    {
        var accelerator = new KeyboardAccelerator { Key = key, Modifiers = WinUIModifiers.Control, ScopeOwner = PaneRoot };
        accelerator.Invoked += (_, e) =>
        {
            e.Handled = true;
            run();
        };
        PaneRoot.KeyboardAccelerators.Add(accelerator);
    }

    // editorReported: the document's height (CSS pixels, the view's DIPs:
    // the compose editor has no zoom).
    private void OnEditorHeight(double css)
    {
        contentHeight = css;
        ApplyEditorHeight();
    }

    // applyEditorHeight: the reported height clamped to the inline rule;
    // the host hears of a change of at least half a pixel.
    private void ApplyEditorHeight()
    {
        if (!IsInline || tornDown)
        {
            return;
        }
        var h = new EditorHeight(contentHeight ?? 0, visibleHeight);
        EditorScrolls = h.Scrolls;
        if (!double.IsNaN(appliedHeight) && Math.Abs(appliedHeight - h.Height) < 0.5)
        {
            return;
        }
        appliedHeight = h.Height;
        EditorSlot.Height = h.Height;
        Host?.HeightChanged(this);
    }

    private void OnFooterAttachClick(object sender, RoutedEventArgs e) => AttachFiles();

    private void OnFooterDiscardClick(object sender, RoutedEventArgs e) => Discard();

    private void OnFooterSendClick(object sender, RoutedEventArgs e) => Send();

    private void Prefill(TextBox row, string text)
    {
        row.Text = text;
        seen[row] = text;
    }

    // Whether the row's text changed since it was last seen (its own
    // prefill and a TextChanged that repeats it are no edit).
    private bool Edited(TextBox row)
    {
        var text = row.Text;
        if (seen.TryGetValue(row, out var last) && string.Equals(last, text, StringComparison.Ordinal))
        {
            return false;
        }
        seen[row] = text;
        return true;
    }

    // compose.go updateTitle: the subject, or "New Message"; a comment names
    // its issue (Jira.CommentTitle).
    private void UpdateTitle()
    {
        TitleText = parameters.Comment is { } c ? Jira.CommentTitle(c.Issue.Key) : ComposeHeaderRules.WindowTitle(Header.Subject.Text);
        Host?.TitleChanged(this);
    }

    // compose.go wireRows.
    private void WireRows()
    {
        foreach (var row in Header.RecipientFields)
        {
            var controller = new SuggestionsController(
                state.Client,
                () => CallAccount,
                // The typed text after the last badge, and its end: the badges
                // are no part of what is completed.
                () => (row.Pending, Suggest.ScalarOffset(row.Pending.Length, row.Pending)),
                logger: state.Logs.CreateLogger<SuggestionsController>());
            var s = new RecipientSuggestions(row, controller);
            suggestions.Add(s);
            // The value changed (a badge, typed text: the draft is dirty), and
            // the typed text did (what is asked of the address books).
            row.Changed += (_, _) =>
            {
                if (!Gone)
                {
                    draft.MarkDirty();
                }
            };
            row.PendingChanged += (_, _) =>
            {
                if (!Gone)
                {
                    controller.TextChanged();
                }
            };
        }
        Header.Subject.TextChanged += (_, _) =>
        {
            if (Gone || !Edited(Header.Subject))
            {
                return;
            }
            UpdateTitle();
            draft.MarkDirty();
        };
        Header.FromChanged += (_, _) =>
        {
            chosenAccount = Account.Id;
            draft.MarkDirty();
            // Another identity means other address books: what is shown was
            // asked on behalf of the previous one.
            HideSuggestions();
        };
    }

    private void HideSuggestions()
    {
        foreach (var s in suggestions)
        {
            s.Controller.Hide();
        }
    }

    // wireDraft: the draft controller's dialogs, its Sent and its Lost. The
    // window's confirmation of a send goes to the main window through the
    // manager, as it always did; an inline host shows its own (End.Sent).
    private void WireDraft()
    {
        var alerts = state.Alerts;
        draft.ConfirmDiscard = (heading, body, label) => alerts.ConfirmDestructiveAsync(Host?.HostWindow, heading, body, label);
        if (!IsInline)
        {
            draft.SaveDraftQuestion = () => alerts.SaveDraftQuestionAsync(Host?.HostWindow);
        }
        draft.Sent += (_, text) =>
        {
            sentText = text;
            if (!IsInline)
            {
                compose.ReportSent(text);
            }
        };
        draft.OnSendFailed = () => SendFailed?.Invoke(this, EventArgs.Empty);
        draft.OnLost = () => EndWith(EndKind.Lost);
    }

    private void EndWith(EndKind kind)
    {
        if (ended)
        {
            return;
        }
        ended = true;
        Host?.Ended(this, kind);
    }
}
