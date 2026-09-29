// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/WebViews/ComposeWebView.swift,
// CIDSchemeHandler.swift and the view half of
// macos/Sources/MalachiMail/Compose/ComposeEditorView.swift (whose bridge
// half is Core's EditorChannel); GTK: ui/internal/editor (editor.go, cid.go,
// document.go, bridge.go) and ui/data/ui/editor.blp.
//
// The compose editor: a contenteditable document, driven through the
// bridge's window.malachi functions and observed through its messages. It is
// layer 2 of docs/security.md §3.3 for WebView2 (docs/windows-port.md §6.5):
// what the user types, pastes or quotes is hostile until draft.save
// sanitises it, and this view keeps the raw HTML from doing anything
// meanwhile. It knows nothing about mail.
//
// docs/security.md §3.3, rule by rule, as WebView2 does it:
//
// - No page JavaScript: script is ON (the bridge's listeners need it; with
//   script off none ever fires, measured) and the editor CSP blocks every
//   page script, on* handler and javascript: URL, as a response header and
//   as the document's <meta>. The bridge (EditorBridge.Script) is injected
//   with AddScriptToExecuteOnDocumentCreatedAsync; WebView2 has no isolated
//   world, so it installs itself only in the top frame of an editor
//   document and uses prototype accessors captured before any content.
// - A CSP without network access, the request gate (403 for everything but
//   the document and registered cid: pictures), the resolver rule and the
//   dead proxy of the environment.
// - Navigation denied: every navigation but the pending document is
//   cancelled, a clicked link of a quoted original or a pasted form
//   included; no new windows; downloads, external schemes, frames refused.
// - cid: serves only ids the window registered (CidRegistry, checkInline).
// - Messages from the page are accepted only from the current document
//   (Source) and only in the bridge's shapes (EditorChannel.Receive).
// - Files dropped on the page go to FilesDropped (the bridge takes the drop
//   in the capture phase and posts it with postMessageWithAdditionalObjects;
//   WebView2 hands over each file's path), never into the page.
//
// Keys (docs/windows-port.md §11.5): with the WebView2 focused no XAML key
// event fires; every key passes the window's pre-translate handler first.
// The compose window's router must let EditorKeys.BridgeHandles keys through
// (Ctrl+B/I/U format in the page, Ctrl+Shift+I is kept from typing a Tab,
// Ctrl+K and Escape come back as Channel.KeyPressed "link" / "escape") and
// acts on its own keys (Ctrl+Enter, Ctrl+S, Ctrl+W, Ctrl+Q, …), swallowing
// them; browser keys are off (AreBrowserAcceleratorKeysEnabled=false).
//
// The compose window wires it as editor.Editor is wired in GTK: its
// IComposeForm answers EditorHtml/EditorText/FlushEditor with Html, Text and
// Flush; Channel.Ready and Channel.Changed go to the draft controller's
// EditorReady and EditorChanged; Channel.StateChanged to the format bar;
// FilesDropped to attachment.import; Crashed to the editor-failure toast and
// a Load of Html.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Html;
using Malachi.Core.Presentation;
using Microsoft.Web.WebView2.Core;

namespace Malachi.App.WebViews;

/// <summary>The compose editor's web view (editor.Editor, ComposeWebView + ComposeEditorView).</summary>
public sealed partial class ComposeWebView : HardenedWebView
{
    // The view could not load its document and Crashed has said so; nothing
    // more is said until a document loads, so the compose window's reload
    // on a failure cannot loop (macOS reportedUnavailable).
    private bool reportedUnavailable;

    /// <summary>An editor over the process-wide <see cref="CidRegistry.Shared"/>.</summary>
    public ComposeWebView()
        : this(CidRegistry.Shared)
    {
    }

    /// <summary>An editor whose <c>cid:</c> pictures resolve in <paramref name="registry"/>.</summary>
    public ComposeWebView(CidRegistry registry)
        : base(WebViewKind.Editor)
    {
        ArgumentNullException.ThrowIfNull(registry);
        Registry = registry;
    }

    /// <summary>
    /// The editor's page died, or its document could not be loaded; the view
    /// is blank until <see cref="Load"/> is called again (editor.OnCrashed:
    /// the compose window shows its toast and reloads <see cref="Html"/>).
    /// Raised once per text: when the text the window reloaded fails the
    /// same way, the view stays blank and only
    /// <see cref="HardenedWebView.Unavailable"/> follows, so the reload
    /// cannot loop; <see cref="Html"/> keeps the text for a save.
    /// </summary>
    public event EventHandler? Crashed;

    /// <summary>
    /// Files dropped onto the page, as paths (editor.OnDropFiles); the page
    /// never sees them.
    /// </summary>
    public event EventHandler<IReadOnlyList<string>>? FilesDropped;

