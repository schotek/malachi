// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ConversationController.swift;
// GTK: ui/internal/window/conversation_controller.go (conversationController).
//
// The conversation view of the reading pane, the controller half: which
// conversation the pane shows, its model (Conversation, the port of
// ui/internal/conversation), the members' bodies as the cards ask for them,
// and the model kept in step with the list while it is shown. No WinUI: the
// pane lays the cards out and tells this controller which of them are near
// enough to need a body. The Swift callbacks are events: onChange is
// Changed, onLoaded EntryLoaded (Loaded is the held entries, Swift's
// loaded). Swift's summaries compare by value; a C# record compares its
// lists by reference, so a member counts as changed only when its JSON
// does (SameSummary).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Bulk;
using Malachi.Core.Model;

namespace Malachi.Core.Controllers;

/// <summary>
/// Which conversation the reading pane shows and what of it is loaded. The
/// members come from the list controller (<see cref="ListController.EnsureMembers"/>:
/// thread.get scoped to the listed folder, shared with the unfolded row and
/// the actions), with the user's replies in Sent the folder lacks
/// (<see cref="ThreadMembers.Sent"/>, cards with
/// <see cref="ConversationItem.Sent"/>: never marked read, never among the
/// members the actions take), the bodies from the message cache, one card at a time and
/// only for the cards the pane asks for (<see cref="NeedsBody"/>):
/// <c>message.body</c> alone, and <c>message.get</c> too only for a card that
/// needs what it adds to the summary (attachments, the unsubscribe offer of a
/// bulk message, the Cc of the recipients' disclosure). The entries of the cards are held here
/// (<see cref="Loaded"/>), so a long conversation does not lose them to the
/// cache's caps while it is shown.
/// </summary>
/// <remarks>
/// The mark-as-read timer is the list controller's
/// (<see cref="ListController.MarkConversationRead"/> arms it for
/// <see cref="ConversationModel.MarkRead"/> once the members are known); this
/// controller exposes the model, and has the list arm the timer when the
/// members arrive only after a thread.get that failed. Create it, and call
/// it, on the UI thread; every event is raised there.
/// </remarks>
public sealed partial class ConversationController : IDisposable
{
    /// <summary>
    /// The held entries' bodies beyond which the entries of cards the pane
    /// does not keep near are let go (<see cref="Trim"/>).
    /// </summary>
    public const int HeldBytes = 64 << 20;

    private readonly Dictionary<MessageId, LoadedMessage> loaded = [];

    // The members whose entry was asked for and has not settled, and whether
    // message.get was part of the request.
    private readonly Dictionary<MessageId, bool> pending = [];

    // The members whose message.get failed: not asked again for this
    // conversation (the pane asks on every scroll), the summary serves their
    // cards.
    private readonly HashSet<MessageId> noGet = [];

    // The cards whose quoted history the user revealed (Show Quoted Text):
    // kept through updates and a rebuild of the messages, forgotten with the
    // conversation.
    private readonly QuotedReveal quoted = new();

    // The folder members the model was last built or merged from.
    private IReadOnlyList<MessageSummary> members = [];

    // The user's replies in Sent the model was last built or merged from.
    private IReadOnlyList<MessageSummary> sent = [];

    // The conversation's summary as last known (the row's, then thread.get's).
    private ThreadSummary? summary;

    // The model was built from what the listing knew, after thread.get
    // failed: the members, once they arrive, build it anew.
    private bool listing;

    // The list generation (MailModel.ListGen) in which thread.get failed for
    // the conversation: it is not asked again until the list lists the
    // folder anew.
    private ulong? failedGen;

    // Bumped with every selection: a late answer for a conversation left
    // meanwhile is dropped.
    private ulong gen;

    // Bumped by Refresh: a body asked for before the daemon rebuilt the
    // conversation's messages is dropped when it arrives, so that it cannot
    // land in Loaded beside the fresh one.
    private ulong bodyGen;

