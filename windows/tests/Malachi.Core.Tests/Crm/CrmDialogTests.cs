// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CRMDialogTests.swift, the
// counterpart of ui/internal/crm/dialog_test.go (TestDialogPrefill,
// TestDialogSuperior, TestDialogSaveNew, TestDialogEdit, TestDialogMerge,
// TestDialogCleansItems, TestDialogLimit, TestSearchHint). The params are
// compared as their JSON (ApiJson.AssertSameValue), as Go compares them
// with reflect.DeepEqual. Fictional people only.

using System;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.Crm;
using Malachi.Core.Tests.Api;
using Xunit;
using static Malachi.Core.Tests.Crm.CrmFixture;

namespace Malachi.Core.Tests.Crm;

public sealed class CrmDialogTests
{
    [Fact]
    public void Prefill()
    {
        var d = new CrmDialog(Lookup());
        Assert.False(d.Editing || d.IsEmpty || d.Partial || d.Private);
        Assert.Equal("e1", d.AccountId.Value);
        // Contacts and companies by address or domain checked; the rest
        // not; no deal flagged Superior, so none checked.
        Assert.Equal("contact: Novák, Janx | company: Acmex Acme Holding | deal: Fleet renewal | project: Rollout", Show(d));
        Assert.Null(d.Superior);
        var groups = d.Groups();
        Assert.NotEqual("", groups[0].Entries[0].Item.Url);
        Assert.False(groups[0].Entries[0].CanBeSuperior);
        Assert.True(groups[2].Entries[0].CanBeSuperior);

        // Exactly one open deal: the daemon flags it, the dialog checks it as superior.
        var res = Lookup();
        res = res with
        {
            Suggestions = [res.Suggestions[0] with { Superior = true }, .. res.Suggestions.Skip(1)],
            Partial = true,
        };
        d = new CrmDialog(res);
        Assert.Contains("deal: Fleet renewalx*", Show(d), StringComparison.Ordinal);
        Assert.True(d.Partial);
        Assert.True(new CrmDialog(new CrmLookupResult { AccountId = "e1" }).IsEmpty);
    }

    [Fact]
    public void Superior()
    {
        var d = new CrmDialog(Lookup());
        // The first deal or project checked becomes superior, another a link.
        Assert.True(d.Toggle(Ref(CrmItemKind.Project, 6), true));
        Assert.True(d.Toggle(Ref(CrmItemKind.Deal, 5), true));
        Assert.Contains("deal: Fleet renewalx |", Show(d), StringComparison.Ordinal);
        Assert.Contains("project: Rolloutx*", Show(d), StringComparison.Ordinal);
        Assert.False(d.Toggle(Ref(CrmItemKind.Deal, 5), true), "toggle to the same state changed something");
        // The marker moves.
        Assert.True(d.SetSuperior(Ref(CrmItemKind.Deal, 5)));
        Assert.Contains("deal: Fleet renewalx*", Show(d), StringComparison.Ordinal);
        Assert.Contains("project: Rolloutx", Show(d), StringComparison.Ordinal);
        Assert.DoesNotContain("Rolloutx*", Show(d), StringComparison.Ordinal);
        // Unchecking the superior leaves none; the next one checked takes it.
        d.Toggle(Ref(CrmItemKind.Deal, 5), false);
        Assert.Null(d.Superior);
        d.Toggle(Ref(CrmItemKind.Project, 6), false);
        d.Toggle(Ref(CrmItemKind.Project, 6), true);
        Assert.Equal(Ref(CrmItemKind.Project, 6), d.Superior);
        // SetSuperior checks an unchecked one; contacts cannot be superior;
        // unknown items are ignored.
        d.Toggle(Ref(CrmItemKind.Deal, 5), false);
        Assert.True(d.SetSuperior(Ref(CrmItemKind.Deal, 5)));
        Assert.True(d.Checked(Ref(CrmItemKind.Deal, 5)));
        Assert.False(d.SetSuperior(Ref(CrmItemKind.Contact, 1)));
        Assert.False(d.SetSuperior(Ref(CrmItemKind.Deal, 77)));
        Assert.False(d.Toggle(Ref(CrmItemKind.Deal, 77), true));
        // Upper-case GUIDs name the same item.
        Assert.True(d.Checked(new CrmItemRef { Kind = CrmItemKind.Deal, ItemGuid = Guid(5).ToUpperInvariant() }));
    }