    /// <summary>The registry the <c>cid:</c> pictures resolve in.</summary>
    public CidRegistry Registry { get; }

    /// <summary>
    /// The bridge's state and events: <c>Ready</c>, <c>Changed</c>,
    /// <c>StateChanged</c>, <c>KeyPressed</c>.
    /// </summary>
    public EditorChannel Channel { get; } = new();

    /// <summary>editor.Ready: whether the bridge runs in the current document.</summary>
    public bool IsEditorReady => Channel.IsReady;

    /// <summary>editor.HTML: the body's last known innerHTML (or what was loaded).</summary>
    public string Html => Channel.Html;

    /// <summary>editor.Text: the body's last known innerText.</summary>
    public string Text => Channel.Text;

    /// <summary>
    /// editor.Load: replaces the document with <paramref name="bodyHtml"/>
    /// (already safe for the page: escaped quotes, backend-sanitised
    /// drafts). Callers waiting on a flush of the old document are released.
    /// </summary>
    public void Load(string bodyHtml)
    {
        ArgumentNullException.ThrowIfNull(bodyHtml);
        Channel.Load(bodyHtml);
        LoadDocument(Encoding.UTF8.GetBytes(EditorDocument.Document(bodyHtml)), "text/html; charset=utf-8", EditorDocument.Csp);
    }

    /// <summary>
    /// editor.Flush: asks the page for its current content and calls
    /// <paramref name="done"/> once the <c>changed</c> it produced has
    /// arrived, at once when the bridge is not running; a failed evaluation
    /// calls it too (a save must never hang).
    /// </summary>
    public void Flush(Action done)
    {
        ArgumentNullException.ThrowIfNull(done);
        if (Channel.BeginFlush(done) is { } id)
        {
            _ = FlushAsync(id);
        }
    }

    /// <summary>
    /// editor.Exec: runs an editing command (<c>bold</c>, <c>formatBlock</c>
    /// with <c>h1</c>, <c>createLink</c>, <c>foreColor</c>,
    /// <c>insertImage</c>, …) on the selection; ignored until the bridge runs.
    /// </summary>
    public void Exec(string command, string? argument = null)
    {
        ArgumentNullException.ThrowIfNull(command);
        if (Channel.ExecScript(command, argument) is { } script)
        {
            Run(script);
        }
    }

    /// <summary>
    /// editor.RewriteTarget: notes the passage the assistant's rewrite works
    /// on (the selection, or the user's own text above the line
    /// <paramref name="attribution"/>) and calls <paramref name="done"/> with
    /// it; the empty target when the bridge does not run or the evaluation
    /// failed (a rewrite must never hang).
    /// </summary>
    public void RewriteTarget(string attribution, Action<RewriteTarget> done)
    {
        if (Channel.BeginRewriteTarget(attribution, done) is { } script)
        {
            _ = RewriteTargetAsync(script);
        }
    }

    /// <summary>
    /// editor.ApplyRewrite: puts <paramref name="text"/> in place of the
    /// passage <see cref="RewriteTarget"/> noted, or below it, as plain text,
    /// one step the page's undo takes back; ignored until the bridge runs.
    /// </summary>
    public void ApplyRewrite(string text, bool below)
    {
        if (Channel.ApplyRewriteScript(text, below) is { } script)
        {
            Run(script);
        }
    }

    /// <summary>editor.FocusStart: focuses the view and, once the bridge runs, puts the caret at the start.</summary>
    public void FocusStart()
    {
        FocusPage();
        if (Channel.FocusStartScript() is { } script)
        {
            Run(script);
        }
    }

    private protected override async Task ConfigureAsync(CoreWebView2 webView)
    {
        // Before the first navigation: the bridge must exist in the document.
        await webView.AddScriptToExecuteOnDocumentCreatedAsync(EditorBridge.Script);
        webView.WebMessageReceived += OnWebMessageReceived;
    }

    private protected override void OnUnavailable()
    {
        Channel.Crashed();
        if (reportedUnavailable)
        {
            return;
        }
        reportedUnavailable = true;
        Crashed?.Invoke(this, EventArgs.Empty);
    }

    // The first loss of the document's renderer: the window reloads Html
    // (the view's one reload, RendererRecovery).
    private protected override void OnRendererLost()
    {
        Channel.Crashed();
        Crashed?.Invoke(this, EventArgs.Empty);
    }

    // The same text failed again after the window reloaded it: nothing more
    // is said, so the window's reload cannot loop (as a document refused
    // twice, macOS reportedUnavailable); Html keeps the text, so a save
    // loses nothing. Unavailable still follows.
    private protected override void OnGaveUp()
    {
        Channel.Crashed();
        reportedUnavailable = true;
    }

