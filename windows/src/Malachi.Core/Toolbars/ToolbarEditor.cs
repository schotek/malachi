// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/toolbaredit/toolbaredit.go (MaxTokens, Row, Section,
// Editor, New, Bar, Reload, SetFeatures, Layout, Tokens, Sections,
// Available, Add, AddSpace, Remove, MoveUp, MoveDown, CanDrop, Drop,
// Reset, IsDefault). Go's package toolbaredit is this class; New is the
// constructor, Row and Section are ToolbarEditor.Row and
// ToolbarEditor.SectionRows (a nested Section would hide Toolbar.Section).
// Go's (int, bool) results are a bool with the new index in an out
// parameter.
//
// The toolbar editor's pure controller (decision T13,
// docs/plans/toolbar-customization.md §17): the arrangement of one toolbar
// as sections with rows, the items the user may add to a section, moving a
// row by dragging (Drop) or with Ctrl+↑/↓ (MoveUp, MoveDown) only where
// Toolbar.CanInsertFor allows, removing a row, adding a space or a
// flexible space, Restore Defaults, and the tokens the client writes to the
// toolbar's key (Toolbar.StoredFor, so the items the client lacks
// survive). The client draws Sections, calls the methods, writes Tokens
// after each change and calls Reload when the key changes.

using System;
using System.Collections.Generic;
using System.Linq;
using static Malachi.Core.Toolbars.Toolbar;

namespace Malachi.Core.Toolbars;

/// <summary>toolbaredit.Editor: edits one toolbar of one client.</summary>
public sealed class ToolbarEditor
{
    /// <summary>toolbaredit.MaxTokens: what the arrangement may grow to, below Toolbar.MaxTokens so what is written is read back whole.</summary>
    public const int MaxTokens = Toolbar.MaxTokens - 16;

    private readonly ToolbarVariant v;
    private Features f;
    private List<string> stored = [];
    private List<string> layout = [];

    /// <summary>toolbaredit.New: an editor of <paramref name="bar"/> for the client <paramref name="variant"/> over the key's tokens and the features at hand.</summary>
    public ToolbarEditor(ToolbarVariant variant, Bar bar, IReadOnlyList<string>? storedTokens, Features features)
    {
        ArgumentNullException.ThrowIfNull(variant);
        ArgumentNullException.ThrowIfNull(features);
        v = variant;
        Bar = bar;
        f = features;
        Reload(storedTokens);
    }

    /// <summary>Editor.Bar: the toolbar edited.</summary>
    public Bar Bar { get; }

    /// <summary>Editor.Layout: the arrangement as the client's toolbar has it.</summary>
    public IReadOnlyList<string> Layout => [.. layout];

    /// <summary>Editor.Tokens: what the client writes to the toolbar's key for the arrangement.</summary>
    public IReadOnlyList<string> Tokens => StoredFor(v, Bar, stored, layout);

    /// <summary>Editor.IsDefault: whether the arrangement is the client's default (to grey Restore Defaults out).</summary>
    public bool IsDefault => stored.Count == 0 && LayoutEqual(layout, DefaultFor(v, Bar));

    /// <summary>Editor.Reload: reads the key again (after the client wrote Tokens, or a change from elsewhere).</summary>
    public void Reload(IReadOnlyList<string>? storedTokens)
    {
        stored = storedTokens is null ? [] : [.. storedTokens];
        layout = [.. NormalizeFor(v, Bar, storedTokens)];
    }

    /// <summary>Editor.SetFeatures: the features the rows are marked and the items offered by.</summary>
    public void SetFeatures(Features features)
    {
        ArgumentNullException.ThrowIfNull(features);
        f = features;
    }

