// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Compose/FormatToolbar.swift (applyState,
// setBlock, setAlign, toggleQuote, clearFormatting, cssColor) and
// ComposeWindowController.swift's Format items; GTK:
// ui/internal/compose/compose.go (wireActions' block and align actions,
// wireToolbar, applyState) and compose.blp's block_menu and align_menu. A
// presentation class of docs/windows-port.md §7.4: what the formatting bar
// shows for the formatting at the caret, and which editing command each
// control sends. The command names are the DOM's execCommand names, as
// macOS sends them (GTK hands WebKit's own, capitalised).

using System;
using System.Collections.Generic;
using System.Globalization;
using Malachi.Core.Html;
using Malachi.Core.I18n;

namespace Malachi.Core.Presentation;

/// <summary>
/// The formatting bar's state for <see cref="EditorState"/> (applyState):
/// the toggles, the paragraph style's label and the alignment's icon.
/// </summary>
/// <param name="Bold">Bold at the caret.</param>
/// <param name="Italic">Italic at the caret.</param>
/// <param name="Underline">Underlined at the caret.</param>
/// <param name="BulletedList">In a bulleted list.</param>
/// <param name="NumberedList">In a numbered list.</param>
/// <param name="Quote">In a quote (a blockquote).</param>
/// <param name="Block">The paragraph style: <c>p</c>, <c>h1</c>, <c>h2</c> or <c>h3</c>.</param>
/// <param name="BlockLabel">The paragraph style's button label: "Paragraph" or "Heading %s".</param>
/// <param name="Align">The alignment: <c>left</c>, <c>center</c> or <c>right</c>.</param>
/// <param name="AlignIcon">The alignment button's GTK icon name.</param>
public sealed record FormatBarState(
    bool Bold, bool Italic, bool Underline, bool BulletedList, bool NumberedList, bool Quote,
    string Block, string BlockLabel, string Align, string AlignIcon)
{
    /// <summary>The paragraph styles of block_menu, in order.</summary>
    public static IReadOnlyList<string> Blocks { get; } = ["p", "h1", "h2", "h3"];

    /// <summary>The alignments of align_menu, in order.</summary>
    public static IReadOnlyList<string> Aligns { get; } = ["left", "center", "right"];

    /// <summary>
    /// The colour GTK's ColorDialogButton starts with, opaque black (macOS
    /// sets its well to it too).
    /// </summary>
    public const string InitialColor = "#000000";

    /// <summary>applyState: the bar for the formatting at the caret.</summary>
    public static FormatBarState From(EditorState st)
    {
        ArgumentNullException.ThrowIfNull(st);
        var block = st.Block;
        var label = L10n.T("Paragraph");
        switch (block)
        {
            case "h1" or "h2" or "h3":
                label = L10n.T("Heading %s", block[1..]);
                break;
            default:
                block = "p";
                break;
        }
        var align = st.Align is "center" or "right" ? st.Align : "left";
        return new FormatBarState(
            st.Bold, st.Italic, st.Underline, st.Ul, st.Ol, string.Equals(st.Block, "blockquote", StringComparison.Ordinal),
            block, label, align, AlignIconOf(align));
    }

    /// <summary>The bar before the editor reported anything: a left-aligned paragraph.</summary>
    public static FormatBarState Initial { get; } = From(new EditorState());

    /// <summary>block_menu's item labels: Paragraph, Heading 1, Heading 2, Heading 3.</summary>
    public static string BlockTitle(string block)
    {
        ArgumentNullException.ThrowIfNull(block);
        return block switch
        {
            "h1" => L10n.T("Heading 1"),
            "h2" => L10n.T("Heading 2"),
            "h3" => L10n.T("Heading 3"),
            _ => L10n.T("Paragraph"),
        };
    }

    /// <summary>align_menu's item labels: Left, Center, Right.</summary>
    public static string AlignTitle(string align)
    {
        ArgumentNullException.ThrowIfNull(align);
        return align switch
        {
            "center" => L10n.T("Center"),
            "right" => L10n.T("Right"),
            _ => L10n.T("Left"),
        };
    }

    /// <summary>The alignment button's icon: <c>format-justify-&lt;align&gt;-symbolic</c>.</summary>
    public static string AlignIconOf(string align)
    {
        ArgumentNullException.ThrowIfNull(align);
        return "format-justify-" + (align is "center" or "right" ? align : "left") + "-symbolic";
    }

    /// <summary>compose.align: the command of an alignment (anything else is left).</summary>
    public static (string Command, string? Argument) AlignCommand(string align) => align switch
    {
        "center" => ("justifyCenter", null),
        "right" => ("justifyRight", null),
        _ => ("justifyLeft", null),
    };

    /// <summary>compose.block: the command of a paragraph style (<c>p</c>, <c>h1</c>, <c>h2</c>, <c>h3</c>).</summary>
    public static (string Command, string? Argument) BlockCommand(string block)
    {
        ArgumentNullException.ThrowIfNull(block);
        return ("formatBlock", block is "h1" or "h2" or "h3" ? block : "p");
    }

    /// <summary>
    /// The quote toggle: turned on it wraps the paragraph in a blockquote,
    /// turned off it outdents out of one (compose.go's quote handler).
    /// </summary>
    public static (string Command, string? Argument) QuoteCommand(bool on) =>
        on ? ("formatBlock", "blockquote") : ("outdent", null);

    /// <summary>Clear Formatting: the formatting, then the links.</summary>
    public static IReadOnlyList<(string Command, string? Argument)> ClearCommands { get; } =
        [("removeFormat", null), ("unlink", null)];

    /// <summary>
    /// The text colour as <c>foreColor</c>'s argument, <c>#rrggbb</c>
    /// (macOS cssColor; GTK hands <c>rgb(r,g,b)</c>, the same colour).
    /// </summary>
    public static string CssColor(byte r, byte g, byte b) =>
        string.Create(CultureInfo.InvariantCulture, $"#{r:x2}{g:x2}{b:x2}");
}
