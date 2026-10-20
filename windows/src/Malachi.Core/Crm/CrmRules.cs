// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/CRM/CRM.swift; GTK:
// ui/internal/crm/crm.go (Brand, PillLabel, KindBadge, MaxMessages, Icon,
// Accounts, Available, IsMailAccount, CanSave, Row, Offer, OfferFor,
// RecordOf, IsSaved, Key, InFlight, ButtonState, ButtonStateFor, MenuID,
// MenuItem, NotDownloaded, SignInReason, OpenURL, DefaultName, ServiceHost)
// and util.go (capBytes).
//
// The view logic of saving mail to eWay-CRM (account kind eway, capability
// crm; docs/eway.md, docs/api.md §4.17): which eWay-CRM account a client
// uses (Available), which messages can be saved (CanSave, OfferFor), the
// state of the message's button (ButtonStateFor). The dialog's model is
// CrmDialog.cs (dialog.go), the texts CrmText.cs (text.go). The daemon does
// every CRM call; this only turns its API values into small view models.
//
// Every name and detail of a CRM item is untrusted display text, cleaned and
// capped here again (Jira.Clean) and shown as plain text only. Every URL
// (CrmItem.Url, CrmRecord.Url) is built by the daemon from the account's
// stored service URL and a GUID of fixed shape, never taken from the CRM or
// from mail, so the client opens it in the browser without the "Open This
// Link?" question (docs/eway.md D15); OpenUrl only refuses anything that is
// not https. One eWay-CRM account per installation (D11): the first enabled
// one in account.list order.
//
// Windows: Go's package is split as Swift splits it, CrmRules (crm.go),
// CrmDialog (dialog.go) and CrmText (text.go), all in the namespace Crm;
// a class named Crm in it would be hidden by the namespace from every other
// Malachi.Core namespace (the reason Jira lives in IssueTrackers). Go's
// types are top-level with a Crm prefix (CrmRow, CrmOffer, CrmInFlight,
// CrmButtonState, CrmMenuId, CrmMenuItem). Go's "(value, ok)" results are
// nullable values; Go's error arguments are exceptions, the daemon's being
// an RpcException. url.Parse is Compose.UrlSyntax, the port of net/url the
// other link checks use, with the path read as Swift's URLParts reads it.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Text;
using System.Text.Json;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Model;
using Malachi.Core.Text;

namespace Malachi.Core.Crm;

/// <summary>ui/internal/crm (crm.go): the rules of saving mail to eWay-CRM.</summary>
public static class CrmRules
{
    /// <summary>crm.Brand: the service's name in texts; a brand, never translated.</summary>
    public const string Brand = "eWay-CRM";

    /// <summary>crm.PillLabel: the text of the list pill of a saved message; a brand, never translated.</summary>
    public const string PillLabel = "eWay";

    /// <summary>
    /// crm.KindBadge: the capsule of an eWay-CRM account's kind beside its
    /// name (Preferences → Integrations, FolderTree.AccountHeaderBadge), as
    /// Jira.KindBadge is "JIRA"; a brand, never translated.
    /// </summary>
    public const string KindBadge = "EWAY";

    /// <summary>crm.MaxMessages: the most messages one save takes (api.MaxCrmMessages).</summary>
    public const int MaxMessages = API.Limits.MaxCrmMessages;

    /// <summary>crm.Icon: the GTK symbolic icon of eWay-CRM; IconGlyphs maps it.</summary>
    public const string Icon = "x-office-address-book-symbolic";

    /// <summary>crm.Accounts: the enabled eWay-CRM accounts, in the order of <paramref name="accounts"/> (account.list order).</summary>
    public static IReadOnlyList<Account> Accounts(IEnumerable<Account> accounts)
    {
        ArgumentNullException.ThrowIfNull(accounts);
        return [.. accounts.Where(a => a.Enabled && a.Config.ProtocolKind == AccountKind.Eway)];
    }

