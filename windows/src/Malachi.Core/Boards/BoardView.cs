// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/BoardView.swift (view,
// resolveSelection, selectionAfterDone, dueGroup, dayDifference,
// dueGroups, Scope, Context); GTK: ui/internal/board/view.go (View,
// ResolveSelection, SelectionAfterDone, DueGroupOf, NeedsYouToday,
// dayDifference, dueSections, scope, newer, viewContext, newestMessages,
// countText, sentences).
//
// The board's view model: a pure function from a snapshot, the view state
// (style, filters, selection) and the date to everything the three styles
// show — the list's sections, the columns, the Today page, the navigation
// column and the detail of the selected case. Every string of a case is
// cleaned here (CleanLine, CleanBlock), so the views only lay out plain
// text. The selection rules live here too, so the controller and the tests
// share them. Swift's Calendar is a culture (the names of months and days,
// the current one when null) and a time zone (what a day is, the local one
// when null). A deadline's day other than today or tomorrow is the list's
// date (Format.FormatDate), as Go writes it; Swift formats the same two
// strftime msgids itself. Ids compare by code point, as Go's strings do.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Model;
using DateFormat = Malachi.Core.Text.Format;

namespace Malachi.Core.Boards;

public static partial class Board
{
    /// <summary>The view model of snapshot <paramref name="s"/> for the view state <paramref name="v"/> at <paramref name="now"/>.</summary>
    public static ViewModel View(
        Snapshot s, ViewState v, DateTimeOffset now, CultureInfo? culture = null, TimeZoneInfo? timeZone = null)
    {
        ArgumentNullException.ThrowIfNull(s);
        ArgumentNullException.ThrowIfNull(v);
        var scope = new Scope(s, v);
        var ctx = new Context(s, now, culture ?? CultureInfo.CurrentCulture, timeZone ?? TimeZoneInfo.Local);

        // The columns, and their counts for the navigation and the tiles.
        var columns = States.Select(st => new Column
        {
            State = st,
            Title = Text.StateName(st),
            Rows = [.. scope.Live.Where(c => scope.StateOf(c) == st).Select(ctx.Row)],
            EmptyText = Text.ColumnEmpty(st),
        }).ToList();
        int Count(State st) => columns.FirstOrDefault(c => c.State == st)?.Rows.Count ?? 0;

        // The list: the columns' rows under the filter.
        var sections = new List<Section>();
        if (v.Filter.Kind == FilterKind.Snoozed)
        {
            if (scope.Snoozed.Count > 0)
            {
                sections.Add(new Section { Kind = SectionKind.Snoozed, Title = Text.Snoozed, Rows = [.. scope.Snoozed.Select(ctx.Row)] });
            }
        }
        else if (v.Filter.Kind == FilterKind.Done)
        {
            if (scope.Finished.Count > 0)
            {
                sections.Add(new Section { Kind = SectionKind.Done, Title = Text.Done, Rows = [.. scope.Finished.Select(ctx.Row)] });
            }
        }
        else
        {
            foreach (var col in columns)
            {
                if (col.Rows.Count > 0 && (v.Filter.Kind == FilterKind.All || v.Filter.State == col.State))
                {
                    sections.Add(new Section { Kind = SectionKind.State, State = col.State, Title = col.Title, Rows = col.Rows });
                }
            }
        }

        // The commitments: of live cases in the scope, when annotations
        // count; the first Cap.Commitments shown, all of them counted.
        var commitments = new List<CommitmentRow>();
        var commitmentCount = 0;
        if (s.Annotated)
        {
            var live = new Dictionary<BoardCaseId, Case>();
            foreach (var c in scope.Live)
            {
                live.TryAdd(c.Id, c);
            }
            foreach (var k in s.Commitments)
            {
                if (k.State != CommitmentState.Open || !live.TryGetValue(k.CaseId, out var c))
                {
                    continue;
                }
                commitmentCount++;
                if (commitments.Count >= Cap.Commitments)
                {
                    continue;
                }
                var from = ctx.Titled(c);
                commitments.Add(new CommitmentRow
                {
                    Id = k.Id,
                    CaseId = c.Id,
                    Text = CleanLine(k.Text, Cap.Commitment),
                    Quote = CleanLine(k.Quote, Cap.Quote),
                    Due = k.Due is { } due ? ctx.DueLabel(due) : "",
                    From = from.Text,
                    FromIsAssistant = from.Assistant,
                    SpokenFrom = from.Spoken,
                });
            }
        }

        // The navigation column.
        var nav = new List<NavItem> { new(Filter.All, Text.FilterTitle(Filter.All), null, scope.Live.Count, v.Filter == Filter.All) };
        foreach (var st in States)
        {
            var f = Filter.Of(st);
            nav.Add(new NavItem(f, Text.FilterTitle(f), st, Count(st), v.Filter == f));
        }
        nav.Add(new NavItem(Filter.Snoozed, Text.FilterTitle(Filter.Snoozed), null, scope.Snoozed.Count, v.Filter == Filter.Snoozed));
        nav.Add(new NavItem(Filter.Done, Text.FilterTitle(Filter.Done), null, scope.Finished.Count, v.Filter == Filter.Done));

        int LiveIn(AccountId? account) =>
            scope.Unique.Count(c => c.Visibility.IsLive && (account is null || c.Account == account));
        var accounts = new List<AccountItem> { new(null, Text.AllAccounts, "", LiveIn(null), v.Account is null) };
        foreach (var a in s.Accounts)
        {
            var name = CleanLine(a.Name, Cap.Account);
            var badge = CleanLine(a.Badge, Cap.Badge);
            accounts.Add(new AccountItem(a.Id, name, badge, LiveIn(a.Id), v.Account == a.Id)
            {
                Label = Text.TitleWithBadge(name, badge),
            });
        }
        var accountTitle = v.Account is { } accountId ? ctx.AccountName(accountId) : Text.AllAccounts;

        // The Today page.
        var hot = columns[0].Rows;
        var you = columns[1].Rows;
        var tiles = States.Select(st => new Tile(TileKind.State, st, Count(st), Text.StateName(st))).ToList();
        if (s.Annotated)
        {
            tiles.Add(new Tile(TileKind.Commitments, default, commitmentCount, Text.Commitments));
        }
        tiles = [.. tiles.Select(t => t with { ToolTip = Text.TileToolTip(t) })];
        var todo = scope.Live.Count(c => scope.StateOf(c) is State.Hot or State.You && NeedsYouToday(c, s.Annotated, now, ctx.Zone));
        var today = new Today
        {
            Title = Text.StyleTitle(BoardStyle.Today),
            Phrase = Text.TodoPhrase(todo),
            Tiles = tiles,
            Hot = hot,
            You = [.. you.Take(YouTopCount)],
            YouMore = int.Max(0, you.Count - YouTopCount),
            Commitments = commitments,
            DueGroups = DueGroupsOf(scope, ctx),
            DueEmpty = Text.DueEmpty,
        };

        // The selection and its detail.
        var selection = scope.Resolve(v.Selection);
        var detail = selection is { } sel && scope.Unique.FirstOrDefault(c => c.Id == sel) is { } selected
            ? ctx.Detail(selected)
            : null;

        return new ViewModel
        {
            Phase = s.Phase,
            EmptyTitle = Text.EmptyTitleOf(s.Phase),
            EmptyBody = Text.EmptyBodyOf(s.Phase),
            Notice = Text.Notice(s.Phase, s.Truncated),
            Triage = s.Triage,
            Run = s.Run,
            Nav = nav,
            Accounts = accounts,
            AccountTitle = accountTitle,
            Subtitle = accountTitle + Text.Separator + Text.CaseCount(scope.Live.Count),
            IsEmpty = scope.Live.Count == 0 && scope.Finished.Count == 0 && scope.Snoozed.Count == 0,
            Sections = sections,
            SectionsEmptyText = Text.SectionEmpty,
            Commitments = commitments,
            ShowsCommitmentsInList = commitments.Count > 0 && v.Filter.Kind == FilterKind.All,
            Columns = columns,
            Today = today,
            Detail = detail,
            ShowsPanel = selection is not null && (v.Style != BoardStyle.List || !v.InlineDetail),
            StatusLine = Text.StatusLine(s.Annotated, s.Run),
            AssistantOn = s.Annotated,
            Selection = selection,
        };
    }

