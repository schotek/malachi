// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the pure part of ui/internal/window/devstart.go (parseDevStart,
// parseDevInterval, devFindFolder, devFindRow, devRichestCase), the GTK
// counterpart of the macOS client's MALACHI_START
// (macos/Sources/MalachiMail/MainWindow/MainWindowController+DevStart.swift).
// A DEVELOPMENT AID, not a feature (docs/screenshots.md §3, §6): the
// Windows-only variable MALACHI_START is a script of steps separated by ";"
// that the main window runs once it shows, MALACHI_START_INTERVAL seconds
// apart (default 3), so that the scenes of the screenshots can be put on
// the screen without clicking. Nothing of it runs without the variable,
// and nothing of it is translated. The steps that act on the window are
// Malachi.App's (MainWindow.DevStart.cs); this is the parser and the
// finders, tested without a window.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Calendars;
using Malachi.Core.Model;

namespace Malachi.Core.Presentation;

/// <summary>What one step of <c>MALACHI_START</c> does.</summary>
public enum DevelopmentStepKind
{
    /// <summary><c>mail</c>: the Mail mode.</summary>
    Mail,

    /// <summary><c>board</c> or <c>board:list|columns|today[:select]</c>: the Board, in that style, the richest case selected.</summary>
    Board,

    /// <summary><c>calendar</c> or <c>calendar:day|week|month|agenda</c>: the Calendar, in that view.</summary>
    Calendar,

    /// <summary><c>size=WxH</c>: the window's size in effective pixels.</summary>
    Size,

    /// <summary><c>quit</c>: the application quits.</summary>
    Quit,

    /// <summary><c>group=on|off</c>: the Group by Conversation setting.</summary>
    Group,

    /// <summary><c>chat=on|off</c>: the Show as Chat setting.</summary>
    Chat,

    /// <summary><c>folder=&lt;text&gt;</c>: the first folder whose server name contains the text, as a click in the sidebar.</summary>
    Folder,

    /// <summary><c>row=&lt;text&gt;</c>: the first row of the list whose subject contains the text, as a click.</summary>
    Row,

    /// <summary><c>compose-open</c>: a blank New Message window.</summary>
    ComposeOpen,

    /// <summary><c>crm-save</c>: Save to eWay-CRM… over the selected message (the dialog stays open).</summary>
    CrmSave,

    /// <summary><c>shot=&lt;path&gt;</c>: the window as a PNG.</summary>
    Shot,
}

/// <summary>One parsed step of <c>MALACHI_START</c>; <see cref="Name"/> is how it was written.</summary>
public sealed record DevelopmentStep
{
    /// <summary>What the step does.</summary>
    public required DevelopmentStepKind Kind { get; init; }

    /// <summary>The step as written, trimmed.</summary>
    public required string Name { get; init; }

    /// <summary><c>group=</c> and <c>chat=</c>: on or off.</summary>
    public bool On { get; init; }

    /// <summary><c>folder=</c>, <c>row=</c> and <c>shot=</c>: the text or the path.</summary>
    public string Text { get; init; } = "";

    /// <summary><c>size=</c>: the width.</summary>
    public int Width { get; init; }

    /// <summary><c>size=</c>: the height.</summary>
    public int Height { get; init; }

    /// <summary><c>board:&lt;style&gt;</c>: the style; null for a bare <c>board</c>.</summary>
    public BoardStyle? Style { get; init; }

    /// <summary><c>board:&lt;style&gt;:select</c>: the case with the most conversation text is selected.</summary>
    public bool Select { get; init; }

    /// <summary><c>calendar:&lt;view&gt;</c>: the view; null for a bare <c>calendar</c>.</summary>
    public Cal.View? View { get; init; }
}

/// <summary>The parser and the finders of the <c>MALACHI_START</c> development aid.</summary>
public static class DevelopmentStart
{
    /// <summary>The script's variable.</summary>
    public const string Variable = "MALACHI_START";