    /// <summary>
    /// crm.Available: the eWay-CRM account the client saves to, the first
    /// enabled one in account.list order (docs/eway.md D11); null when there
    /// is none, and then every CRM control is hidden.
    /// </summary>
    public static Account? Available(IEnumerable<Account> accounts) => Accounts(accounts) is [var first, ..] ? first : null;

    /// <summary>
    /// crm.IsMailAccount: an account whose messages can be saved: a mail
    /// account (kind imap, the empty kind, or graph). Never an issue
    /// tracker's synthesised messages, never a calendar or CRM account.
    /// </summary>
    public static bool IsMailAccount(Account a)
    {
        ArgumentNullException.ThrowIfNull(a);
        var k = a.Config.ProtocolKind.Value;
        return string.IsNullOrEmpty(k) || k == AccountKind.Imap || k == AccountKind.Graph;
    }

    /// <summary>
    /// crm.CanSave (docs/eway.md D6): a message of a mail account, not in the
    /// Outbox or a Drafts folder, and not a local draft. Junk is allowed. The
    /// daemon refuses the same with invalidArgument.
    /// </summary>
    public static bool CanSave(Account messageAccount, FolderRole folderRole, bool localDraft)
    {
        if (!IsMailAccount(messageAccount) || localDraft)
        {
            return false;
        }
        return folderRole != FolderRole.Outbox && folderRole != FolderRole.Drafts;
    }

    /// <summary>
    /// crm.OfferFor: the <see cref="CrmOffer"/> for the selected rows (one
    /// account: the selection never spans two, Selection.Restrict). Nothing
    /// when no eWay-CRM account is enabled, nothing is selected, or a row is
    /// a conversation's. One row: shown and enabled when it
    /// <see cref="CanSave"/>. Several: shown when their account is a mail
    /// account, enabled when every row can be saved and there are at most
    /// <see cref="MaxMessages"/>; several rows are always offered as Save,
    /// saved or not.
    /// </summary>
    public static CrmOffer OfferFor(IEnumerable<Account> accounts, IReadOnlyList<CrmRow> rows)
    {
        ArgumentNullException.ThrowIfNull(rows);
        if (Available(accounts) is null || rows.Count == 0 || rows.Any(r => r.Conversation))
        {
            return default;
        }
        if (rows.Count == 1)
        {
            var r = rows[0];
            return CanSave(r.Account, r.Role, r.LocalDraft) ? new CrmOffer(true, true, 1, r.Saved) : default;
        }
        if (!IsMailAccount(rows[0].Account))
        {
            return default;
        }
        var enabled = rows.Count <= MaxMessages && rows.All(r => CanSave(r.Account, r.Role, r.LocalDraft));
        return new CrmOffer(true, enabled, rows.Count, false);
    }

    /// <summary>
    /// crm.RecordOf: the message's record in eWay-CRM account
    /// <paramref name="crm"/> (<see cref="Message.Crm"/>, from message.get); a
    /// null or empty <paramref name="crm"/> takes the first record. Null when
    /// the message is not saved there.
    /// </summary>
    public static CrmRecord? RecordOf(Message? m, AccountId? crm)
    {
        if (m?.Crm is not { } records)
        {
            return null;
        }
        var want = crm?.Value ?? "";
        return records.FirstOrDefault(r => want.Length == 0 || r.AccountId.Value == want);
    }

    /// <summary>
    /// crm.IsSaved: whether the message is saved in eWay-CRM account
    /// <paramref name="crm"/>: the loaded message's record when
    /// <paramref name="m"/> is the message <paramref name="s"/> (message.get
    /// is the newer word), else the list's <see cref="MessageSummary.CrmSaved"/>
    /// (which already counts only enabled CRM accounts).
    /// </summary>
    public static bool IsSaved(MessageSummary s, Message? m, AccountId? crm)
    {
        ArgumentNullException.ThrowIfNull(s);
        if (m is not null && m.Summary.Id == s.Id && m.Summary.AccountId == s.AccountId)
        {
            return RecordOf(m, crm) is not null;
        }
        return s.IsCrmSaved;
    }

