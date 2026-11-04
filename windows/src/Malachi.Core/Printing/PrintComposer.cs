// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Printing/PrintController.swift (the
// Mail and Calendar parts: printMessages, isOwn, section, printCalendar,
// printEvent, printDay) and of the layout choice of
// MessageActionsController.printSelected; GTK: the GTK port follows
// (docs/plans/toolbar-customization.md §8). macOS keeps this in the AppKit
// target; here it is Core, testable over a FakeDaemon, and the app only
// prints what it builds (Malachi.App/Printing/PrintJob.cs).
//
// The print document of a selection, a conversation, a message window's
// message, a calendar event or a calendar day. A message's header comes
// from message.get through the cache, its body from message.body asked
// here without trimQuoted (the whole message, quoted history included):
// never through the cache's variant switch, which would change what the
// reader shows. The pictures are what the message shows by default (no
// remote-content override). Bcc only of the user's own messages (P6: a
// folder of role Sent, Drafts or Outbox). Every string goes through
// Print's escaping; a body is the sanitiser's HTML or its text in a <pre>.
// UI-thread-affine, as the cache it reads.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.Model;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Printing;

/// <summary>A print: the fragment the locked view loads, and the print job's title.</summary>
/// <param name="Fragment">The <see cref="Print.Fragment"/>.</param>
/// <param name="Title">The job's title (the subject, the event's title or the day's heading).</param>
public sealed record PrintDocument(string Fragment, string Title);

/// <summary>Builds what Print prints (macOS <c>PrintController</c>).</summary>
public sealed partial class PrintComposer
{
    private readonly MessageCache cache;
    private readonly Func<MessageSummary, FolderRole> role;
    private readonly ILogger logger;

    /// <param name="cache">The message cache (message.get, and its client for message.body).</param>
    /// <param name="role">The role of the folder a message lies in, for its Bcc (P6).</param>
    /// <param name="logger">The log.</param>
    public PrintComposer(MessageCache cache, Func<MessageSummary, FolderRole> role, ILogger? logger = null)
    {
        ArgumentNullException.ThrowIfNull(cache);
        ArgumentNullException.ThrowIfNull(role);
        this.cache = cache;
        this.role = role;
        this.logger = logger ?? NullLogger.Instance;
    }

    // Mail

    /// <summary>
    /// How the messages of a selection print (macOS <c>printSelected</c>):
    /// several rows each on a page of its own, one conversation row's
    /// members one after the other, one message alone.
    /// </summary>
    public static Print.Layout LayoutFor(int rows, int messages) =>
        rows > 1 ? Print.Layout.Pages : messages > 1 ? Print.Layout.Conversation : Print.Layout.One;

    /// <summary>Whether a message of a folder of <paramref name="role"/> is the user's own (P6: its Bcc prints).</summary>
    public static bool IsOwn(FolderRole role) => role == FolderRole.Sent || role == FolderRole.Drafts || role == FolderRole.Outbox;

    /// <summary>
    /// The print of <paramref name="messages"/> in <paramref name="layout"/>,
    /// at most <see cref="Print.MaxMessages"/>; a conversation oldest first.
    /// Null for none.
    /// </summary>
    public async Task<PrintDocument?> MessagesAsync(IReadOnlyList<MessageSummary> messages, Print.Layout layout, CancellationToken ct = default)
    {
        ArgumentNullException.ThrowIfNull(messages);
        IEnumerable<MessageSummary> taken = messages.Take(Print.MaxMessages);
        if (layout == Print.Layout.Conversation)
        {
            taken = taken.OrderBy(s => s.Date).ThenBy(s => s.Id.Value, StringComparer.Ordinal);
        }
        var list = taken.ToList();
        if (list.Count == 0)
        {
            return null;
        }
        var title = LoadedMessageText.SubjectText(list[0].Subject);
        var sections = new List<string>(list.Count);
        foreach (var s in list)
        {
            sections.Add(await SectionAsync(s, IsOwn(role(s)), ct));
        }
        return new PrintDocument(Print.Fragment(sections, layout), title);
    }

    // One message's section: the header of message.get (the cache's) and
    // the whole body; a message that cannot be fetched prints its summary
    // and snippet.
    private async Task<string> SectionAsync(MessageSummary s, bool own, CancellationToken ct)
    {
        var lm = await FetchAsync(s, ct);
        MessageBodyResult? body;
        try
        {
            body = await cache.Client.CallAsync(
                API.MessageBody,
                new MessageBodyParams { AccountId = s.AccountId, MessageId = s.Id, TrimQuoted = false },
                API.MessageBody.Timeout,
                ct);
        }
        catch (Exception e) when (e is RpcException or RpcClientException or TimeoutException)
        {
            LogBodyFailed(logger, e.GetType().Name);
            body = lm.Body;
        }
        return Print.Message(Print.HeaderRows(HeaderOf(s, lm.Msg, body, own)), BodyOf(body, s.Snippet));
    }

    // The cache calls back after each half (message.get, the body) and
    // again on later updates: the print takes the first answer with nothing
    // in flight any more, once. A null quoted keeps the variant the reader
    // shows.
    private Task<LoadedMessage> FetchAsync(MessageSummary s, CancellationToken ct)
    {
        var done = new TaskCompletionSource<LoadedMessage>(TaskCreationOptions.RunContinuationsAsynchronously);
        cache.Fetch(s, lm =>
        {
            if (!lm.Getting && !lm.Fetching)
            {
                done.TrySetResult(lm);
            }
        });
        return done.Task.WaitAsync(ct);
    }

