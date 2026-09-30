// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/JiraAccountControllerTests.swift and
// ui/internal/jiraaccount/controller_test.go (which ports it and adds
// TestHostileListingIsCleaned): the settings of a Jira account against a
// fake daemon, account.listSpaces with the stored token and
// account.update with empty credentials. Fictional sites and people only.
//
// Swift checks the state right after a call starts, before its reply can
// arrive; here those checks run in the same turn of the UI thread as the
// call, and an answer that Swift delays is held (HeldAnswer).

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Tests.Api;
using Malachi.Core.Tests.IssueTrackers;
using Malachi.Core.Text;
using Xunit;
using static Malachi.Core.Tests.Controllers.DaemonHarness;

namespace Malachi.Core.Tests.Controllers;

public sealed class JiraAccountControllerTests
{
    private const string ListingJson =
        """{"user":{"name":"Jana Dvořáková","email":"jana@acme.example"},"spaces":["""
        + """{"id":"10001","key":"ITSD","name":"IT Service Desk","serviceDesk":true,"issues":-1},"""
        + """{"id":"10003","key":"MOB","name":"Mobile","issues":-1},"""
        + """{"id":"10002","key":"WEB","name":"Website","issues":-1}],"""
        + """ "statuses":[{"id":"1","name":"Open","category":"todo"},"""
        + """{"id":"3","name":"In Progress","category":"inProgress"},"""
        + """{"id":"5","name":"Resolved","category":"done"},"""
        + """{"id":"6","name":"Done","category":"done"},"""
        + """{"id":"10010","name":"Done","category":"done"}]}""";

    [Fact]
    public async Task LoadsTheSpacesAndStatusesWithTheStoredToken()
    {
        await using var h = await ListingHarnessAsync();
        var account = StoredAccount();
        var (c, rec) = await MakeAsync(h, account);

        // Before the listing: what is stored.
        Assert.Equal("Jira Account", c.Title);
        Assert.Equal("Save", JiraAccountController.SaveLabel);
        Assert.Equal(
            new JiraSiteInfo { Address = "https://acme.atlassian.net", Deployment = "Jira Cloud", User = "jana@acme.example", TokenLabel = "API Token" },
            c.Site);
        Assert.Equal(["10001", "10002"], c.SpaceRows.Select(r => r.Id));
        Assert.Equal(["10001", "10002"], c.SelectedSpaces.Order(StringComparer.Ordinal));
        Assert.Empty(c.StatusGroups);
        Assert.Equal(["1 week", "1 month", "3 months", "1 year"], JiraAccountController.OfflineLabels);
        Assert.Equal(1, c.OfflineIndex);
        Assert.True(c.NotificationIndex == 0 && c.NotificationHint.Length == 0 && c.SendersEditable);
        Assert.Equal("@acme.atlassian.net", c.SendersPlaceholder);
        Assert.All(Jira.VirtualFolders, v => Assert.True(c.FolderShown(v)));
        Assert.True(!c.IsChanged && c.CanSave && !c.Busy);

        await h.Ui.RunAsync(() =>
        {
            c.Start();
            Assert.True(c.Busy && !c.Saving);
            Assert.Equal(["Loading the spaces"], rec.Busy);
        });
        await h.IdleAsync();
        Assert.NotNull(c.Listing);
        Assert.Equal(["Loading the spaces", null], rec.Busy);
        Assert.Empty(rec.Banners);
        Assert.Equal(2, rec.Changes); // the initial state and the listing

        var sent = h.Params.Last<AccountListSpacesParams>(API.AccountListSpaces.Name)!;
        Assert.Equal("acc-j1", sent.AccountId?.Value);
        ApiJson.AssertSameValue(account.Config, sent.Config);
        Assert.Null(sent.Credentials.Password); // no token: the daemon takes the stored one
        Assert.NotEqual(true, sent.Counts);
        Assert.Empty(ApiJson.Keys(ApiJson.Parse(h.Params.LastRaw(API.AccountListSpaces.Name)!).GetProperty("credentials")));

        Assert.Equal(("Jana Dvořáková", "jana@acme.example"), (c.Site.User, c.Site.UserDetail));
        Assert.Equal(
            [
                new JiraSpaceRow { Id = "10001", Title = "ITSD – IT Service Desk", ServiceDesk = true },
                new JiraSpaceRow { Id = "10003", Title = "MOB – Mobile" },
                new JiraSpaceRow { Id = "10002", Title = "WEB – Website" },
            ],
            c.SpaceRows);
        Assert.Equal(["10001", "10002"], c.SelectedSpaces.Order(StringComparer.Ordinal));
        Assert.Equal(["To Do", "In Progress", "Done"], c.StatusGroups.Select(g => g.Title));
        Assert.Equal("Done[status-done]: Resolved*(5) Done*(6,10010)", JiraSettingsTests.GroupsText([c.StatusGroups[^1]]));
        Assert.False(c.IsChanged);
        Assert.Equal([API.AccountListSpaces.Name], h.Fake.Calls);
    }