    /// <summary>Editor.Sections: the toolbar's sections with their rows (the separators are the sections' headings, not rows).</summary>
    public IReadOnlyList<SectionRows> Sections()
    {
        var sections = Toolbar.Sections(Bar);
        var rows = new List<Row>[sections.Count];
        for (var k = 0; k < rows.Length; k++)
        {
            rows[k] = [];
        }
        var cur = 0;
        for (var i = 0; i < layout.Count; i++)
        {
            var id = layout[i];
            if (IsSeparator(id))
            {
                var s = SectionAt(Bar, layout, i);
                for (var k = 0; k < sections.Count; k++)
                {
                    if (sections[k] == s)
                    {
                        cur = k;
                    }
                }
                continue;
            }
            var isFixed = false;
            var unavailable = false;
            if (LookupFor(v, Bar, id) is { } it)
            {
                isFixed = it.Fixed;
                unavailable = it.Feature is { } need && !f.Have.Contains(need);
            }
            rows[cur].Add(new Row(i, id, isFixed, IsSpace(id), unavailable, Target(i, -1, out _), Target(i, +1, out _)));
        }
        var output = new List<SectionRows>();
        for (var k = 0; k < sections.Count; k++)
        {
            output.Add(new SectionRows(sections[k], rows[k]));
        }
        return output;
    }

    /// <summary>
    /// Editor.Available: the items the user may add to section <paramref name="s"/>:
    /// what the client offers that the toolbar does not have and may stand
    /// there, in the catalog's order. The spaces are always addable.
    /// </summary>
    public IReadOnlyList<string> Available(Section s)
    {
        var have = new HashSet<string>(layout, StringComparer.Ordinal);
        return [.. AllowedFor(v, Bar, f).Where(id => !have.Contains(id) && CanInsertFor(v, Bar, id, s))];
    }

    /// <summary>Editor.Add: <paramref name="id"/> at the end of section <paramref name="s"/>; false when it may not stand there, is there already or the toolbar is full.</summary>
    public bool Add(string id, Section s)
    {
        if (IsSpace(id) || Available(s).Contains(id, StringComparer.Ordinal))
        {
            return Insert(id, SectionEndIndex(s));
        }
        return false;
    }

    /// <summary>
    /// Editor.AddSpace: a space or a flexible space after the row at index
    /// <paramref name="i"/> (at the start of the section, after its fixed
    /// items, when it is a separator; -1 for the first section).
    /// </summary>
    public bool AddSpace(string kind, int i)
    {
        if (!IsSpace(kind) || i < -1 || i >= layout.Count)
        {
            return false;
        }
        var at = i + 1;
        while (at < layout.Count && IsFixed(layout[at]))
        {
            at++;
        }
        return Insert(kind, at);
    }

    /// <summary>Editor.Remove: takes the row at index <paramref name="i"/> off the toolbar; false for a fixed item or a separator.</summary>
    public bool Remove(int i)
    {
        if (i < 0 || i >= layout.Count)
        {
            return false;
        }
        var id = layout[i];
        if (IsSeparator(id) || IsFixed(id))
        {
            return false;
        }
        layout.RemoveAt(i);
        Renormalize();
        return true;
    }

    /// <summary>
    /// Editor.MoveUp (Ctrl+↑): the row at <paramref name="i"/> one place up,
    /// past the row above or past the section's heading to the end of the
    /// section above when it may stand there; <paramref name="index"/> is its
    /// new index (the old one when it did not move).
    /// </summary>
    public bool MoveUp(int i, out int index) => StepBy(i, -1, out index);

    /// <summary>Editor.MoveDown (Ctrl+↓): the row at <paramref name="i"/> one place down.</summary>
    public bool MoveDown(int i, out int index) => StepBy(i, +1, out index);

    /// <summary>
    /// Editor.CanDrop: whether the row at <paramref name="fromIndex"/> may be
    /// dropped at insertion index <paramref name="toIndex"/>, counted in the
    /// layout without that row (0 before the first token, Layout.Count - 1
    /// after the last).
    /// </summary>
    public bool CanDrop(int fromIndex, int toIndex)
    {
        if (fromIndex < 0 || fromIndex >= layout.Count)
        {
            return false;
        }
        var id = layout[fromIndex];
        if (IsSeparator(id) || IsFixed(id))
        {
            return false;
        }
        var rest = new List<string>(layout);
        rest.RemoveAt(fromIndex);
        if (toIndex < 0 || toIndex > rest.Count)
        {
            return false;
        }
        return CanStand(rest, id, toIndex);
    }

