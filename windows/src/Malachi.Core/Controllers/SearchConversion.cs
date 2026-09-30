// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/SearchConversion.swift
// (SearchConversion: today, running, convert, cancel, outcome); GTK:
// ui/internal/assistantpanel/oneshot.go (Searcher, NewSearcher, Today,
// Running, Convert, Cancel, searchOutcome). Swift's completion is an
// Action parameter. Today's default is AssistantPanelController.LocalDate
// (Swift's localDate) on the request's clock; the failure of words that
// are too long is reported after the caller's turn, as Swift's Task does,
// through the controller infrastructure (docs/windows-port.md §7.2).
// Dispose (the window closed: Cancel, and that failure dropped) is Windows'
// own.

using System;
using System.Threading.Tasks;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers.Infrastructure;

namespace Malachi.Core.Controllers;

/// <summary>
/// The search in the user's own words (the In App target) without the
/// search field: the typed words go to the user's Claude Code
/// (<see cref="Assistant.SearchMessage"/> under
/// <see cref="Assistant.SearchSystemPrompt"/>, the answer shaped by
/// <see cref="Assistant.SearchSchema"/>, one <see cref="AssistantRequest"/>),
/// and the query of its answer (<see cref="Assistant.ParseSearchQuery"/> of
/// the result's <c>structured_output</c>, or of its text when Claude Code
/// gave none) is what the field then searches for.
/// </summary>
/// <remarks>
/// A failure is the toast "The search could not be converted: %s" with the
/// reason (<see cref="Assistant.SearchFailedText"/>); the caller keeps the
/// typed words. A new conversion cancels the one under way;
/// <see cref="Cancel"/> ends it, and its completion is not called. Call it
/// on the UI thread.
/// </remarks>
public sealed partial class SearchConversion : IDisposable
{
    private readonly ControllerScope scope;

    /// <summary>A conversion through <paramref name="request"/> (its consent hook is the window's).</summary>
    /// <param name="request">The request that asks.</param>
    /// <param name="pending">Counts the deferred failure of <see cref="Convert"/>; one of its own when null.</param>
    public SearchConversion(AssistantRequest request, PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(request);
        Request = request;
        scope = new ControllerScope(pending);
        Today = () => AssistantPanelController.LocalDate(request.Time);
    }

    /// <summary>The conversion's request.</summary>
    public AssistantRequest Request { get; }

    /// <summary>The date for the system prompt, YYYY-MM-DD: <see cref="AssistantPanelController.LocalDate"/> on the request's clock by default.</summary>
    public Func<string> Today { get; set; }

    /// <summary>A conversion is under way.</summary>
    public bool Running => Request.Running;

    /// <summary>
    /// The query of an answer, or the toast (Swift <c>outcome</c>, GTK
    /// <c>searchOutcome</c>).
    /// </summary>
    public static Outcome OutcomeOf(AssistantRequest.Outcome outcome)
    {
        ArgumentNullException.ThrowIfNull(outcome);
        switch (outcome)
        {
            case AssistantRequest.Outcome.Answered answered:
                var query = (answered.Structured is { } structured ? Assistant.ParseSearchQuery(structured) : null)
                    ?? Assistant.ParseSearchQuery(Assistant.Utf8(answered.Text));
                return query is not null
                    ? new Outcome.Query(query)
                    : new Outcome.Failed(Assistant.SearchFailedText("the answer holds no query"));
            case AssistantRequest.Outcome.Failed failed:
                return new Outcome.Failed(Assistant.SearchFailedText(failed.Failure.Reason));
            default:
                return new Outcome.Declined();
        }
    }

    /// <summary>
    /// Asks for the query of <paramref name="words"/>. False, and nothing
    /// happens, when there are no words; otherwise
    /// <paramref name="completion"/> is called once, later (also for words
    /// that are too long), unless the conversion is cancelled.
    /// </summary>
    public bool Convert(string words, Action<Outcome> completion)
    {
        ArgumentNullException.ThrowIfNull(words);
        ArgumentNullException.ThrowIfNull(completion);
        string message;
        try
        {
            message = Assistant.SearchMessage(words);
        }
        catch (AssistantException e) when (e.Kind == AssistantError.NoWords)
        {
            return false;
        }
        catch (AssistantException e)
        {
            Request.Cancel();
            var failed = new Outcome.Failed(Assistant.SearchFailedText(e.Message));
            scope.Run(_ =>
            {
                if (!scope.IsClosed)
                {
                    scope.Guard(() => completion(failed));
                }
                return Task.CompletedTask;
            });
            return true;
        }
        Request.Start(
            Assistant.SearchSystemPrompt(Today()),
            message,
            outcome => completion(OutcomeOf(outcome)),
            jsonSchema: Assistant.SearchSchema);
        return true;
    }

    /// <summary>Ends the conversion under way; its completion is not called.</summary>
    public void Cancel() => Request.Cancel();

    /// <summary>
    /// Cancels the conversion and drops a failure not reported yet (the
    /// window closed); the request is the window's to close.
    /// </summary>
    public void Dispose()
    {
        Cancel();
        scope.Close();
    }
}
