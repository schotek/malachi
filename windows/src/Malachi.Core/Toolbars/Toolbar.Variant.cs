// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/toolbar/variant.go (LookupFor, CatalogFor,
// DefaultFor, NormalizeFor, CanInsertFor, ImmovableFor, AllowedFor,
// PresentFor, MissingActionsFor, StoredFor, storage, omitted, sameIDs,
// indexOf, sectionEnd); the variants themselves are ToolbarVariant.
// What a client lacks is left out when it reads a layout (NormalizeFor)
// and kept when it writes one (StoredFor, §17.4 of the plan).

using System;
using System.Collections.Generic;
using System.Linq;

namespace Malachi.Core.Toolbars;

public static partial class Toolbar
{
    private static ToolbarVariant? storage;

    // storage: how StoredFor reads the stored list, the catalog with the
    // extra sections of every client.
    private static ToolbarVariant Storage => storage ??= MakeStorage();

    /// <summary>
    /// toolbar.LookupFor: <paramref name="id"/>'s entry in <paramref name="bar"/>'s
    /// catalog for the client: null for an omitted item, the sections with
    /// the variant's extra ones.
    /// </summary>
    public static Item? LookupFor(ToolbarVariant v, Bar bar, string id)
    {
        ArgumentNullException.ThrowIfNull(v);
        var it = Lookup(bar, id);
        if (it is null || v.Omits(bar, id))
        {
            return null;
        }
        var extra = v.ExtraSections(bar, id);
        if (extra.Count == 0)
        {
            return it;
        }
        var secs = new List<Section>(it.Sections);
        foreach (var s in extra)
        {
            if (!it.Allows(s))
            {
                secs.Add(s);
            }
        }
        return it with { Sections = secs };
    }

    /// <summary>toolbar.CatalogFor: what the client's <paramref name="bar"/> can hold, in the catalog's order.</summary>
    public static IReadOnlyList<Item> CatalogFor(ToolbarVariant v, Bar bar)
    {
        var output = new List<Item>();
        foreach (var it in Catalogs[bar])
        {
            if (LookupFor(v, bar, it.Id) is { } x)
            {
                output.Add(x);
            }
        }
        return output;
    }

    /// <summary>toolbar.DefaultFor: the client's default arrangement of <paramref name="bar"/>.</summary>
    public static IReadOnlyList<string> DefaultFor(ToolbarVariant v, Bar bar)
    {
        ArgumentNullException.ThrowIfNull(v);
        IReadOnlyList<string> source = Defaults[bar];
        if (v.Defaults.TryGetValue(bar, out var d) && d is not null)
        {
            source = d;
        }
        return [.. source.Where(id => !v.Omits(bar, id))];
    }

    /// <summary>
    /// toolbar.NormalizeFor: the client's valid arrangement of <paramref name="bar"/>
    /// from a stored token list: an empty list is the client's default;
    /// unknown and omitted ids, a second copy of an item and anything past
    /// <see cref="MaxTokens"/> go; separators stand in their order, each once;
    /// an item in a section it may not stand in goes to the end of its home
    /// section; a fixed item stands at the start of its home section in the
    /// order of the client's default.
    /// </summary>
    public static IReadOnlyList<string> NormalizeFor(ToolbarVariant v, Bar bar, IReadOnlyList<string>? stored)
    {
        ArgumentNullException.ThrowIfNull(v);
        var secs = SectionsOf[bar];
        if (stored is null || stored.Count == 0 || secs.Length == 0)
        {
            return DefaultFor(v, bar);
        }
        var count = Math.Min(stored.Count, MaxTokens);
        var parts = new List<string>[secs.Length];
        var moved = new List<string>[secs.Length];
        for (var i = 0; i < secs.Length; i++)
        {
            parts[i] = [];
            moved[i] = [];
        }
        var seen = new HashSet<string>(StringComparer.Ordinal);
        var cur = 0;
        for (var n = 0; n < count; n++)
        {
            var tok = stored[n] ?? "";
            if (IsSeparator(tok))
            {
                if (TryParseSeparator(bar, tok, out var sec))
                {
                    var i = Array.IndexOf(secs, sec);
                    if (i > cur)
                    {
                        cur = i;
                    }
                }
                continue;
            }
            if (IsSpace(tok))
            {
                parts[cur].Add(tok);
                continue;
            }
            var it = LookupFor(v, bar, tok);
            if (it is null || seen.Contains(tok) || it.Fixed)
            {
                continue;
            }
            seen.Add(tok);
            if (it.Allows(secs[cur]))
            {
                parts[cur].Add(tok);
            }
            else
            {
                moved[Array.IndexOf(secs, it.Home)].Add(tok);
            }
        }
        var def = DefaultFor(v, bar);
        var output = new List<string>();
        for (var i = 0; i < secs.Length; i++)
        {
            var s = secs[i];
            if (i > 0)
            {
                output.Add(Separator(s));
            }
            foreach (var id in def)
            {
                if (LookupFor(v, bar, id) is { Fixed: true } it && it.Home == s)
                {
                    output.Add(id);
                }
            }
            output.AddRange(parts[i]);
            output.AddRange(moved[i]);
        }
        return output;
    }