    /// <summary>
    /// crm.ButtonStateFor: the button of the open message
    /// <paramref name="r"/>: hidden unless the message can be saved
    /// (<see cref="OfferFor"/> of the one row), Saving while
    /// <paramref name="busy"/> (a call of <see cref="CrmInFlight"/> runs for
    /// it), else Saved or NotSaved by <see cref="CrmRow.Saved"/>.
    /// </summary>
    public static CrmButtonState ButtonStateFor(IEnumerable<Account> accounts, CrmRow r, bool busy)
    {
        var o = OfferFor(accounts, [r]);
        if (!o.Shown)
        {
            return CrmButtonState.Hidden;
        }
        if (busy)
        {
            return CrmButtonState.Saving;
        }
        return o.Saved ? CrmButtonState.Saved : CrmButtonState.NotSaved;
    }

    /// <summary>
    /// crm.NotDownloaded: the messages <c>crm.save</c> needs downloaded first
    /// (the data of its partNotDownloaded, docs/eway.md D6), each once; null
    /// for any other error. The client runs message.download for each, shows
    /// CrmText.DownloadingText meanwhile, and calls crm.save again.
    /// </summary>
    public static IReadOnlyList<MessageId>? NotDownloaded(Exception? error) => NotDownloaded(RpcErrorText.DaemonError(error));

    /// <summary><see cref="NotDownloaded(Exception?)"/> of the daemon's error object.</summary>
    public static IReadOnlyList<MessageId>? NotDownloaded(RpcError? e)
    {
        if (e is null || e.Code != ErrorCode.PartNotDownloaded || Data(e, ApiJsonContext.Wire.MessagesNotDownloadedData) is not { } d)
        {
            return null;
        }
        var seen = new HashSet<MessageId>();
        var output = new List<MessageId>();
        foreach (var id in d.MessageIds)
        {
            if (!string.IsNullOrEmpty(id.Value) && seen.Add(id))
            {
                output.Add(id);
            }
        }
        return output.Count > 0 ? output : null;
    }

    /// <summary>
    /// crm.SignInReason: why the instance cannot be signed in to, the data of
    /// crmSignInUnsupported (1205); null for any other error or an unknown
    /// reason.
    /// </summary>
    public static CrmSignInReason? SignInReason(Exception? error) => SignInReason(RpcErrorText.DaemonError(error));

    /// <summary><see cref="SignInReason(Exception?)"/> of the daemon's error object.</summary>
    public static CrmSignInReason? SignInReason(RpcError? e)
    {
        if (e is null || e.Code != ErrorCode.CrmSignInUnsupported || Data(e, ApiJsonContext.Wire.CrmSignInUnsupportedData) is not { } d)
        {
            return null;
        }
        // Not a conditional with null: the string conversion of CrmSignInReason
        // would take the null and the result would never be null.
        if (d.Reason.Value is CrmSignInReason.Microsoft or CrmSignInReason.Plan)
        {
            return d.Reason;
        }
        return null;
    }

    /// <summary>
    /// crm.OpenURL: the URL a client opens for a record or an item:
    /// <paramref name="u"/> when it is an https URL with a host and no user
    /// info, else "" (nothing to open). The daemon builds it (docs/eway.md
    /// D15), so no question is asked.
    /// </summary>
    public static string OpenUrl(string? u)
    {
        if (u is null || UrlSyntax.Parse(u) is not { } p || p.Scheme != "https" || p.Rest.Host.Length == 0 || p.Rest.HasUserinfo)
        {
            return "";
        }
        return u;
    }

    /// <summary>
    /// crm.DefaultName: the name an added account gets: the last segment of
    /// the service URL's path ("https://hosting.eway-crm.com/company" →
    /// "company"), else its host. "" for an address that is not a URL.
    /// </summary>
    public static string DefaultName(string serviceUrl)
    {
        ArgumentNullException.ThrowIfNull(serviceUrl);
        if (ParseUrl(serviceUrl.Trim()) is not { } p || p.Host.Length == 0)
        {
            return "";
        }
        var path = p.Path.Trim('/');
        var i = path.LastIndexOf('/');
        if (i >= 0)
        {
            path = path[(i + 1)..];
        }
        // url.PathUnescape of the path url.Parse unescaped once already.
        if (UrlSyntax.Unescape(UrlSyntax.Bytes(path), UrlSyntax.Mode.Path) is { } s)
        {
            path = s;
        }
        var name = Jira.Clean(path);
        return name.Length > 0 ? name : Jira.Clean(CodePoints.ToLower(UrlSyntax.SplitHostPort(p.Host).Host));
    }

