// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Compose/ComposeWindowController.swift
// (wireEditor, wireToolbar, focusEditor); GTK: ui/internal/compose/compose.go
// (newWindow's editor callbacks: OnState, OnChanged, OnDropFiles, OnReady,
// OnCrashed; wireToolbar). The compose window's side of the editor of
// docs/windows-port.md §6.5: Ready and Changed go to the draft controller
// (EditorReady, the Windows baseline of the flush echo, and EditorChanged),
// the formatting at the caret to the bar, the bridge's Escape to the close
// request and its Ctrl+K to the link popover, dropped files to
// attachment.import, and a failed page to the toast and a reload of the
// last text (the view reloads a text once; ComposeWebView). The WebView2 is
// marked as the editor for the window's keys and named for Narrator, again
// whenever a failed browser process gave the view a new one. Plain text
// that looks like Markdown, pasted into the page, goes to the draft
// controller (PasteMarkdown: draft.markdown) and comes back to the page as
// the daemon's sanitised HTML, or as the text it was; a comment is no
// different.

using Malachi.App.Commands;
using Malachi.Core.Compose;
using Malachi.Core.I18n;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml.Automation;

namespace Malachi.App.Compose;

/// <summary>The editor of a compose window.</summary>
public sealed partial class ComposeWindow
{
    private void WireEditor()
    {
        EditorSlot.Child = editor;
        MarkEditor();
        editor.CoreWebViewInitialized += (_, _) => MarkEditor();
        editor.Channel.Ready += (_, _) => OnEditorReady();
        editor.Channel.Changed += (_, _) => draft.EditorChanged();
        editor.Channel.StateChanged += (_, st) => FormatBar.ApplyState(FormatBarState.From(st));
        editor.Channel.KeyPressed += (_, key) => OnEditorKey(key);
        editor.Channel.PasteRequested += (_, paste) => draft.PasteMarkdown(paste.Text, html => editor.Pasted(paste.Id, html));
        editor.FilesDropped += (_, paths) =>
        {
            // A comment has no attachments: what is dropped is refused.
            if (!IsComment)
            {
                attachments.AttachFiles(paths);
            }
        };
        editor.Crashed += (_, _) => OnEditorCrashed();
    }

    // compose.go wireToolbar: the bar's commands go to the page.
    private void WireToolbar()
    {
        FormatBar.Exec = (command, argument) => editor.Exec(command, argument);
        FormatBar.FocusEditor = FocusEditor;
        FormatBar.InsertImageRequested = () => _ = InsertImageAsync();
    }

    // The keys of the window pass the page's own (KeyboardRouting.IsEditor);
    // the name is what Narrator reads for the page.
    private void MarkEditor()
    {
        KeyboardRouting.SetIsEditor(editor.Web, true);
        // Windows-only string: GTK and macOS give the editor no name.
        AutomationProperties.SetName(editor.Web, "Message body");
        AutomationProperties.SetAutomationId(editor.Web, "ComposeEditor");
    }

    // OnReady: the flush echo's baseline (Windows), and for a reply or a
    // forward the caret at the start of the body (compose.go).
    private void OnEditorReady()
    {
        draft.EditorReady();
        if (parameters.Kind is not (ComposeKind.New or ComposeKind.Edit))
        {
            editor.FocusStart();
        }
    }

    // The bridge's keys (BridgeMessage.Key): Escape is the window's Escape,
    // Ctrl+K Insert Link.
    private void OnEditorKey(string key)
    {
        switch (key)
        {
            case "escape":
                if (!PopupOpen)
                {
                    RequestClose();
                }
                break;
            case "link":
                FormatBar.ShowLinkFlyout();
                break;
        }
    }

    // OnCrashed: the page died, or its document could not be loaded; the
    // last text comes back.
    private void OnEditorCrashed()
    {
        if (draft.Draft.Closed)
        {
            return;
        }
        Toast(L10n.T("The editor crashed; your last text was restored"));
        editor.Load(editor.Html);
    }

    // editor.GrabFocus: the keyboard back to the page.
    private void FocusEditor() => editor.FocusPage();
}
