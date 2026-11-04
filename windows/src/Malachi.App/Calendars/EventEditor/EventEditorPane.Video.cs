// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/EventEditor/EventEditorViewController.swift
// (renderVideo, videoChosen, askLink); GTK: the event editor follows
// (docs/calendar.md §6.4), the texts are ui/internal/calendar/editor.go
// (AddVideoCall, PasteLink, VideoProviderName).
//
// Add Video Call ▾ of the event editor (docs/calendar.md §5.6, M7): its
// title is the chosen provider, or the link's host, else Add Video Call;
// its menu lists the calendar's providers (a meeting the account creates),
// Paste Link… and, once there is a call, Remove. Paste Link… asks for an
// https link in a dialog prefilled from an https link on the clipboard (one
// line), else the link already set. Windows: a link the controller refuses
// (not https, no host) asks again with what was typed, where macOS beeps.

using System;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Windows.ApplicationModel.DataTransfer;

namespace Malachi.App.Calendars;

/// <summary>Add Video Call ▾ of the event editor's form.</summary>
public sealed partial class EventEditorPane
{
    // The host of a link, cleaned; null when it has none.
    private static string? HostOf(string? url) =>
        Uri.TryCreate(url, UriKind.Absolute, out var u) && u.Host.Length > 0 ? CalendarDisplay.Line(u.Host) : null;

    // renderVideo: the button's title says what is chosen.
    private void RenderVideo(EventEditorController c)
    {
        var title = Cal.AddVideoCall;
        string? link = null;
        if (c.VideoCall is { } v)
        {
            if (v.Mode == VideoCallMode.Create)
            {
                title = Cal.VideoProviderName(v.Provider);
            }
            else if (v.Mode == VideoCallMode.Link)
            {
                title = HostOf(v.Url) ?? Cal.AddVideoCall;
                link = v.Url;
            }
        }
        VideoTitle.Text = title;
        AutomationProperties.SetName(VideoButton, title);
        ToolTipService.SetToolTip(VideoButton, link);
    }

    // The menu as it opens: the providers, Paste Link…, Remove.
    private void OnVideoMenuOpening(object? sender, object e)
    {
        VideoMenu.Items.Clear();
        if (controller is not { } c)
        {
            return;
        }
        var v = c.VideoCall;
        var isLink = v is not null && v.Mode == VideoCallMode.Link;
        var provider0 = v is not null && v.Mode == VideoCallMode.Create ? v.Provider : null;
        var providers = c.VideoProviders;
        foreach (var p in providers)
        {
            var item = new ToggleMenuFlyoutItem
            {
                Text = Cal.VideoProviderName(p),
                IsChecked = string.Equals(provider0, p, StringComparison.Ordinal),
            };
            AutomationProperties.SetAutomationId(item, "EventEditorVideoProvider");
            var provider = p;
            item.Click += (_, _) => controller?.SetVideoCall(new VideoCallInput { Mode = VideoCallMode.Create, Provider = provider });
            VideoMenu.Items.Add(item);
        }
        if (providers.Count > 0)
        {
            VideoMenu.Items.Add(new MenuFlyoutSeparator());
        }
        var paste = new ToggleMenuFlyoutItem { Text = Cal.PasteLink, IsChecked = isLink };
        AutomationProperties.SetAutomationId(paste, "EventEditorPasteLink");
        paste.Click += (_, _) => _ = PasteLinkAsync();
        VideoMenu.Items.Add(paste);
        if (v is not null)
        {
            VideoMenu.Items.Add(new MenuFlyoutSeparator());
            var remove = new MenuFlyoutItem { Text = L10n.T("Remove") };
            AutomationProperties.SetAutomationId(remove, "EventEditorRemoveVideoCall");
            remove.Click += (_, _) => controller?.SetVideoCall(null);
            VideoMenu.Items.Add(remove);
        }
    }

    // askLink: an https link, the clipboard's when it holds one.
    private async Task PasteLinkAsync()
    {
        if (closed || controller is not { } c || state is not { } s)
        {
            return;
        }
        var current = c.VideoCall is { } v && v.Mode == VideoCallMode.Link ? v.Url ?? "" : "";
        var initial = await ClipboardLinkAsync() ?? current;
        while (!closed)
        {
            var text = await s.Alerts.VideoLinkAsync(HostWindow, initial);
            if (text is null || closed || controller is null)
            {
                return;
            }
            if (controller.SetVideoCall(new VideoCallInput { Mode = VideoCallMode.Link, Url = text }))
            {
                return;
            }
            initial = text;
        }
    }

    // A single-line https link on the clipboard; null for anything else or
    // a clipboard another program holds.
    private static async Task<string?> ClipboardLinkAsync()
    {
        try
        {
            var content = Clipboard.GetContent();
            if (!content.Contains(StandardDataFormats.Text))
            {
                return null;
            }
            var s = (await content.GetTextAsync()).Trim();
            return s.StartsWith("https://", StringComparison.OrdinalIgnoreCase) && !s.Contains('\n', StringComparison.Ordinal) ? s : null;
        }
        catch (Exception e) when (e is System.Runtime.InteropServices.COMException or UnauthorizedAccessException)
        {
            return null;
        }
    }
}