    /// <summary>
    /// A controller over <paramref name="list"/> and <paramref name="cache"/>:
    /// it follows the list's <see cref="ListController.ThreadMembersChanged"/>
    /// and <see cref="ListController.ConversationChanged"/> until disposed.
    /// </summary>
    public ConversationController(ListController list, MessageCache cache)
    {
        ArgumentNullException.ThrowIfNull(list);
        ArgumentNullException.ThrowIfNull(cache);
        List = list;
        Cache = cache;
        list.ThreadMembersChanged += OnThreadMembersChanged;
        list.ConversationChanged += OnConversationChanged;
    }

    /// <summary>Called after every change of <see cref="Thread"/> or <see cref="Model"/> (Swift <c>onChange</c>).</summary>
    public event EventHandler<Change>? Changed;

    /// <summary>
    /// A card's entry has news (a half of it arrived through
    /// <see cref="NeedsBody"/>); the entry is in <see cref="Loaded"/>. The
    /// cache's own fan-out (<see cref="MessageCache.MessageLoaded"/>) tells
    /// the pane about later news, such as remote images (Swift
    /// <c>onLoaded</c>).
    /// </summary>
    public event EventHandler<(MessageId Id, LoadedMessage Entry)>? EntryLoaded;

    /// <summary>The list half of the window.</summary>
    public ListController List { get; }

    /// <summary>The message cache.</summary>
    public MessageCache Cache { get; }

    /// <summary>The conversation on display; null when the pane shows something else.</summary>
    public ThreadId? Thread { get; private set; }

    /// <summary>Its model; null while the members are on their way.</summary>
    public ConversationModel? Model { get; private set; }

    /// <summary>The cache entries of the cards whose body was asked for, by member.</summary>
    public IReadOnlyDictionary<MessageId, LoadedMessage> Loaded => loaded;

    private MailModel Mail => List.Mailbox.Model;

    /// <summary>Stops following the list.</summary>
    public void Dispose()
    {
        List.ThreadMembersChanged -= OnThreadMembersChanged;
        List.ConversationChanged -= OnConversationChanged;
    }

    // Selection

    /// <summary>
    /// The list's selection changed (<see cref="ListController.SelectedRowChanged"/>):
    /// a conversation row (<see cref="ListRow.ShowsConversation"/>) is shown
    /// here, and true is returned; anything else clears the conversation and
    /// returns false (the pane shows the row's message, or its empty page).
    /// The same conversation announced again keeps what is shown.
    /// </summary>
    public bool Show(ListRow? row)
    {
        if (row is not { ShowsConversation: true, Key.Thread: { } tid })
        {
            Clear();
            return false;
        }
        if (tid == Thread)
        {
            if (row.Summary is { } s)
            {
                summary = s;
            }
            return true;
        }
        Reset();
        Thread = tid;
        quoted.Show(tid.Value);
        summary = row.Summary;
        Changed?.Invoke(this, Change.Loading);
        RequestMembers(tid);
        return true;
    }

    /// <summary>Shows nothing (another kind of row, or none, is selected).</summary>
    public void Clear()
    {
        var had = Thread is not null;
        Reset();
        if (had)
        {
            Changed?.Invoke(this, Change.Cleared);
        }
    }

    // Forgets the conversation on display.
    private void Reset()
    {
        gen++;
        Thread = null;
        Model = null;
        members = [];
        sent = [];
        loaded.Clear();
        pending.Clear();
        noGet.Clear();
        summary = null;
        listing = false;
        failedGen = null;
        quoted.Clear();
    }

    // Asks the list for the folder members of tid; they arrive through
    // MembersChanged, a failure builds from what the listing told
    // (BuildFromListing) and is remembered, so that thread.get is not asked
    // again before the list lists the folder anew.
    private void RequestMembers(ThreadId tid)
    {
        var g = gen;
        List.EnsureMembers(
            tid,
            () =>
            {
                if (gen == g)
                {
                    MembersChanged(tid);
                }
            },
            () =>
            {
                if (gen != g || Thread != tid)
                {
                    return;
                }
                failedGen = Mail.ListGen;
                if (Model is null)
                {
                    BuildFromListing(tid);
                }
            });
    }