    [Fact]
    public void SaveNew()
    {
        var d = new CrmDialog(Lookup());
        d.Toggle(Ref(CrmItemKind.Project, 6), true);
        d.Toggle(Ref(CrmItemKind.Deal, 5), true);
        d.Private = true;
        Assert.True(d.CanSave && d.Changed, "a new save can always go");
        var p = d.SaveParams("m1", ["m_1", "m_2"]);
        Assert.NotNull(p);
        ApiJson.AssertSameValue(
            new CrmSaveParams
            {
                AccountId = "e1",
                MessageAccountId = "m1",
                MessageIds = ["m_1", "m_2"],
                Link = [Ref(CrmItemKind.Contact, 1), Ref(CrmItemKind.Company, 2), Ref(CrmItemKind.Deal, 5)],
                Superior = Ref(CrmItemKind.Project, 6),
                Private = true,
            },
            p);
        // Nothing checked: the message is saved without links.
        var empty = new CrmDialog(new CrmLookupResult { AccountId = "e1", Saved = [new CrmSavedMessage { MessageId = "m_1" }] });
        var e = empty.SaveParams("m1", ["m_1"]);
        Assert.True(e is { Link: null, Superior: null } && empty.CanSave);
        Assert.Null(d.SaveParams("m1", []));
        Assert.Null(d.SaveParams("m1", [.. Enumerable.Range(0, API.Limits.MaxCrmMessages + 1).Select(i => new MessageId("m_" + i))]));
    }

    [Fact]
    public void Edit()
    {
        var d = new CrmDialog(SavedLookup());
        Assert.True(d.Editing && d.Private);
        // The record's items checked, the suggestions after them unchecked.
        Assert.Equal("contact: Novák, Janx | company: Acme Holdingx Acme | deal: Fleet renewalx* | project: Rollout", Show(d));
        Assert.False(d.Changed || d.CanSave, "nothing changed yet");
        d.Private = false;
        Assert.True(d.CanSave, "privacy changed");
        var p = d.SaveParams("m1", ["m_1"]);
        Assert.True(p is { Link: null, Unlink: null, Superior: null, Private: null }, "only privacy");
        d.Private = true;

        // Uncheck a company, check another, make the project superior while
        // the old deal stays checked (as a plain link).
        d.Toggle(Ref(CrmItemKind.Company, 3), false);
        d.Toggle(Ref(CrmItemKind.Company, 2), true);
        d.SetSuperior(Ref(CrmItemKind.Project, 6));
        p = d.SaveParams("m1", ["m_1"]);
        Assert.NotNull(p);
        ApiJson.AssertSameValue(
            new CrmSaveParams
            {
                AccountId = "e1",
                MessageAccountId = "m1",
                MessageIds = ["m_1"],
                Link = [Ref(CrmItemKind.Company, 2), Ref(CrmItemKind.Deal, 5)],
                Unlink = [Ref(CrmItemKind.Company, 3)],
                Superior = Ref(CrmItemKind.Project, 6),
                Private = true,
            },
            p);
        // Unchecking the recorded superior unlinks it.
        d = new CrmDialog(SavedLookup());
        d.Toggle(Ref(CrmItemKind.Deal, 5), false);
        p = d.SaveParams("m1", ["m_1"]);
        Assert.NotNull(p);
        Assert.Equal([Ref(CrmItemKind.Deal, 5)], p.Unlink!);
        Assert.Null(p.Superior);
        Assert.Null(p.Link);
        // Unchecked and checked again: no change.
        d.Toggle(Ref(CrmItemKind.Deal, 5), true);
        Assert.False(d.Changed, Show(d));
        // Editing saves exactly one message.
        Assert.Null(d.SaveParams("m1", ["m_1", "m_2"]));
    }