    /// <summary>
    /// toolbar.CanInsertFor: whether the user of the client may put
    /// <paramref name="id"/> into section <paramref name="s"/> of
    /// <paramref name="bar"/>. Spaces go anywhere; separators, fixed and
    /// omitted items never.
    /// </summary>
    public static bool CanInsertFor(ToolbarVariant v, Bar bar, string id, Section s)
    {
        if (IsSpace(id))
        {
            return true;
        }
        if (IsSeparator(id))
        {
            return false;
        }
        return LookupFor(v, bar, id) is { Fixed: false } it && it.Allows(s);
    }

    /// <summary>toolbar.ImmovableFor: the client's items of <paramref name="bar"/> the user can neither move nor remove, its separators included.</summary>
    public static IReadOnlyList<string> ImmovableFor(ToolbarVariant v, Bar bar)
    {
        var output = new List<string>();
        foreach (var it in CatalogFor(v, bar))
        {
            if (it.Fixed)
            {
                output.Add(it.Id);
            }
        }
        foreach (var s in SectionsOf[bar].Skip(1))
        {
            output.Add(Separator(s));
        }
        return output;
    }

    /// <summary>toolbar.AllowedFor: what the client's editor offers: its catalog without the items whose feature is missing, in the catalog's order.</summary>
    public static IReadOnlyList<string> AllowedFor(ToolbarVariant v, Bar bar, Features f)
    {
        ArgumentNullException.ThrowIfNull(f);
        var output = new List<string>();
        foreach (var it in CatalogFor(v, bar))
        {
            if (it.Feature is not { } need || f.Have.Contains(need))
            {
                output.Add(it.Id);
            }
        }
        return output;
    }

    /// <summary>
    /// toolbar.PresentFor: what the client's toolbar holds for <paramref name="l"/>:
    /// the hidden sections with their separators and the hidden separators
    /// left out, an item whose feature is missing kept in its place and
    /// hidden, an item the client omits left out.
    /// </summary>
    public static IReadOnlyList<Shown> PresentFor(ToolbarVariant v, Bar bar, IReadOnlyList<string> l, Features f)
    {
        ArgumentNullException.ThrowIfNull(v);
        ArgumentNullException.ThrowIfNull(l);
        ArgumentNullException.ThrowIfNull(f);
        var output = new List<Shown>();
        var secs = SectionsOf[bar];
        Section? s = secs.Length > 0 ? secs[0] : Section.Main;
        foreach (var id in l)
        {
            if (IsSeparator(id))
            {
                // An unknown separator starts a section nothing hides (Go's
                // section of an unknown name).
                s = TryParseSeparator(bar, id, out var x) ? x : null;
                if ((s is { } hs && f.HiddenSections.Contains(hs)) || f.HiddenSeparators.Contains(id))
                {
                    continue;
                }
                output.Add(new Shown(id));
                continue;
            }
            if ((s is { } cs && f.HiddenSections.Contains(cs)) || v.Omits(bar, id))
            {
                continue;
            }
            var hidden = Lookup(bar, id) is { Feature: { } need } && !f.Have.Contains(need);
            output.Add(new Shown(id, hidden));
        }
        return output;
    }

    /// <summary>
    /// toolbar.MissingActionsFor: the actions the client's More Actions menu
    /// adds because <paramref name="l"/> does not show them, in the catalog's
    /// order (T8); an action whose feature is missing, or that the client
    /// omits, is none. The client adds the ones its toolbar moved to its
    /// overflow.
    /// </summary>
    public static IReadOnlyList<string> MissingActionsFor(ToolbarVariant v, Bar bar, IReadOnlyList<string> l, Features f)
    {
        ArgumentNullException.ThrowIfNull(f);
        var shown = new HashSet<string>(StringComparer.Ordinal);
        foreach (var e in PresentFor(v, bar, l, f))
        {
            if (!e.Hidden)
            {
                shown.Add(e.Id);
            }
        }
        var output = new List<string>();
        foreach (var it in CatalogFor(v, bar))
        {
            if (it.InMore && !shown.Contains(it.Id) && (it.Feature is not { } need || f.Have.Contains(need)))
            {
                output.Add(it.Id);
            }
        }
        return output;
    }

