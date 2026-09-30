// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/MessagesChangedTests.swift
// (MessagesChangedTests), the counterpart of the handleMessagesChanged
// cases of ui/internal/window/notify_test.go: notify.messagesChanged
// (docs/api.md §5) over the mailbox controller against MailFixture. A Jira
// account hid notification mails in a mail account, or showed them again:
// the folders of that account are read again for their counts, and the
// selected folder is listed again when the notification is about it. The
// notifications are handed to the controller as the app routes them
// (MailboxControllerFoldersTests covers the socket). Swift's jiraEditorOfARoute
// is JiraAccountsTests.EditorByKind. The reading half is
// MessagesChangedReadingTests.

using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Model;
using Xunit;
using static Malachi.Core.Tests.Model.MailModelTests;

namespace Malachi.Core.Tests.Controllers;

public sealed class MessagesChangedTests
{
    private static readonly FolderKey Inbox1 = new("acc1", "inbox");

    [Fact]
    public async Task TheNamedFolderIsListedAgainWithItsCounts()
    {
        var (h, log) = await StartAsync();
        await using var _ = h;
        Assert.Equal(Inbox1, h.Mailbox.Model.Selected);
        Assert.Equal(1, log.Reloads);
        Assert.Equal(2, h.Mailbox.Model.Folder(Inbox1)?.Unread);
        var lists = h.Fixture.CallCount(API.FolderList.Name);

        // Two notification mails were hidden: the Inbox counts less.
        var folders = TestAccounts().Folders["acc1"];
        var i = IndexOf(folders, "inbox");
        h.Fixture.SetFolders(Replace(folders, i, f => f with { Unread = 0, Total = 5 }), "acc1");
        await PushAsync(h, "acc1", "inbox", "trash");
        Assert.Equal(2, log.Reloads); // the selected folder is one of those named
        Assert.Equal(lists + 1, h.Fixture.CallCount(API.FolderList.Name)); // the folders of that account alone
        Assert.Equal(0, h.Mailbox.Model.Folder(Inbox1)?.Unread);
        Assert.Equal(5, h.Mailbox.Model.Folder(Inbox1)?.Total);
        Assert.Equal(Inbox1, h.Mailbox.Model.Selected); // the selection stays
    }

    [Fact]
    public async Task AnotherFolderOnlyReadsTheCounts()
    {
        var (h, log) = await StartAsync();
        await using var _ = h;
        var lists = h.Fixture.CallCount(API.FolderList.Name);
        await PushAsync(h, "acc1", "trash");
        Assert.Equal(1, log.Reloads); // the selected folder is not named
        Assert.Equal(lists + 1, h.Fixture.CallCount(API.FolderList.Name));
    }

    [Fact]
    public async Task NoFolderNamedMeansEveryFolderOfTheAccount()
    {
        var (h, log) = await StartAsync();
        await using var _ = h;
        await PushAsync(h, "acc1");
        Assert.Equal(2, log.Reloads); // the selected folder belongs to the account

        // Another account's: its counts are read, the list stays.
        var lists = h.Fixture.CallCount(API.FolderList.Name);
        await PushAsync(h, "acc2");
        Assert.Equal(2, log.Reloads);
        Assert.Equal(lists + 1, h.Fixture.CallCount(API.FolderList.Name));

        // A folder of that name in another account is not the selected one.
        await PushAsync(h, "acc2", "inbox");
        Assert.Equal(2, log.Reloads);
    }

    [Fact]
    public async Task AnAccountThatIsNotShownIsLeftAlone()
    {
        var (h, log) = await StartAsync();
        await using var _ = h;
        var lists = h.Fixture.CallCount(API.FolderList.Name);
        var rebuilds = log.Rebuilds;
        // A paused account, and one this client does not know.
        await PushAsync(h, "acc3");
        await PushAsync(h, "nobody", "inbox");
        Assert.Equal(lists, h.Fixture.CallCount(API.FolderList.Name));
        Assert.True(log.Rebuilds == rebuilds && log.Reloads == 1);
    }

    [Fact]
    public async Task NothingSelectedListsNothing()
    {
        var (h, log) = await StartAsync();
        await using var _ = h;
        await h.On(() => h.Mailbox.Model.Selected = null);
        await PushAsync(h, "acc2");
        // The rebuild selects the initial folder again, which lists it once;
        // the notification itself asked for no list.
        Assert.Equal(Inbox1, h.Mailbox.Model.Selected);
        Assert.Equal(2, log.Reloads);
    }

    private static int IndexOf(System.Collections.Generic.IReadOnlyList<Folder> list, string id)
    {
        for (var i = 0; i < list.Count; i++)
        {
            if (list[i].Id == id)
            {
                return i;
            }
        }
        return -1;
    }

    // The notification, as the app routes it, and what it set off.
    private static async Task PushAsync(MailboxControllerHarness h, string account, params string[] folders)
    {
        var n = new MessagesChangedNotification { AccountId = account, FolderIds = [.. System.Linq.Enumerable.Select(folders, f => new FolderId(f))] };
        await h.On(() => h.Mailbox.HandleNotification(new DaemonNotification.MessagesChanged(n)));
        await h.IdleAsync();
    }

    // A fixture with the three test accounts, a connected client and the
    // folder half, whose reloads are counted (the list half's loadMessages,
    // as far as the folder half sees it).
    private static async Task<(MailboxControllerHarness, Log)> StartAsync()
    {
        var log = new Log();
        var (accounts, folders) = TestAccounts();
        var h = await MailboxControllerHarness.StartAsync(
            f =>
            {
                f.SetAccounts(accounts);
                f.SetFolders(folders);
            },
            wire: h =>
            {
                var mailbox = h.Mailbox;
                mailbox.EntriesChanged += (_, _) => log.Rebuilds++;
                mailbox.ReloadMessages = () =>
                {
                    mailbox.Model.ListFolder = mailbox.Model.Selected;
                    log.Reloads++;
                };
            });
        await h.IdleAsync();
        Assert.True(log.Rebuilds >= 1);
        return (h, log);
    }

    private sealed class Log
    {
        public int Rebuilds { get; set; }

        public int Reloads { get; set; }
    }
}
