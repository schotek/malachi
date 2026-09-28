// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Compose/ComposeWindowController.swift
// (init, wireRows, wireToolbar, wireDraft, validateRow, updateTitle,
// windowShouldClose, windowWillClose, ComposeForm, ComposeWindowHandle,
// the compose actions); GTK: ui/internal/compose/compose.go (Window,
// newWindow, setAccounts, fromLocked, account, wireRows, setCcBccVisible,
// updateTitle, validateRow, recipients, wireActions, toast, setStatus) and
// draft.go (closeRequest, cleanup as the window runs them). The draft's
// lifecycle is Core's ComposeDraftController, which reads this window
// through IComposeForm; the attachments are Core's
// ComposeAttachmentsController, the recipient completion Core's
// SuggestionsController, the header's rules ComposeHeaderRules; the account
// list arrives from ComposeController through IComposeWindowHandle.
//
// Windows specifics (docs/windows-port.md §6.5, §11.3, §11.5):
// - the window is tracked (WindowTracker.Track, WindowKind.Compose): its
//   colour scheme, its toasts, and its CommandRouter, which runs Ctrl+Enter
//   (Send), Ctrl+S (Save Draft), Escape and Ctrl+W (the close request, not
//   while a popup of the window is open), and the application's keys; the
//   editor's WebView2 is marked as an editor, so its Ctrl+B, I, U, K and
//   Escape stay with the bridge, which posts Escape and Ctrl+K back
//   (Channel.KeyPressed);
// - closing (the caption's button, Alt+F4, Escape, Ctrl+W) is the close
//   request of draft.go: nothing at stake closes at once, otherwise "Save
//   changes to this draft?" (AlertService: Save Draft the default, Discard,
//   Cancel), and the window closes when the answer lets it;
// - Quit saves the draft without asking (SaveForQuitAsync) and asks the
//   close question only when that failed (CloseForQuitAsync);
// - TextBox.TextChanged also comes for the window's own prefill and for an
//   accepted suggestion, after the setter returned: a row counts as edited
//   when its text differs from the text last seen in it.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Threading.Tasks;
using Malachi.App.Shell;
using Malachi.App.WebViews;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.Controllers;
using Malachi.Core.Html;
using Malachi.Core.I18n;
using Malachi.Core.Presentation;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Composition.SystemBackdrops;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Windows.Graphics;
using Windows.Win32;
using Windows.Win32.Foundation;

namespace Malachi.App.Compose;

/// <summary>One compose window (compose.blp, compose.Window).</summary>
[System.Diagnostics.CodeAnalysis.SuppressMessage("Design", "CA1001", Justification = "The draft, attachment and suggestion controllers are closed when the window closes (OnClosed).")]
public sealed partial class ComposeWindow : Window, IComposeForm, IComposeWindowHandle
{
    /// <summary>compose.blp default-width.</summary>
    public const int DefaultWidth = 760;

    /// <summary>compose.blp default-height.</summary>
    public const int DefaultHeight = 640;

    /// <summary>compose.blp width-request.</summary>
    public const int MinWidth = 360;

    /// <summary>compose.blp height-request.</summary>
    public const int MinHeight = 420;

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
    // account the window was opened for.
    private AccountId? chosenAccount;

    // compose.send's enabled state (setSendEnabled).
    private bool sendEnabled = true;

    // The window is closing for good: no question any more.
    private bool closing;

    // The close question while it is up; a second request waits for it.
    private Task<bool>? closeQuestion;

