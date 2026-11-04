// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/CRM/CRMText.swift; GTK:
// ui/internal/crm/text.go (Button, ButtonFor, savedText, MenuLabel,
// SavedMenu, SaveMenuLabel, DialogStrings, DialogTexts, GroupTitle, BarItem,
// Bar, BarFor, Pill, PillFor, SaveSummary, Summarize, SaveToast,
// RemovedToast, Question, RemoveQuestion, ErrorText, signInText, Action,
// ActionText, contacting, DownloadingText, WizardPage, WizardStrings,
// WizardTexts, CheckServiceInput, httpOnly, WizardStep, WizardField,
// Failure, FailureOf).
//
// Every text of saving mail to eWay-CRM (docs/eway.md D20), with the GTK
// msgid as the key. "eWay-CRM" and "eWay" are brands and stay as they are in
// every language. Go's Translator is L10n, as in the other ports.
//
// Windows: Go's types are top-level with a Crm prefix (CrmButton,
// CrmDialogStrings, CrmBar, CrmBarItem, CrmPill, CrmSaveSummary,
// CrmQuestion, CrmAction, CrmWizardPage, CrmWizardStrings, CrmWizardStep,
// CrmWizardField, CrmFailure); Go's "(value, ok)" results are nullable
// values and SaveToast's pair a tuple, as Swift's.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Compose;
using Malachi.Core.I18n;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Model;
using Malachi.Core.Text;

namespace Malachi.Core.Crm;

/// <summary>ui/internal/crm (text.go): the texts of saving mail to eWay-CRM.</summary>
public static class CrmText
{
    // maxSubjectBytes caps the subject quoted in RemoveQuestion.
    private const int MaxSubjectBytes = 200;

    /// <summary>crm.ButtonFor: the button of <paramref name="state"/>.</summary>
    public static CrmButton ButtonFor(CrmButtonState state) => state switch
    {
        // TRANSLATORS: the button of a message that saves it to eWay-CRM (a CRM; a brand name).
        CrmButtonState.NotSaved => new CrmButton(state, L10n.T("Save to eWay-CRM"), L10n.T("Save to eWay-CRM"), Enabled: true, Menu: false),
        // TRANSLATORS: tooltip of the button while the message is being saved to eWay-CRM.
        CrmButtonState.Saving => new CrmButton(state, L10n.T("Save to eWay-CRM"), L10n.T("Saving to eWay-CRM…"), Enabled: false, Menu: false),
        // TRANSLATORS: the button of a message saved in eWay-CRM; it opens a menu.
        CrmButtonState.Saved => new CrmButton(state, L10n.T("In eWay-CRM"), SavedText(), Enabled: true, Menu: true),
        _ => new CrmButton(state, "", "", Enabled: false, Menu: false),
    };

    /// <summary>crm.MenuLabel: the label of an item of <see cref="SavedMenu"/>.</summary>
    public static string MenuLabel(CrmMenuId id) => id switch
    {
        // TRANSLATORS: opens the saved message (or an item) in eWay-CRM in the browser.
        CrmMenuId.Open => L10n.T("Open in eWay-CRM"),
        // TRANSLATORS: menu item: change which contacts, companies, deals and projects the saved message is linked to.
        CrmMenuId.EditLinks => L10n.T("Edit Links…"),
        CrmMenuId.Remove => L10n.T("Remove from eWay-CRM"),
        _ => "",
    };

    /// <summary>crm.SavedMenu: the menu of a saved message: Open in eWay-CRM, Edit Links…, Remove from eWay-CRM.</summary>
    public static IReadOnlyList<CrmMenuItem> SavedMenu() =>
    [
        new(CrmMenuId.Open, MenuLabel(CrmMenuId.Open), false),
        new(CrmMenuId.EditLinks, MenuLabel(CrmMenuId.EditLinks), false),
        new(CrmMenuId.Remove, MenuLabel(CrmMenuId.Remove), true),
    ];