    [Fact]
    public async Task AFailedListingStillEditsWhatIsStored()
    {
        await using var h = await DaemonHarness.StartAsync();
        h.On(API.AccountListSpaces.Name, Fails(ErrorCode.AuthFailed, "401"));
        h.On(API.AccountUpdate.Name, _ => "{}");
        var account = StoredAccount(jc => jc with { ClosedStatuses = [new StatusRef { Id = "5", Name = "Resolved" }] });
        var (c, rec) = await MakeAsync(h, account);
        await h.Ui.RunAsync(c.Start);
        await h.IdleAsync();
        Assert.Equal(2, rec.Busy.Count);
        Assert.Equal(["The Jira site rejected the token"], rec.Banners);
        Assert.Equal("The Jira site rejected the token", c.Banner);
        Assert.Null(c.Listing);
        // The stored spaces and statuses are what there is to choose from.
        Assert.Equal(["ITSD – IT Service Desk", "WEB – Website"], c.SpaceRows.Select(r => r.Title));
        Assert.Equal("Other[]: Resolved*(5)", JiraSettingsTests.GroupsText(c.StatusGroups));
        Assert.Equal("", c.StatusGroups[0].Category.Value);
        Assert.Equal("jana@acme.example", c.Site.User);

        // Editing keeps the banner of the listing; Save stores the change.
        await h.Ui.RunAsync(() =>
        {
            c.SetSpace("10002", false);
            c.SetOnlyMine(true);
        });
        Assert.Single(rec.Banners);
        Assert.True(c.IsChanged && c.CanSave);
        await h.Ui.RunAsync(c.Save);
        await h.IdleAsync();
        Assert.Single(rec.Done);
        var sent = h.Params.Last<AccountUpdateParams>(API.AccountUpdate.Name)!;
        ApiJson.AssertSameValue<IReadOnlyList<SpaceRef>>([new SpaceRef { Id = "10001", Key = "ITSD", Name = "IT Service Desk" }], sent.Config.Jira!.Spaces);
        Assert.True(sent.Config.Jira.OnlyMine);
        ApiJson.AssertSameValue<IReadOnlyList<StatusRef>?>([new StatusRef { Id = "5", Name = "Resolved" }], sent.Config.Jira.ClosedStatuses);

        // Other failures: the client's sentence for the step.
        h.On(API.AccountListSpaces.Name, Fails(ErrorCode.NetworkError, "no route"));
        var (c2, rec2) = await MakeAsync(h, account);
        await h.Ui.RunAsync(c2.Start);
        await h.IdleAsync();
        Assert.Equal([RpcErrorText.Text("Loading the spaces", DaemonHarness.Daemon(ErrorCode.NetworkError, "no route"))], rec2.Banners);
        h.On(API.AccountListSpaces.Name, Fails(ErrorCode.AuthRequired, "no token"));
        var (c3, rec3) = await MakeAsync(h, account);
        await h.Ui.RunAsync(c3.Start);
        await h.IdleAsync();
        Assert.Equal(["Enter the API token for this account"], rec3.Banners);
    }