    /// <summary>
    /// The selection the board shows: <see cref="ViewState.Selection"/>
    /// while that case is shown (in the list: under its filter; in Columns
    /// and Today: live in the account scope), else none — except that the
    /// list with its detail beside it selects its first row.
    /// </summary>
    public static BoardCaseId? ResolveSelection(Snapshot s, ViewState v)
    {
        ArgumentNullException.ThrowIfNull(s);
        ArgumentNullException.ThrowIfNull(v);
        return new Scope(s, v).Resolve(v.Selection);
    }

    /// <summary>
    /// The selection after the case <paramref name="id"/> leaves the list
    /// (done, reopened or moved out of the filter), computed on the snapshot
    /// from before: the next row, else the previous one. Columns and Today
    /// select nothing; null also when <paramref name="id"/> is not in the list.
    /// </summary>
    public static BoardCaseId? SelectionAfterDone(BoardCaseId id, Snapshot s, ViewState v)
    {
        ArgumentNullException.ThrowIfNull(s);
        ArgumentNullException.ThrowIfNull(v);
        if (v.Style != BoardStyle.List)
        {
            return null;
        }
        var shown = new Scope(s, v).Shown();
        var i = shown.IndexOf(id);
        if (i < 0)
        {
            return null;
        }
        if (i + 1 < shown.Count)
        {
            return shown[i + 1];
        }
        return i > 0 ? shown[i - 1] : (BoardCaseId?)null;
    }