    // CIDSchemeHandler.start: a registered id only, through checkInline; a
    // file is read off the UI thread, a fetcher is bounded by the timeout.
    private protected override async Task ServePictureAsync(CoreWebView2Environment environment, GateDecision decision, CoreWebView2WebResourceRequestedEventArgs args)
    {
        if (decision is not GateDecision.InlineImage image || Registry.Lookup(image.Id) is not { } entry)
        {
            Respond(environment, args, GateDecision.Refused.NotFound);
            return;
        }
        var deferral = args.GetDeferral();
        try
        {
            var picture = await Fetch(entry, GenerationToken);
            CidRegistry.CheckInline(picture.Data.Span, picture.ContentType);
            if (!Gate.IsCurrent(image.Generation) || IsClosed
                || ResponseHeaders.Picture(picture.ContentType, picture.Data.Length) is not { } headers)
            {
                Respond(environment, args, GateDecision.Refused.NotFound);
                return;
            }
            Respond(environment, args, picture.Data.ToArray(), headers);
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            WebViewLog.PictureFailed(Log, e);
            Respond(environment, args, GateDecision.Refused.NotFound);
        }
        finally
        {
            deferral.Complete();
        }
    }

    private static async Task<InlineImage> Fetch(CidEntry entry, CancellationToken cancellationToken)
    {
        switch (entry)
        {
            case CidEntry.File file:
                // A local file, as GTK serves it: a regular file (after
                // links, as os.Stat sees it) within the cap before it is read.
                var path = file.Path;
                var contentType = file.ContentType;
                var data = await Task.Run(() => ReadFile(path), cancellationToken);
                return new InlineImage(data, contentType);
            case CidEntry.Fetcher fetcher:
                using (var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken))
                {
                    timeout.CancelAfter(CidRegistry.FetchTimeout);
                    return await fetcher.Fetch(timeout.Token).WaitAsync(timeout.Token);
                }
            default:
                throw new InlineImageException(InlineImageError.NotAPicture);
        }
    }

    private static byte[] ReadFile(string path)
    {
        var info = new FileInfo(path);
        if (info.LinkTarget is not null && info.ResolveLinkTarget(returnFinalTarget: true) is FileInfo target)
        {
            info = target;
        }
        if (!info.Exists || info.Attributes.HasFlag(FileAttributes.Directory) || info.Length > CidRegistry.MaxCidBytes)
        {
            throw new InlineImageException("inline image unavailable");
        }
        return File.ReadAllBytes(info.FullName);
    }

    private async Task FlushAsync(long id)
    {
        var result = await EvaluateAsync(EditorBridge.FlushScript);
        Channel.Flushed(id, result);
    }

    // The passage comes as the page's "rewrite" message; a failed
    // evaluation answers the waiting rewrites with the empty target.
    private async Task RewriteTargetAsync(string script)
    {
        if (await EvaluateAsync(script) is null)
        {
            Channel.RewriteFailed();
        }
    }

    // editor.onMessage: only from the document on display, only strings of
    // the bridge's shapes; a drop brings its files beside the message.
    private void OnWebMessageReceived(CoreWebView2 sender, CoreWebView2WebMessageReceivedEventArgs args)
    {
        if (IsClosed || Gate.DocumentUri is not { } current || !string.Equals(args.Source, current, StringComparison.Ordinal))
        {
            WebViewLog.ForeignBridgeMessage(Log);
            return;
        }
        string raw;
        try
        {
            raw = args.TryGetWebMessageAsString();
        }
        catch (Exception e) when (e is ArgumentException or InvalidOperationException or System.Runtime.InteropServices.COMException)
        {
            WebViewLog.BadBridgeMessage(Log);
            return;
        }
        var message = Channel.Receive(raw);
        if (message is null)
        {
            WebViewLog.BadBridgeMessage(Log);
            return;
        }
        if (message.Type == BridgeMessage.Kinds.Ready)
        {
            reportedUnavailable = false;
        }
        if (message.Type == BridgeMessage.Kinds.Drop)
        {
            var paths = DroppedPaths(args);
            if (paths.Count > 0)
            {
                FilesDropped?.Invoke(this, paths);
            }
        }
    }

    private static List<string> DroppedPaths(CoreWebView2WebMessageReceivedEventArgs args)
    {
        var paths = new List<string>();
        if (args.AdditionalObjects is not { } objects)
        {
            return paths;
        }
        foreach (var file in objects.OfType<CoreWebView2File>())
        {
            if (!string.IsNullOrEmpty(file.Path))
            {
                paths.Add(file.Path);
            }
        }
        return paths;
    }
}