    /// <summary>crm.SaveMenuLabel: the context menu's Save item for <paramref name="count"/> selected rows.</summary>
    public static string SaveMenuLabel(int count) => count > 1
        // TRANSLATORS: context menu of several selected messages; %d is their number.
        ? L10n.N("Save %d Message to eWay-CRM…", "Save %d Messages to eWay-CRM…", count, count)
        // TRANSLATORS: context menu of a message: opens the dialog that saves it to eWay-CRM.
        : L10n.T("Save to eWay-CRM…");

    /// <summary>crm.DialogTexts: the dialog's texts; <paramref name="editing"/> as <see cref="CrmDialog.Editing"/>.</summary>
    public static CrmDialogStrings DialogTexts(bool editing) => new()
    {
        // TRANSLATORS: title of the dialog that changes a saved message's links in eWay-CRM.
        Title = editing ? L10n.T("Edit Links") : L10n.T("Save to eWay-CRM"),
        // TRANSLATORS: placeholder of the search field of the Save to eWay-CRM dialog.
        Search = L10n.T("Search eWay-CRM"),
        // TRANSLATORS: marks the one deal or project the message belongs under in eWay-CRM.
        SuperiorItem = L10n.T("Superior Item"),
        // TRANSLATORS: check box: the saved message is visible to its owner only in eWay-CRM.
        Private = L10n.C("crm", "Private"),
        PrivateHint = L10n.T("Only you will see this message in eWay-CRM."),
        Empty = L10n.T("Nothing in eWay-CRM matches these addresses."),
        Partial = L10n.T("eWay-CRM did not answer in time; some suggestions may be missing."),
        // TRANSLATORS: under the groups of the Save to eWay-CRM dialog when no deal or project is listed.
        SearchHint = L10n.T("Deals and projects are added by searching above"),
        Save = L10n.T("_Save"),
    };

    /// <summary>crm.GroupTitle: the heading of a kind's group in the dialog; "" for an unknown kind.</summary>
    public static string GroupTitle(CrmItemKind k) => k.Value switch
    {
        // TRANSLATORS: eWay-CRM contacts (people).
        CrmItemKind.Contact => L10n.C("crm", "Contacts"),
        // TRANSLATORS: eWay-CRM companies.
        CrmItemKind.Company => L10n.C("crm", "Companies"),
        // TRANSLATORS: eWay-CRM deals (sales opportunities, "Leads" in its API).
        CrmItemKind.Deal => L10n.C("crm", "Deals"),
        // TRANSLATORS: eWay-CRM projects.
        CrmItemKind.Project => L10n.C("crm", "Projects"),
        _ => "",
    };

    /// <summary>crm.BarFor: the bar of the message's record (<see cref="CrmRules.RecordOf"/>); null when there is none.</summary>
    public static CrmBar? BarFor(CrmRecord? rec)
    {
        if (rec is null)
        {
            return null;
        }
        var items = new List<CrmBarItem>();
        var names = new List<string>();
        foreach (var k in CrmDialog.Kinds)
        {
            foreach (var it in rec.Items)
            {
                if (CrmDialog.RefOf(it.Kind, it.ItemGuid) is not { } r || r.Kind != k.Value)
                {
                    continue;
                }
                var name = CrmRules.CapBytes(Jira.Clean(it.Name), API.Limits.MaxCrmNameBytes);
                if (name.Length == 0)
                {
                    continue;
                }
                items.Add(new CrmBarItem(k, name, it.IsSuperior, CrmRules.OpenUrl(it.Url)));
                names.Add(name);
            }
        }
        return new CrmBar
        {
            Title = SavedText(),
            Items = items,
            Names = CrmRules.CapBytes(string.Join(", ", names), API.Limits.MaxCrmDetailBytes),
            Url = CrmRules.OpenUrl(rec.Url),
            OpenLabel = MenuLabel(CrmMenuId.Open),
            Note = rec.Private ? L10n.T("Only you will see this message in eWay-CRM.") : "",
        };
    }

    /// <summary>crm.PillFor: the pill of a list row; null when the message is not saved.</summary>
    public static CrmPill? PillFor(MessageSummary s)
    {
        ArgumentNullException.ThrowIfNull(s);
        return s.IsCrmSaved ? new CrmPill(CrmRules.PillLabel, SavedText()) : null;
    }

