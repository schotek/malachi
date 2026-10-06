// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/BoardConversationCards.swift; Go:
// ui/internal/board/conversation_cards.go (ConversationCards,
// ConversationCompensatedTop).
//
// What the cards of the board detail's conversation show: the folded
// preview, the plain text excerpt board.get gave, or the message's
// sanitised HTML in a locked web view of its own (message.body, the card
// web view of Mail's conversation in its sized mode); how many web views
// the detail keeps and which card gives way; which board refreshes change
// the cards at all; and how far the detail scrolls when a card above the
// viewport changes its height. The views only apply it.
//
// Swift's value type is a class here (docs/windows-port.md §3.1: a mutable
// struct becomes a class with Clone where a snapshot is taken), compared
// by value. Go's Apply opens the newest card when removing (or reordering)
// the members promotes a folded one to the newest, and counts its cached
// HTML against the limit at once; Swift keeps the old fold there, which
// leaves the newest, which has no fold arrow, closed for good. Go is
// followed.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;

namespace Malachi.Core.Boards;

public static partial class Board
{
    /// <summary>
    /// The state of the detail's conversation cards for the case on show
    /// (<see cref="Apply"/>), oldest first like the detail's messages.
    /// </summary>
    /// <remarks>
    /// A card is one member (its message id and the excerpt the daemon gave
    /// for it). The newest opens, the older ones start folded to a preview.
    /// An open card of a member with an id asks for its body once
    /// (<see cref="NeedsBody"/>, <see cref="Asked"/>); what came back
    /// (<see cref="Answered"/>) decides: HTML shows in a web view while the
    /// detail is live, any other answer leaves the excerpt, silently.
    /// Members without an id (the invented samples) never ask and stay text.
    /// At most <see cref="MaxLive"/> open cards hold HTML: opening one more,
    /// or HTML arriving for one more, folds back the card opened least
    /// recently, never the newest and never the card just opened.
    /// </remarks>
    public sealed class ConversationCards : IEquatable<ConversationCards>
    {
        /// <summary>
        /// The most web views the detail's conversation keeps: the newest and
        /// a few the user opened, beside the inline reply editor, itself a web
        /// view.
        /// </summary>
        public const int MaxLiveWebViews = 4;

        private readonly List<bool> folds = [];
        private readonly Dictionary<Member, Body> bodies = [];

        // The members in the order they were opened, the latest last.
        private readonly List<Member> opened = [];

        /// <summary>Cards that keep at most <paramref name="maxLive"/> web views (at least 1).</summary>
        public ConversationCards(int maxLive = MaxLiveWebViews)
        {
            MaxLive = Math.Max(maxLive, 1);
        }

        /// <summary>What message.body gave for a member.</summary>
        public enum Body
        {
            /// <summary>Not asked for yet.</summary>
            Unknown,

            /// <summary>Asked for; no answer yet (the excerpt shows meanwhile).</summary>
            Asked,

            /// <summary>Sanitised HTML: the web view.</summary>
            Html,

            /// <summary>Anything else: the excerpt stays.</summary>
            Text,
        }

        /// <summary>What a card shows.</summary>
        public enum Shows
        {
            /// <summary>The three-line preview of the excerpt: no body, no web view.</summary>
            Folded,

            /// <summary>The whole excerpt.</summary>
            Text,

            /// <summary>The sanitised HTML in a web view.</summary>
            Web,
        }

        /// <summary>The most open cards with HTML.</summary>
        public int MaxLive { get; }

        /// <summary>The case shown; null before the first <see cref="Apply"/>.</summary>
        public BoardCaseId? CaseId { get; private set; }

        /// <summary>The members, oldest first.</summary>
        public IReadOnlyList<Member> Members { get; private set; } = [];

        /// <summary>The index of the newest member, -1 when there is none.</summary>
        public int Newest => Members.Count - 1;

        /// <summary>The open cards holding HTML, which the limit counts.</summary>
        public IReadOnlyList<int> WebCards =>
            [.. Enumerable.Range(0, Members.Count).Where(i => !folds[i] && BodyOf(i) == Body.Html)];

        /// <summary>The key of <paramref name="d"/>'s conversation.</summary>
        public static Key KeyOf(Detail d)
        {
            ArgumentNullException.ThrowIfNull(d);
            return new Key(d.Id, [.. d.Messages.Select(m => new Member(m.Id, m.Text))]);
        }

