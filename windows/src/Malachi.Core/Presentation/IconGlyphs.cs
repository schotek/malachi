// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/Icons.swift (Icon.symbols,
// symbolName); GTK: the icon names of the Blueprints and of the Go UI
// (the model's roleIcon hands out GTK names, and so does Core). Windows
// draws them with the Segoe Fluent Icons font (the app's FontIcons), so
// the code keeps one key per GTK icon, as macOS does with SF Symbols. The
// table is research 05 Appendix B, each glyph checked against the font on
// Windows 11 26100. Segoe Fluent Icons has no numbered list: the plain
// list stands in for view-list-ordered (view-list-bullet has the bullets).
// An unknown name is a question mark, and the caller logs it once.

using System;
using System.Collections.Frozen;
using System.Collections.Generic;

namespace Malachi.Core.Presentation;

/// <summary>Segoe Fluent Icons glyphs for the GTK icon names.</summary>
public static class IconGlyphs
{
    /// <summary>The glyph of a name the table does not know (dialog-question).</summary>
    public static readonly string Unknown = G(0xE9CE);

    /// <summary>The font the glyphs are in.</summary>
    public const string FontFamily = "Segoe Fluent Icons";

    private const string Symbolic = "-symbolic";

    // One glyph of the font's private use area, by its code point.
    private static string G(int codePoint) => ((char)codePoint).ToString();

    /// <summary>GTK icon name (without <c>-symbolic</c>) → glyph.</summary>
    public static IReadOnlyDictionary<string, string> Table { get; } = new Dictionary<string, string>
    {
        ["mail-message-new"] = G(0xE932),
        ["open-menu"] = G(0xE700),
        ["view-refresh"] = G(0xE72C),
        ["edit-find"] = G(0xE721),
        ["mail-reply-sender"] = G(0xE8CA),
        ["mail-reply-all"] = G(0xE8C2),
        ["mail-forward"] = G(0xE89C),
        ["user-trash"] = G(0xE74D),
        ["mail-mark-junk"] = G(0xE733),
        ["folder-download"] = G(0xE7B8),
        ["non-starred"] = G(0xE734),
        ["starred"] = G(0xE735),
        ["view-more"] = G(0xE712),
        ["mail-unread"] = G(0xE715),
        ["mail-read"] = G(0xE8C3),
        ["system-users"] = G(0xE716),
        ["folder"] = G(0xE8B7),
        ["document-edit"] = G(0xE70F),
        ["mail-send"] = G(0xE724),
        ["mail-attachment"] = G(0xE723),
        ["pan-end"] = G(0xE76C),
        ["go-next"] = G(0xE76C),
        ["pan-down"] = G(0xE70D),
        ["network-offline"] = G(0xF384),
        ["network-idle"] = G(0xE895),
        ["network-transmit-receive"] = G(0xE968),
        // An attachment kept on the mail server (attachments.go
        // remoteIconNames: network-server, then folder-remote for an icon
        // theme without it): the cloud with the download arrow, as macOS
        // shows icloud.and.arrow.down for both.
        ["network-server"] = G(0xEBD3),
        ["folder-remote"] = G(0xEBD3),
        ["web-browser"] = G(0xE774),
        ["dialog-warning"] = G(0xE7BA),
        ["dialog-error"] = G(0xE783),
        ["dialog-question"] = Unknown,
        ["emblem-ok"] = G(0xE930),
        ["list-add"] = G(0xE710),
        ["list-drag-handle"] = G(0xE76F),
        ["document-save"] = G(0xE74E),
        ["window-close"] = G(0xE711),
        ["format-text-bold"] = G(0xE8DD),
        ["format-text-italic"] = G(0xE8DB),
        ["format-text-underline"] = G(0xE8DC),
        ["format-justify-left"] = G(0xE8E4),
        ["format-justify-center"] = G(0xE8E3),
        ["format-justify-right"] = G(0xE8E2),
        ["view-list-bullet"] = G(0xE8FD),
        ["view-list-ordered"] = G(0xEA37),
        ["format-indent-more"] = G(0xE9B2),
        ["insert-link"] = G(0xE71B),
        ["insert-image"] = G(0xE8B9),
        ["edit-clear"] = G(0xE75C),
        ["x-office-address-book"] = G(0xE779),
        ["document-open-recent"] = G(0xE823),
        ["image-x-generic"] = G(0xE91B),
        ["goa-account"] = G(0xE715),
        ["avatar-default"] = G(0xE77B),
        ["sidebar-show"] = G(0xE8A0),
        ["preferences-system"] = G(0xE713),
        ["applications-graphics"] = G(0xE790),
        ["applications-science"] = G(0xF196),
        // The Assistant (ui/internal/assistant): its own sparkle is
        // ChatSparkle, the panel's toggle DockRight, the context chip's
        // all mail the stacked layers (macOS tray.2), a finished tool the
        // check mark, several pinned contexts the list.
        ["malachi-assistant"] = G(0xEAB7),
        ["sidebar-show-right"] = G(0xE90D),
        ["mail-inbox"] = G(0xE81E),
        ["object-select"] = G(0xE73E),
        ["view-list"] = G(0xEA37),
        // Jira accounts: Reply as Comment (macOS text.bubble), the account's
        // row (GTK's stand-in for a ticket, as Adwaita has none), the views
        // Assigned to Me, Watching and Open as saved queries (Filter), and
        // the remove button of the settings' list editors.
        ["chat-message-new"] = G(0xE90A),
        ["checkbox-checked"] = G(0xE73A),
        ["folder-saved-search"] = G(0xE71C),
        ["list-remove"] = G(0xE738),
    }.ToFrozenDictionary(StringComparer.Ordinal);

    /// <summary>Whether <paramref name="gtkName"/> has a glyph of its own.</summary>
    public static bool IsKnown(string gtkName) => TryGlyph(gtkName, out _);

    /// <summary>
    /// The glyph of a GTK icon name, with or without <c>-symbolic</c>;
    /// <see cref="Unknown"/> for a name the table does not know.
    /// </summary>
    public static string Glyph(string? gtkName) => TryGlyph(gtkName, out var glyph) ? glyph : Unknown;

    /// <summary>The glyph of a GTK icon name when the table knows it (Swift <c>symbolName</c>).</summary>
    public static bool TryGlyph(string? gtkName, out string glyph)
    {
        glyph = Unknown;
        if (string.IsNullOrEmpty(gtkName))
        {
            return false;
        }
        var key = gtkName.EndsWith(Symbolic, StringComparison.Ordinal) ? gtkName[..^Symbolic.Length] : gtkName;
        if (Table.TryGetValue(key, out var found))
        {
            glyph = found;
            return true;
        }
        // goa-account-google, goa-account-msn, ... all have the envelope (U6).
        if (key.StartsWith("goa-account", StringComparison.Ordinal))
        {
            glyph = Table["goa-account"];
            return true;
        }
        return false;
    }
}
