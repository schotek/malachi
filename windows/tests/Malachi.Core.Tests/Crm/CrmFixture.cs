// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the fixtures of ui/internal/crm/crm_test.go (imapAcc, graphAcc,
// jiraAcc, ewayOff, ewayA, ewayB) and dialog_test.go (guid, item, ref,
// show, lookup, savedLookup); macOS: the crm* helpers of
// macos/Tests/MalachiCoreTests/CRMTests.swift. Fictional accounts and
// items only.

using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Crm;

namespace Malachi.Core.Tests.Crm;

/// <summary>The accounts and items of the CRM tests.</summary>
internal static class CrmFixture
{
    public static readonly Account Imap = Acc("m1", null, email: "jana@acme.example");
    public static readonly Account Graph = Acc("g1", AccountKind.Graph);
    public static readonly Account JiraAcc = Acc("j1", AccountKind.Jira);
    public static readonly Account EwayOff = Acc("e0", AccountKind.Eway, enabled: false, eway: new EwayConfig { ServiceUrl = "https://hosting.eway-crm.com/old", Username = "jana" });
    public static readonly Account EwayA = Acc("e1", AccountKind.Eway, eway: new EwayConfig { ServiceUrl = "https://hosting.eway-crm.com/acme", Username = "jana" });
    public static readonly Account EwayB = Acc("e2", AccountKind.Eway, eway: new EwayConfig { ServiceUrl = "https://crm.example.org", Username = "jana" });
    public static readonly Account CalDav = Acc("c1", AccountKind.CalDAV);

    public static Account Acc(string id, string? kind, bool enabled = true, string email = "", EwayConfig? eway = null) => new()
    {
        Id = id,
        Enabled = enabled,
        Config = new AccountConfig { Name = "", Email = email, Kind = kind is null ? null : new AccountKind(kind), Eway = eway },
        State = new SyncState { AccountId = id, Status = SyncStatus.Idle },
    };

    // dialog_test.go guid: a GUID of the right shape for item n.
    public static string Guid(int n) => $"00000000-0000-4000-8000-{n:D12}";

    public static CrmItem Item(string kind, int n, string name, string? reason) => new()
    {
        Kind = kind,
        ItemGuid = Guid(n),
        Name = name,
        Reason = reason is null ? null : new CrmReason(reason),
        Url = "https://open.eway-crm.com/?l=" + Guid(n),
    };

    public static CrmItemRef Ref(string kind, int n) => new() { Kind = kind, ItemGuid = Guid(n) };

    // dialog_test.go show: the groups as "kind: name[x][*] …".
    public static string Show(CrmDialog d) =>
        string.Join(" | ", d.Groups().Select(g =>
            g.Kind.Value + ": " + string.Join(" ", g.Entries.Select(e => e.Item.Name + (e.Checked ? "x" : "") + (e.Superior ? "*" : "")))));

    // dialog_test.go lookup: one message that is not saved.
    public static CrmLookupResult Lookup() => new()
    {
        AccountId = "e1",
        Saved = [new CrmSavedMessage { MessageId = "m_1" }],
        Suggestions =
        [
            Item(CrmItemKind.Deal, 5, "Fleet renewal", CrmReason.Contact),
            Item(CrmItemKind.Contact, 1, "Novák, Jan", CrmReason.Address),
            Item(CrmItemKind.Company, 2, "Acme", CrmReason.Domain),
            Item(CrmItemKind.Company, 3, "Acme Holding", CrmReason.Contact),
            Item(CrmItemKind.Project, 6, "Rollout", CrmReason.Company),
            // Invalid: an unknown kind, a GUID of another shape, a repeat.
            new CrmItem { Kind = "lead", ItemGuid = Guid(9), Name = "x" },
            new CrmItem { Kind = CrmItemKind.Contact, ItemGuid = "not-a-guid", Name = "y" },
            Item(CrmItemKind.Contact, 1, "Novák again", CrmReason.Address),
        ],
    };

    // dialog_test.go savedLookup: one saved message.
    public static CrmLookupResult SavedLookup()
    {
        var res = Lookup();
        return res with
        {
            Saved =
            [
                new CrmSavedMessage
                {
                    MessageId = "m_1",
                    Record = new CrmRecord
                    {
                        AccountId = "e1",
                        ItemGuid = Guid(100),
                        Private = true,
                        Url = "https://open.eway-crm.com/?l=e",
                        SavedAt = System.DateTimeOffset.UnixEpoch,
                        CheckedAt = System.DateTimeOffset.UnixEpoch,
                        Items =
                        [
                            Item(CrmItemKind.Contact, 1, "Novák, Jan", CrmReason.Saved),
                            Item(CrmItemKind.Company, 3, "Acme Holding", CrmReason.Saved),
                            new CrmItem { Kind = CrmItemKind.Deal, ItemGuid = Guid(5), Name = "Fleet renewal", Reason = new CrmReason(CrmReason.Saved), Superior = true },
                        ],
                    },
                },
            ],
        };
    }
}