    /// <summary>The seconds between steps.</summary>
    public const string IntervalVariable = "MALACHI_START_INTERVAL";

    /// <summary>The window's size at start, <c>WxH</c> in effective pixels.</summary>
    public const string SizeVariable = "MALACHI_START_SIZE";

    /// <summary>The interval when <see cref="IntervalVariable"/> is empty or not a positive number.</summary>
    public static readonly TimeSpan DefaultInterval = TimeSpan.FromSeconds(3);

    /// <summary>
    /// Parses a script (<c>parseDevStart</c>); empty steps are skipped, an
    /// unknown one is a <see cref="FormatException"/> naming it.
    /// </summary>
    public static IReadOnlyList<DevelopmentStep> Parse(string? script)
    {
        var steps = new List<DevelopmentStep>();
        foreach (var raw in (script ?? "").Split(';'))
        {
            var name = raw.Trim();
            if (name.Length == 0)
            {
                continue;
            }
            steps.Add(ParseStep(name));
        }
        return steps;
    }

    /// <summary>
    /// <see cref="IntervalVariable"/> in seconds, fractions allowed
    /// (<c>parseDevInterval</c>); <see cref="DefaultInterval"/> when empty or
    /// not a positive number.
    /// </summary>
    public static TimeSpan ParseInterval(string? value)
    {
        if (double.TryParse((value ?? "").Trim(), NumberStyles.Float, CultureInfo.InvariantCulture, out var seconds)
            && seconds > 0 && seconds < TimeSpan.MaxValue.TotalSeconds)
        {
            return TimeSpan.FromSeconds(seconds);
        }
        return DefaultInterval;
    }

    /// <summary>
    /// <c>WxH</c> (<see cref="SizeVariable"/>): the size, or null when it is
    /// not two positive integers.
    /// </summary>
    public static (int Width, int Height)? ParseSize(string? value)
    {
        var parts = (value ?? "").Trim().Split('x', 2);
        if (parts.Length == 2
            && int.TryParse(parts[0], NumberStyles.None, CultureInfo.InvariantCulture, out var w)
            && int.TryParse(parts[1], NumberStyles.None, CultureInfo.InvariantCulture, out var h)
            && w > 0 && h > 0)
        {
            return (w, h);
        }
        return null;
    }

    /// <summary>
    /// The first folder, <paramref name="accounts"/> in the sidebar's order,
    /// whose server name contains <paramref name="text"/>, case-insensitively
    /// (<c>devFindFolder</c>); null without one.
    /// </summary>
    public static Folder? FindFolder(IEnumerable<Account> accounts, IReadOnlyDictionary<AccountId, IReadOnlyList<Folder>> folders, string text)
    {
        ArgumentNullException.ThrowIfNull(accounts);
        ArgumentNullException.ThrowIfNull(folders);
        ArgumentNullException.ThrowIfNull(text);
        foreach (var account in accounts)
        {
            if (!folders.TryGetValue(account.Id, out var of))
            {
                continue;
            }
            foreach (var f in of)
            {
                if (f.Name.Contains(text, StringComparison.OrdinalIgnoreCase))
                {
                    return f;
                }
            }
        }
        return null;
    }

    /// <summary>
    /// The index of the first row whose subject contains <paramref name="text"/>,
    /// case-insensitively (<c>devFindRow</c>); -1 without one.
    /// </summary>
    public static int FindRow(IReadOnlyList<ListRow> rows, string text)
    {
        ArgumentNullException.ThrowIfNull(rows);
        ArgumentNullException.ThrowIfNull(text);
        for (var i = 0; i < rows.Count; i++)
        {
            if (rows[i].Message.Subject.Contains(text, StringComparison.OrdinalIgnoreCase))
            {
                return i;
            }
        }
        return -1;
    }