    /// <summary>crm.Summarize: counts the outcomes of crm.save.</summary>
    public static CrmSaveSummary Summarize(CrmSaveResult res)
    {
        ArgumentNullException.ThrowIfNull(res);
        var saved = 0;
        var failed = 0;
        RpcError? first = null;
        foreach (var o in res.Results)
        {
            if (o.Error is { } e)
            {
                failed++;
                first ??= e;
            }
            else
            {
                saved++;
            }
        }
        return new CrmSaveSummary(saved, failed, first);
    }

    /// <summary>
    /// crm.SaveToast: the toast after crm.save: Saved to eWay-CRM, the number
    /// saved, or what failed. When Text is null the client shows its general
    /// sentence for Error (RpcErrorText with ActionText(Save)).
    /// </summary>
    public static (string? Text, RpcError? Error) SaveToast(CrmSaveSummary s)
    {
        if (s.Failed == 0 && s.Saved <= 1)
        {
            // TRANSLATORS: toast after one message was saved to eWay-CRM.
            return (L10n.T("Saved to eWay-CRM"), null);
        }
        if (s.Failed == 0)
        {
            // TRANSLATORS: toast after several messages were saved to eWay-CRM; %d is their number.
            return (L10n.N("%d message saved to eWay-CRM", "%d messages saved to eWay-CRM", s.Saved, s.Saved), null);
        }
        if (s.Saved + s.Failed == 1)
        {
            var t = ErrorText(s.Error);
            return t.Length > 0 ? (t, null) : (null, s.Error);
        }
        // TRANSLATORS: toast after saving several messages to eWay-CRM, some of which failed; %d is the number that failed.
        return (L10n.N("%d message was not saved to eWay-CRM.", "%d messages were not saved to eWay-CRM.", s.Failed, s.Failed), null);
    }

    /// <summary>crm.RemovedToast: the toast after crm.remove.</summary>
    public static string RemovedToast() =>
        // TRANSLATORS: toast after a message was removed from eWay-CRM.
        L10n.T("Removed from eWay-CRM");

    /// <summary>
    /// crm.RemoveQuestion: asks before crm.remove deletes the message's item
    /// from eWay-CRM, for every user of the CRM (docs/eway.md D18).
    /// <paramref name="subject"/> is the message's subject (untrusted;
    /// cleaned and capped here).
    /// </summary>
    public static CrmQuestion RemoveQuestion(string? subject)
    {
        var s = CrmRules.CapBytes(Jira.Clean(subject), MaxSubjectBytes);
        if (s.Length == 0)
        {
            s = L10n.T("(No subject)");
        }
        return new CrmQuestion(
            // TRANSLATORS: heading of the question before a saved message is deleted from eWay-CRM.
            L10n.T("Remove from eWay-CRM?"),
            // TRANSLATORS: %s is the message's subject.
            L10n.T("“%s” will be deleted from eWay-CRM with its links, for everyone who uses it. The message in Malachi Mail stays.", s),
            L10n.T("_Remove"));
    }

    /// <summary>
    /// crm.ErrorText: the sentence for an error of the CRM's own
    /// (docs/eway.md D14): sign-in unsupported (microsoft, plan), a
    /// duplicate, an item gone. "" for any other error: the client shows its
    /// general sentence with an <see cref="ActionText"/> (and on
    /// partNotDownloaded downloads first, CrmRules.NotDownloaded).
    /// </summary>
    public static string ErrorText(Exception? error) => ErrorText(RpcErrorText.DaemonError(error));

    /// <summary><see cref="ErrorText(Exception?)"/> of the daemon's error object.</summary>
    public static string ErrorText(RpcError? e)
    {
        if (CrmRules.SignInReason(e) is { } reason)
        {
            return SignInText(reason);
        }
        return e?.Code.Value switch
        {
            // TRANSLATORS: eWay-CRM refused the message as a duplicate of another saved item.
            ErrorCode.CrmDuplicate => L10n.T("eWay-CRM already has this message."),
            // TRANSLATORS: a contact, company, deal or project (or the saved message) was deleted in eWay-CRM meanwhile.
            ErrorCode.CrmItemNotFound => L10n.T("eWay-CRM no longer has this item."),
            _ => "",
        };
    }