    /// <summary>
    /// compose.newWindow: builds a window prefilled from <paramref name="p"/>
    /// (not shown yet; ComposeManager places and presents it).
    /// </summary>
    public ComposeWindow(AppState state, ComposeController compose, ComposeParams p)
    {
        ArgumentNullException.ThrowIfNull(state);
        ArgumentNullException.ThrowIfNull(compose);
        ArgumentNullException.ThrowIfNull(p);
        this.state = state;
        this.compose = compose;
        parameters = p;
        logger = state.Logs.CreateLogger<ComposeWindow>();
        InitializeComponent();

        ExtendsContentIntoTitleBar = true;
        SetTitleBar(ComposeTitleBar);
        AppWindow.TitleBar.PreferredHeightOption = TitleBarHeightOption.Tall;
        var icon = Path.Combine(AppContext.BaseDirectory, "Assets", "Malachi.ico");
        if (File.Exists(icon))
        {
            AppWindow.SetIcon(icon);
        }
        if (!MicaController.IsSupported())
        {
            SolidBackground.Visibility = Visibility.Visible;
        }
        ApplySize();
        Tracked = state.Windows.Track(this, WindowKind.Compose, Root, ToastsHost);
        var send = Mnemonic.Parse(L10n.T("_Send"));
        SendLabel.Text = send.Label;
        SendButton.AccessKey = send.AccessKey ?? "";
        AutomationProperties.SetName(SendButton, send.Label);

        draft = new ComposeDraftController(
            state.Client, state.Settings, () => compose.Placeholder, CidRegistry.Shared,
            logger: state.Logs.CreateLogger<ComposeDraftController>())
        {
            Form = this,
        };
        attachments = new ComposeAttachmentsController(
            state.Client, () => CallAccount, CidRegistry.Shared, logger: state.Logs.CreateLogger<ComposeAttachmentsController>());
        editor = new ComposeWebView();

        // The editor's callbacks first, as in compose.go: Ready and the
        // state may follow the load at any time.
        WireEditor();
        WireAttachments();

        // Prefill before the change handlers so it does not count as an edit.
        Prefill(Header.To, AddressList.Format(p.To));
        Prefill(Header.Cc, AddressList.Format(p.Cc));
        Prefill(Header.Bcc, AddressList.Format(p.Bcc));
        Prefill(Header.Subject, p.Subject);
        Header.SetCcBccVisible(cc: p.Cc.Count > 0, bcc: p.Bcc.Count > 0);
        UpdateTitle();
        draft.SetOriginal(p.InReplyTo, p.Forwarding);
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
        WireCommands();
        WireToolbar();
        WireRows();
        // Text-only phase (draft.go richText): no formatting to offer, no
        // inline images, and the user is told what will go out.
        FormatBar.Visibility = ComposeDraftController.RichText ? Visibility.Visible : Visibility.Collapsed;
        PlainTextHint.Visibility = ComposeDraftController.RichText ? Visibility.Collapsed : Visibility.Visible;
        InsertImageItem.IsEnabled = ComposeDraftController.RichText;

        AppWindow.Closing += OnClosing;
        Root.Loaded += OnRootLoaded;
        Closed += OnClosed;
    }

    /// <summary>The window as the shell tracks it.</summary>
    public TrackedWindow Tracked { get; }

    /// <inheritdoc/>
    Account IComposeForm.Account => Account;

    /// <summary>compose.go <c>account</c>: the selected identity.</summary>
    private Account Account
    {
        get
        {
            var i = Header.SelectedAccountIndex;
            if (i < accounts.Count)
            {
                return accounts[i];
            }
            return accounts.Count > 0 ? accounts[0] : ComposeController.PlaceholderAccounts[0];
        }
    }

    /// <inheritdoc/>
    string IComposeForm.Subject => Header.Subject.Text;

    // The identity for the window's own calls (imports, the template's
    // pictures, the address books): while the From row lists the
    // placeholder, because the account list is on its way, the account the
    // window was opened for, whose store holds its template's files.
    private AccountId CallAccount => compose.Placeholder && parameters.AccountId is { } opened ? opened : Account.Id;

    /// <inheritdoc/>
    IReadOnlyList<DraftAttachment> IComposeForm.Attachments => attachments.Attachments;

    // Whether a popup of the window is open: Escape and Ctrl+W close it,
    // not the window (macOS EscapeCloser.shouldClose).
    private bool PopupOpen =>
        suggestions.Any(s => s.IsVisible) || FormatBar.IsPopupOpen || Header.IsFromOpen
        || DraftMenuButton.Flyout?.IsOpen == true;