    /// <summary>
    /// The deadline group of <paramref name="due"/>, by calendar days in
    /// <paramref name="timeZone"/> (the local one when null) from
    /// <paramref name="now"/>: before today, today, tomorrow, the rest of the
    /// next seven days, later (Swift <c>dueGroup</c>).
    /// </summary>
    public static DueGroupKind DueGroupOf(DateTimeOffset due, DateTimeOffset now, TimeZoneInfo? timeZone = null)
    {
        var days = DayDifference(now, due, timeZone ?? TimeZoneInfo.Local);
        return days switch
        {
            < 0 => DueGroupKind.Overdue,
            0 => DueGroupKind.Today,
            1 => DueGroupKind.Tomorrow,
            <= 7 => DueGroupKind.ThisWeek,
            _ => DueGroupKind.Later,
        };
    }

    /// <summary>
    /// Whether the Today page's phrase counts case <paramref name="c"/> (of
    /// the hot cases and those waiting for the user): new since yesterday's
    /// midnight in <paramref name="timeZone"/> (its latest activity), due
    /// today (its annotation counts and its deadline is today), or back from
    /// a reminder.
    /// </summary>
    public static bool NeedsYouToday(Case c, bool annotated, DateTimeOffset now, TimeZoneInfo? timeZone = null)
    {
        ArgumentNullException.ThrowIfNull(c);
        var zone = timeZone ?? TimeZoneInfo.Local;
        if (c.Reminded)
        {
            return true;
        }
        if (DayDifference(c.Date, now, zone) <= 1)
        {
            return true;
        }
        return AnnotationOf(c, annotated) is { Due: { } due } && DayDifference(now, due, zone) == 0;
    }

    // How many calendar days in zone lie from a's day to b's.
    internal static int DayDifference(DateTimeOffset a, DateTimeOffset b, TimeZoneInfo zone) =>
        (int)(LocalDate(b, zone) - LocalDate(a, zone)).TotalDays;