    /// <summary>crm.ActionText: the action in progressive form, the "%s" of the client's general error sentence ("%s failed: …").</summary>
    public static string ActionText(CrmAction a) => a switch
    {
        CrmAction.Lookup => Contacting(),
        // TRANSLATORS: progressive form for an error text ("Searching eWay-CRM failed: …").
        CrmAction.Search => L10n.T("Searching eWay-CRM"),
        // TRANSLATORS: progressive form for an error text ("Removing from eWay-CRM failed: …").
        CrmAction.Remove => L10n.T("Removing from eWay-CRM"),
        // TRANSLATORS: progressive form for an error text ("Downloading the message failed: …").
        CrmAction.Download => L10n.T("Downloading the message"),
        // TRANSLATORS: progressive form for an error text ("Saving to eWay-CRM failed: …").
        _ => L10n.T("Saving to eWay-CRM"),
    };

    /// <summary>crm.DownloadingText: the status while the <paramref name="n"/> messages crm.save named are downloaded before it is called again.</summary>
    public static string DownloadingText(int n) => n <= 1
        // TRANSLATORS: status line: a message is downloaded before it is saved to eWay-CRM.
        ? L10n.T("Downloading the message first…")
        // TRANSLATORS: status line: messages are downloaded before they are saved to eWay-CRM; %d is their number.
        : L10n.N("Downloading %d message first…", "Downloading %d messages first…", n, n);

    /// <summary>crm.WizardTexts: the account assistant's texts.</summary>
    public static CrmWizardStrings WizardTexts() => new()
    {
        // TRANSLATORS: title of the assistant that adds an eWay-CRM account.
        Title = L10n.T("Add eWay-CRM Account"),
        EditTitle = L10n.T("Edit Account"),
        CredentialsTitle = L10n.T("Sign In"),
        // TRANSLATORS: the address of the eWay-CRM web service, the one its Outlook add-in uses.
        ServiceLabel = L10n.T("Web Service Address"),
        // TRANSLATORS: under the address field of the eWay-CRM assistant; where the address is found.
        ServiceHint = L10n.T("The address of your eWay-CRM, such as https://hosting.eway-crm.com/company. In the web application, point at your company's name in your account panel to see it; in Outlook it is under eWay-CRM → Settings → Connection."),
        NameLabel = L10n.T("Account Name"),
        UserLabel = L10n.T("User Name"),
        PasswordLabel = L10n.T("Password"),
        NewPasswordLabel = L10n.T("New Password (leave empty to keep)"),
        PasswordPrompt = L10n.T("Enter the password for this account"),
        Next = L10n.T("_Next"),
        Add = L10n.T("_Add Account"),
        Save = L10n.T("_Save"),
        Contacting = Contacting(),
        // TRANSLATORS: progressive form for the RPC error text
        SigningIn = L10n.T("Signing in"),
        Adding = L10n.T("Adding the account"),
        Saving = L10n.T("Saving the account"),
        // TRANSLATORS: check box of the eWay-CRM account: once a message is saved, the later messages of its conversation are saved too.
        FollowThreads = L10n.T("Also save later messages of saved conversations"),
        // TRANSLATORS: the line under the check box "Also save later messages of saved conversations".
        FollowThreadsHint = L10n.T("Replies and new messages in a conversation you saved are saved to eWay-CRM with the same links."),
    };