    // thread.get failed (the list has said why): the conversation as the
    // listing knows it, its newest member, with the row of the older ones
    // left out. Nothing is marked read without the members; once they
    // arrive the model is built anew (listing).
    private void BuildFromListing(ThreadId tid)
    {
        if (summary is not { } t)
        {
            return;
        }
        var known = Mail.Members.TryGetValue(tid, out var mem) && mem.List.Count > 0 ? mem.List : [t.Latest];
        listing = true;
        members = known;
        sent = mem?.Sent ?? [];
        Model = Conversation.Build(t, known, Account(t.AccountId), sent);
        Changed?.Invoke(this, Change.Opened);
    }

    // Members

    private void OnThreadMembersChanged(object? sender, ThreadId tid) => MembersChanged(tid);

    private void OnConversationChanged(object? sender, ThreadId tid) => Refresh(tid);

    /// <summary>
    /// The list's model changed the members of conversation
    /// <paramref name="tid"/> (<see cref="ListController.ThreadMembersChanged"/>),
    /// or they arrived: the model is built the first time, then kept in step
    /// by merging what changed and removing what went
    /// (<see cref="Conversation.Merge"/>, <see cref="Conversation.Remove"/>),
    /// and so are the user's replies in Sent
    /// (<see cref="Conversation.MergeSent"/>). Members the list lost to a
    /// reload are asked for again, unless thread.get failed for this listing
    /// of the folder already. A model
    /// built from the listing after such a failure is not merged into: the
    /// members build it anew (the row of older members it showed would stay
    /// otherwise), and only then is the newest one marked read.
    /// </summary>
    public void MembersChanged(ThreadId tid)
    {
        if (tid != Thread || !Mail.Members.TryGetValue(tid, out var mem))
        {
            return;
        }
        if (!mem.Complete)
        {
            if (failedGen != Mail.ListGen)
            {
                RequestMembers(tid);
            }
            return;
        }
        if (List.RowFor(new ListKey(Thread: tid))?.Summary is { } row)
        {
            summary = row;
        }
        if (summary is not { } t)
        {
            return;
        }
        // The account tells the user's own mail (ConversationItem.Mine).
        var own = Account(t.AccountId);
        if (Model is not { } current || listing)
        {
            var recovered = listing;
            listing = false;
            members = mem.List;
            sent = mem.Sent;
            Model = Conversation.Build(t, mem.List, own, mem.Sent);
            Changed?.Invoke(this, Change.Opened);
            if (recovered)
            {
                // The list's own wait for the members ended with the
                // thread.get that failed.
                List.MarkConversationRead(tid);
            }
            return;
        }
        var m = current;
        var before = new Dictionary<MessageId, MessageSummary>();
        foreach (var s in members)
        {
            before.TryAdd(s.Id, s);
        }
        var now = mem.List.Select(s => s.Id).ToHashSet();
        foreach (var s in mem.List)
        {
            if (!before.TryGetValue(s.Id, out var was) || !SameSummary(was, s))
            {
                m = Conversation.Merge(m, s, own);
            }
        }
        foreach (var s in members)
        {
            if (!now.Contains(s.Id))
            {
                m = Conversation.Remove(m, s.Id);
                loaded.Remove(s.Id);
                pending.Remove(s.Id);
            }
        }
        // The replies after the members: a member that took a reply's place
        // (Merge) is not merged back as a reply.
        var sentBefore = new Dictionary<MessageId, MessageSummary>();
        foreach (var s in sent)
        {
            sentBefore.TryAdd(s.Id, s);
        }
        var sentNow = mem.Sent.Select(s => s.Id).ToHashSet();
        foreach (var s in sent)
        {
            if (!sentNow.Contains(s.Id) && !now.Contains(s.Id))
            {
                m = Conversation.Remove(m, s.Id);
                loaded.Remove(s.Id);
                pending.Remove(s.Id);
            }
        }
        foreach (var s in mem.Sent)
        {
            if (!sentBefore.TryGetValue(s.Id, out var was) || !SameSummary(was, s))
            {
                m = Conversation.MergeSent(m, s, own);
            }
        }
        members = mem.List;
        sent = mem.Sent;
        if (m.Items.Count == 0 && m.Earlier > 0)
        {
            // Every shown member went while older ones are left out: the
            // conversation is loaded again.
            Model = null;
            Changed?.Invoke(this, Change.Loading);
            List.RefreshMembers(tid);
            RequestMembers(tid);
            return;
        }
        if (ReferenceEquals(m, current))
        {
            return;
        }
        Model = m;
        Changed?.Invoke(this, Change.Updated);
    }