    [Fact]
    public void Merge()
    {
        var d = new CrmDialog(Lookup());
        d.Toggle(Ref(CrmItemKind.Deal, 5), true);
        var n = d.Merge(new CrmSearchResult
        {
            Items =
            [
                Item(CrmItemKind.Deal, 5, "Fleet renewal (search)", CrmReason.Search),
                new CrmItem { Kind = CrmItemKind.Project, ItemGuid = Guid(7), Name = "Pilot", Reason = new CrmReason(CrmReason.Search), Superior = true },
                Item(CrmItemKind.Contact, 8, "Dvořák, Petr", CrmReason.Search),
                new CrmItem { Kind = CrmItemKind.Contact, ItemGuid = "bad", Name = "z" },
            ],
        });
        Assert.Equal(2, n);
        Assert.Equal("contact: Novák, Janx Dvořák, Petr | company: Acmex Acme Holding | deal: Fleet renewalx* | project: Rollout Pilot", Show(d));
        var sp = d.SearchParams("  ac  ");
        Assert.True(sp is { Query: "ac" } && sp.AccountId?.Value == "e1");
        foreach (var q in new[] { "", " a ", new string('x', API.Limits.MaxCrmQueryBytes + 1) })
        {
            Assert.Null(d.SearchParams(q));
        }
    }

    // Hostile names are cleaned and capped; a URL that is not https dropped.
    [Fact]
    public void CleansItems()
    {
        var res = new CrmLookupResult
        {
            AccountId = "e1",
            Suggestions =
            [
                new CrmItem
                {
                    Kind = CrmItemKind.Contact,
                    ItemGuid = Guid(1).ToUpperInvariant(),
                    Reason = new CrmReason(CrmReason.Address),
                    Name = "Eve‮\u0000 \n Evil" + new string('ž', 400),
                    Detail = "a\tb",
                    Url = "javascript:alert(1)",
                },
            ],
        };
        var e = new CrmDialog(res).Groups()[0].Entries[0];
        var bytes = Encoding.UTF8.GetByteCount(e.Item.Name);
        Assert.True(e.Item.Name.StartsWith("Eve Evil", StringComparison.Ordinal) && bytes <= API.Limits.MaxCrmNameBytes && e.Item.Name.EndsWith('…'), $"{e.Item.Name} ({bytes} bytes)");
        Assert.Equal("a b", e.Item.Detail);
        Assert.Equal("", e.Item.Url);
        Assert.Equal(Guid(1), e.Item.ItemGuid);
    }

    // A save names at most api.MaxCrmItems items: the check over it is refused.
    [Fact]
    public void Limit()
    {
        var res = new CrmLookupResult
        {
            AccountId = "e1",
            Saved = [new CrmSavedMessage { MessageId = "m_1" }],
            Suggestions = [.. Enumerable.Range(0, API.Limits.MaxCrmItems + 1).Select(i => Item(CrmItemKind.Contact, i, "c" + i, CrmReason.Contact))],
        };
        var d = new CrmDialog(res);
        for (var i = 0; i < API.Limits.MaxCrmItems; i++)
        {
            Assert.True(d.Toggle(Ref(CrmItemKind.Contact, i), true), $"check {i}");
        }
        Assert.False(d.Toggle(Ref(CrmItemKind.Contact, API.Limits.MaxCrmItems), true));
        Assert.False(d.Checked(Ref(CrmItemKind.Contact, API.Limits.MaxCrmItems)));
        var p = d.SaveParams("m1", ["m_1"]);
        Assert.Equal(API.Limits.MaxCrmItems, p?.Link?.Count);
        Assert.True(d.CanSave);
    }

    // The quiet line under the groups: shown with contacts or companies
    // listed but no deal or project, gone once a search adds one, never on
    // an empty dialog.
    [Fact]
    public void SearchHint()
    {
        var res = Lookup();
        res = res with { Suggestions = [.. res.Suggestions.Where(it => !CrmDialog.CanBeSuperior(it.Kind))] };
        var d = new CrmDialog(res);
        Assert.True(d.OffersSearchHint, "contacts and companies only: hint expected");
        d.Merge(new CrmSearchResult { Items = [Item(CrmItemKind.Deal, 9, "Found deal", CrmReason.Search)] });
        Assert.False(d.OffersSearchHint, "a deal listed: no hint");
        Assert.False(new CrmDialog(new CrmLookupResult { AccountId = "" }).OffersSearchHint, "empty dialog: no hint, the empty text speaks");
        Assert.False(new CrmDialog(Lookup()).OffersSearchHint, "deals in the lookup: no hint");
    }
}
