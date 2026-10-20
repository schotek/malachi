// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/QuickReplyController.swift
// (QuickReplyController, callTimeout, saveDraft); GTK:
// ui/internal/quickreply/controller.go (Controller, New, Show, Update,
// SetChatShown, SetText, SetMode, State, Send, SaveDraft, OpenWindow,
// Forward, CallTimeout). Swift's onChange is the event Changed, its
// keptThreads the property KeptThreads (Core has no InternalsVisibleTo).
//
// The quick-reply bar under a conversation shown as chat in Mail
// (docs/plans/chat-view.md §10 and §11) without its widgets: what the bar
// shows (its target, modes and text, per conversation) and what its buttons
// do — Send through the daemon's usual way, Open in a Window and Forward…
// through the compose window. The rules of the bar's content are
// Conversation's (QuickReplyTarget, QuickReplyModes, QuickReplyHtml,
// QuickReplyBlank, QuickReplyBody); the texts are its too and the view's
// (L10n): this file has none, the host says what happened (Sent, Failed)
// and the view says it in words. The window gives it an IQuickReplyHost over
// its client and its message actions (Malachi.App).
//
// Threads: everything but Send's calls runs on the UI thread. The calls run
// in a task of their own (after Task.Yield, as every fire-and-forget path of
// the controllers starts, docs/windows-port.md §7) and touch nothing of the
// controller; their outcome comes back through IQuickReplyHost.Post, as Go's
// goroutine does through Host.Post. No log line: what fails is the host's to
// say (Failed).

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Malachi.Core.Platform;

namespace Malachi.Core.Controllers;

/// <summary>
/// quickreply.Controller: the quick-reply bar of one main window.
/// UI-thread-affine (docs/windows-port.md §7.1).
/// </summary>
public sealed class QuickReplyController
{
    /// <summary>
    /// quickreply.CallTimeout: bounds each call of <see cref="Send"/>
    /// (<c>draft.create</c>, <c>draft.save</c>, <c>message.send</c>,
    /// <c>draft.delete</c>): <c>draft.create</c> re-reads the original and
    /// copies its pictures, which can take longer than an ordinary call. The
    /// compose window's <c>draft.create</c> bound.
    /// </summary>
    public static readonly TimeSpan CallTimeout = RpcTimeouts.Compose;

    private readonly IQuickReplyHost host;

    // The bar's state of each conversation, kept in memory until the text is
    // sent, moved into a window or cleared (Q9); a conversation in its zero
    // state is forgotten (Prune).
    private readonly Dictionary<ThreadId, ThreadState> threads = [];

    // The conversation shown; chatShown says the chat layout is.
    private ThreadId shown = new("");
    private bool chatShown;

    // What Update learnt of the shown conversation.
    private ConversationItem? target;
    private IReadOnlyList<QuickReplyMode> modes = [];
    private bool reply;
    private bool comment;
    private Account? account;

    /// <summary>quickreply.New: a controller over <paramref name="host"/>, which owns it.</summary>
    public QuickReplyController(IQuickReplyHost host)
    {
        ArgumentNullException.ThrowIfNull(host);
        this.host = host;
    }

    /// <summary>Raised after every change of <see cref="State"/> (the view reads it again), on the UI thread.</summary>
    public event EventHandler? Changed;

    /// <summary>The conversations whose state is kept (tests: a conversation emptied is forgotten).</summary>
    public IReadOnlyCollection<ThreadId> KeptThreads => [.. threads.Keys];

    /// <summary>quickreply.State: what the bar shows now.</summary>
    public QuickReplyState State
    {
        get
        {
            var text = "";
            var sending = false;
            if (!IsEmpty(shown) && threads.TryGetValue(shown, out var st))
            {
                text = st.Text;
                sending = st.Sending;
            }
            return new QuickReplyState
            {
                Thread = shown,
                Visible = Visible,
                Target = target,
                Modes = modes,
                Mode = CurrentMode,
                Text = text,
                Blank = Conversation.QuickReplyBlank(text),
                Sending = sending,
                Comment = comment,
            };
        }
    }

    // The bar shows (QuickReplyState.Visible).
    private bool Visible => chatShown && !IsEmpty(shown) && target is not null && reply;