    // The Today page's deadlines: of live cases in the scope whose annotation
    // counts, soonest first.
    private static List<DueGroup> DueGroupsOf(Scope scope, Context ctx)
    {
        if (!scope.Snapshot.Annotated)
        {
            return [];
        }
        var dated = new List<(Case Case, Annotation Annotation, DateTimeOffset Due)>();
        foreach (var c in scope.Live)
        {
            if (AnnotationOf(c, true) is { Due: { } due } a)
            {
                dated.Add((c, a, due));
            }
        }
        dated.Sort((x, y) =>
        {
            var d = x.Due.CompareTo(y.Due);
            return d != 0 ? d : CompareIds(x.Case.Id, y.Case.Id);
        });
        var groups = new Dictionary<DueGroupKind, List<DueItem>>();
        foreach (var (c, a, due) in dated)
        {
            var kind = DueGroupOf(due, ctx.Now, ctx.Zone);
            var title = ctx.Titled(c);
            if (!groups.TryGetValue(kind, out var items))
            {
                items = [];
                groups[kind] = items;
            }
            items.Add(new DueItem
            {
                CaseId = c.Id,
                Label = ctx.DueLabel(due),
                Title = title.Text,
                TitleIsAssistant = title.Assistant,
                SpokenTitle = title.Spoken,
                Person = CleanLine(c.Person, Cap.Person),
                Quote = CleanLine(a.DueQuote, Cap.Quote),
            });
        }
        return [.. Enum.GetValues<DueGroupKind>()
            .Where(groups.ContainsKey)
            .Select(k => new DueGroup { Kind = k, Title = Text.DueGroupTitle(k), Items = groups[k] })];
    }

    // Go's cmp.Compare of two ids: by code point.
    private static int CompareIds(BoardCaseId a, BoardCaseId b) => CodePoints.Compare(a.Value ?? "", b.Value ?? "");

    // The newest first, then by id.
    private static int Newer(Case a, Case b)
    {
        var d = b.Date.CompareTo(a.Date);
        return d != 0 ? d : CompareIds(a.Id, b.Id);
    }

    // The cases in the account scope, ordered, and what the list shows.
    private sealed class Scope
    {
        public Scope(Snapshot s, ViewState v)
        {
            Snapshot = s;
            View = v;
            var seen = new HashSet<BoardCaseId>();
            foreach (var c in s.Cases)
            {
                if (!seen.Add(c.Id))
                {
                    continue;
                }
                Unique.Add(c);
                if (v.Account is { } account && c.Account != account)
                {
                    continue;
                }
                switch (c.Visibility.Kind)
                {
                    case VisibilityKind.Live:
                        Live.Add(c);
                        break;
                    case VisibilityKind.Done:
                        Finished.Add(c);
                        break;
                    case VisibilityKind.Snoozed:
                        Snoozed.Add(c);
                        break;
                }
            }
            Live.Sort((a, b) =>
            {
                var d = StateOf(a).CompareTo(StateOf(b));
                if (d != 0)
                {
                    return d;
                }
                // Back from a reminder first in its state.
                if (a.Reminded != b.Reminded)
                {
                    return a.Reminded ? -1 : 1;
                }
                return Newer(a, b);
            });
            Finished.Sort(Newer);
            Snoozed.Sort((a, b) =>
            {
                var d = Nullable.Compare(a.Visibility.RemindAt, b.Visibility.RemindAt);
                return d != 0 ? d : Newer(a, b);
            });
        }

        public Snapshot Snapshot { get; }

        public ViewState View { get; }

        // The snapshot's cases, the first of each id only: a source that
        // repeats an id must not give two rows one identity.
        public List<Case> Unique { get; } = [];

        // On the board, in the account scope: by state, back from a reminder
        // first, then newest first.
        public List<Case> Live { get; } = [];

        // Done, in the account scope: newest first.
        public List<Case> Finished { get; } = [];

        // Snoozed, in the account scope: the soonest back first.
        public List<Case> Snoozed { get; } = [];