        /// <summary>
        /// How far the detail's viewport top moves when a card's height
        /// changes by <paramref name="delta"/>: a card that ended at or above
        /// the viewport's top (document coordinates top down, before the
        /// change) moves what the user sees, and the viewport follows it; a
        /// card in view or below moves nothing above it. Clamped to the
        /// document after the change.
        /// </summary>
        public static double CompensatedTop(
            double viewportTop, double cardMaxY, double delta, double documentHeight, double viewportHeight)
        {
            var move = cardMaxY <= viewportTop + 0.5 ? delta : 0;
            var maxTop = Math.Max(0, documentHeight - viewportHeight);
            return Math.Min(Math.Max(0, viewportTop + move), maxTop);
        }

        // Applying the detail

        /// <summary>
        /// Takes the case's members as <paramref name="key"/> has them. Another
        /// case starts over: the newest open, the rest folded, nothing asked
        /// for. The same case keeps every member that is still there with its
        /// fold and its body (matched in order, so a sample's repeated excerpt
        /// keeps its own card; a member with the same id and another excerpt
        /// is matched next and keeps its card too); a new member opens when it
        /// is the newest and starts folded otherwise, and the newest is always
        /// open.
        /// </summary>
        public Change Apply(Key key)
        {
            ArgumentNullException.ThrowIfNull(key);
            if (key.CaseId != CaseId)
            {
                CaseId = key.CaseId;
                Members = [.. key.Members];
                folds.Clear();
                folds.AddRange(Enumerable.Range(0, Members.Count).Select(i => i != Newest));
                bodies.Clear();
                opened.Clear();
                if (Members.Count > 0)
                {
                    opened.Add(Members[^1]);
                }
                return new Change.Reset();
            }
            if (key.Members.SequenceEqual(Members))
            {
                return new Change.None();
            }
            var free = new Dictionary<Member, Queue<int>>();
            for (var i = 0; i < Members.Count; i++)
            {
                if (!free.TryGetValue(Members[i], out var slots))
                {
                    free[Members[i]] = slots = new Queue<int>();
                }
                slots.Enqueue(i);
            }
            var kept = new Dictionary<int, int>();
            var used = new HashSet<int>();
            for (var i = 0; i < key.Members.Count; i++)
            {
                if (free.TryGetValue(key.Members[i], out var slots) && slots.Count > 0)
                {
                    var old = slots.Dequeue();
                    kept[i] = old;
                    used.Add(old);
                }
            }
            // The same id with another excerpt: the daemon rebuilt the
            // message in place. The card stays (its fold, its place, its web
            // view); only its text and, when open, its body are new.
            var changed = new HashSet<int>();
            for (var i = 0; i < key.Members.Count; i++)
            {
                if (kept.ContainsKey(i) || key.Members[i].Id is not { } id)
                {
                    continue;
                }
                for (var old = 0; old < Members.Count; old++)
                {
                    if (!used.Contains(old) && Members[old].Id == id)
                    {
                        kept[i] = old;
                        used.Add(old);
                        changed.Add(i);
                        break;
                    }
                }
            }
            var newFolds = new List<bool>(key.Members.Count);
            var carried = new Dictionary<Member, Body>();
            var renamed = new Dictionary<Member, Member>();
            var reload = new List<int>();
            var last = key.Members.Count - 1;
            for (var i = 0; i < key.Members.Count; i++)
            {
                var m = key.Members[i];
                if (!kept.TryGetValue(i, out var old))
                {
                    newFolds.Add(i != last);
                    if (i == last)
                    {
                        opened.RemoveAll(x => x == m);
                        opened.Add(m);
                    }
                    continue;
                }
                newFolds.Add(folds[old]);
                if (!changed.Contains(i))
                {
                    continue;
                }
                // A body known for the old excerpt is kept for an open card
                // (the view shows it until the new one arrives) and asked
                // again; anything else asks the usual way.
                var prior = Members[old];
                renamed[prior] = m;
                if (!folds[old] && bodies.TryGetValue(prior, out var b) && b is Body.Html or Body.Text)
                {
                    carried[m] = b;
                    reload.Add(i);
                }
            }
            for (var k = 0; k < opened.Count; k++)
            {
                if (renamed.TryGetValue(opened[k], out var replacement))
                {
                    opened[k] = replacement;
                }
            }
            // Removing (or reordering) the newest can promote a folded kept
            // card. The newest has no fold arrow, so keeping that fold would
            // leave it closed for good (Go).
            var promoted = false;
            if (last >= 0 && newFolds[last])
            {
                promoted = true;
                newFolds[last] = false;
                var m = key.Members[last];
                opened.RemoveAll(x => x == m);
                opened.Add(m);
            }
            var present = key.Members.ToHashSet();
            opened.RemoveAll(m => !present.Contains(m));
            Members = [.. key.Members];
            folds.Clear();
            folds.AddRange(newFolds);
            foreach (var gone in bodies.Keys.Where(m => !present.Contains(m)).ToList())
            {
                bodies.Remove(gone);
            }
            foreach (var (m, b) in carried)
            {
                bodies[m] = b;
            }
            // A promoted card may already have cached HTML: it counts at once,
            // without another answer it does not need to ask for.
            if (promoted)
            {
                Enforce(Newest);
            }
            return new Change.Members(kept, reload);
        }

