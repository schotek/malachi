// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Bulk/BulkMail.swift; GTK:
// ui/internal/bulkmail/bulkmail.go (Tag, StripFor, senderName, listName,
// Confirm, subject, Fallback, Applied, Queued, ErrorWhat, Refused,
// OpenableURL).
//
// The view logic of bulk mail: the tag a newsletter, mailing-list or
// automated message carries in the message list, the strip above an opened
// message (with the Unsubscribe button), the confirmation before the daemon
// acts, the dialog that offers the sender's web page when the daemon could
// not verify a one-click request, and the texts of the toasts. The daemon
// classifies the mail and does the unsubscribing (message.unsubscribe,
// docs/api.md); this only turns its API values into texts and small view
// models, one to one with the Go package. Go's Translator is L10n here:
// every text goes through it with the GTK msgid.
//
// Every string of a message here (the list id, the domain, the target, the
// URL) is hostile input; the clients show it as plain text only.
//
// Windows: Go's "(value, ok)" results are nullable values (Confirm,
// OpenableUrl), and Applied takes the clock for the time it puts on an
// offer the daemon gave none for (Go's time.Now). OpenableUrl reads the
// URL as net/url does (Compose.UrlSyntax); Go's range over a string reads
// invalid UTF-8 as U+FFFD, which a lone surrogate of a C# string stands
// for, and none of the refused classes contains.

using System;
using System.Buffers;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Compose;
using Malachi.Core.I18n;
using Malachi.Core.IssueTrackers;

namespace Malachi.Core.Bulk;

/// <summary>ui/internal/bulkmail: a namespace of functions, so that the Go and Swift names map 1:1.</summary>
public static class BulkMail
{
    /// <summary>The longest URL a page may be opened from (bulkmail.OpenableURL).</summary>
    public const int MaxUrlBytes = 2048;

    /// <summary>
    /// bulkmail.Tag: the neutral pill next to a message's subject in the
    /// list: "" for personal mail (<paramref name="b"/> null) and for a kind
    /// this client does not know.
    /// </summary>
    public static string Tag(BulkInfo? b)
    {
        if (b is null)
        {
            return "";
        }
        switch (b.Kind.Value)
        {
            case BulkKind.Newsletter:
                return L10n.C("message tag", "Bulk");
            case BulkKind.List:
                // TRANSLATORS: tag of a message from a discussion mailing list
                return L10n.C("message tag", "Mailing List");
            case BulkKind.Automated:
                // TRANSLATORS: tag of a machine-sent message (receipt, ticket)
                return L10n.C("message tag", "Automated");
            default:
                return "";
        }
    }

    /// <summary>
    /// bulkmail.StripFor: the strip of message <paramref name="m"/> shown
    /// from a folder of the given <paramref name="role"/>.
    /// <paramref name="date"/> formats the full date of the client's message
    /// headers; without it a message the user unsubscribed from shows its
    /// offer again.
    /// </summary>
    public static BulkStrip StripFor(Message? m, FolderRole role, Func<DateTimeOffset, string>? date)
    {
        if (m?.Summary.Bulk is not { } b)
        {
            return new BulkStrip();
        }
        var offer = m.Unsubscribe;
        switch (b.Kind.Value)
        {
            case BulkKind.Newsletter:
            case BulkKind.List:
                if (role.Value == FolderRole.Junk)
                {
                    return new BulkStrip
                    {
                        Kind = BulkStripKind.Junk,
                        Text = L10n.T("Unsubscribing would confirm to the sender that your address exists."),
                        Warning = true,
                    };
                }
                break;
            case BulkKind.Automated:
                return new BulkStrip { Kind = BulkStripKind.Automated, Text = L10n.T("Automated message") };
            default:
                return new BulkStrip();
        }
        if (offer?.UnsubscribedAt is { } at && date is not null)
        {
            return new BulkStrip { Kind = BulkStripKind.Unsubscribed, Text = L10n.T("Unsubscribed on %s", date(at)) };
        }
        var isUrl = offer?.Method.Value == UnsubscribeMethod.Url;
        if (b.Kind.Value == BulkKind.List)
        {
            return new BulkStrip
            {
                Kind = BulkStripKind.List,
                Text = L10n.T("Message from mailing list %s", ListName(b, offer)),
                Action = offer is null ? "" : isUrl ? L10n.T("_Leave List…") : L10n.T("_Leave List"),
            };
        }
        return new BulkStrip
        {
            Kind = BulkStripKind.Newsletter,
            Text = L10n.T("Bulk message from %s", SenderName(b, offer)),
            Action = offer is null ? "" : isUrl ? L10n.T("_Unsubscribe…") : L10n.T("_Unsubscribe"),
        };
    }

    /// <summary>
    /// bulkmail.senderName: the domain the sender writes from; the list id or
    /// the offer's target stand in for a missing one.
    /// </summary>
    public static string SenderName(BulkInfo? b, UnsubscribeOffer? offer)
    {
        if (b?.Domain is { Length: > 0 } domain)
        {
            return domain;
        }
        if (b?.ListId is { Length: > 0 } listId)
        {
            return listId;
        }
        return offer?.Target ?? "";
    }

    /// <summary>bulkmail.listName: the list id of a mailing list; the domain falls in for a list without one.</summary>
    public static string ListName(BulkInfo? b, UnsubscribeOffer? offer) =>
        b?.ListId is { Length: > 0 } listId ? listId : SenderName(b, offer);