    [Fact]
    public async Task SavesTheFormAndKeepsTheToken()
    {
        await using var h = await ListingHarnessAsync();
        var account = StoredAccount();
        var (c, rec) = await MakeAsync(h, account);
        await StartListedAsync(h, c);

        await h.Ui.RunAsync(() =>
        {
            c.SetName("  Acme Jira ");
            c.SetSpace("10003", true);
            c.SetSpace("10001", false);
            c.SetSpace("99999", true);
            Assert.Equal(["10002", "10003"], c.SelectedSpaces.Order(StringComparer.Ordinal)); // an unknown space is ignored
            c.SetOfflineIndex(2);
            c.SetOfflineIndex(17);
            Assert.Equal(2, c.OfflineIndex);
            c.SetOnlyMine(true);
            c.SetShowEvents(false);
            c.SetFolder(VirtualFolder.Watching, false);
            c.SetFolder("archive", false);
            Assert.True(!c.FolderShown(VirtualFolder.Watching) && c.FolderShown(VirtualFolder.Open));
            var resolved = c.StatusGroups[^1].Choices[0];
            c.SetStatus(resolved, false);
            Assert.Equal([false, true], c.StatusGroups[^1].Choices.Select(ch => ch.Selected));
            c.SetNotificationIndex(1);
            Assert.Equal("Hidden e-mails stay in your mailbox and come back when you turn this off", c.NotificationHint);
            Assert.True(c.AddEntry(JiraListKind.Senders, " Jira@Acme.Example "));
            Assert.True(c.AddEntry(JiraListKind.BotNames, Jira.SuggestedBotName));
            Assert.True(c.AddEntry(JiraListKind.MetadataFilters, Jira.SuggestedMetadataFilter));
            Assert.True(c.AddEntry(JiraListKind.AuthorPrefixes, "ACME"));
            Assert.True(c.IsChanged && c.CanSave);
            var changes = rec.Changes;

            c.Save();
            Assert.True(c.Saving && c.Busy && !c.CanSave);
            Assert.Equal(changes + 1, rec.Changes);
            // While it saves the page waits.
            c.SetOnlyMine(false);
            Assert.False(c.AddEntry(JiraListKind.AuthorPrefixes, "Globex"));
            c.Save();
        });
        await h.IdleAsync();
        Assert.Single(rec.Done);
        Assert.Equal(["Saving the account", null], rec.Busy[^2..]);
        Assert.False(c.Saving);

        var want = account.Config with
        {
            Name = "Acme Jira",
            Jira = account.Config.Jira! with
            {
                Spaces = [new SpaceRef { Id = "10003", Key = "MOB", Name = "Mobile" }, new SpaceRef { Id = "10002", Key = "WEB", Name = "Website" }],
                OfflineDays = 90,
                OnlyMine = true,
                HideEvents = true,
                DisabledFolders = [VirtualFolder.Watching],
                ClosedStatuses = [new StatusRef { Id = "6", Name = "Done" }, new StatusRef { Id = "10010", Name = "Done" }],
                NotificationMail = NotificationMailMode.Hide,
                NotificationSenders = ["jira@acme.example"],
                BotNames = [Jira.SuggestedBotName],
                MetadataFilters = [Jira.SuggestedMetadataFilter],
                AuthorPrefixes = ["ACME"],
            },
        };
        ApiJson.AssertSameValue(
            new AccountUpdateParams { AccountId = "acc-j1", Config = want, Credentials = new Credentials() },
            h.Params.Last<AccountUpdateParams>(API.AccountUpdate.Name));
        // No password on the wire: the daemon keeps the stored token.
        Assert.Empty(ApiJson.Keys(ApiJson.Parse(h.Params.LastRaw(API.AccountUpdate.Name)!).GetProperty("credentials")));
        Assert.Equal(["acc-j1"], rec.Done);
        ApiJson.AssertSameValue(want, Assert.Single(rec.DoneConfigs));
        Assert.Equal(0, rec.Closes);
        Assert.Equal(1, h.Params.Count(API.AccountUpdate.Name));
        Assert.Empty(rec.Banners);
    }

    [Fact]
    public async Task AFormThatChangesNothingClosesWithoutACall()
    {
        await using var h = await ListingHarnessAsync();
        // The stored account spells its defaults out.
        var (c, rec) = await MakeAsync(h, StoredAccount(jc => jc with { OfflineDays = 30, NotificationMail = NotificationMailMode.Sync }));
        await StartListedAsync(h, c);
        await h.Ui.RunAsync(() =>
        {
            // Changes that come back to where they were.
            c.SetOnlyMine(true);
            c.SetOnlyMine(false);
            c.SetSpace("10001", false);
            c.SetSpace("10001", true);
            c.SetName(" Acme ");
            var done = c.StatusGroups[^1].Choices[^1];
            c.SetStatus(done, false);
            c.SetStatus(done, true);
            Assert.Empty(c.Form.ClosedStatuses); // the statuses of the category done are the default
            Assert.False(c.IsChanged);
            c.Save();
            Assert.Equal(1, rec.Closes);
            Assert.True(!c.Busy && rec.Done.Count == 0);
        });
        await h.IdleAsync();
        Assert.Equal(0, h.Params.Count(API.AccountUpdate.Name));
    }