    // The chosen mode of the conversation shown when it is still offered,
    // else the first mode offered that is not Forward, else Reply; on an
    // issue always Reply.
    private QuickReplyMode CurrentMode
    {
        get
        {
            if (comment)
            {
                return QuickReplyMode.Reply;
            }
            if (threads.TryGetValue(shown, out var st) && modes.Contains(st.Mode))
            {
                return st.Mode;
            }
            foreach (var m in modes)
            {
                if (m != QuickReplyMode.Forward)
                {
                    return m;
                }
            }
            return QuickReplyMode.Reply;
        }
    }

    /// <summary>
    /// quickreply.Show: which conversation the pane shows now ("" for none).
    /// Its target is unknown until the next <see cref="Update"/>; the text and
    /// mode of every conversation stay.
    /// </summary>
    public void Show(ThreadId thread)
    {
        var t = Normal(thread);
        if (t == shown)
        {
            return;
        }
        shown = t;
        target = null;
        modes = [];
        reply = false;
        comment = false;
        account = null;
        RaiseChanged();
    }

    /// <summary>
    /// quickreply.Update: takes the shown conversation's model
    /// <paramref name="m"/>, the actions of its target's card
    /// (<see cref="Conversation.CardActions"/> for the target,
    /// <see cref="ConversationController.QuickReplyInput"/>) and its account.
    /// A model of another conversation (its thread neither "" nor the one
    /// shown) shows that one first, as <see cref="Show"/> does.
    /// </summary>
    public void Update(ConversationModel m, CapabilityActions actions, Account? account)
    {
        ArgumentNullException.ThrowIfNull(m);
        var t = Normal(m.Thread);
        if (!IsEmpty(t) && t != shown)
        {
            shown = t;
        }
        target = Conversation.QuickReplyTarget(m);
        modes = Conversation.QuickReplyModes(actions);
        reply = actions.Reply;
        comment = actions.Comment;
        this.account = account;
        RaiseChanged();
    }

    /// <summary>quickreply.SetChatShown: whether the chat layout is shown (the timeline has no bar).</summary>
    public void SetChatShown(bool shownNow)
    {
        if (shownNow == chatShown)
        {
            return;
        }
        chatShown = shownNow;
        RaiseChanged();
    }

    /// <summary>quickreply.SetText: what the field holds now, for the conversation shown.</summary>
    public void SetText(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        if (IsEmpty(shown))
        {
            return;
        }
        var known = threads.TryGetValue(shown, out var st);
        if (!known && text.Length == 0)
        {
            return;
        }
        if (string.Equals(st.Text, text, StringComparison.Ordinal))
        {
            return;
        }
        threads[shown] = st with { Text = text };
        Prune(shown);
        RaiseChanged();
    }

    /// <summary>
    /// quickreply.SetMode: chooses <see cref="QuickReplyMode.Reply"/> or
    /// <see cref="QuickReplyMode.ReplyAll"/> among the modes offered for the
    /// conversation shown; anything else (Forward, a mode not offered) is
    /// ignored.
    /// </summary>
    public void SetMode(QuickReplyMode mode)
    {
        if (IsEmpty(shown) || mode == QuickReplyMode.Forward || !modes.Contains(mode))
        {
            return;
        }
        // A conversation without state has Reply: choosing it changes
        // nothing (Go keeps an empty entry then; here, as in Swift, none is
        // made).
        var st = threads.GetValueOrDefault(shown);
        if (st.Mode == mode)
        {
            return;
        }
        threads[shown] = st with { Mode = mode };
        Prune(shown);
        RaiseChanged();
    }