    /// <summary>
    /// bulkmail.Confirm: the confirmation of the unsubscribe offer of
    /// <paramref name="m"/>; null when the message has none.
    /// </summary>
    public static UnsubscribeConfirmation? Confirm(Message? m)
    {
        if (m?.Unsubscribe is not { } offer)
        {
            return null;
        }
        var b = m.Summary.Bulk;
        switch (offer.Method.Value)
        {
            case UnsubscribeMethod.OneClick:
                return new UnsubscribeConfirmation(
                    L10n.T("Unsubscribe from %s?", Subject(b, offer)),
                    L10n.T(
                        "Malachi Mail will ask %s to stop sending these messages. The sender may still send a few more over the next days.",
                        offer.Target),
                    L10n.T("_Unsubscribe"));
            case UnsubscribeMethod.Mailto:
                return new UnsubscribeConfirmation(
                    b?.Kind.Value == BulkKind.List
                        ? L10n.T("Leave the mailing list %s?", ListName(b, offer))
                        : L10n.T("Unsubscribe from %s?", Subject(b, offer)),
                    L10n.T("Malachi Mail will send an unsubscribe request to %s from your account. It will appear in Sent.", offer.Target),
                    L10n.T("_Send Request"));
            case UnsubscribeMethod.Url:
                var page = offer.Url ?? "";
                return new UnsubscribeConfirmation(
                    L10n.T("Open the unsubscribe page?"),
                    L10n.T("The sender does not offer unsubscribing in one step. This page opens in your browser:\n%s", page),
                    L10n.T("_Open in Browser"));
            default:
                return null;
        }
    }

    // bulkmail.subject: what the heading of an unsubscribe confirmation
    // names: the list id of a list, else the sender's domain.
    private static string Subject(BulkInfo? b, UnsubscribeOffer? offer) =>
        b?.Kind.Value == BulkKind.List ? ListName(b, offer) : SenderName(b, offer);

    /// <summary>
    /// bulkmail.Fallback: the dialog after the daemon answered openUrl for a
    /// one-click offer it could not verify: it sent nothing and offers the
    /// sender's page.
    /// </summary>
    public static UnsubscribeConfirmation Fallback(Message? m, MessageUnsubscribeResult res)
    {
        ArgumentNullException.ThrowIfNull(res);
        var b = m?.Summary.Bulk;
        var offer = m?.Unsubscribe;
        var page = res.Url ?? "";
        return new UnsubscribeConfirmation(
            L10n.T("The sender could not be verified"),
            L10n.T(
                "Malachi Mail sent nothing because the message is not signed by %s. You can unsubscribe on the sender's page instead:\n%s",
                SenderName(b, offer),
                page),
            L10n.T("_Open in Browser"));
    }

    /// <summary>
    /// bulkmail.Applied: the offer of a message after
    /// <c>message.unsubscribe</c> answered <paramref name="res"/>: a copy with
    /// the time the daemon remembered for unsubscribed and queued (now, by
    /// <paramref name="time"/>, when it gave none), the offer unchanged for
    /// any other outcome (and null for null).
    /// </summary>
    public static UnsubscribeOffer? Applied(UnsubscribeOffer? offer, MessageUnsubscribeResult res, TimeProvider? time = null)
    {
        ArgumentNullException.ThrowIfNull(res);
        if (offer is null)
        {
            return null;
        }
        if (res.Outcome.Value is UnsubscribeOutcome.Unsubscribed or UnsubscribeOutcome.Queued)
        {
            return offer with { UnsubscribedAt = res.UnsubscribedAt ?? (time ?? TimeProvider.System).GetUtcNow() };
        }
        return offer with { };
    }

    /// <summary>bulkmail.Queued: the toast after the unsubscribe request went to the outbox.</summary>
    public static string Queued() => L10n.T("Unsubscribe request queued");

    /// <summary>bulkmail.ErrorWhat: the action of the error toast, in the progressive form <c>RpcErrorText.Text</c> takes.</summary>
    public static string ErrorWhat() => L10n.T("Unsubscribing");

    /// <summary>
    /// bulkmail.Refused: the sentence for error 1505 (unsubscribeFailed): the
    /// sender's server refused or did not answer the one-click request.
    /// </summary>
    public static string Refused() => L10n.T("The sender's server refused the request.");

    /// <summary>
    /// bulkmail.OpenableURL: <paramref name="raw"/> when a page may be opened
    /// in the browser: an https URL with a host and no control, space or bidi
    /// characters; null for anything else (http, javascript:, data:, a
    /// relative reference).
    /// </summary>
    public static string? OpenableUrl(string? raw)
    {
        if (string.IsNullOrEmpty(raw) || Encoding.UTF8.GetByteCount(raw) > MaxUrlBytes)
        {
            return null;
        }
        for (var i = 0; i < raw.Length;)
        {
            var status = Rune.DecodeFromUtf16(raw.AsSpan(i), out var r, out var used);
            i += Math.Max(used, 1);
            if (status != OperationStatus.Done)
            {
                continue; // Go reads invalid UTF-8 as U+FFFD, which is none of these
            }
            if (Assistant.IsControl(r.Value) || Assistant.IsSpace(r.Value) || Jira.IsFormat(r.Value))
            {
                return null;
            }
        }
        if (UrlSyntax.Parse(raw) is not { Scheme: "https" } || UrlSyntax.Hostname(raw) is not { Length: > 0 })
        {
            return null;
        }
        return raw;
    }
}
