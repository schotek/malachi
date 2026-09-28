// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Compose/ComposeAttachments.swift
// (attachFiles' and insertImage's continuations, importFile,
// removeAttachment, setAttachments, registerInline) and of the chip of
// AttachmentChipsView.swift (tailEllipsis, the icon); GTK:
// ui/internal/compose/compose.go (attachGioFiles, insertImage, importFile,
// addChip, removeAttachment, setAttachments, registerInline). A
// presentation class macOS keeps in AppKit (docs/windows-port.md §7.4):
// the attachments of one compose window without the pickers and the chips.
//
// The window (Malachi.App Compose/ComposeWindow) owns it: it hands over
// what its pickers and the editor's drops produced (paths; an item without
// one is refused with "Only local files can be attached", as GTK refuses a
// gio.File without a local path), lists Chips under the editor, answers
// IComposeForm.Attachments with Attachments, and turns Edited into the
// draft's MarkDirty, StatusChanged into its status line, ImportFailed into
// its RefreshStatus and ToastRequested into a toast. The daemon imports and
// stores the files (attachment.import); only local paths the user picked
// or dropped are ever handed to it (docs/windows-port.md §10). Inline
// pictures are served to the editor through the cid: registry: a file the
// window inserted by its path, a copy the daemon made (a quoted original's
// picture, from draft.create or draft.open) through attachment.get.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Html;
using Malachi.Core.I18n;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Presentation;

/// <summary>
/// The attachments of a compose window: importing, removing, the list the
/// backend kept, the <c>cid:</c> registrations of inline pictures, and the
/// chips. Created by its window on the UI thread and UI-thread-affine
/// (docs/windows-port.md §7.1).
/// </summary>
public sealed partial class ComposeAttachmentsController : IDisposable
{
    /// <summary>
    /// How many characters of a file name a chip shows before it cuts the
    /// end with an ellipsis (compose.go <c>addChip</c>'s max-width-chars,
    /// AttachmentChipsView.swift <c>nameChars</c>).
    /// </summary>
    public const int ChipNameChars = 24;

    private readonly List<DraftAttachment> attachments = [];
    private readonly RpcClient client;
    private readonly Func<AccountId> account;
    private readonly CidRegistry registry;
    private readonly ILogger logger;
    private readonly ControllerScope scope;
    private bool closed;

    /// <param name="client">The transport.</param>
    /// <param name="account">The From identity's id: files are imported into its store, and a picture of it is fetched from there (read when the editor asks for it).</param>
    /// <param name="registry">The <c>cid:</c> registry the editor serves from; <see cref="CidRegistry.Shared"/> by default.</param>
    /// <param name="pending">Where the background work is counted (tests wait on it); a tracker of its own by default.</param>
    /// <param name="logger">Method names and errors only, never a path or a file name.</param>
    public ComposeAttachmentsController(
        RpcClient client,
        Func<AccountId> account,
        CidRegistry? registry = null,
        PendingWork? pending = null,
        ILogger<ComposeAttachmentsController>? logger = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        ArgumentNullException.ThrowIfNull(account);
        this.client = client;
        this.account = account;
        this.registry = registry ?? CidRegistry.Shared;
        this.logger = logger ?? (ILogger)NullLogger.Instance;
        scope = new ControllerScope(pending);
    }

    /// <summary>The list changed: the chips follow <see cref="Chips"/>.</summary>
    public event EventHandler? Changed;

    /// <summary>A file was attached or removed: an edit of the draft (markDirty).</summary>
    public event EventHandler? Edited;

    /// <summary>The status line under the window ("Attaching %s…").</summary>
    public event EventHandler<string>? StatusChanged;

    /// <summary>A transient message over the window (a failure, a refused item).</summary>
    public event EventHandler<string>? ToastRequested;

    /// <summary>An import failed after its status was shown: the status line goes back (refreshStatus).</summary>
    public event EventHandler? ImportFailed;

    /// <summary>The attachments the window lists, in order (compose.go <c>attachments</c>).</summary>
    public IReadOnlyList<DraftAttachment> Attachments => [.. attachments];

    /// <summary>The chips of <see cref="Attachments"/>, in order.</summary>
    public IReadOnlyList<AttachmentChip> Chips => [.. attachments.Select(AttachmentChip.For)];