    /// <summary>
    /// crm.CheckServiceInput: the address field as typed: Ok when it can be
    /// looked up, else the problem to show under it ("" while it is empty).
    /// https is assumed when no scheme is typed; http is refused here, before
    /// anything is asked (docs/eway.md D2).
    /// </summary>
    public static (bool Ok, string Problem) CheckServiceInput(string raw)
    {
        ArgumentNullException.ThrowIfNull(raw);
        var s = raw.Trim();
        if (s.Length == 0)
        {
            return (false, "");
        }
        var bad = (false, L10n.T("This is not a web address"));
        foreach (var r in s.EnumerateRunes())
        {
            if (Assistant.IsSpace(r.Value) || Assistant.IsControl(r.Value) || Jira.IsFormat(r.Value) || r.Value == '\\')
            {
                return bad;
            }
        }
        var i = s.IndexOf("://", StringComparison.Ordinal);
        if (i >= 0)
        {
            switch (CodePoints.ToLower(s[..i]))
            {
                case "https":
                    break;
                case "http":
                    return (false, HttpOnly());
                default:
                    return bad;
            }
        }
        else
        {
            s = "https://" + s;
        }
        if (UrlSyntax.Parse(s) is not { } u || u.Rest.Host.Length == 0 || u.Rest.HasUserinfo || UrlSyntax.SplitHostPort(u.Rest.Host).Host.Length == 0)
        {
            return bad;
        }
        return (true, "");
    }

    /// <summary>
    /// crm.FailureOf: what the assistant does after a failed step;
    /// <paramref name="editing"/> as the assistant edits an existing account
    /// (it has no service page). A Microsoft sign-in is told on the service
    /// page before any password (crmSignInUnsupported microsoft), a refused
    /// password flags the password field with the wizard's existing sentence.
    /// </summary>
    public static CrmFailure FailureOf(CrmWizardStep step, Exception? error, bool editing) =>
        FailureOf(step, RpcErrorText.DaemonError(error), editing);

    /// <summary><see cref="FailureOf(CrmWizardStep, Exception?, bool)"/> of the daemon's error object.</summary>
    public static CrmFailure FailureOf(CrmWizardStep step, RpcError? e, bool editing)
    {
        var page = step == CrmWizardStep.Detect && !editing ? CrmWizardPage.Service : CrmWizardPage.Credentials;
        var what = step switch
        {
            CrmWizardStep.Detect => Contacting(),
            CrmWizardStep.Test => L10n.T("Signing in"),
            _ => editing ? L10n.T("Saving the account") : L10n.T("Adding the account"),
        };
        var f = new CrmFailure(page, "", what, CrmWizardField.None);
        if (CrmRules.SignInReason(e) is { } reason)
        {
            f = f with { Banner = SignInText(reason) };
            if (reason == CrmSignInReason.Microsoft && !editing)
            {
                f = f with { Page = CrmWizardPage.Service, Field = CrmWizardField.Service };
            }
            return f;
        }
        var code = e?.Code.Value;
        switch (code)
        {
            case ErrorCode.AuthFailed:
                return f with { Page = CrmWizardPage.Credentials, Field = CrmWizardField.Password, Banner = L10n.T("The server rejected the user name or password") };
            case ErrorCode.AuthRequired:
                return f with { Page = CrmWizardPage.Credentials, Field = CrmWizardField.Password, Banner = L10n.T("Enter the password for this account") };
            case ErrorCode.InvalidArgument or ErrorCode.ServerError when f.Page == CrmWizardPage.Service:
                return f with
                {
                    Field = CrmWizardField.Service,
                    Banner = code == ErrorCode.InvalidArgument ? L10n.T("This is not a web address") : "",
                };
            default:
                return f;
        }
    }

    // savedText: the tooltip of the saved message's button and pill, and the
    // bar's title.
    private static string SavedText() =>
        // TRANSLATORS: the message is saved in eWay-CRM (pill tooltip, bar title).
        L10n.T("Saved in eWay-CRM");

    // signInText: the sentence of crmSignInUnsupported's reason.
    private static string SignInText(CrmSignInReason reason) => reason == CrmSignInReason.Plan
        ? L10n.T("This eWay-CRM does not allow access through its API.")
        : L10n.T("This eWay-CRM signs in with a Microsoft account, which Malachi Mail cannot do yet.");