    /// <summary>
    /// The case whose loaded conversation has the most text, in UTF-8 bytes
    /// (<c>devRichestCase</c>; the first of equals); null without cases.
    /// </summary>
    public static BoardCaseId? RichestCase(IEnumerable<Board.Case> cases)
    {
        ArgumentNullException.ThrowIfNull(cases);
        BoardCaseId? best = null;
        var most = -1;
        foreach (var c in cases)
        {
            var n = 0;
            foreach (var m in c.Messages ?? [])
            {
                n += Encoding.UTF8.GetByteCount(m.Text);
            }
            if (n > most)
            {
                best = c.Id;
                most = n;
            }
        }
        return best;
    }

    private static DevelopmentStep ParseStep(string name)
    {
        switch (name)
        {
            case "mail":
                return new DevelopmentStep { Kind = DevelopmentStepKind.Mail, Name = name };
            case "board":
                return new DevelopmentStep { Kind = DevelopmentStepKind.Board, Name = name };
            case "calendar":
                return new DevelopmentStep { Kind = DevelopmentStepKind.Calendar, Name = name };
            case "quit":
                return new DevelopmentStep { Kind = DevelopmentStepKind.Quit, Name = name };
            case "compose-open":
                return new DevelopmentStep { Kind = DevelopmentStepKind.ComposeOpen, Name = name };
            case "crm-save":
                return new DevelopmentStep { Kind = DevelopmentStepKind.CrmSave, Name = name };
        }
        if (name.StartsWith("calendar:", StringComparison.Ordinal))
        {
            var (view, known) = Cal.ParseView(name["calendar:".Length..]);
            if (!known)
            {
                throw new FormatException($"unknown calendar view in step \"{name}\"");
            }
            return new DevelopmentStep { Kind = DevelopmentStepKind.Calendar, Name = name, View = view };
        }
        if (name.StartsWith("size=", StringComparison.Ordinal))
        {
            if (ParseSize(name["size=".Length..]) is not { } size)
            {
                throw new FormatException($"step \"{name}\": want size=WxH");
            }
            return new DevelopmentStep { Kind = DevelopmentStepKind.Size, Name = name, Width = size.Width, Height = size.Height };
        }
        if (name.StartsWith("group=", StringComparison.Ordinal) || name.StartsWith("chat=", StringComparison.Ordinal))
        {
            var key = name[..name.IndexOf('=', StringComparison.Ordinal)];
            var value = name[(key.Length + 1)..];
            if (value is not ("on" or "off"))
            {
                throw new FormatException($"step \"{name}\": want {key}=on or {key}=off");
            }
            var kind = key == "chat" ? DevelopmentStepKind.Chat : DevelopmentStepKind.Group;
            return new DevelopmentStep { Kind = kind, Name = name, On = value == "on" };
        }
        if (name.StartsWith("folder=", StringComparison.Ordinal) || name.StartsWith("row=", StringComparison.Ordinal)
            || name.StartsWith("shot=", StringComparison.Ordinal))
        {
            var key = name[..name.IndexOf('=', StringComparison.Ordinal)];
            var value = name[(key.Length + 1)..].Trim();
            if (value.Length == 0)
            {
                throw new FormatException($"step \"{name}\": want {key}=<text>");
            }
            var kind = key switch
            {
                "folder" => DevelopmentStepKind.Folder,
                "row" => DevelopmentStepKind.Row,
                _ => DevelopmentStepKind.Shot,
            };
            return new DevelopmentStep { Kind = kind, Name = name, Text = value };
        }
        if (name.StartsWith("board:", StringComparison.Ordinal))
        {
            var parts = name["board:".Length..].Split(':');
            var style = Board.ParseStyle(parts[0]);
            if (style.Nick != parts[0] || parts.Length > 2 || (parts.Length == 2 && parts[1] != "select"))
            {
                throw new FormatException($"step \"{name}\": want board:list|columns|today[:select]");
            }
            return new DevelopmentStep { Kind = DevelopmentStepKind.Board, Name = name, Style = style, Select = parts.Length == 2 };
        }
        throw new FormatException($"unknown step \"{name}\"");
    }
}