    /// <summary>
    /// Whether <paramref name="path"/> names a file on the file system, the
    /// counterpart of a gio.File with a local path: a drive-absolute path
    /// (<c>C:\…</c>) or a share's (<c>\\server\share\…</c>). Nothing else is
    /// handed to <c>attachment.import</c>: an item the picker could not give
    /// a path (a phone, a virtual folder), a relative path, and the device
    /// namespaces (<c>\\?\</c>, <c>\\.\</c>).
    /// </summary>
    public static bool IsLocalPath(string? path)
    {
        if (string.IsNullOrEmpty(path) || path.Contains('\0', StringComparison.Ordinal))
        {
            return false;
        }
        if (path.Length >= 3 && char.IsAsciiLetter(path[0]) && path[1] == ':' && path[2] is '\\' or '/')
        {
            return true;
        }
        return path.Length >= 5 && path[0] is '\\' or '/' && path[1] is '\\' or '/'
            && path[2] is not ('?' or '.' or '\\' or '/');
    }

    /// <summary>
    /// A chip's name: <paramref name="filename"/> cut to
    /// <see cref="ChipNameChars"/> characters (text elements, so an emoji or
    /// a combining accent is never split) with a trailing ellipsis (Pango's
    /// EllipsizeEnd at max-width-chars, Swift <c>tailEllipsis</c>).
    /// </summary>
    public static string ChipName(string filename)
    {
        ArgumentNullException.ThrowIfNull(filename);
        var info = new StringInfo(filename);
        if (info.LengthInTextElements <= ChipNameChars)
        {
            return filename;
        }
        return info.SubstringByTextElements(0, ChipNameChars - 1) + "\u2026";
    }

    /// <summary>
    /// attachGioFiles: the files a picker chose or the editor's drop brought
    /// are imported, in order; each item without a local path is refused
    /// with a toast.
    /// </summary>
    public void AttachFiles(IEnumerable<string?> paths)
    {
        ArgumentNullException.ThrowIfNull(paths);
        scope.VerifyAccess();
        if (closed)
        {
            return;
        }
        foreach (var path in paths)
        {
            if (!IsLocalPath(path))
            {
                scope.Raise(ToastRequested, this, L10n.T("Only local files can be attached"));
                continue;
            }
            Import(path!, Path.GetFileName(path!), inline: false, then: null);
        }
    }

    /// <summary>
    /// insertImage's continuation: the picture the picker chose is imported
    /// inline, registered as <c>cid:&lt;contentId&gt;</c> by its path, and
    /// handed to <paramref name="insert"/> as that URL (the window inserts it
    /// at the caret). An item without a local path is refused with a toast.
    /// </summary>
    public void InsertImage(string? path, Action<string> insert)
    {
        ArgumentNullException.ThrowIfNull(insert);
        scope.VerifyAccess();
        if (closed)
        {
            return;
        }
        if (!IsLocalPath(path))
        {
            scope.Raise(ToastRequested, this, L10n.T("Only local images can be inserted"));
            return;
        }
        var local = path!;
        Import(local, Path.GetFileName(local), inline: true, then: att =>
        {
            if (att.ContentId is not { Length: > 0 } cid)
            {
                return;
            }
            registry.Register(cid, local, att.ContentType);
            insert("cid:" + cid);
        });
    }

    /// <summary>
    /// importFile: hands <paramref name="path"/> to the backend and lists the
    /// attachment on success; <paramref name="then"/> (optional) runs
    /// afterwards. The status line says what is being attached; a failure is
    /// a toast and puts the status line back.
    /// </summary>
    public void Import(string path, string name, bool inline, Action<DraftAttachment>? then)
    {
        ArgumentNullException.ThrowIfNull(path);
        ArgumentNullException.ThrowIfNull(name);
        scope.VerifyAccess();
        if (closed)
        {
            return;
        }
        scope.Raise(StatusChanged, this, L10n.T("Attaching %s…", name));
        var parameters = new AttachmentImportParams { AccountId = account(), Path = path, Filename = name, Inline = inline };
        scope.Perform(client, API.AttachmentImport, parameters, outcome =>
        {
            if (closed)
            {
                return;
            }
            if (!outcome.TryGetValue(out var res, out var error))
            {
                LogCallFailed(logger, API.AttachmentImport.Name, error!);
                scope.Raise(ToastRequested, this, RpcErrorText.Text(L10n.T("Attaching %s", name), error));
                Raise(ImportFailed);
                return;
            }
            attachments.Add(res.Attachment);
            Raise(Changed);
            Raise(Edited);
            then?.Invoke(res.Attachment);
        });
    }

