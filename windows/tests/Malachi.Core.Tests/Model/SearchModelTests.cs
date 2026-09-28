// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/SearchModelTests.swift, the
// counterpart of ui/internal/window/search_model_test.go and
// ui/internal/widget/highlight_test.go: the search side of the list model.
// Two Go cases the Swift suite did not port are here: a scope outside the
// known ones reads as All (TestSearchRequest's "" scope; a C# enum can hold
// any number, a Swift enum cannot), and a range that ends at the end of the
// text (TestValidRanges' "end of text"). The catalogue is English in tests,
// so the msgids come back verbatim.

using System;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Malachi.Core.Settings;
using Xunit;
using static Malachi.Core.Tests.Model.MailModelTests;
using FolderMap = System.Collections.Generic.Dictionary<Malachi.Core.Api.AccountId, System.Collections.Generic.IReadOnlyList<Malachi.Core.Api.Folder>>;

namespace Malachi.Core.Tests.Model;

public sealed class SearchModelTests
{
    [Fact]
    public void Ready()
    {
        (string Text, bool Want)[] cases = [("", false), ("a", false), ("  a  ", false), ("ab", true), ("př", true), ("ř", false)];
        foreach (var (text, want) in cases)
        {
            Assert.True(SearchModel.SearchReady(text) == want, text);
        }
    }

    [Fact]
    public void Request()
    {
        var m = NewSearchModel();
        var page = new Page { Limit = API.Limits.DefaultPageLimit };
        (SearchScope Scope, SearchQueryParams Want, SearchScope Effective)[] cases =
        [
            (SearchScope.Folder, new() { AccountId = "a1", FolderId = "f_in", Query = "faktura", Page = page }, SearchScope.Folder),
            (SearchScope.Account, new() { AccountId = "a1", Query = "faktura", Page = page }, SearchScope.Account),
            (SearchScope.All, new() { Query = "faktura", Page = page }, SearchScope.All),
            // Go's "" scope: anything else is All.
            ((SearchScope)(-1), new() { Query = "faktura", Page = page }, SearchScope.All),
        ];
        foreach (var (scope, want, effective) in cases)
        {
            var got = m.SearchRequest("  faktura ", scope);
            Assert.True(got.Params == want && got.Effective == effective, $"{scope}");
        }
        // Nothing selected: nothing to narrow to.
        m.Selected = null;
        var none = m.SearchRequest("x", SearchScope.Folder);
        Assert.True(none.Params.AccountId is null && none.Params.FolderId is null && none.Effective == SearchScope.All);
    }

    [Fact]
    public void ResultsAndRows()
    {
        var m = NewSearchModel();
        MatchRange[] hit = [new() { Start = 3, End = 8 }];
        m.Search.Active = true;
        m.Search.Effective = SearchScope.All;
        m.SetSearchResults(new SearchQueryResult
        {
            Results =
            [
                new() { Message = Summary("m1", "a1", "f_x"), Snippet = "…a přílohy", Ranges = hit, Score = 0 },
                new() { Message = Summary("m2", "a2", "g_in"), Snippet = "summary", Score = 0 },
            ],
            Page = new PageInfo { NextCursor = "c", Total = 3 },
        });
        Assert.True(m.Messages.Count == 2 && m.NextCursor == "c" && m.Total == 3);
        var added = m.AppendSearchResults(new SearchQueryResult
        {
            Results =
            [
                new() { Message = Summary("m3", "a1", "f_in"), Snippet = "x", Score = 0 },
                new() { Message = Summary("m1", "a1", "f_x"), Snippet = "", Score = 0 },
            ],
            Page = new PageInfo { Total = 3 },
        });
        Assert.True(added == 1 && m.Messages.Count == 3 && m.NextCursor is null);

        // A repeated result keeps the excerpt it came with.
        var row = m.RowMessage(m.Messages[0]);
        Assert.Equal("…a přílohy", row.Snippet);
        Assert.Equal(hit, row.Highlights);
        // Windows-only: the folder is isolated before the account (DisplayText).
        Assert.Equal("\u2068Faktury\u2069 · Work", row.Origin);
        Assert.Equal("Archiv/Faktury\nWork", row.OriginTooltip);
        Assert.Equal("\u2068Inbox\u2069 · me@home.example", m.RowMessage(m.Messages[1]).Origin);

        m.Search.Effective = SearchScope.Account;
        Assert.Equal("Faktury", m.RowMessage(m.Messages[0]).Origin);
        m.Search.Effective = SearchScope.Folder;
        Assert.True(m.RowMessage(m.Messages[0]).Origin == "" && m.RowMessage(m.Messages[0]).OriginTooltip == "");
        // One enabled account: no account in the label.
        m.Search.Effective = SearchScope.All;
        m.Accounts = Replace(m.Accounts, 1, a => a with { Enabled = false });
        Assert.Equal("Faktury", m.RowMessage(m.Messages[0]).Origin);

        // Outside search a row is the plain summary.
        m.Search.Active = false;
        var plain = m.RowMessage(m.Messages[0]);
        Assert.True(plain.Snippet == "summary" && plain.Highlights.Count == 0 && plain.Origin.Length == 0);
        m.ClearSearchResults();
        Assert.True(m.Messages.Count == 0 && m.Search.Hits.Count == 0);
    }