        public State StateOf(Case c) => Board.StateOf(c, Snapshot.Annotated);

        // The ids the current style shows, in order: the list's rows under
        // its filter, or every live case for Columns and Today.
        public List<BoardCaseId> Shown()
        {
            if (View.Style != BoardStyle.List || View.Filter.Kind == FilterKind.All)
            {
                return [.. Live.Select(c => c.Id)];
            }
            if (View.Filter.Kind == FilterKind.State)
            {
                return [.. Live.Where(c => StateOf(c) == View.Filter.State).Select(c => c.Id)];
            }
            if (View.Filter.Kind == FilterKind.Snoozed)
            {
                return [.. Snoozed.Select(c => c.Id)];
            }
            return [.. Finished.Select(c => c.Id)];
        }

        public BoardCaseId? Resolve(BoardCaseId? selection)
        {
            var shown = Shown();
            if (selection is { } sel && shown.Contains(sel))
            {
                return sel;
            }
            return View.Style == BoardStyle.List && View.InlineDetail && shown.Count > 0 ? shown[0] : (BoardCaseId?)null;
        }
    }

    // A case's title: its text, whether it is the assistant's, and how the
    // screen reader says it.
    private readonly record struct Titled(string Text, bool Assistant, string Spoken);

    // What every row and detail is built with: the accounts, the date and
    // how to write it.
    private sealed class Context
    {
        private readonly Snapshot snapshot;
        private readonly CultureInfo culture;
        private readonly Dictionary<AccountId, AccountInfo> accounts = [];

        public Context(Snapshot snapshot, DateTimeOffset now, CultureInfo culture, TimeZoneInfo zone)
        {
            this.snapshot = snapshot;
            Now = now;
            this.culture = culture;
            Zone = zone;
            foreach (var a in snapshot.Accounts)
            {
                accounts.TryAdd(a.Id, a);
            }
        }

        public DateTimeOffset Now { get; }

        public TimeZoneInfo Zone { get; }

        private bool Annotated => snapshot.Annotated;

        public string AccountName(AccountId id) =>
            CleanLine(accounts.TryGetValue(id, out var a) ? a.Name : "", Cap.Account);

        // The case's subject, cleaned; "(No subject)" for none.
        private static string Subject(Case c)
        {
            var s = CleanLine(c.Subject, Cap.Title);
            return s.Length > 0 ? s : L10n.T("(No subject)");
        }

        // The assistant's title when its annotation counts, else the subject.
        public Titled Titled(Case c)
        {
            if (Annotation(c) is { } a)
            {
                var t = CleanLine(a.Title, Cap.Title);
                if (t.Length > 0)
                {
                    return new Titled(t, true, Text.SpokenAssistant(t));
                }
            }
            var s = Subject(c);
            return new Titled(s, false, s);
        }

        // The annotation, when it counts (AnnotationOf).
        private Annotation? Annotation(Case c) => AnnotationOf(c, Annotated);

        // "Tomorrow at 09:00", "20 Oct at 09:00": when a snoozed case comes
        // back.
        private string RemindLabel(DateTimeOffset at) => Text.DayAndTime(DueLabel(at), DateFormat.FormatTime(at, culture, Zone));

        // A case's badges' texts: Reminded, New contact.
        private static List<string> Badges(Case c)
        {
            var badges = new List<string>(2);
            if (c.Reminded)
            {
                badges.Add(Text.Reminded);
            }
            if (c.NewContact)
            {
                badges.Add(Text.NewContact);
            }
            return badges;
        }

        // A deadline's day: Today, Tomorrow, else the list's date (in this
        // branch never today, so never a time).
        public string DueLabel(DateTimeOffset due) => DueGroupOf(due, Now, Zone) switch
        {
            DueGroupKind.Today => Text.DueGroupTitle(DueGroupKind.Today),
            DueGroupKind.Tomorrow => Text.DueGroupTitle(DueGroupKind.Tomorrow),
            _ => DateFormat.FormatDate(due, Now, culture, Zone),
        };