    // contacting: the progress of account.detectEway and crm.lookup.
    private static string Contacting() =>
        // TRANSLATORS: progress, and progressive form for an error text ("Contacting eWay-CRM failed: …").
        L10n.T("Contacting eWay-CRM");

    // httpOnly: says that http is not used.
    private static string HttpOnly() => L10n.T("Malachi Mail connects to eWay-CRM over https only.");
}

/// <summary>crm.Button: the CRM button of an open message.</summary>
/// <param name="State">Its state.</param>
/// <param name="Label">Its text; "" when hidden.</param>
/// <param name="Tooltip">Its tooltip; "" when hidden.</param>
/// <param name="Enabled">It can be pressed (not while Saving).</param>
/// <param name="Menu">It opens CrmText.SavedMenu instead of the dialog.</param>
public sealed record CrmButton(CrmButtonState State, string Label, string Tooltip, bool Enabled, bool Menu);

/// <summary>crm.DialogStrings: the fixed texts of the dialog.</summary>
public sealed record CrmDialogStrings
{
    /// <summary>Save to eWay-CRM, or Edit Links over a saved message.</summary>
    public required string Title { get; init; }

    /// <summary>The search field's placeholder.</summary>
    public required string Search { get; init; }

    /// <summary>Labels the marker of the superior deal or project.</summary>
    public required string SuperiorItem { get; init; }

    /// <summary>The check box.</summary>
    public required string Private { get; init; }

    /// <summary>Explains the check box.</summary>
    public required string PrivateHint { get; init; }

    /// <summary>Shown when nothing is listed.</summary>
    public required string Empty { get; init; }

    /// <summary>Shown when the CRM did not answer every lookup in time.</summary>
    public required string Partial { get; init; }

    /// <summary>The quiet line under the groups when no deal or project is listed (CrmDialog.OffersSearchHint).</summary>
    public required string SearchHint { get; init; }

    /// <summary>The default button (with its mnemonic).</summary>
    public required string Save { get; init; }
}

/// <summary>crm.BarItem: an item a saved message is linked to, as the bar shows it.</summary>
/// <param name="Kind">Its kind.</param>
/// <param name="Name">Its name, cleaned.</param>
/// <param name="Superior">It is the superior item.</param>
/// <param name="Url">Opens the item (CrmRules.OpenUrl); "" when there is none.</param>
public sealed record CrmBarItem(CrmItemKind Kind, string Name, bool Superior, string Url);

/// <summary>crm.Bar: the strip under a saved message's header: plain texts only.</summary>
public sealed record CrmBar
{
    /// <summary>Saved in eWay-CRM.</summary>
    public required string Title { get; init; }

    /// <summary>The linked items in CrmDialog.Kinds' order.</summary>
    public required IReadOnlyList<CrmBarItem> Items { get; init; }

    /// <summary>Their names joined for one line (capped), "" when the message is linked to nothing.</summary>
    public required string Names { get; init; }

    /// <summary>Opens the saved e-mail item; "" when there is none.</summary>
    public required string Url { get; init; }

    /// <summary>The open button's label.</summary>
    public required string OpenLabel { get; init; }

    /// <summary>The private hint when the saved item is private, else "".</summary>
    public required string Note { get; init; }
}

/// <summary>crm.Pill: the list pill of a saved message.</summary>
/// <param name="Label">"eWay", a brand.</param>
/// <param name="Tooltip">Saved in eWay-CRM.</param>
public sealed record CrmPill(string Label, string Tooltip);

/// <summary>crm.SaveSummary: the outcomes of crm.save counted.</summary>
/// <param name="Saved">Saved messages.</param>
/// <param name="Failed">Failed messages.</param>
/// <param name="Error">The first failed message's error; null when none failed.</param>
public readonly record struct CrmSaveSummary(int Saved, int Failed, RpcError? Error);

/// <summary>crm.Question: a confirmation.</summary>
/// <param name="Heading">Its heading.</param>
/// <param name="Body">Its text.</param>
/// <param name="Confirm">The label of the destructive button that goes ahead (with its mnemonic).</param>
public sealed record CrmQuestion(string Heading, string Body, string Confirm);