    /// <summary>
    /// The daemon rebuilt the messages of the shown conversation in place
    /// (notify.messagesChanged: a Jira pass with other rendering settings, a
    /// comment edited or re-attributed, the issue renamed; docs/api.md §5),
    /// which the list reports through <see cref="ListController.ConversationChanged"/>
    /// after it forgot the folder members and before it lists the folder
    /// again. The held entries are let go (the cache let go of its own) and
    /// the pane is told the conversation changed, so it asks for the bodies
    /// of the cards near the viewport again (<see cref="NeedsBody"/>, which
    /// finds nothing held and fetches); the cards keep what they show until
    /// the fresh body arrives. The members come back through
    /// <see cref="MembersChanged"/> once the reload asked thread.get for
    /// them, and merge into the model.
    /// </summary>
    public void Refresh(ThreadId tid)
    {
        if (tid != Thread)
        {
            return;
        }
        loaded.Clear();
        pending.Clear();
        noGet.Clear();
        bodyGen++;
        if (Model is not null)
        {
            Changed?.Invoke(this, Change.Updated);
        }
    }

    // Bodies

    /// <summary>
    /// The card of member <paramref name="id"/> is near the viewport: its
    /// body is fetched unless held already (<c>message.body</c>;
    /// <c>message.get</c> as well when <paramref name="details"/>, or when the
    /// message has attachments, whose chips need it, or is a newsletter or a
    /// mailing list, whose strip needs its offer, unless it failed for
    /// this member before). An event has no body. The entry is held in
    /// <see cref="Loaded"/> and announced through <see cref="EntryLoaded"/>
    /// whenever a half of it arrives. The body is the variant the user chose
    /// for the card: without its quoted history unless revealed
    /// (<see cref="SetQuoted"/>).
    /// </summary>
    public void NeedsBody(MessageId id, bool details = false)
    {
        if (Member(id) is not { } s || IssueReading.ReadsWithoutBody(s))
        {
            return;
        }
        var full = (details || s.HasAttachments || BulkReading.WantsOffer(s)) && !noGet.Contains(id);
        var reveal = quoted.IsRevealed(id);
        if (loaded.TryGetValue(id, out var held) && held.QuotedShown == reveal && held.BodySettled && (!full || held.Msg is not null))
        {
            return;
        }
        // Asked already (the pane asks on every scroll): the answer comes.
        if (pending.TryGetValue(id, out var asked) && (asked || !full))
        {
            return;
        }
        pending[id] = full;
        var g = gen;
        var bg = bodyGen;
        void Then(LoadedMessage lm)
        {
            if (gen != g || bodyGen != bg || Member(id) is null)
            {
                return;
            }
            if (!lm.Getting && !lm.Fetching)
            {
                pending.Remove(id);
                if (full && lm.Msg is null)
                {
                    noGet.Add(id); // logged by the cache; the summary serves
                }
            }
            loaded[id] = lm;
            EntryLoaded?.Invoke(this, (id, lm));
        }
        if (full)
        {
            Cache.Fetch(s, Then, reveal);
        }
        else
        {
            Cache.FetchBody(s, Then, reveal);
        }
    }

