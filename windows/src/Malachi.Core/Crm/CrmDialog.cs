// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/CRM/CRMDialog.swift; GTK:
// ui/internal/crm/dialog.go (Kinds, CanBeSuperior, guidShape, knownKind,
// refOf, cleanItem, DialogEntry, DialogGroup, Dialog, NewDialog and its
// methods).
//
// The model of the Save to eWay-CRM dialog over the result of crm.lookup
// (and the Edit Links dialog of one saved message), with the search results
// merged in. Prefill (docs/eway.md D9, §5 Q2): contacts and companies found
// by an address or a tracked domain are checked; the one deal or project the
// daemon flags Superior is checked as the superior item; everything else is
// listed unchecked. Over one saved message (Editing) the record's items are
// checked instead, the superior one as recorded, and the suggestions are
// listed unchecked. At most one superior item (§5 Q1): checking a deal or
// project while none is superior makes it the superior item, SetSuperior
// moves the marker, unchecking the superior item leaves none.
//
// Windows: Go's Dialog is this class (NewDialog its constructor), its
// DialogEntry and DialogGroup are CrmDialogEntry and CrmDialogGroup; Go's
// map iteration is never part of a result, so the order kept per kind is
// the only order there is.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Text;
using System.Text.RegularExpressions;
using Malachi.Core.Api;
using Malachi.Core.IssueTrackers;

namespace Malachi.Core.Crm;

/// <summary>dialog.DialogEntry: a row of the dialog: an item with its check box and whether it is the superior item.</summary>
/// <param name="Item">The item, cleaned.</param>
/// <param name="Checked">Its check box.</param>
/// <param name="Superior">It is the superior item (the "Superior Item" marker, a radio among the checked deals and projects).</param>
public sealed record CrmDialogEntry(CrmItem Item, bool Checked, bool Superior)
{
    /// <summary>Ref: names the entry's item.</summary>
    public CrmItemRef Ref => new() { Kind = Item.Kind, ItemGuid = Item.ItemGuid };

    /// <summary>CanBeSuperior: the entry offers the Superior Item marker.</summary>
    public bool CanBeSuperior => CrmDialog.CanBeSuperior(Item.Kind);
}

/// <summary>dialog.DialogGroup: the entries of one kind, in <see cref="CrmDialog.Kinds"/>' order.</summary>
/// <param name="Kind">The kind.</param>
/// <param name="Entries">Its entries, in the order they were listed.</param>
public sealed record CrmDialogGroup(CrmItemKind Kind, IReadOnlyList<CrmDialogEntry> Entries);

/// <summary>
/// crm.Dialog: the model of the Save to eWay-CRM dialog (and Edit Links).
/// Not thread-safe: the dialog touches it on the UI thread.
/// </summary>
public sealed partial class CrmDialog
{
    private readonly Dictionary<(string Kind, string Guid), CrmItem> items = [];
    private readonly Dictionary<string, List<(string Kind, string Guid)>> order = new(StringComparer.Ordinal);
    private readonly HashSet<(string Kind, string Guid)> checkedItems = [];
    private readonly HashSet<(string Kind, string Guid)> recorded = [];
    private readonly List<MessageId> messages = [];
    private readonly (string Kind, string Guid)? recordedSuperior;
    private readonly bool recordedPrivate;

    // The superior item; null for none.
    private (string Kind, string Guid)? superior;

    /// <summary>
    /// crm.NewDialog: the dialog over <paramref name="res"/>, the answer of
    /// crm.lookup for the messages the dialog saves. It edits when
    /// <paramref name="res"/> is about one message that is saved.
    /// </summary>
    public CrmDialog(CrmLookupResult res)
    {
        ArgumentNullException.ThrowIfNull(res);
        AccountId = res.AccountId;
        Partial = res.IsPartial;
        foreach (var s in res.Saved)
        {
            messages.Add(s.MessageId);
        }
        if (res.Saved is [{ Record: { } rec }])
        {
            Editing = true;
            Private = rec.Private;
            recordedPrivate = rec.Private;
            foreach (var it in rec.Items)
            {
                if (Add(it) is not { } r)
                {
                    continue;
                }
                checkedItems.Add(r);
                recorded.Add(r);
                if (it.IsSuperior && CanBeSuperior(r.Kind) && superior is null)
                {
                    superior = r;
                    recordedSuperior = r;
                }
            }
        }
        foreach (var it in res.Suggestions)
        {
            if (Add(it) is not { } r || Editing)
            {
                continue;
            }
            if ((r.Kind == CrmItemKind.Contact || r.Kind == CrmItemKind.Company)
                && (it.Reason?.Value is CrmReason.Address or CrmReason.Domain))
            {
                checkedItems.Add(r);
            }
            else if (it.IsSuperior && CanBeSuperior(r.Kind) && superior is null)
            {
                checkedItems.Add(r);
                superior = r;
            }
        }
    }