    private nint Handle => WindowPresenter.Handle(this);

    /// <inheritdoc/>
    public (IReadOnlyList<Address> To, IReadOnlyList<Address> Cc, IReadOnlyList<Address> Bcc, bool Ok) Recipients()
    {
        var ok = true;
        IReadOnlyList<Address> Parse(TextBox row)
        {
            var (addresses, invalid) = AddressList.Parse(row.Text);
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

    /// <inheritdoc/>
    public void Toast(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        ToastsHost.Show(text);
    }

    /// <inheritdoc/>
    public void SetSendEnabled(bool enabled)
    {
        sendEnabled = enabled;
        SendButton.IsEnabled = enabled;
        Tracked.Commands.Send.Refresh();
    }

    /// <inheritdoc/>
    public void CloseWindow() => CloseForGood();

    /// <summary>
    /// compose.go <c>setAccounts</c>: fills the From row, keeping the
    /// identity the user picked while it is listed; until they pick, the
    /// account the window was opened for. The row takes a choice only with
    /// more than one identity, and never for a reply or a forward.
    /// </summary>
    public void SetAccounts(IReadOnlyList<Account> accounts, bool placeholder)
    {
        ArgumentNullException.ThrowIfNull(accounts);
        this.accounts = accounts;
        var (index, found) = ComposeHeaderRules.FromSelection(accounts, chosenAccount, parameters.AccountId);
        Header.SetAccounts(
            [.. accounts.Select(ComposeHeaderRules.FromLabel)],
            index,
            ComposeHeaderRules.FromEnabled(accounts.Count, ComposeHeaderRules.FromLocked(parameters), found));
        if (placeholder)
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

    /// <summary>Brings the window to the front.</summary>
    public void Present() => WindowPresenter.Present(this);

    /// <summary>Quit: saves the draft without asking; true when nothing unsaved is left.</summary>
    public Task<bool> SaveForQuitAsync() => closing ? Task.FromResult(true) : draft.SaveForQuitAsync();

    /// <summary>
    /// Quit could not save the draft: the close question, and the window
    /// closes when the answer lets it; false (the Quit is abandoned) when it
    /// stays.
    /// </summary>
    public Task<bool> CloseForQuitAsync() => CloseAsync();

    /// <summary>The window's size at its display's scale, and its smallest (compose.blp).</summary>
    private void ApplySize()
    {
        var scale = Scale();
        AppWindow.Resize(new SizeInt32((int)Math.Round(DefaultWidth * scale), (int)Math.Round(DefaultHeight * scale)));
        if (AppWindow.Presenter is OverlappedPresenter overlapped)
        {
            overlapped.PreferredMinimumWidth = (int)Math.Round(MinWidth * scale);
            overlapped.PreferredMinimumHeight = (int)Math.Round(MinHeight * scale);
        }
    }

    private double Scale()
    {
        var dpi = PInvoke.GetDpiForWindow((HWND)Handle);
        return dpi == 0 ? 1.0 : dpi / 96.0;
    }

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

    // compose.go updateTitle: the subject, or "New Message".
    private void UpdateTitle()
    {
        var title = ComposeHeaderRules.WindowTitle(Header.Subject.Text);
        Title = title;
        ComposeTitleBar.Title = title;
    }

    // compose.go validateRow: flags a recipient row with unparsable tokens.
    private void ValidateRow(TextBox row)
    {
        var (_, invalid) = AddressList.Parse(row.Text);
        Header.SetInvalid(row, invalid.Count > 0);
    }

    // compose.go wireRows.
    private void WireRows()
    {
        foreach (var row in Header.RecipientFields)
        {
            var controller = new SuggestionsController(
                state.Client,
                () => CallAccount,
                () => (row.Text, Suggest.ScalarOffset(Math.Clamp(row.SelectionStart, 0, row.Text.Length), row.Text)),
                logger: state.Logs.CreateLogger<SuggestionsController>());
            var s = new RecipientSuggestions(row, controller);
            suggestions.Add(s);
            row.TextChanged += (_, _) =>
            {
                if (closing || !Edited(row))
                {
                    return;
                }
                ValidateRow(row);
                draft.MarkDirty();
                controller.TextChanged();
            };
        }
        Header.Subject.TextChanged += (_, _) =>
        {
            if (closing || !Edited(Header.Subject))
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

    // wireDraft: the draft controller's dialogs and its Sent.
    private void WireDraft()
    {
        var alerts = state.Alerts;
        draft.ConfirmDiscard = (heading, body, label) => alerts.ConfirmDestructiveAsync(this, heading, body, label);
        draft.SaveDraftQuestion = () => alerts.SaveDraftQuestionAsync(this);
        draft.Sent += (_, text) => compose.ReportSent(text);
    }

    // compose.go wireActions: compose.send and compose.save through the
    // window's commands (their keys are the router's); the close request.
    private void WireCommands()
    {
        var c = Tracked.Commands;
        c.Send.Handler = draft.Send;
        c.Send.CanExecute = () => sendEnabled;
        c.SaveDraft.Handler = () => draft.Save(SaveReason.Explicit);
        c.CloseWindow.Handler = RequestClose;
        c.CloseWindow.CanExecute = () => !PopupOpen;
    }

    private void OnDiscardClick(object sender, RoutedEventArgs e) => draft.Discard();

    // send_button and the menu's Save Draft run the window's commands (a
    // XamlUICommand would put its own label in place of the button's).
    private void OnSendClick(object sender, RoutedEventArgs e) => Tracked.Commands.Send.TryExecute();

    private void OnSaveDraftClick(object sender, RoutedEventArgs e) => Tracked.Commands.SaveDraft.TryExecute();

    // Once shown, the keyboard is in To (compose.blp focus-widget); a
    // reply's editor takes it at the start of the body once it is ready.
    private void OnRootLoaded(object sender, RoutedEventArgs e)
    {
        Root.Loaded -= OnRootLoaded;
        Header.FocusTo();
    }

    // The caption's button and Alt+F4: the close request.
    private void OnClosing(AppWindow sender, AppWindowClosingEventArgs args)
    {
        if (closing)
        {
            return;
        }
        args.Cancel = true;
        RequestClose();
    }

    // closeRequest (Escape, Ctrl+W, the caption).
    private void RequestClose() => _ = CloseAsync();

    // closeRequest: the window goes at once when nothing is at stake;
    // otherwise the draft controller asks, and the window closes when the
    // answer lets it. True when it closed.
    private async Task<bool> CloseAsync()
    {
        if (closing)
        {
            return true;
        }
        if (draft.CanCloseWithoutAsking)
        {
            draft.Cleanup();
            CloseForGood();
            return true;
        }
        closeQuestion ??= AskAsync();
        return await closeQuestion;
    }

    private async Task<bool> AskAsync()
    {
        try
        {
            var allowed = await draft.CloseRequestAsync();
            if (allowed)
            {
                CloseForGood();
            }
            return allowed;
        }
        catch (Exception e) when (e is InvalidOperationException or System.Runtime.InteropServices.COMException or TaskCanceledException)
        {
            // The question could not be shown: the window and its draft stay.
            LogCloseQuestionFailed(logger, e);
            return false;
        }
        finally
        {
            closeQuestion = null;
        }
    }

    private void CloseForGood()
    {
        if (closing)
        {
            return;
        }
        closing = true;
        Close();
    }

    // cleanup: the window really closes. Late replies are dropped, the
    // popups go, the inline pictures are forgotten (the draft controller
    // reads the attachments, so it runs before they close), the editor lets
    // go of its browser, and the manager forgets the window.
    private void OnClosed(object sender, WindowEventArgs args)
    {
        closing = true;
        foreach (var s in suggestions)
        {
            s.Dispose();
        }
        FormatBar.HidePopups();
        draft.Cleanup();
        attachments.Dispose();
        editor.Close();
        compose.Remove(this);
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "the close question of a compose window could not be asked")]
    private static partial void LogCloseQuestionFailed(ILogger logger, Exception error);
}