    [Fact]
    public void Bar()
    {
        var m = NewSearchModel();
        m.Search.Scope = SearchScope.Account;
        var bar = m.SearchBar();
        Assert.True(bar.Scope == SearchScope.Account && bar.NarrowEnabled);
        // Windows-only: the folder is isolated in the sentence (DisplayText).
        Assert.Equal("Search in \u2068Inbox\u2069", bar.FolderTooltip);
        Assert.Equal("Search every folder of Work except Trash and Junk", bar.AccountTooltip);
        m.Selected = null;
        bar = m.SearchBar();
        Assert.True(!bar.NarrowEnabled && bar.FolderTooltip == "Select a folder to search in it" && bar.AccountTooltip.Length == 0);
    }

    [Fact]
    public void Texts()
    {
        Assert.Equal("Searches the mail of the last 90 days stored on this computer.", SearchModel.SearchRetentionText(90, known: true));
        Assert.Equal("Searches the mail of the last 1 day stored on this computer.", SearchModel.SearchRetentionText(1, known: true));
        Assert.Equal("Searches all mail stored on this computer.", SearchModel.SearchRetentionText(0, known: true));
        Assert.Equal("Searches the mail stored on this computer.", SearchModel.SearchRetentionText(0, known: false));
        Assert.Equal("3 results", SearchModel.SearchTotalText(3, shown: true));
        Assert.Equal("More than 1000 results", SearchModel.SearchTotalText(-1, shown: true));
        Assert.Equal("", SearchModel.SearchTotalText(3, shown: false));
        Assert.NotEqual(SearchModel.SearchEmptyText(SearchScope.Folder), SearchModel.SearchEmptyText(SearchScope.All));
    }

    [Fact]
    public void Highlights()
    {
        const string text = "posílám přílohy k faktuře"; // "í" and "ř" are two bytes each
        static MatchRange R(int s, int e) => new() { Start = s, End = e };
        (string Name, MatchRange[] Ranges, string[] Want)[] cases =
        [
            ("good", [R(10, 19)], ["přílohy"]),
            ("sorted", [R(22, 30), R(10, 19)], ["přílohy", "faktuře"]),
            ("end of text", [R(22, Encoding.UTF8.GetByteCount(text))], ["faktuře"]),
            ("overlap dropped", [R(10, 19), R(11, 20)], ["přílohy"]),
            ("mid-character start", [R(4, 8)], []),
            ("mid-character end", [R(0, 4)], []),
            ("outside", [R(-1, 3), R(10, 99), R(5, 5), R(8, 6)], []),
        ];
        foreach (var (name, ranges, want) in cases)
        {
            var got = SearchModel.HighlightRanges(text, ranges).Select(r => text.Substring(r.Start, r.Length)).ToArray();
            Assert.True(got.SequenceEqual(want), $"{name}: [{string.Join(", ", got)}]");
        }
        MatchRange[] many = [.. Enumerable.Range(0, 50).Select(i => R(i, i + 1))];
        Assert.Equal(SearchModel.MaxHighlights, SearchModel.HighlightRanges(new string('x', 60), many).Count);
    }

    // Windows-only (DisplayText, docs/security.md §4): a result's origin
    // shows the server's name and path of its folder cleaned, the name
    // isolated before the account.
    [Fact]
    public void TheOriginOfAResultIsCleaned()
    {
        var m = NewSearchModel();
        m.Folders["a1"] = [TestFolder("f_in", "INBOX", FolderRole.Inbox, name: "INBOX"), TestFolder("f_x", "Archiv/\u202Egnp.exe\u0007", name: "\u202Egnp.exe")];
        m.Search.Active = true;
        m.Search.Effective = SearchScope.All;
        m.SetSearchResults(new SearchQueryResult
        {
            Results = [new() { Message = Summary("m1", "a1", "f_x"), Snippet = "x", Score = 0 }],
            Page = new PageInfo { Total = 1 },
        });
        var row = m.RowMessage(m.Messages[0]);
        Assert.Equal("\u2068gnp.exe\u2069 · Work", row.Origin);
        Assert.Equal("Archiv/gnp.exe\nWork", row.OriginTooltip);
    }

    private static MailModel NewSearchModel()
    {
        var m = new MailModel(
            [
                TestAccount("a1", name: "Work", email: "me@work.example"),
                TestAccount("a2", email: "me@home.example"),
            ],
            new FolderMap
            {
                ["a1"] =
                [
                    TestFolder("f_in", "INBOX", FolderRole.Inbox, name: "INBOX"),
                    TestFolder("f_x", "Archiv/Faktury", name: "Faktury"),
                ],
                ["a2"] = [TestFolder("g_in", "INBOX", FolderRole.Inbox, name: "INBOX")],
            })
        {
            Selected = new FolderKey("a1", "f_in"),
        };
        return m;
    }

    private static MessageSummary Summary(string id, string acc, string folder) => new()
    {
        Id = id,
        AccountId = acc,
        FolderId = folder,
        ThreadId = null,
        From = [new Address { Name = "Alice", Email = "alice@example.invalid" }],
        Subject = id,
        Date = DateTimeOffset.FromUnixTimeSeconds(0),
        Snippet = "summary",
        Flags = [Flag.Seen],
        HasAttachments = false,
        Size = 0,
    };
}