    /// <summary>
    /// A message's printed header (P6): the summary of message.get, or of
    /// the list for a message it did not give; the attachments as the
    /// reader's tiles name them, with their sizes; the Bcc only of the
    /// user's own messages (<paramref name="own"/>).
    /// </summary>
    public static Print.Header HeaderOf(MessageSummary s, Message? m, MessageBodyResult? body, bool own)
    {
        ArgumentNullException.ThrowIfNull(s);
        var summary = m?.Summary ?? s;
        return new Print.Header
        {
            Subject = LoadedMessageText.SubjectText(summary.Subject),
            From = Format.FormatParticipants(summary.From),
            To = Format.FormatParticipants(summary.To ?? []),
            Cc = Format.FormatParticipants(m?.Cc ?? []),
            Bcc = own ? Format.FormatParticipants(m?.Bcc ?? []) : "",
            Date = summary.Date.IsGoZero ? "" : Format.FormatDateTime(summary.Date),
            Attachments = [.. AttachmentChips.ChipAttachments(m?.Attachments ?? [], body)
                .Select(a => AttachmentChips.ChipName(a) + " (" + Format.FormatSize(a.Size) + ")")],
        };
    }

    /// <summary>
    /// A message's printed body: the sanitiser's HTML of a fetched body with
    /// HTML that was not withheld, else its text (the snippet without a body).
    /// </summary>
    public static Print.Body BodyOf(MessageBodyResult? body, string snippet)
    {
        if (LoadedMessageText.ShowsHtml(body) && body!.HtmlWithheld != true && body.Html is { Length: > 0 } html)
        {
            return new Print.Body(Html: html);
        }
        return new Print.Body(Text: body?.Text ?? snippet);
    }

    // Calendar

    /// <summary>
    /// P8: the print of <paramref name="calendar"/>'s selected occurrence's
    /// event, else of the day its view stands on (a week's or a month's
    /// anchor). Null when the event cannot be read.
    /// </summary>
    public async Task<PrintDocument?> CalendarAsync(CalendarController calendar)
    {
        ArgumentNullException.ThrowIfNull(calendar);
        if (calendar.SelectedOccurrence is { EventId: { } id } o && !string.IsNullOrEmpty(id.Value))
        {
            return await EventAsync(calendar, id, o.RecurrenceId);
        }
        return Day(calendar, calendar.Anchor, DateTimeOffset.Now);
    }

    /// <summary>An event's print: its detail as the popover shows it. Null when it cannot be read.</summary>
    public async Task<PrintDocument?> EventAsync(CalendarController calendar, CalendarEventId id, DateOrTime? recurrenceId)
    {
        ArgumentNullException.ThrowIfNull(calendar);
        CalendarEvent e;
        try
        {
            e = await calendar.Source.EventAsync(id, recurrenceId, reload: false);
        }
        catch (Exception ex) when (ex is RpcException or RpcClientException or TimeoutException)
        {
            LogEventFailed(logger, ex.GetType().Name);
            return null;
        }
        var s = calendar.Source.Snapshot;
        var d = Cal.DetailOf(e, s.AccountName(e.AccountId), e.CalendarId is { } c ? s.CalendarName(c) : "", calendar.Env);
        return new PrintDocument(Print.Fragment([Print.EventSection(EventOf(d))], Print.Layout.One), d.Title);
    }

    /// <summary>
    /// An event's printed lines (macOS <c>printEvent</c>): the time,
    /// recurrence, location, calendar and people lines, the attendees as
    /// "name — response" (the address without a name, the name alone
    /// without a response) and the notes.
    /// </summary>
    public static Print.PrintedEvent EventOf(Cal.Detail d)
    {
        ArgumentNullException.ThrowIfNull(d);
        return new Print.PrintedEvent
        {
            Title = d.Title,
            Lines = [d.TimeLine, d.Recurrence, d.Location, d.CalendarLine, d.PeopleLine],
            Attendees = [.. d.Attendees.Select(a =>
            {
                var who = a.Name.Length == 0 ? a.Address : a.Name;
                return a.ResponseText.Length == 0 ? who : who + " — " + a.ResponseText;
            })],
            Notes = d.Notes,
        };
    }

    /// <summary>
    /// P7: <paramref name="day"/>'s print, its events as the agenda lists
    /// them (all-day first), the agenda's empty text for a day without any.
    /// </summary>
    public static PrintDocument Day(CalendarController calendar, DateTimeOffset day, DateTimeOffset now)
    {
        ArgumentNullException.ThrowIfNull(calendar);
        var env = calendar.Env;
        var agenda = Cal.Agenda(calendar.Occurrences, day, 1, now, env, calendar.ShowDeclined);
        IReadOnlyList<Cal.AgendaItem> items = agenda.Count > 0 ? agenda[0].Items : [];
        var heading = Cal.DayHeading(day, now, env);
        var rows = items.Select(it => new Print.DayItem(Cal.ItemTime(it, env), it.Occurrence.Title, it.Occurrence.Location ?? "")).ToList();
        return new PrintDocument(Print.Fragment([Print.DaySection(heading, rows, Cal.AgendaEmpty)], Print.Layout.One), heading);
    }

    [LoggerMessage(Level = LogLevel.Error, Message = "print: message.body failed: {Kind}")]
    private static partial void LogBodyFailed(ILogger logger, string kind);

    [LoggerMessage(Level = LogLevel.Error, Message = "print: event failed: {Kind}")]
    private static partial void LogEventFailed(ILogger logger, string kind);
}