    /// <summary>crm.Kinds: the kinds of CRM items in the order the dialog groups them: Contacts, Companies, Deals, Projects.</summary>
    public static IReadOnlyList<CrmItemKind> Kinds { get; } =
        [CrmItemKind.Contact, CrmItemKind.Company, CrmItemKind.Deal, CrmItemKind.Project];

    /// <summary>The eWay-CRM account crm.lookup answered for.</summary>
    public AccountId AccountId { get; }

    /// <summary>The messages of the lookup, in order.</summary>
    public IReadOnlyList<MessageId> Messages => [.. messages];

    /// <summary>The dialog edits the links of one saved message (its title is Edit Links).</summary>
    public bool Editing { get; }

    /// <summary>The CRM did not answer every lookup in time; the dialog shows the hint.</summary>
    public bool Partial { get; }

    /// <summary>Nothing is listed; the dialog shows its empty text (the search still works).</summary>
    public bool IsEmpty => items.Count == 0;

    /// <summary>
    /// OffersSearchHint: the dialog shows the quiet line that deals and
    /// projects are found by searching: something is listed, but no deal or
    /// project (owner, 2026-10-17).
    /// </summary>
    public bool OffersSearchHint => items.Count > 0 && !items.Keys.Any(k => CanBeSuperior(k.Kind));

    /// <summary>The Private check box (off for a new save, docs/eway.md §5 Q4; the record's when editing).</summary>
    public bool Private { get; set; }

    /// <summary>The superior item; null when there is none.</summary>
    public CrmItemRef? Superior => superior is { } s ? new CrmItemRef { Kind = s.Kind, ItemGuid = s.Guid } : null;

    /// <summary>crm.CanBeSuperior: a kind that can be a saved message's superior item (eWay-CRM's OUTLOOKPROJECT): a deal or a project.</summary>
    public static bool CanBeSuperior(CrmItemKind k) => k.Value is CrmItemKind.Deal or CrmItemKind.Project;

    // CanBeSuperior of a kind's wire string.
    private static bool CanBeSuperior(string k) => k is CrmItemKind.Deal or CrmItemKind.Project;

    /// <summary>An item's check box.</summary>
    public bool Checked(CrmItemRef r)
    {
        ArgumentNullException.ThrowIfNull(r);
        return RefOf(r.Kind, r.ItemGuid) is { } k && checkedItems.Contains(k);
    }

    /// <summary>
    /// The listed items by kind in <see cref="Kinds"/>' order, each in the
    /// order it was listed (the record's, the daemon's ranking, then search
    /// results); a kind without items is left out.
    /// </summary>
    public IReadOnlyList<CrmDialogGroup> Groups()
    {
        var output = new List<CrmDialogGroup>();
        foreach (var kind in Kinds)
        {
            if (!order.TryGetValue(kind.Value, out var refs) || refs.Count == 0)
            {
                continue;
            }
            output.Add(new CrmDialogGroup(
                kind,
                [.. refs.Select(r => new CrmDialogEntry(items[r], checkedItems.Contains(r), superior == r))]));
        }
        return output;
    }