        public Row Row(Case c)
        {
            var st = StateOf(c, Annotated);
            var person = CleanLine(c.Person, Cap.Person);
            var title = Titled(c);
            var a = Annotation(c);
            var snippet = CleanLine(a?.Summary, Cap.Snippet);
            var snippetIsAssistant = snippet.Length > 0;
            if (snippet.Length == 0)
            {
                snippet = CleanLine(c.Snippet, Cap.Snippet);
            }
            var issueKey = CleanLine(c.Issue?.Key, Cap.IssueKey);
            var issueStatus = CleanLine(c.Issue?.Status, Cap.Status);
            var due = a?.Due is { } d ? DueLabel(d) : "";
            var n = int.Max(1, c.MessageCount);
            var remind = c.Visibility.RemindAt is { } at ? RemindLabel(at) : "";

            var badges = Badges(c);
            var spoken = new List<string> { Text.StateName(st) };
            spoken.AddRange(badges);
            spoken.Add(person);
            spoken.Add(title.Spoken);
            if (due.Length > 0)
            {
                spoken.Add(Text.SpokenDue(due));
            }
            if (issueKey.Length > 0)
            {
                spoken.Add(issueStatus.Length == 0 ? issueKey : issueKey + ", " + issueStatus);
            }
            if (n > 1)
            {
                spoken.Add(Text.MessageCount(n));
            }
            if (remind.Length > 0)
            {
                spoken.Add(Text.SpokenRemind(remind));
            }
            if (c.HasAttachments)
            {
                spoken.Add(Text.SpokenAttachments);
            }
            if (c.Unread)
            {
                spoken.Add(Text.SpokenUnread);
            }
            return new Row
            {
                Id = c.Id,
                State = st,
                Person = person,
                Time = DateFormat.FormatDate(c.Date, Now, culture, Zone),
                Title = title.Text,
                TitleIsAssistant = title.Assistant,
                Snippet = snippet,
                SnippetIsAssistant = snippetIsAssistant,
                Account = AccountName(c.Account),
                IssueKey = issueKey,
                IssueStatus = issueStatus,
                IssueStyle = c.Issue?.Style ?? JiraStatusStyle.Plain,
                Due = due,
                DueOverdue = a?.Due is { } od && DueGroupOf(od, Now, Zone) == DueGroupKind.Overdue,
                Remind = remind,
                Reminded = c.Reminded,
                NewContact = c.NewContact,
                Badges = badges,
                Attachments = c.HasAttachments,
                CountText = DateFormat.ThreadCountText(n),
                Unread = c.Unread,
                Spoken = Sentences(spoken),
            };
        }