    /// <summary>
    /// quickreply.Send: sends the text of the conversation shown as a reply
    /// (or comment) to its target: <c>draft.create</c> on the target (after
    /// <see cref="IQuickReplyHost.BeforeCreate"/>, never for a comment) with
    /// the attribution the window would use, one <c>draft.save</c> of its
    /// draft with the typed text above the original quoted
    /// (<see cref="SaveDraft"/>), and <c>message.send</c>. Success clears the
    /// text (only if it is unchanged by then) and says
    /// <see cref="IQuickReplyHost.Sent"/>; a failure says
    /// <see cref="IQuickReplyHost.Failed"/> and keeps the text, and a failed
    /// <c>message.send</c> first deletes the saved draft (<c>draft.delete</c>,
    /// its error ignored) so that no draft the user never saw stays behind.
    /// The outcome goes to the conversation Send started in, whichever is
    /// shown by then. Nothing happens when the bar is hidden, the text is
    /// blank or a Send of this conversation is on its way.
    /// </summary>
    public void Send()
    {
        if (!Visible || !threads.TryGetValue(shown, out var st) || st.Sending || Conversation.QuickReplyBlank(st.Text)
            || target?.Message is not { } message)
        {
            return;
        }
        var t = shown;
        var text = st.Text;
        var isComment = comment;
        var mode = CurrentMode;
        var acc = message.AccountId;
        if (string.IsNullOrEmpty(acc.Value))
        {
            acc = account?.Id ?? new AccountId("");
        }
        var id = message.Id;
        threads[t] = st with { Sending = true };
        RaiseChanged();

        void Start()
        {
            var parameters = new DraftCreateParams { AccountId = acc, Mode = mode.ComposeMode, MessageId = id };
            if (!isComment)
            {
                var line = host.Attribution(mode, id);
                parameters = parameters with { Attribution = line.Length == 0 ? null : line };
            }
            _ = DeliverThenFinishAsync(parameters, t, text, isComment);
        }
        if (isComment)
        {
            Start();
            return;
        }
        host.BeforeCreate(id, Start);
    }

    /// <summary>
    /// quickreply.SaveDraft: the draft a quick reply saves with
    /// <paramref name="text"/> typed in <paramref name="mode"/> on
    /// <paramref name="draft"/>, <c>draft.create</c>'s result: the shape of
    /// the compose window's own save (<see cref="ComposeDraftController.Build"/>)
    /// — the template's identity, recipients and subject, the body
    /// <see cref="Conversation.QuickReplyBody"/> (the typed text above the
    /// original quoted; a comment's text alone), the text as typed (the
    /// daemon derives its own from the HTML), the template's attachments by
    /// id (else the quote's pictures would be lost; only the id is read), and
    /// a comment always public (the bar has no visibility; the window has).
    /// <see cref="Draft.To"/> is never null, so it goes as <c>[]</c> on the
    /// wire; a draft without attachments has null, as Go's nil slice.
    /// </summary>
    public static Draft SaveDraft(ComposeMode mode, string text, Draft draft)
    {
        ArgumentNullException.ThrowIfNull(text);
        ArgumentNullException.ThrowIfNull(draft);
        var output = new Draft
        {
            Id = draft.Id,
            AccountId = draft.AccountId,
            Version = draft.Version,
            To = draft.To,
            Cc = draft.Cc,
            Bcc = draft.Bcc,
            Subject = draft.Subject,
            TextBody = text,
            HtmlBody = Conversation.QuickReplyBody(mode, text, draft),
            InReplyTo = draft.InReplyTo,
            Forwarding = draft.Forwarding,
            Replaces = draft.Replaces,
        };
        if (draft.Comment is { } c)
        {
            output = output with { Comment = new DraftComment { Issue = c.Issue, Visibility = CommentVisibility.Public } };
        }
        if (draft.Attachments is { Count: > 0 } list)
        {
            output = output with
            {
                Attachments = [.. list.Select(a => new DraftAttachment { Id = a.Id, Filename = "", ContentType = "", Size = 0, Inline = false })],
            };
        }
        return output;
    }

    /// <summary>
    /// quickreply.OpenWindow: moves the text of the conversation shown into
    /// the compose window: a reply in the chosen mode (a comment on an issue)
    /// to the target, prepared by the daemon as the window always is, with
    /// the text as the user's own. The bar's text is cleared once the window
    /// opened (<see cref="IQuickReplyHost.OpenCompose"/>'s opened), and only
    /// if it is unchanged by then. No draft is saved here; the window's own
    /// autosave does that.
    /// </summary>
    public void OpenWindow() => Open(CurrentMode);

    /// <summary>
    /// quickreply.Forward: the mode menu's Forward…: the compose window with a
    /// forward of the target and the text of the conversation shown above it,
    /// as <see cref="OpenWindow"/>. Nothing happens when the account does not
    /// forward.
    /// </summary>
    public void Forward()
    {
        if (!modes.Contains(QuickReplyMode.Forward))
        {
            return;
        }
        Open(QuickReplyMode.Forward);
    }