    /// <summary>
    /// Toggle: sets an item's check box; whether anything changed. Checking a
    /// deal or project while none is superior makes it the superior item;
    /// unchecking the superior item leaves none. A check that would make the
    /// save change more than <see cref="API.Limits.MaxCrmItems"/> items is
    /// refused (false; the view leaves the box unchecked).
    /// </summary>
    public bool Toggle(CrmItemRef r, bool on)
    {
        ArgumentNullException.ThrowIfNull(r);
        if (RefOf(r.Kind, r.ItemGuid) is not { } k || !items.ContainsKey(k) || checkedItems.Contains(k) == on)
        {
            return false;
        }
        if (!on)
        {
            checkedItems.Remove(k);
            if (superior == k)
            {
                superior = null;
            }
            return true;
        }
        var prevSuperior = superior;
        checkedItems.Add(k);
        if (CanBeSuperior(k.Kind) && superior is null)
        {
            superior = k;
        }
        if (ChangeCount() > API.Limits.MaxCrmItems)
        {
            checkedItems.Remove(k);
            superior = prevSuperior;
            return false;
        }
        return true;
    }

    /// <summary>
    /// SetSuperior: makes a deal or project the superior item, checking it
    /// when it is not; false when it cannot be one, is not listed, or the
    /// check is refused (<see cref="Toggle"/>).
    /// </summary>
    public bool SetSuperior(CrmItemRef r)
    {
        ArgumentNullException.ThrowIfNull(r);
        if (RefOf(r.Kind, r.ItemGuid) is not { } k || !items.ContainsKey(k) || !CanBeSuperior(k.Kind) || superior == k)
        {
            return false;
        }
        if (!checkedItems.Contains(k) && !Toggle(new CrmItemRef { Kind = k.Kind, ItemGuid = k.Guid }, true))
        {
            return false;
        }
        var prev = superior;
        superior = k;
        if (ChangeCount() > API.Limits.MaxCrmItems)
        {
            superior = prev;
            return false;
        }
        return true;
    }

    /// <summary>
    /// Merge: lists the items of a crm.search answer that are not listed
    /// yet, unchecked, after the others of their kind; checks and the
    /// superior item stay. How many it added.
    /// </summary>
    public int Merge(CrmSearchResult res)
    {
        ArgumentNullException.ThrowIfNull(res);
        var n = 0;
        foreach (var it in res.Items)
        {
            if (RefOf(it.Kind, it.ItemGuid) is not { } k || items.ContainsKey(k))
            {
                continue;
            }
            Add(it with { Superior = false });
            n++;
        }
        return n;
    }

    /// <summary>Changed: over a saved message, the links or Private differ from the record. A new save is always a change.</summary>
    public bool Changed => !Editing || ChangeCount() > 0 || Private != recordedPrivate;

    /// <summary>CanSave: the Save button's state: a new save always (with no item checked the message is saved without links), an edit when it <see cref="Changed"/>.</summary>
    public bool CanSave => Changed && ChangeCount() <= API.Limits.MaxCrmItems;

    /// <summary>
    /// SaveParams: crm.save for the dialog's choice over the messages
    /// <paramref name="messageIds"/> of <paramref name="messageAccountId"/>
    /// (those of the lookup): the items to link and, when editing, to unlink,
    /// the new superior item and Private. Null when the call would break a
    /// limit of docs/api.md §4.17: 1 to <see cref="API.Limits.MaxCrmMessages"/>
    /// messages (exactly one when editing), at most
    /// <see cref="API.Limits.MaxCrmItems"/> items.
    /// </summary>
    public CrmSaveParams? SaveParams(AccountId messageAccountId, IReadOnlyList<MessageId> messageIds)
    {
        ArgumentNullException.ThrowIfNull(messageIds);
        var n = messageIds.Count;
        if (n == 0 || n > API.Limits.MaxCrmMessages || (Editing && n != 1))
        {
            return null;
        }
        var (link, unlink, newSuperior) = Diff();
        if (link.Count + unlink.Count + (newSuperior is null ? 0 : 1) > API.Limits.MaxCrmItems)
        {
            return null;
        }
        return new CrmSaveParams
        {
            AccountId = AccountId,
            MessageAccountId = messageAccountId,
            MessageIds = [.. messageIds],
            Link = link.Count > 0 ? link : null,
            Unlink = Editing && unlink.Count > 0 ? unlink : null,
            Superior = newSuperior,
            Private = Private,
        };
    }