    /// <summary>
    /// crm.ServiceHost: the host and path of an account's service URL, the
    /// subtitle of its row ("hosting.eway-crm.com/company"); "" without one.
    /// </summary>
    public static string ServiceHost(Account a)
    {
        ArgumentNullException.ThrowIfNull(a);
        if (a.Config.Eway is not { } e || ParseUrl(e.ServiceUrl.Trim()) is not { } p || p.Host.Length == 0)
        {
            return "";
        }
        return Jira.Clean(CodePoints.ToLower(p.Host) + p.Path.TrimEnd('/'));
    }

    // capBytes: s capped at n UTF-8 bytes on a character boundary, with an
    // ellipsis when cut.
    internal static string CapBytes(string s, int n)
    {
        const string ellipsis = "…";
        if (Encoding.UTF8.GetByteCount(s) <= n)
        {
            return s;
        }
        return Jira.Truncate(s, n - Encoding.UTF8.GetByteCount(ellipsis)) + ellipsis;
    }

    // An error's data as T; null without data or with data of another shape.
    private static T? Data<T>(RpcError e, System.Text.Json.Serialization.Metadata.JsonTypeInfo<T> info)
        where T : class
    {
        if (e.Data is not { ValueKind: JsonValueKind.Object } data)
        {
            return null;
        }
        try
        {
            return data.Deserialize(info);
        }
        catch (JsonException)
        {
            return null;
        }
    }

    // What the rules read of a URL, as Go's url.Parse has it (Swift's
    // URLParts): the host with its port and the decoded path; null where
    // url.Parse fails.
    private static (string Host, string Path)? ParseUrl(string raw)
    {
        if (UrlSyntax.Parse(raw) is not { } u)
        {
            return null;
        }
        if (!u.Rest.Opaque.IsEmpty)
        {
            return (u.Rest.Host, "");
        }
        var rest = raw;
        var hash = rest.IndexOf('#', StringComparison.Ordinal);
        if (hash >= 0)
        {
            rest = rest[..hash];
        }
        var q = rest.IndexOf('?', StringComparison.Ordinal);
        if (q >= 0)
        {
            rest = rest[..q];
        }
        if (u.Scheme.Length > 0)
        {
            rest = rest[(rest.IndexOf(':', StringComparison.Ordinal) + 1)..];
        }
        if (rest.StartsWith("//", StringComparison.Ordinal))
        {
            rest = rest[2..];
            var slash = rest.IndexOf('/', StringComparison.Ordinal);
            rest = slash >= 0 ? rest[slash..] : "";
        }
        return (u.Rest.Host, UrlSyntax.Unescape(UrlSyntax.Bytes(rest), UrlSyntax.Mode.Path) ?? rest);
    }
}

/// <summary>
/// crm.Row: what the rules need of one selected row (a message, or a
/// conversation's row standing for its members in the folder).
/// </summary>
/// <param name="Account">The message's account.</param>
/// <param name="Role">The role of the message's folder.</param>
/// <param name="LocalDraft">The row is a local draft (a draft card), not a message.</param>
/// <param name="Saved">The message is saved in eWay-CRM (<see cref="CrmRules.IsSaved"/>).</param>
/// <param name="Conversation">
/// The row is a conversation's row (folded or not), which stands for its
/// members in the folder rather than for one message: it offers nothing
/// (owner, 2026-10-17); the user unfolds it and saves the members.
/// </param>
public readonly record struct CrmRow(Account Account, FolderRole Role, bool LocalDraft = false, bool Saved = false, bool Conversation = false);