/// <summary>crm.Action: a CRM call, for the client's general error sentence.</summary>
public enum CrmAction
{
    /// <summary>crm.save.</summary>
    Save,

    /// <summary>crm.lookup.</summary>
    Lookup,

    /// <summary>crm.search.</summary>
    Search,

    /// <summary>crm.remove.</summary>
    Remove,

    /// <summary>message.download before crm.save.</summary>
    Download,
}

/// <summary>crm.WizardPage: a page of the account assistant. Editing opens on Credentials.</summary>
public enum CrmWizardPage
{
    /// <summary>The web service address (account.detectEway).</summary>
    Service,

    /// <summary>The sign-in (account.test, then account.add or account.update).</summary>
    Credentials,
}

/// <summary>crm.WizardStrings: the assistant's fixed texts.</summary>
public sealed record CrmWizardStrings
{
    /// <summary>Adds.</summary>
    public required string Title { get; init; }

    /// <summary>Edits.</summary>
    public required string EditTitle { get; init; }

    /// <summary>The title of the credentials page.</summary>
    public required string CredentialsTitle { get; init; }

    /// <summary>The address field's label.</summary>
    public required string ServiceLabel { get; init; }

    /// <summary>The line under the address field (where the address is found).</summary>
    public required string ServiceHint { get; init; }

    /// <summary>The name field's label.</summary>
    public required string NameLabel { get; init; }

    /// <summary>The user name field's label.</summary>
    public required string UserLabel { get; init; }

    /// <summary>The password field's label when adding.</summary>
    public required string PasswordLabel { get; init; }

    /// <summary>The password field's label when editing (empty keeps it).</summary>
    public required string NewPasswordLabel { get; init; }

    /// <summary>The credentials page's banner when a password is needed.</summary>
    public required string PasswordPrompt { get; init; }

    /// <summary>The service page's button (with its mnemonic).</summary>
    public required string Next { get; init; }

    /// <summary>The credentials page's button when adding.</summary>
    public required string Add { get; init; }

    /// <summary>The credentials page's button when editing.</summary>
    public required string Save { get; init; }

    /// <summary>The progress of account.detectEway (also its error's action).</summary>
    public required string Contacting { get; init; }

    /// <summary>The progress of account.test.</summary>
    public required string SigningIn { get; init; }

    /// <summary>The progress of account.add.</summary>
    public required string Adding { get; init; }

    /// <summary>The progress of account.update.</summary>
    public required string Saving { get; init; }

    /// <summary>The sign-in page's check box of EwayConfig.FollowThreads (docs/eway.md D22).</summary>
    public required string FollowThreads { get; init; }

    /// <summary>The line under it.</summary>
    public required string FollowThreadsHint { get; init; }
}

/// <summary>crm.WizardStep: a call of the assistant.</summary>
public enum CrmWizardStep
{
    /// <summary>account.detectEway.</summary>
    Detect,

    /// <summary>account.test (one sign-in).</summary>
    Test,

    /// <summary>account.add, or account.update when editing.</summary>
    Save,
}

/// <summary>crm.WizardField: a field the assistant flags and focuses.</summary>
public enum CrmWizardField
{
    /// <summary>None.</summary>
    None,

    /// <summary>The web service address.</summary>
    Service,

    /// <summary>The user name.</summary>
    User,

    /// <summary>The password.</summary>
    Password,
}

/// <summary>
/// crm.Failure: what the assistant does after a failed step: it shows
/// <paramref name="Page"/> with <paramref name="Banner"/> and flags and
/// focuses <paramref name="Field"/>. An empty banner means the client's
/// general sentence for the error with <paramref name="What"/>, the step's
/// action.
/// </summary>
/// <param name="Page">The page to show.</param>
/// <param name="Banner">Its banner; "" for the general sentence.</param>
/// <param name="What">The step's action.</param>
/// <param name="Field">The field to flag.</param>
public readonly record struct CrmFailure(CrmWizardPage Page, string Banner, string What, CrmWizardField Field);