    /// <summary>
    /// toolbar.StoredFor: what the settings key keeps after the client's user
    /// arranged <paramref name="bar"/> as <paramref name="l"/>, the key holding
    /// <paramref name="stored"/> before (T12): the client's arrangement with
    /// the items the client omits merged back from <paramref name="stored"/>.
    /// An empty stored list stands for macOS's default. Each omitted item of
    /// the stored list goes after the nearest token before it there that the
    /// result has in the same section, else at the end of that section. The
    /// key is emptied only when <paramref name="l"/> is the client's default
    /// and the omitted items in <paramref name="stored"/> are the ones
    /// macOS's default has. For the zero variant this is <see cref="Stored"/>.
    /// </summary>
    public static IReadOnlyList<string> StoredFor(ToolbarVariant v, Bar bar, IReadOnlyList<string>? stored, IReadOnlyList<string> l)
    {
        ArgumentNullException.ThrowIfNull(v);
        var edited = NormalizeFor(v, bar, l);
        var source = NormalizeFor(Storage, bar, stored);
        if (LayoutEqual(edited, DefaultFor(v, bar)) && SameIds(Omitted(v, bar, source), Omitted(v, bar, DefaultLayout(bar))))
        {
            return [];
        }
        var output = new List<string>(edited);
        var have = new HashSet<string>(output.Where(id => !IsSpace(id)), StringComparer.Ordinal);
        for (var j = 0; j < source.Count; j++)
        {
            var id = source[j];
            if (IsSpace(id) || IsSeparator(id) || !v.Omits(bar, id))
            {
                continue;
            }
            var sec = SectionAt(bar, source, j);
            var at = -1;
            for (var k = j - 1; k >= 0; k--)
            {
                var y = source[k];
                if (IsSpace(y) || !have.Contains(y))
                {
                    continue;
                }
                var i = output.IndexOf(y);
                if (i >= 0 && SectionAt(bar, output, i) == sec)
                {
                    at = i + 1;
                    break;
                }
            }
            if (at < 0)
            {
                at = SectionEnd(bar, output, sec);
            }
            output.Insert(at, id);
            have.Add(id);
        }
        return output;
    }

    private static ToolbarVariant MakeStorage()
    {
        var extra = new Dictionary<Bar, Dictionary<string, List<Section>>>();
        foreach (var v in new[] { ToolbarVariant.Gtk, ToolbarVariant.Windows })
        {
            foreach (var (bar, m) in v.Extra)
            {
                if (!extra.TryGetValue(bar, out var byId))
                {
                    byId = new Dictionary<string, List<Section>>(StringComparer.Ordinal);
                    extra[bar] = byId;
                }
                foreach (var (id, secs) in m)
                {
                    if (!byId.TryGetValue(id, out var list))
                    {
                        list = [];
                        byId[id] = list;
                    }
                    foreach (var s in secs)
                    {
                        if (!list.Contains(s))
                        {
                            list.Add(s);
                        }
                    }
                }
            }
        }
        var frozen = new Dictionary<Bar, IReadOnlyDictionary<string, IReadOnlyList<Section>>>();
        foreach (var (bar, byId) in extra)
        {
            frozen[bar] = byId.ToDictionary(p => p.Key, p => (IReadOnlyList<Section>)p.Value, StringComparer.Ordinal);
        }
        return new ToolbarVariant { Extra = frozen };
    }

    // omitted: the items of l the client omits, in l's order.
    private static List<string> Omitted(ToolbarVariant v, Bar bar, IReadOnlyList<string> l) =>
        [.. l.Where(id => !IsSpace(id) && !IsSeparator(id) && v.Omits(bar, id))];

    private static bool SameIds(List<string> a, List<string> b)
    {
        if (a.Count != b.Count)
        {
            return false;
        }
        var set = new HashSet<string>(a, StringComparer.Ordinal);
        return b.All(set.Contains);
    }

    // sectionEnd: the index just past the last token of section s in l
    // (before the separator of the next section), l.Count for the last one.
    private static int SectionEnd(Bar bar, List<string> l, Section s)
    {
        var secs = SectionsOf[bar];
        var inside = secs.Length > 0 && secs[0] == s;
        var sep = Separator(s);
        for (var i = 0; i < l.Count; i++)
        {
            if (IsSeparator(l[i]))
            {
                if (inside)
                {
                    return i;
                }
                if (string.Equals(l[i], sep, StringComparison.Ordinal))
                {
                    inside = true;
                }
            }
        }
        return l.Count;
    }
}