    /// <summary>
    /// removeAttachment: the chip's Remove. The attachment leaves the list
    /// (and the <c>cid:</c> registry when inline) at once, which is an edit;
    /// the backend is told to drop it from its store, best effort (the sweep
    /// takes what is left).
    /// </summary>
    public void Remove(string id)
    {
        ArgumentNullException.ThrowIfNull(id);
        scope.VerifyAccess();
        var i = attachments.FindIndex(a => string.Equals(a.Id, id, StringComparison.Ordinal));
        if (closed || i < 0)
        {
            return;
        }
        var removed = attachments[i];
        attachments.RemoveAt(i);
        if (removed.Inline && removed.ContentId is { } cid)
        {
            registry.Unregister(cid);
        }
        Raise(Changed);
        Raise(Edited);
        var parameters = new AttachmentRemoveParams { AccountId = account(), AttachmentId = id };
        scope.PerformPastClose(client, API.AttachmentRemove, parameters, outcome =>
        {
            if (!outcome.IsSuccess)
            {
                LogCallFailed(logger, API.AttachmentRemove.Name, outcome.Error!);
            }
        });
    }

    /// <summary>
    /// setAttachments: replaces the list with what the backend kept (a save
    /// that dropped some, or the template of <c>draft.create</c> and
    /// <c>draft.open</c>). An inline picture that is gone is forgotten; one
    /// the window did not insert itself (a copy the backend made) is served
    /// to the editor from the backend. Not an edit.
    /// </summary>
    public void Set(IReadOnlyList<DraftAttachment> list)
    {
        ArgumentNullException.ThrowIfNull(list);
        scope.VerifyAccess();
        var kept = list.Where(a => a.Inline && a.ContentId is not null).Select(a => a.ContentId!).ToHashSet(StringComparer.Ordinal);
        foreach (var a in attachments)
        {
            if (a.Inline && a.ContentId is { } cid && !kept.Contains(cid))
            {
                registry.Unregister(cid);
            }
        }
        attachments.Clear();
        foreach (var a in list)
        {
            attachments.Add(a);
            if (a.Inline && a.ContentId is { Length: > 0 } cid && !registry.IsRegistered(cid))
            {
                RegisterInline(a, cid);
            }
        }
        Raise(Changed);
    }

    /// <summary>
    /// The window is gone: late imports are dropped and nothing more is
    /// accepted. The draft controller's Cleanup forgets the inline pictures
    /// (it reads <see cref="Attachments"/>, so it runs first). Idempotent.
    /// </summary>
    public void Dispose()
    {
        closed = true;
        scope.Close();
    }

    // registerInline: the editor fetches the picture behind cid:<contentId>
    // from the backend (attachment.get), for a copy the backend made in the
    // account of the window. The account is read when the editor asks, on
    // its (the UI) thread before the call: GTK reads it when it registers,
    // which for the first window of a run is the placeholder identity while
    // the account list is on its way (measured: attachmentNotFound, a broken
    // quoted picture).
    private void RegisterInline(DraftAttachment a, string cid)
    {
        var c = client;
        var id = a.Id;
        registry.RegisterFetcher(cid, async cancellationToken =>
        {
            var parameters = new AttachmentGetParams { AccountId = account(), AttachmentId = id };
            var res = await c.CallAsync(API.AttachmentGet, parameters, cancellationToken);
            return new InlineImage(res.Data, res.ContentType);
        });
    }

    // ControllerEvents.Raise for a plain event: each handler in its own
    // guard, so that a view's failure never breaks the controller.
    private void Raise(EventHandler? handlers)
    {
        if (handlers is null)
        {
            return;
        }
        foreach (var handler in handlers.GetInvocationList())
        {
            scope.Guard(() => ((EventHandler)handler).Invoke(this, EventArgs.Empty));
        }
    }

    [LoggerMessage(Level = LogLevel.Debug, Message = "{Method} failed")]
    private static partial void LogCallFailed(ILogger logger, string method, Exception error);
}