        public Detail Detail(Case c)
        {
            var st = StateOf(c, Annotated);
            var source = StateSourceOf(c, Annotated);
            var a = Annotation(c);
            var title = Titled(c);
            var subject = Subject(c);

            var why = CleanLine(a?.Why, Cap.Reason);
            var whyIsAssistant = why.Length > 0;
            if (why.Length == 0)
            {
                why = Text.Reason(c.RuleReason);
            }
            // The summary box is the assistant's: no snippet stands in for
            // it (the row's snippet does fall back, and the conversation
            // shows the text anyway).
            var summary = CleanBlock(a?.Summary, Cap.Summary);
            var issue = c.Issue is { } i
                ? new IssueInfo(CleanLine(i.Key, Cap.IssueKey), CleanLine(i.Status, Cap.Status), i.Style)
                : null;
            var tasks = (a?.Tasks ?? []).Take(Cap.TaskScan).Select(t => CleanLine(t, Cap.Task))
                .Where(t => t.Length > 0).Take(Cap.Tasks).ToList();
            var messages = Newest(c.Messages ?? [], Cap.Messages).Select(m => new MessageCard(
                m.Id, m.Mine ? Text.You : CleanLine(m.From, Cap.Person), DateFormat.FormatDate(m.Date, Now, culture, Zone),
                CleanBlock(m.Text, Cap.Message), m.Mine)).ToList();
            var messagesNote = c.Messages is not null ? "" : c.MessagesFailed ? Text.MessagesFailed : Text.MessagesLoading;
            var stale = Annotated && c.Annotation is { Stale: true };
            var whyNotes = new List<string>(2);
            if (c.Reminded)
            {
                whyNotes.Add(Text.ReasonReminded);
            }
            if (c.UserState is not null)
            {
                whyNotes.Add(Text.ReasonUserKeeps);
            }
            var person = CleanLine(c.Person, Cap.Person);
            var when = DateFormat.FormatDateTime(c.Date, culture, Zone);
            return new Detail
            {
                Id = c.Id,
                AccountId = c.Account,
                Thread = c.Thread,
                Reply = c.Reply,
                LatestMessage = c.LatestMessage,
                State = st,
                StateTitle = Text.StateName(st),
                Source = source,
                Why = why,
                WhyNotes = whyNotes,
                WhyIsAssistant = whyIsAssistant,
                SourceText = Text.SourceText(source, snapshot.Run),
                Account = AccountName(c.Account),
                Issue = issue,
                Person = person,
                Time = when,
                Byline = Text.PersonAndTime(person, when),
                Reminded = c.Reminded,
                NewContact = c.NewContact,
                Badges = Badges(c),
                Title = title.Text,
                TitleIsAssistant = title.Assistant,
                SpokenTitle = title.Spoken,
                Subject = title.Text == subject ? "" : subject,
                Due = a?.Due is { } due ? DueLabel(due) : "",
                DueQuote = a?.Due is null ? "" : CleanLine(a.DueQuote, Cap.Quote),
                Summary = summary,
                Tasks = tasks,
                Draft = CleanBlock(c.Draft?.Text, Cap.Draft),
                DraftId = c.Draft?.Id,
                CanUnstar = CanUnstar(c),
                StaleNote = stale ? Text.StaleNotes : "",
                IsDone = c.Visibility.IsDone,
                IsSnoozed = c.Visibility.RemindAt is not null,
                RemindText = c.Visibility.RemindAt is { } at ? Text.SnoozedUntil(RemindLabel(at)) : "",
                CanArchive = c.CanArchive,
                ConversationTitle = Text.Conversation(int.Max(1, c.MessageCount)),
                Messages = messages,
                MessagesLoading = c.Messages is null && !c.MessagesFailed,
                MessagesNote = messagesNote,
                MessagesRetry = c.Messages is null && c.MessagesFailed,
            };
        }

        // The newest n of ms, oldest first, as a stable sort by date would
        // leave them (of equal dates, the later in ms is newer), without
        // sorting all of ms.
        private static List<CaseMessage> Newest(IReadOnlyList<CaseMessage> ms, int n)
        {
            if (n <= 0 || ms.Count == 0)
            {
                return [];
            }
            // Indices into ms, ordered by date and index.
            var kept = new List<int>(int.Min(n, ms.Count));
            for (var i = 0; i < ms.Count; i++)
            {
                var d = ms[i].Date;
                if (kept.Count == n)
                {
                    if (d < ms[kept[0]].Date)
                    {
                        continue;
                    }
                    kept.RemoveAt(0);
                }
                var j = kept.Count;
                while (j > 0 && ms[kept[j - 1]].Date > d)
                {
                    j--;
                }
                kept.Insert(j, i);
            }
            return [.. kept.Select(k => ms[k])];
        }

        // The parts as sentences: each ends with a full stop unless it ends
        // with punctuation already; empty parts go.
        private static string Sentences(List<string> parts) =>
            string.Join(" ", parts.Where(p => p.Length > 0).Select(p =>
                p.EndsWith('.') || p.EndsWith('?') || p.EndsWith('!') || p.EndsWith('…') ? p : p + "."));
    }
}