    [Fact]
    public async Task ARefusedSaveSaysWhy()
    {
        await using var h = await DaemonHarness.StartAsync();
        h.On(API.AccountListSpaces.Name, _ => ListingJson);
        h.On(API.AccountUpdate.Name, Fails(ErrorCode.Conflict, "exists"));
        var (c, rec) = await MakeAsync(h, StoredAccount());
        await StartListedAsync(h, c);
        await h.Ui.RunAsync(() =>
        {
            c.SetOnlyMine(true);
            c.Save();
        });
        await h.IdleAsync();
        Assert.Equal(["An account for this Jira site already exists"], rec.Banners);
        Assert.True(!c.Saving && !c.Busy && c.CanSave);
        Assert.True(rec.Done.Count == 0 && rec.Closes == 0);
        Assert.True(c.Form.OnlyMine); // the form stays as it was

        // The next change takes the banner away.
        await h.Ui.RunAsync(() => c.SetShowEvents(false));
        Assert.Null(rec.Banners[^1]);
        Assert.Null(c.Banner);

        var invalid = DaemonHarness.Daemon(ErrorCode.InvalidArgument, "jira: metadataFilters entry 1 is not a valid RE2 pattern");
        h.On(API.AccountUpdate.Name, (Func<string, string>)(_ => throw invalid));
        await h.Ui.RunAsync(c.Save);
        await h.IdleAsync();
        Assert.Equal(3, rec.Banners.Count);
        Assert.Equal(RpcErrorText.Text("Saving the account", invalid), rec.Banners[^1]);
        Assert.StartsWith("Saving the account was rejected", c.Banner, StringComparison.Ordinal);

        h.On(API.AccountUpdate.Name, Fails(ErrorCode.AuthFailed, "401"));
        await h.Ui.RunAsync(c.Save);
        await h.IdleAsync();
        Assert.Equal("The Jira site rejected the token", rec.Banners[^1]);
        h.On(API.AccountUpdate.Name, Fails(ErrorCode.KeyringError, "locked"));
        await h.Ui.RunAsync(c.Save);
        await h.IdleAsync();
        Assert.Equal(RpcErrorText.Text("Saving the account", DaemonHarness.Daemon(ErrorCode.KeyringError, "locked")), c.Banner);
    }

    [Fact]
    public async Task AFormThatCannotBeSavedIsNotSent()
    {
        await using var h = await ListingHarnessAsync();
        var (c, rec) = await MakeAsync(h, StoredAccount());
        await StartListedAsync(h, c);
        await h.Ui.RunAsync(() =>
        {
            c.SetSpace("10001", false);
            c.SetSpace("10002", false);
            Assert.Empty(c.SelectedSpaces);
            Assert.Equal("Select at least one space", c.SpacesProblem);
            Assert.False(c.CanSave);
            c.Save();
            Assert.Equal(["Select at least one space"], rec.Banners);
            Assert.False(c.Busy);
            c.SetSpace("10003", true);
            Assert.Null(rec.Banners[^1]);
            Assert.True(c.CanSave && c.SpacesProblem.Length == 0);
        });
        await h.IdleAsync();
        Assert.Equal(0, h.Params.Count(API.AccountUpdate.Name));
    }