    /// <summary>
    /// The role of the folder <paramref name="s"/> lies in (the bulk strip of
    /// a card changes in the junk folder); <see cref="FolderRole.None"/> for
    /// a folder the model does not know.
    /// </summary>
    public FolderRole FolderRoleOf(MessageSummary s)
    {
        ArgumentNullException.ThrowIfNull(s);
        return Mail.FolderRole(new FolderKey(s.AccountId, s.FolderId));
    }

    /// <summary>Whether the quoted history of the card of member <paramref name="id"/> shows.</summary>
    public bool QuotedRevealed(MessageId id) => quoted.IsRevealed(id);

    /// <summary>
    /// The card's Show Quoted Text (<paramref name="on"/>) or Hide Quoted
    /// Text: the choice holds while the conversation is shown, and the card's
    /// body switches to that variant (the one held, or fetched as
    /// <see cref="NeedsBody"/> does; <paramref name="details"/> as there).
    /// </summary>
    public void SetQuoted(MessageId id, bool on, bool details = false)
    {
        if (Member(id) is null)
        {
            return;
        }
        quoted.Set(id, on);
        // A request for the other variant is in flight: this one is asked
        // for beside it.
        pending.Remove(id);
        NeedsBody(id, details);
    }

    /// <summary>
    /// The cache has news about member <paramref name="id"/> (its
    /// <see cref="MessageCache.MessageLoaded"/> fan-out: the remote images, a
    /// download): the card's entry is that one from now on. An id that is
    /// not shown is ignored.
    /// </summary>
    public void Adopt(MessageId id, LoadedMessage lm)
    {
        ArgumentNullException.ThrowIfNull(lm);
        if (Member(id) is null)
        {
            return;
        }
        loaded[id] = lm;
    }

    /// <summary>
    /// Lets go of the entries of the cards not in <paramref name="near"/>
    /// while the held bodies are above <paramref name="budget"/> bytes,
    /// largest first: their cards keep what they show and ask again when
    /// they come near.
    /// </summary>
    public void Trim(IReadOnlySet<MessageId> near, int budget = HeldBytes)
    {
        ArgumentNullException.ThrowIfNull(near);
        long total = loaded.Values.Sum(lm => (long)lm.Size);
        if (total <= budget)
        {
            return;
        }
        foreach (var (id, lm) in loaded.Where(e => !near.Contains(e.Key)).OrderByDescending(e => e.Value.Size).ToList())
        {
            if (total <= budget)
            {
                break;
            }
            total -= lm.Size;
            loaded.Remove(id);
        }
    }

    // Lookup

    /// <summary>The shown member <paramref name="id"/>, as the model has it now.</summary>
    public MessageSummary? Member(MessageId id)
    {
        if (Model is not { } m)
        {
            return null;
        }
        var i = m.Index(id);
        return i >= 0 ? m.Items[i].Message : null;
    }

    /// <summary>The account of the shown conversation's members.</summary>
    public Account? Account(AccountId id) => Mail.Account(id);

    /// <summary>
    /// The buttons the card of <paramref name="s"/> offers
    /// (<see cref="Conversation.CardActions"/>): none for an account the
    /// window does not know.
    /// </summary>
    public CapabilityActions Actions(MessageSummary s)
    {
        ArgumentNullException.ThrowIfNull(s);
        if (Mail.Account(s.AccountId) is not { } a)
        {
            return default;
        }
        return Conversation.CardActions(a, s, Capabilities.ForwardAccounts(Mail.Accounts).Count > 0);
    }

    /// <summary>
    /// The site of the Jira account <paramref name="id"/> ("" when unknown or
    /// not Jira): the only site the issue card's key may open.
    /// </summary>
    public string IssueSite(AccountId id) => Account(id)?.Config.Jira?.SiteUrl ?? "";

    // Swift's == on summaries: their JSON, as a record compares its lists by
    // reference.
    private static bool SameSummary(MessageSummary a, MessageSummary b) =>
        ReferenceEquals(a, b) || JsonCoding.EncodeToString(a) == JsonCoding.EncodeToString(b);
}