    /// <summary>Editor.Drop: the row at <paramref name="fromIndex"/> to insertion index <paramref name="toIndex"/> (as CanDrop); a refused drop leaves it where it was.</summary>
    public bool Drop(int fromIndex, int toIndex, out int index)
    {
        if (!CanDrop(fromIndex, toIndex))
        {
            index = fromIndex;
            return false;
        }
        index = Move(fromIndex, toIndex);
        return true;
    }

    /// <summary>Editor.Reset: Restore Defaults: the client's default arrangement and the empty list to write (another client's items in the key go too).</summary>
    public IReadOnlyList<string> Reset()
    {
        stored = [];
        layout = [.. DefaultFor(v, Bar)];
        return [];
    }

    private bool StepBy(int i, int d, out int index)
    {
        if (!Target(i, d, out var to))
        {
            index = i;
            return false;
        }
        index = Move(i, to);
        return true;
    }

    // target: the insertion index (in the layout without the row) that a
    // step of the row at i in direction d gives.
    private bool Target(int i, int d, out int to)
    {
        to = 0;
        if (i < 0 || i >= layout.Count)
        {
            return false;
        }
        var id = layout[i];
        if (IsSeparator(id) || IsFixed(id))
        {
            return false;
        }
        // Without the row, the row above is at i-1 and the row below at i.
        var t = d > 0 ? i + 1 : i - 1;
        if (t < 0 || t > layout.Count - 1 || !CanDrop(i, t))
        {
            return false;
        }
        to = t;
        return true;
    }

    private bool IsFixed(string id) => LookupFor(v, Bar, id) is { Fixed: true };

    // canStand: whether id may be inserted into l at index at: the section
    // there allows it and no fixed item would stand after it in that section.
    private bool CanStand(List<string> l, string id, int at)
    {
        var sec = Toolbar.Sections(Bar)[0];
        if (at > 0)
        {
            sec = SectionAt(Bar, l, at - 1);
        }
        if (!CanInsertFor(v, Bar, id, sec))
        {
            return false;
        }
        return at >= l.Count || IsSeparator(l[at]) || !IsFixed(l[at]);
    }

    private bool Insert(string id, int at)
    {
        if (layout.Count >= MaxTokens || at < 0 || at > layout.Count || !CanStand(layout, id, at))
        {
            return false;
        }
        layout.Insert(at, id);
        Renormalize();
        return true;
    }

    private int Move(int fromIndex, int toIndex)
    {
        var id = layout[fromIndex];
        layout.RemoveAt(fromIndex);
        layout.Insert(toIndex, id);
        Renormalize();
        if (IsSpace(id))
        {
            return toIndex;
        }
        var i = layout.IndexOf(id);
        return i >= 0 ? i : toIndex;
    }

    // sectionEnd: the insertion index at the end of section s, -1 for a
    // section the toolbar lacks.
    private int SectionEndIndex(Section s)
    {
        var inside = Toolbar.Sections(Bar)[0] == s;
        var sep = Separator(s);
        for (var i = 0; i < layout.Count; i++)
        {
            if (IsSeparator(layout[i]))
            {
                if (inside)
                {
                    return i;
                }
                inside = string.Equals(layout[i], sep, StringComparison.Ordinal);
            }
        }
        return inside ? layout.Count : -1;
    }

    // normalize: keeps the arrangement what the client reads back; every
    // change above is valid, so this changes nothing but guards the rules.
    private void Renormalize() => layout = [.. NormalizeFor(v, Bar, layout)];

    /// <summary>toolbaredit.Row: an entry of the editor's list; Index is its place in Layout, the argument of the moves.</summary>
    public sealed record Row(int Index, string Id, bool Fixed, bool Space, bool Unavailable, bool CanMoveUp, bool CanMoveDown);

    /// <summary>toolbaredit.Section: a section of the toolbar with its rows.</summary>
    public sealed record SectionRows(Section Section, IReadOnlyList<Row> Rows);
}