    // Send's calls, after the caller's turn: draft.create, draft.save,
    // message.send, and draft.delete after a failed message.send; then the
    // outcome on the UI thread.
    private async Task DeliverThenFinishAsync(DraftCreateParams parameters, ThreadId t, string text, bool isComment)
    {
        await Task.Yield();
        var failure = await DeliverAsync(host, parameters, text);
        host.Post(() => Finish(t, text, isComment, failure));
    }

    // Send's calls; null when all went through. Touches nothing of the
    // controller (it may run off the UI thread).
    private static async Task<Exception?> DeliverAsync(IQuickReplyHost h, DraftCreateParams parameters, string text)
    {
#pragma warning disable CA1031 // Every failure of a call is Send's outcome, said by the host (Failed).
        DraftCreateResult created;
        try
        {
            created = await h.CallAsync(API.DraftCreate, parameters, CallTimeout);
        }
        catch (Exception e)
        {
            return e;
        }
        var d = SaveDraft(parameters.Mode, text, created.Draft);
        if (string.IsNullOrEmpty(d.AccountId.Value))
        {
            d = d with { AccountId = parameters.AccountId };
        }
        DraftSaveResult saved;
        try
        {
            saved = await h.CallAsync(API.DraftSave, new DraftSaveParams { Draft = d }, CallTimeout);
        }
        catch (Exception e)
        {
            return e;
        }
        try
        {
            await h.CallAsync(
                API.MessageSend,
                new MessageSendParams { AccountId = d.AccountId, DraftId = saved.DraftId, Version = saved.Version },
                CallTimeout);
            return null;
        }
        catch (Exception e)
        {
            // An unknown draft is no error of draft.delete, so a message.send
            // that queued the message and then timed out leaves nothing; any
            // other failure leaves a draft the conversation shows.
            try
            {
                await h.CallAsync(API.DraftDelete, new DraftDeleteParams { AccountId = d.AccountId, DraftId = saved.DraftId }, CallTimeout);
            }
            catch (Exception)
            {
                // Ignored: the send's failure is what the user hears of.
            }
            return e;
        }
#pragma warning restore CA1031
    }

    // Ends a Send of conversation t whose text was text.
    private void Finish(ThreadId t, string text, bool isComment, Exception? failure)
    {
        var st = threads.GetValueOrDefault(t) with { Sending = false };
        // What was typed meanwhile (the field is disabled while sending, but
        // a view may still pass a change) stays.
        if (failure is null && string.Equals(st.Text, text, StringComparison.Ordinal))
        {
            st = st with { Text = "" };
        }
        threads[t] = st;
        if (failure is not null)
        {
            host.Failed(failure);
        }
        else
        {
            host.Sent(isComment);
        }
        Prune(t);
        RaiseChanged();
    }

    private void Open(QuickReplyMode mode)
    {
        if (!Visible || target?.Message is not { } message)
        {
            return;
        }
        var t = shown;
        var text = "";
        if (threads.TryGetValue(t, out var st))
        {
            if (st.Sending)
            {
                return;
            }
            text = st.Text;
        }
        host.OpenCompose(mode, message.Id, Conversation.QuickReplyHtml(text), () =>
        {
            if (text.Length == 0 || !threads.TryGetValue(t, out var now) || !string.Equals(now.Text, text, StringComparison.Ordinal))
            {
                return;
            }
            threads[t] = now with { Text = "" };
            Prune(t);
            RaiseChanged();
        });
    }

    // Forgets a conversation whose state is the zero one.
    private void Prune(ThreadId t)
    {
        if (threads.TryGetValue(t, out var st) && st == default)
        {
            threads.Remove(t);
        }
    }

    private void RaiseChanged() => Changed?.Invoke(this, EventArgs.Empty);

    private static bool IsEmpty(ThreadId t) => string.IsNullOrEmpty(t.Value);

    // "" for default(ThreadId), so that the two never differ as keys.
    private static ThreadId Normal(ThreadId t) => t.Value is null ? new ThreadId("") : t;

    // quickreply thread: the bar's state of one conversation; the default is
    // the zero state (no text, Reply, not sending). An empty text is kept as
    // null, so that a state emptied again equals the default (Prune).
    private readonly record struct ThreadState
    {
        private readonly string? text;

        public string Text
        {
            get => text ?? "";
            init => text = string.IsNullOrEmpty(value) ? null : value;
        }

        public QuickReplyMode Mode { get; init; }

        public bool Sending { get; init; }
    }
}