        // Reading

        /// <summary>Whether card <paramref name="i"/> starts or is folded.</summary>
        public bool IsFolded(int i) => Valid(i) && folds[i];

        /// <summary>Whether card <paramref name="i"/> folds at all: every card but the newest.</summary>
        public bool Foldable(int i) => Valid(i) && i != Newest;

        /// <summary>
        /// Whether card <paramref name="i"/> offers its fold arrow. A card that
        /// can show its message formatted (an id, and the detail can ask for
        /// bodies) always does; otherwise only while the excerpt is longer
        /// than the folded preview (<paramref name="isLong"/>, Swift's
        /// <c>long</c>; null until measured: no arrow).
        /// </summary>
        public bool Arrow(int i, bool? isLong, bool canFetch)
        {
            if (!Foldable(i))
            {
                return false;
            }
            return (canFetch && Members[i].Id is not null) || isLong == true;
        }

        /// <summary>What message.body gave for card <paramref name="i"/> (Swift <c>body(_:)</c>).</summary>
        public Body BodyOf(int i) => Valid(i) && bodies.TryGetValue(Members[i], out var b) ? b : Body.Unknown;

        /// <summary>Card <paramref name="i"/> should ask for its body now: it is open, has an id and was not asked yet.</summary>
        public bool NeedsBody(int i) => Valid(i) && Members[i].Id is not null && !folds[i] && BodyOf(i) == Body.Unknown;

        /// <summary>What card <paramref name="i"/> shows; <paramref name="live"/> says the detail may hold web views now.</summary>
        public Shows ShowsOf(int i, bool live)
        {
            if (!Valid(i))
            {
                return Shows.Text;
            }
            if (folds[i])
            {
                return Shows.Folded;
            }
            return live && BodyOf(i) == Body.Html ? Shows.Web : Shows.Text;
        }

        /// <summary>The index of <paramref name="id"/>'s card, if the case has one.</summary>
        public int? IndexOf(MessageId id)
        {
            for (var i = 0; i < Members.Count; i++)
            {
                if (Members[i].Id == id)
                {
                    return i;
                }
            }
            return null;
        }

        // Changes

        /// <summary>Card <paramref name="i"/> asked for its body.</summary>
        public void Asked(int i)
        {
            if (Valid(i) && BodyOf(i) == Body.Unknown)
            {
                bodies[Members[i]] = Body.Asked;
            }
        }

        /// <summary>
        /// The body of card <paramref name="i"/> arrived (or changed): HTML or
        /// not. Returns the cards the limit folded back, for the view to fold.
        /// <paramref name="html"/> false for a card that cannot show HTML after
        /// all (its web view is unavailable) takes it out of the count at once.
        /// </summary>
        public IReadOnlyList<int> Answered(int i, bool html)
        {
            if (!Valid(i))
            {
                return [];
            }
            bodies[Members[i]] = html ? Body.Html : Body.Text;
            return html && !folds[i] ? Enforce(i) : [];
        }

        /// <summary>
        /// The user folds or opens card <paramref name="i"/> (never the
        /// newest). Returns the cards the limit folded back: opening a card
        /// whose body is known to be HTML may push one out.
        /// </summary>
        public IReadOnlyList<int> SetFolded(int i, bool folded)
        {
            if (!Foldable(i) || folds[i] == folded)
            {
                return [];
            }
            folds[i] = folded;
            var m = Members[i];
            opened.RemoveAll(x => x == m);
            if (folded)
            {
                return [];
            }
            opened.Add(m);
            // A card whose HTML is still to come counts once it arrives.
            return BodyOf(i) == Body.Html ? Enforce(i) : [];
        }