    [Fact]
    public async Task TheListsCheckWhatIsAdded()
    {
        await using var h = await ListingHarnessAsync();
        var (c, rec) = await MakeAsync(h, StoredAccount(jc => jc with { BotNames = ["Deploy Bot"] }));
        await StartListedAsync(h, c);
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(["Deploy Bot"], c.Entries(JiraListKind.BotNames));
            Assert.Equal([new JiraSuggestion(Jira.SuggestedBotName, "Add Issue Sync – Synchronization for Jira")], c.Suggestions(JiraListKind.BotNames));
            Assert.Equal([Jira.SuggestedMetadataFilter], c.Suggestions(JiraListKind.MetadataFilters).Select(s => s.Value));
            Assert.True(c.Suggestions(JiraListKind.AuthorPrefixes).Count == 0 && c.Suggestions(JiraListKind.Senders).Count == 0);

            // Nothing typed: nothing added, nothing wrong.
            Assert.True(c.AddEntry(JiraListKind.BotNames, "  "));
            Assert.Equal(["Deploy Bot"], c.Entries(JiraListKind.BotNames));
            Assert.Equal("", c.Problem(JiraListKind.BotNames));

            Assert.False(c.AddEntry(JiraListKind.BotNames, "ab"));
            Assert.Equal("A bot name needs at least 3 characters", c.Problem(JiraListKind.BotNames));
            Assert.Equal("", c.Problem(JiraListKind.MetadataFilters)); // a problem belongs to its list
            var changes = rec.Changes;
            c.EntryTyped(JiraListKind.BotNames);
            Assert.True(c.Problem(JiraListKind.BotNames).Length == 0 && rec.Changes == changes + 1);
            c.EntryTyped(JiraListKind.BotNames);
            Assert.Equal(changes + 1, rec.Changes); // nothing to clear

            Assert.False(c.AddEntry(JiraListKind.BotNames, "deploy  bot"));
            Assert.Equal("This entry is already in the list", c.Problem(JiraListKind.BotNames));
            Assert.False(c.AddEntry(JiraListKind.MetadataFilters, "(unclosed"));
            Assert.Equal("This pattern is not valid: missing closing )", c.Problem(JiraListKind.MetadataFilters));
            Assert.False(c.AddEntry(JiraListKind.MetadataFilters, "(?<=Remote).*"));
            Assert.Equal("This pattern is not valid: invalid named capture", c.Problem(JiraListKind.MetadataFilters));
            Assert.False(c.AddEntry(JiraListKind.Senders, "acme.example"));
            Assert.Equal("Enter an address, or a domain such as @example.org", c.Problem(JiraListKind.Senders));
            Assert.False(c.IsChanged); // nothing was added

            // A suggestion is added as it is, once.
            c.AddSuggestion(JiraListKind.BotNames, Jira.SuggestedBotName);
            c.AddSuggestion(JiraListKind.BotNames, Jira.SuggestedBotName);
            c.AddSuggestion(JiraListKind.BotNames, "Somebody Else");
            Assert.Equal(["Deploy Bot", Jira.SuggestedBotName], c.Entries(JiraListKind.BotNames));
            Assert.True(c.Problem(JiraListKind.BotNames).Length == 0 && c.Suggestions(JiraListKind.BotNames).Count == 0);
            Assert.True(c.AddEntry(JiraListKind.MetadataFilters, " ^Sent from .*$ "));
            Assert.Equal(["^Sent from .*$"], c.Entries(JiraListKind.MetadataFilters));
            Assert.Equal("", c.Problem(JiraListKind.MetadataFilters));
            Assert.True(c.AddEntry(JiraListKind.Senders, "@Acme.Example"));
            Assert.Equal(["@acme.example"], c.Entries(JiraListKind.Senders));
            Assert.True(c.IsChanged);

            c.RemoveEntry(JiraListKind.BotNames, 0);
            c.RemoveEntry(JiraListKind.BotNames, 7);
            Assert.Equal([Jira.SuggestedBotName], c.Entries(JiraListKind.BotNames));
            c.RemoveEntry(JiraListKind.BotNames, 0);
            Assert.Empty(c.Entries(JiraListKind.BotNames));
            Assert.Single(c.Suggestions(JiraListKind.BotNames)); // offered again once it is gone

            // The senders do not matter when notifications are left alone.
            c.SetNotificationIndex(2);
            Assert.True(!c.SendersEditable && c.NotificationIndex == 2);
            c.SetNotificationIndex(9);
            Assert.Equal(2, c.NotificationIndex);
        });
    }

    [Fact]
    public async Task ALateReplyIsDropped()
    {
        await using var h = await DaemonHarness.StartAsync();
        var listing = h.Hold();
        var secondUpdate = h.Hold();
        h.On(API.AccountListSpaces.Name, async _ =>
        {
            await listing.WaitAsync();
            return ListingJson;
        });
        h.On(API.AccountUpdate.Name, async _ =>
        {
            if (h.Params.Count(API.AccountUpdate.Name) >= 2)
            {
                await secondUpdate.WaitAsync();
            }
            return "{}";
        });
        // Save while the spaces load: the listing is not waited for.
        var (c, rec) = await MakeAsync(h, StoredAccount());
        await h.Ui.RunAsync(c.Start);
        await listing.ArrivedAsync();
        await h.Ui.RunAsync(() =>
        {
            c.SetOnlyMine(true);
            c.Save();
            Assert.Equal(["Loading the spaces", "Saving the account"], rec.Busy);
        });
        // The listing is held: wait for the save alone.
        await rec.Conditions.WhenAsync(h.Ui, () => rec.Done.Count == 1, "saved");
        listing.Release();
        await h.IdleAsync();
        Assert.Null(c.Listing); // the listing arrived after Save started
        Assert.Equal(["Loading the spaces", "Saving the account", null], rec.Busy);

        // Closed while it saves: nothing is reported any more.
        var (c2, rec2) = await MakeAsync(h, StoredAccount());
        await h.Ui.RunAsync(() =>
        {
            c2.SetOnlyMine(true);
            c2.Save();
        });
        await secondUpdate.ArrivedAsync();
        await h.Ui.RunAsync(c2.Close);
        Assert.True(c2.IsClosed && !c2.Busy && !c2.Saving);
        secondUpdate.Release();
        await h.IdleAsync();
        Assert.True(rec2.Done.Count == 0 && rec2.Banners.Count == 0);
        Assert.Equal(["Saving the account"], rec2.Busy);
        // A closed page does nothing.
        await h.Ui.RunAsync(() =>
        {
            c2.Save();
            c2.ReplaceToken();
            c2.TokenReplaced();
        });
        Assert.Empty(rec2.TokenRequests);
        await h.IdleAsync();
        Assert.Equal(2, h.Params.Count(API.AccountUpdate.Name));
    }

    [Fact]
    public async Task ReplacingTheTokenListsAgain()
    {
        await using var h = await DaemonHarness.StartAsync();
        h.On(API.AccountListSpaces.Name, Fails(ErrorCode.AuthFailed, "401"));
        var (c, rec) = await MakeAsync(h, StoredAccount());
        await h.Ui.RunAsync(c.Start);
        await h.IdleAsync();
        Assert.Equal(["The Jira site rejected the token"], rec.Banners);

        await h.Ui.RunAsync(() =>
        {
            c.SetOnlyMine(true);
            c.ReplaceToken();
        });
        Assert.Equal(["acc-j1"], rec.TokenRequests);

        // The assistant stored a new token: the listing works now, and the
        // form keeps what was edited.
        h.On(API.AccountListSpaces.Name, _ => ListingJson);
        await h.Ui.RunAsync(() =>
        {
            c.TokenReplaced();
            Assert.Null(rec.Banners[^1]); // the old reason goes at once
        });
        await h.IdleAsync();
        Assert.NotNull(c.Listing);
        Assert.Equal(["Loading the spaces", null, "Loading the spaces", null], rec.Busy);
        Assert.Null(c.Banner);
        Assert.Equal(3, c.SpaceRows.Count);
        Assert.True(c.Form.OnlyMine);
    }

    [Fact]
    public async Task ADataCenterAccount()
    {
        await using var h = await DaemonHarness.StartAsync();
        h.On(API.AccountListSpaces.Name, Fails(ErrorCode.AuthRequired, "no token"));
        var account = new Account
        {
            Id = "acc-dc",
            Config = JiraSettingsTests.DataCenterAccount(),
            Enabled = true,
            State = new SyncState { AccountId = "acc-dc", Status = SyncStatus.AuthRequired },
            Capabilities = [],
        };
        var (c, rec) = await MakeAsync(h, account);
        Assert.Equal(JiraDeployment.Datacenter, c.Deployment);
        Assert.Equal(("Jira Data Center", "Personal Access Token"), (c.Site.Deployment, c.Site.TokenLabel));
        Assert.Equal("", c.SendersPlaceholder);
        await h.Ui.RunAsync(c.Start);
        await h.IdleAsync();
        Assert.Equal(["Enter the personal access token for this account"], rec.Banners);
    }

    // controller_test.go TestHostileListingIsCleaned: every string the page
    // shows from the site is cleaned before it gets to a view: a hostile
    // listing (bidirectional overrides, line breaks, zero-width characters)
    // comes out as one plain line.
    [Fact]
    public async Task HostileListingIsCleaned()
    {
        var rlo = ((char)0x202E).ToString();
        var zwsp = ((char)0x200B).ToString();
        var hostile = new AccountListSpacesResult
        {
            User = new SiteUser { Name = "Eve" + rlo + "\n<b>Admin</b>", Email = "eve@acme.example" + zwsp },
            Spaces = [new Space { Id = "10001", Key = "ITSD" + zwsp, Name = "IT\r\nService" + rlo + " Desk", Issues = -1 }],
            Statuses = [new IssueStatus { Id = "6", Name = "Do" + zwsp + "ne\t<i>x</i>", Category = IssueStatusCategory.Done }],
        };
        var body = JsonCoding.EncodeToString(hostile);
        await using var h = await DaemonHarness.StartAsync();
        h.On(API.AccountListSpaces.Name, _ => body);
        var (c, _) = await MakeAsync(h, StoredAccount());
        await StartListedAsync(h, c);
        Assert.Equal(("Eve <b>Admin</b>", "eve@acme.example"), (c.Site.User, c.Site.UserDetail));
        Assert.Equal("ITSD – IT Service Desk", c.SpaceRows[0].Title);
        foreach (var choice in c.StatusGroups.SelectMany(g => g.Choices))
        {
            Assert.False(choice.Name.Contains('\t', StringComparison.Ordinal) || choice.Name.Contains('\n', StringComparison.Ordinal) || choice.Name.Contains(zwsp, StringComparison.Ordinal), choice.Name);
        }
    }

    // A fake that lists the spaces and accepts the update, both recorded.
    private static async Task<DaemonHarness> ListingHarnessAsync()
    {
        var h = await DaemonHarness.StartAsync();
        h.On(API.AccountListSpaces.Name, _ => ListingJson);
        h.On(API.AccountUpdate.Name, _ => "{}");
        return h;
    }

    // The stored account, as account.list returns it.
    private static Account StoredAccount(Func<JiraConfig, JiraConfig>? edit = null)
    {
        var cfg = JiraSettingsTests.CloudAccount();
        if (edit is not null)
        {
            cfg = cfg with { Jira = edit(cfg.Jira!) };
        }
        return new Account
        {
            Id = "acc-j1",
            Config = cfg,
            Enabled = true,
            State = new SyncState { AccountId = "acc-j1", Status = SyncStatus.Idle },
            Capabilities = [Capability.Comment, Capability.Forward],
        };
    }

    // A controller on a connected client, its events recorded; not started.
    private static async Task<(JiraAccountController, Recorder)> MakeAsync(DaemonHarness h, Account account)
    {
        var client = await h.ConnectAsync();
        return await h.Ui.RunAsync(() =>
        {
            var c = new JiraAccountController(client, account, pending: h.Pending);
            h.CloseAtEnd(c.Close);
            var rec = new Recorder();
            rec.Attach(c);
            return (c, rec);
        });
    }

    // Started, with the listing in.
    private static async Task StartListedAsync(DaemonHarness h, JiraAccountController c)
    {
        await h.Ui.RunAsync(c.Start);
        await h.IdleAsync();
        Assert.NotNull(c.Listing);
    }

    // Collects what the controller reports (the Swift suite's Recorder).
    private sealed class Recorder
    {
        public int Changes { get; private set; }

        public List<string?> Busy { get; } = [];

        public List<string?> Banners { get; } = [];

        public List<AccountId> Done { get; } = [];

        public List<AccountConfig> DoneConfigs { get; } = [];

        public int Closes { get; private set; }

        public List<AccountId> TokenRequests { get; } = [];

        public UiConditions Conditions { get; } = new();

        public void Attach(JiraAccountController c)
        {
            c.Changed += (_, _) => Note(() => Changes++);
            c.BusyChanged += (_, text) => Note(() => Busy.Add(text));
            c.BannerChanged += (_, text) => Note(() => Banners.Add(text));
            c.Done += (_, e) => Note(() =>
            {
                Done.Add(e.Id);
                DoneConfigs.Add(e.Config);
            });
            c.CloseRequested += (_, _) => Note(() => Closes++);
            c.ReplaceTokenRequested += (_, a) => Note(() => TokenRequests.Add(a.Id));
        }

        private void Note(Action record)
        {
            record();
            Conditions.Changed();
        }
    }
}