    /// <summary>
    /// SearchParams: crm.search for the dialog's search field as typed; null
    /// while the query is shorter than <see cref="API.Limits.MinCrmQueryBytes"/>
    /// or longer than <see cref="API.Limits.MaxCrmQueryBytes"/> after trimming
    /// (the field then searches nothing and the listed items stay).
    /// </summary>
    public CrmSearchParams? SearchParams(string raw)
    {
        ArgumentNullException.ThrowIfNull(raw);
        var q = raw.Trim();
        var bytes = Encoding.UTF8.GetByteCount(q);
        if (bytes < API.Limits.MinCrmQueryBytes || bytes > API.Limits.MaxCrmQueryBytes)
        {
            return null;
        }
        return new CrmSearchParams { AccountId = AccountId, Query = q };
    }

    // Lists it unless it is listed already or invalid; its key either way
    // when valid.
    private (string Kind, string Guid)? Add(CrmItem it)
    {
        if (RefOf(it.Kind, it.ItemGuid) is not { } k)
        {
            return null;
        }
        if (!items.ContainsKey(k))
        {
            items[k] = CleanItem(it, k);
            if (!order.TryGetValue(k.Kind, out var list))
            {
                list = [];
                order[k.Kind] = list;
            }
            list.Add(k);
        }
        return k;
    }

    // What the save changes: items to link, to unlink, and the new superior
    // item (null: none added), each in the dialog's order.
    private (List<CrmItemRef> Link, List<CrmItemRef> Unlink, CrmItemRef? Superior) Diff()
    {
        var link = new List<CrmItemRef>();
        var unlink = new List<CrmItemRef>();
        foreach (var kind in Kinds)
        {
            if (!order.TryGetValue(kind.Value, out var refs))
            {
                continue;
            }
            foreach (var k in refs)
            {
                var cur = checkedItems.Contains(k);
                var was = recorded.Contains(k);
                if (cur && !was && superior != k)
                {
                    link.Add(Ref(k));
                }
                else if (!cur && was)
                {
                    unlink.Add(Ref(k));
                }
            }
        }
        CrmItemRef? newSuperior = null;
        if (superior is { } s && superior != recordedSuperior)
        {
            newSuperior = Ref(s);
            // The superior item it replaces stays as a plain link when it
            // stays checked (eWay-CRM keeps one superior item per message).
            if (recordedSuperior is { } old && checkedItems.Contains(old))
            {
                link.Add(Ref(old));
            }
        }
        return (link, unlink, newSuperior);
    }

    // How many items the save names (api.MaxCrmItems).
    private int ChangeCount()
    {
        var (link, unlink, s) = Diff();
        return link.Count + unlink.Count + (s is null ? 0 : 1);
    }

    private static CrmItemRef Ref((string Kind, string Guid) k) => new() { Kind = k.Kind, ItemGuid = k.Guid };

    // refOf: an item's key: its kind and its GUID in lower case; null for an
    // unknown kind or a GUID of another shape (such an item is left out: it
    // could not be saved).
    internal static (string Kind, string Guid)? RefOf(CrmItemKind kind, string? guid)
    {
        var g = (guid ?? "").Trim().ToLowerInvariant();
        if (!Kinds.Contains(kind) || !GuidShape().IsMatch(g))
        {
            return null;
        }
        return (kind.Value, g);
    }

    // cleanItem: it with its display texts cleaned and capped again and its
    // URL dropped unless it is https.
    private static CrmItem CleanItem(CrmItem it, (string Kind, string Guid) k) => new()
    {
        Kind = k.Kind,
        ItemGuid = k.Guid,
        Name = CrmRules.CapBytes(Jira.Clean(it.Name), API.Limits.MaxCrmNameBytes),
        Detail = CrmRules.CapBytes(Jira.Clean(it.Detail), API.Limits.MaxCrmDetailBytes),
        Reason = it.Reason,
        Superior = it.Superior,
        Url = CrmRules.OpenUrl(it.Url),
    };

    // guidShape: the shape of an eWay-CRM GUID (the daemon's eway.ValidGUID),
    // matched against the lower-cased text.
    [GeneratedRegex(@"^[0-9a-f]{8}-([0-9a-f]{4}-){3}[0-9a-f]{12}\z", RegexOptions.CultureInvariant)]
    private static partial Regex GuidShape();
}