        /// <summary>A copy to compare with later (Swift's value semantics).</summary>
        public ConversationCards Clone()
        {
            var c = new ConversationCards(MaxLive) { CaseId = CaseId, Members = Members };
            c.folds.AddRange(folds);
            foreach (var (m, b) in bodies)
            {
                c.bodies[m] = b;
            }
            c.opened.AddRange(opened);
            return c;
        }

        /// <inheritdoc/>
        public bool Equals(ConversationCards? other) =>
            other is not null && MaxLive == other.MaxLive && CaseId == other.CaseId && Members.SequenceEqual(other.Members)
            && folds.SequenceEqual(other.folds) && opened.SequenceEqual(other.opened) && bodies.Count == other.bodies.Count
            && bodies.All(kv => other.bodies.TryGetValue(kv.Key, out var b) && b == kv.Value);

        /// <inheritdoc/>
        public override bool Equals(object? obj) => Equals(obj as ConversationCards);

        /// <inheritdoc/>
        public override int GetHashCode() => HashCode.Combine(MaxLive, CaseId, Members.Count);

        private bool Valid(int i) => i >= 0 && i < Members.Count;

        // Folds the open HTML cards beyond MaxLive, the least recently opened
        // first (one never opened by the user counts as older than any
        // opened, ties by position), sparing the newest and the protected one.
        private List<int> Enforce(int protectedCard)
        {
            var folded = new List<int>();
            var web = WebCards.ToList();
            while (web.Count > MaxLive)
            {
                var victim = -1;
                var rank = 0;
                foreach (var i in web)
                {
                    if (i == Newest || i == protectedCard)
                    {
                        continue;
                    }
                    var r = opened.IndexOf(Members[i]);
                    // web is in index order, so equal recency prefers the first.
                    if (victim < 0 || r < rank)
                    {
                        victim = i;
                        rank = r;
                    }
                }
                if (victim < 0)
                {
                    break;
                }
                folds[victim] = true;
                opened.RemoveAll(x => x == Members[victim]);
                folded.Add(victim);
                web.Remove(victim);
            }
            folded.Sort();
            return folded;
        }

        /// <summary>One card: the message (null for the samples) and the excerpt shown for it.</summary>
        /// <param name="Id">The message.</param>
        /// <param name="Text">The excerpt.</param>
        public sealed record Member(MessageId? Id, string Text);

        /// <summary>What decides whether a refresh of the board touches the cards: the case and its members.</summary>
        /// <param name="CaseId">The case.</param>
        /// <param name="Members">Its members, oldest first.</param>
        public sealed record Key(BoardCaseId CaseId, IReadOnlyList<Member> Members)
        {
            /// <inheritdoc/>
            public bool Equals(Key? other) => other is not null && CaseId == other.CaseId && Members.SequenceEqual(other.Members);

            /// <inheritdoc/>
            public override int GetHashCode() => HashCode.Combine(CaseId, Members.Count);
        }

        /// <summary>What <see cref="Apply"/> changed, for the view.</summary>
        public abstract record Change
        {
            // Only the cases below derive from it.
            private Change()
            {
            }

            /// <summary>Nothing: no card is made, moved or let go.</summary>
            public sealed record None : Change;

            /// <summary>Another case (or the first): every card is made anew.</summary>
            public sealed record Reset : Change;

            /// <summary>
            /// The same case with other members: <paramref name="Kept"/>[i] is
            /// the index in the previous members of the card that stays as
            /// member i (its view and web view with it); a member without an
            /// entry gets a new card, a previous card no entry names goes.
            /// <paramref name="Reload"/> lists the open kept cards whose excerpt
            /// changed: their body is fetched again and shown in place, the
            /// previous one staying meanwhile.
            /// </summary>
            /// <param name="Kept">New index to old index.</param>
            /// <param name="Reload">The cards to fetch again.</param>
            public sealed record Members(IReadOnlyDictionary<int, int> Kept, IReadOnlyList<int> Reload) : Change
            {
                /// <inheritdoc/>
                public bool Equals(Members? other) =>
                    other is not null && Kept.Count == other.Kept.Count
                    && Kept.All(kv => other.Kept.TryGetValue(kv.Key, out var v) && v == kv.Value)
                    && Reload.SequenceEqual(other.Reload);

                /// <inheritdoc/>
                public override int GetHashCode() => HashCode.Combine(Kept.Count, Reload.Count);
            }
        }
    }
}
