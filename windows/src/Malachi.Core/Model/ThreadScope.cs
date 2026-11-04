// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: thread ids are unique within an account only (a Graph
// account's thread id is the raw Exchange conversationId, which two
// mailboxes of one tenant share), but MailModel keys threads, members, rows
// and folds by thread id alone. The All Inboxes list holds conversations of
// several accounts, so everything entering the model from it carries a
// scoped id, "<account>\u001f<thread>" (the separator is a control character
// that neither id can hold), and only the call that asks the daemon about
// a conversation (thread.get) takes the raw one back.

using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>Thread ids made unique across accounts for the All Inboxes list.</summary>
public static class ThreadScope
{
    private const char Separator = '\u001f';

    /// <summary>The id of thread <paramref name="thread"/> of <paramref name="account"/> in the All Inboxes list.</summary>
    public static ThreadId Scoped(AccountId account, ThreadId thread) =>
        new(account.ToString() + Separator + thread.ToString());

    /// <summary>The daemon's id of a scoped (or plain) thread id.</summary>
    public static ThreadId Raw(ThreadId scoped)
    {
        var text = scoped.ToString();
        var at = text.IndexOf(Separator, System.StringComparison.Ordinal);
        return at < 0 ? scoped : new ThreadId(text[(at + 1)..]);
    }

    /// <summary>A conversation row of the All Inboxes list: its id, and its latest message's, scoped.</summary>
    public static ThreadSummary ScopeThread(ThreadSummary t)
    {
        System.ArgumentNullException.ThrowIfNull(t);
        return t with { Id = Scoped(t.AccountId, t.Id), Latest = ScopeMessage(t.Latest) };
    }

    /// <summary>A message of the All Inboxes list with its thread id scoped (as it is).</summary>
    public static MessageSummary ScopeMessage(MessageSummary s)
    {
        System.ArgumentNullException.ThrowIfNull(s);
        return s.ThreadId is { } tid && !tid.ToString().Contains(Separator, System.StringComparison.Ordinal)
            ? s with { ThreadId = Scoped(s.AccountId, tid) }
            : s;
    }

    /// <summary>The messages of <paramref name="list"/> scoped.</summary>
    public static IReadOnlyList<MessageSummary> ScopeMessages(IEnumerable<MessageSummary> list) => [.. list.Select(ScopeMessage)];
}