/// <summary>
/// crm.Offer: what the button, the context menu and the multi-selection
/// offer; one rule for all three. The default value offers nothing.
/// </summary>
/// <param name="Shown">The CRM item exists for the selection (else hidden).</param>
/// <param name="Enabled">It can run now.</param>
/// <param name="Count">The number of rows (the label of several).</param>
/// <param name="Saved">One row, saved: the saved-state items replace Save.</param>
public readonly record struct CrmOffer(bool Shown, bool Enabled, int Count, bool Saved);

/// <summary>crm.Key: a message of a mail account, for <see cref="CrmInFlight"/>.</summary>
/// <param name="Account">The mail account.</param>
/// <param name="Message">The message.</param>
public readonly record struct CrmKey(AccountId Account, MessageId Message);

/// <summary>
/// crm.InFlight: the messages a crm.save or crm.remove runs for, so that
/// their button reads Saving and a second save is not started (the daemon
/// would answer conflict). Not thread-safe: the clients touch it on their UI
/// thread.
/// </summary>
public sealed class CrmInFlight
{
    private readonly Dictionary<CrmKey, int> keys = [];

    /// <summary>Nothing runs.</summary>
    public bool IsEmpty => keys.Count == 0;

    /// <summary>Marks the messages busy; <see cref="End"/> releases them (counted, so overlapping calls nest).</summary>
    public void Begin(AccountId account, IEnumerable<MessageId> ids)
    {
        ArgumentNullException.ThrowIfNull(ids);
        foreach (var id in ids)
        {
            var k = new CrmKey(account, id);
            keys[k] = keys.GetValueOrDefault(k) + 1;
        }
    }

    /// <summary>Releases what <see cref="Begin"/> marked.</summary>
    public void End(AccountId account, IEnumerable<MessageId> ids)
    {
        ArgumentNullException.ThrowIfNull(ids);
        foreach (var id in ids)
        {
            var k = new CrmKey(account, id);
            if (keys.GetValueOrDefault(k) <= 1)
            {
                keys.Remove(k);
            }
            else
            {
                keys[k]--;
            }
        }
    }

    /// <summary>Whether a call runs for the message.</summary>
    public bool Has(AccountId account, MessageId id) => keys.GetValueOrDefault(new CrmKey(account, id)) > 0;

    /// <summary>Whether a call runs for any of the messages.</summary>
    public bool Any(AccountId account, IEnumerable<MessageId> ids)
    {
        ArgumentNullException.ThrowIfNull(ids);
        return ids.Any(id => Has(account, id));
    }
}

/// <summary>crm.ButtonState: the state of the CRM button on an open message (docs/eway.md D20).</summary>
public enum CrmButtonState
{
    /// <summary>No eWay-CRM account, or the message cannot be saved.</summary>
    Hidden,

    /// <summary>"Save to eWay-CRM", which opens the dialog.</summary>
    NotSaved,

    /// <summary>A save or removal runs; the button is off.</summary>
    Saving,

    /// <summary>"In eWay-CRM", which opens CrmText.SavedMenu.</summary>
    Saved,
}

/// <summary>crm.MenuID: an item of the saved message's menu (the button's, and the context menu of one saved message), in this order.</summary>
public enum CrmMenuId
{
    /// <summary>Opens the saved e-mail item (<see cref="CrmRecord.Url"/>); Go "open".</summary>
    Open,

    /// <summary>Opens the dialog over the saved message (CrmDialog in its editing mode); Go "editLinks".</summary>
    EditLinks,

    /// <summary>Asks CrmText.RemoveQuestion, then calls crm.remove; Go "remove".</summary>
    Remove,
}

/// <summary>crm.MenuItem: an entry of CrmText.SavedMenu.</summary>
/// <param name="Id">The item.</param>
/// <param name="Label">Its text.</param>
/// <param name="Destructive">The client marks it as such (it deletes the item from the CRM for every user, after a question).</param>
public readonly record struct CrmMenuItem(CrmMenuId Id, string Label, bool Destructive);
