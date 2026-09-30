// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/JiraAccountController.swift;
// GTK: ui/internal/jiraaccount/controller.go (Controller). Its texts and
// rules are ui/internal/jira/settings.go and wizard.go (Jira.Settings.cs,
// Jira.Wizard.cs).
//
// The Swift callbacks are events of the same words: onChange is Changed,
// onBusy BusyChanged, onBanner BannerChanged, onDone Done, onClose
// CloseRequested, onReplaceToken ReplaceTokenRequested. Every call goes
// through the controller's ControllerScope with the op counter of
// controller.go.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.I18n;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Platform;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>
/// The settings of a Jira account (kind <c>jira</c>) with the widgets
/// replaced by events: the window Preferences → Accounts opens for such an
/// account. The page opens with <c>account.listSpaces</c> for the stored
/// account, without a token (the daemon takes the stored one): the spaces
/// and the statuses to choose from and the user the account signs in as.
/// When that fails the banner says why and the page edits what is stored.
/// Everything the page changes is a copy (<see cref="Form"/>) until Save,
/// which is <c>account.update</c> with the form applied to the stored
/// configuration and empty credentials, so the token stays as it is; a form
/// that changes nothing the daemon acts on closes without a call. The token
/// itself is replaced by the account assistant in its edit mode
/// (<see cref="JiraWizardController"/>), which the window opens on
/// <see cref="ReplaceTokenRequested"/>.
/// </summary>
/// <remarks>
/// Create it, and call it, on the UI thread; every event is raised there.
/// Subscribe, then call <see cref="Start"/>. Every reply is dropped once the
/// page closed or another call started since (<see cref="Op"/>). Nothing
/// typed here is logged.
/// </remarks>
public sealed partial class JiraAccountController : IDisposable
{
    private readonly ControllerScope scope;
    private readonly ILogger logger;

    // Why the entry typed last was not added, per list.
    private readonly Dictionary<JiraListKind, string> problems = [];

    // Why account.listSpaces failed; stays until it is asked again.
    private string? loadProblem;

    // Why Save did nothing; goes with the next change of the form.
    private string? saveProblem;

    // The banner the UI was told last.
    private string? shownBanner;

    /// <summary>The settings of the stored <paramref name="account"/>, on the calling (UI) thread.</summary>
    /// <param name="client">The daemon.</param>
    /// <param name="account">The account as it is stored.</param>
    /// <param name="logger">Receives steps, counts and error classes, never what was typed.</param>
    /// <param name="pending">Counts the page's background work; one of its own when null.</param>
    public JiraAccountController(RpcClient client, Account account, ILogger<JiraAccountController>? logger = null, PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        ArgumentNullException.ThrowIfNull(account);
        Client = client;
        Account = account;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        scope = new ControllerScope(pending);
        Form = Jira.NewSettingsForm(account.Config);
    }

    // Outputs

    /// <summary>
    /// What the page shows changed (the rows, a selection, a list, a
    /// problem): the UI reads the controller again (Swift <c>onChange</c>).
    /// </summary>
    public event EventHandler? Changed;

    /// <summary>
    /// A call started, with its progress text, or finished (null). While the
    /// spaces load the page stays usable; while it saves it waits
    /// (<see cref="Saving"/>; Swift <c>onBusy</c>).
    /// </summary>
    public event EventHandler<string?>? BusyChanged;

    /// <summary>The page's banner; null hides it (Swift <c>onBanner</c>).</summary>
    public event EventHandler<string?>? BannerChanged;

    /// <summary>The account was stored; the UI closes the page (Swift <c>onDone</c>).</summary>
    public event EventHandler<(AccountId Id, AccountConfig Config)>? Done;

    /// <summary>Save had nothing to store; the UI closes the page (Swift <c>onClose</c>).</summary>
    public event EventHandler? CloseRequested;

    /// <summary>
    /// "Replace Token…": the UI opens the account assistant in its edit mode
    /// for the account and calls <see cref="TokenReplaced"/> once it stored
    /// the new token (Swift <c>onReplaceToken</c>).
    /// </summary>
    public event EventHandler<Account>? ReplaceTokenRequested;

    // State

    /// <summary>The daemon.</summary>
    public RpcClient Client { get; }

    /// <summary>The account as it is stored.</summary>
    public Account Account { get; }

    /// <summary>The fixed texts (<see cref="Jira.SettingsTexts"/>).</summary>
    public JiraSettingsStrings Texts { get; } = Jira.SettingsTexts();

    /// <summary>The edited copy.</summary>
    public JiraSettingsForm Form { get; private set; }

    /// <summary>What <c>account.listSpaces</c> answered; null before it did and when it failed.</summary>
    public AccountListSpacesResult? Listing { get; private set; }

    /// <summary>The banner shown; null for none: why Save failed, else why the spaces could not be listed.</summary>
    public string? Banner => saveProblem ?? loadProblem;

    /// <summary>The progress text of the call under way; null when none runs.</summary>
    public string? Progress { get; private set; }

    /// <summary><c>account.update</c> is under way: the page waits.</summary>
    public bool Saving { get; private set; }

    /// <summary>The page went away: late replies are dropped (Swift <c>closed</c>).</summary>
    public bool IsClosed => scope.IsClosed;

    /// <summary>Bumped per RPC so that stale replies bail out (controller.go <c>op</c>).</summary>
    public int Op { get; private set; }

    // Presentation

    /// <summary>The window's title.</summary>
    public string Title => Texts.Title;

    /// <summary>The label of the button that saves, without its mnemonic.</summary>
    public static string SaveLabel => WizardController.WithoutMnemonic(L10n.T("_Save"));

    /// <summary>The account's deployment; Jira Cloud when it has none.</summary>
    public JiraDeployment Deployment => Account.Config.Jira?.Deployment ?? JiraDeployment.Cloud;

    /// <summary>The site's rows; the user is the listing's once it answered.</summary>
    public JiraSiteInfo Site => Jira.SettingsSite(Account.Config, Listing?.User);

    private IReadOnlyList<SpaceRef> StoredSpaces => Account.Config.Jira?.Spaces ?? [];

    private IReadOnlyList<Space> ListedSpaces => Listing?.Spaces ?? [];

    private IReadOnlyList<IssueStatus> ListedStatuses => Listing?.Statuses ?? [];

    /// <summary>The spaces to choose from.</summary>
    public IReadOnlyList<JiraSpaceRow> SpaceRows => Jira.SettingsSpaceRows(StoredSpaces, ListedSpaces);

    /// <summary>The ids of the chosen spaces.</summary>
    public IReadOnlySet<string> SelectedSpaces => new HashSet<string>(Form.Spaces.Select(r => r.Id), StringComparer.Ordinal);

    /// <summary>Why the chosen spaces cannot be saved; "" when they can.</summary>
    public string SpacesProblem => Jira.SpacesProblem(Form.Spaces.Count);

    /// <summary>The labels of the offline window's choices.</summary>
    public static IReadOnlyList<string> OfflineLabels => Jira.OfflineChoiceLabels();

    /// <summary>The offline window's choice shown.</summary>
    public int OfflineIndex => Jira.IndexOfOfflineDays(Form.OfflineDays);

    /// <summary>The picker of the closed statuses.</summary>
    public IReadOnlyList<JiraStatusGroup> StatusGroups => Jira.StatusGroups(ListedStatuses, Form.ClosedStatuses);

    /// <summary>Why the chosen statuses cannot be saved; "" when they can.</summary>
    public string StatusesProblem => Jira.StatusesProblem(Form.ClosedStatuses);

    /// <summary>The labels of the notification modes.</summary>
    public static IReadOnlyList<string> NotificationLabels => Jira.NotificationModeLabels();

    /// <summary>The notification mode shown.</summary>
    public int NotificationIndex => Jira.IndexOfNotificationMode(Form.NotificationMail);

    /// <summary>The text under the notification mode.</summary>
    public string NotificationHint => Jira.NotificationHint(Form.NotificationMail);

    /// <summary>Whether the senders matter (the mode does something with the mails).</summary>
    public bool SendersEditable => Jira.SendersEditable(Form.NotificationMail);

    /// <summary>What an empty list of senders stands for.</summary>
    public string SendersPlaceholder => Jira.DefaultSenders(Account.Config);

    /// <summary>A call runs.</summary>
    public bool Busy => Progress is not null;

    /// <summary>The form differs from the stored account in what the daemon acts on.</summary>
    public bool IsChanged => Jira.Changed(Account.Config, Form.Apply(Account.Config));

    /// <summary>Save is offered: nothing is being saved and the form can be.</summary>
    public bool CanSave => !Saving && Form.SettingsProblem().Length == 0;

    /// <summary>Whether the view <paramref name="v"/> is switched on.</summary>
    public bool FolderShown(VirtualFolder v) => Jira.FolderShown(Form.DisabledFolders, v);

    /// <summary>The entries of a list.</summary>
    public IReadOnlyList<string> Entries(JiraListKind kind) => kind switch
    {
        JiraListKind.BotNames => Form.BotNames,
        JiraListKind.MetadataFilters => Form.MetadataFilters,
        JiraListKind.AuthorPrefixes => Form.AuthorPrefixes,
        _ => Form.NotificationSenders,
    };

    /// <summary>Why the entry typed last was not added to the list; "" for none.</summary>
    public string Problem(JiraListKind kind) => problems.GetValueOrDefault(kind, "");

    /// <summary>The entries offered for a list with one click.</summary>
    public IReadOnlyList<JiraSuggestion> Suggestions(JiraListKind kind) => Jira.Suggestions(kind, Entries(kind));

    // Lifecycle

    /// <summary>Delivers the initial state and asks for the spaces and statuses. Call once, after subscribing.</summary>
    public void Start()
    {
        scope.VerifyAccess();
        Notify();
        Load();
    }

    /// <summary>The page went away: every late reply is dropped from now on.</summary>
    public void Close()
    {
        Progress = null;
        Saving = false;
        scope.Close();
    }

    /// <inheritdoc/>
    public void Dispose() => Close();

    // Inputs from the UI

    /// <summary>The account's name as typed.</summary>
    public void SetName(string name)
    {
        ArgumentNullException.ThrowIfNull(name);
        if (name == Form.Name)
        {
            return;
        }
        Form = Form with { Name = name };
        Edited();
    }

    /// <summary>A space's check box.</summary>
    public void SetSpace(string id, bool on)
    {
        if (Saving || !SpaceRows.Any(r => r.Id == id) || on == SelectedSpaces.Contains(id))
        {
            return;
        }
        Form = Form with { Spaces = Jira.SetSpaceSelected(Form.Spaces, StoredSpaces, ListedSpaces, id, on) };
        Edited();
    }

    /// <summary>The offline window's choice (an index of <see cref="Jira.OfflineChoices"/>).</summary>
    public void SetOfflineIndex(int i)
    {
        if (Saving || i < 0 || i >= Jira.OfflineChoices.Count || Jira.OfflineChoices[i] == Form.OfflineDays)
        {
            return;
        }
        Form = Form with { OfflineDays = Jira.OfflineChoices[i] };
        Edited();
    }

    /// <summary>"Only Issues Involving Me".</summary>
    public void SetOnlyMine(bool on)
    {
        if (Saving || on == Form.OnlyMine)
        {
            return;
        }
        Form = Form with { OnlyMine = on };
        Edited();
    }

    /// <summary>"Show Status and Assignee Changes".</summary>
    public void SetShowEvents(bool on)
    {
        if (Saving || on == Form.ShowEvents)
        {
            return;
        }
        Form = Form with { ShowEvents = on };
        Edited();
    }

    /// <summary>The switch of a view.</summary>
    public void SetFolder(VirtualFolder v, bool shown)
    {
        if (Saving || !Jira.VirtualFolders.Contains(v) || shown == FolderShown(v))
        {
            return;
        }
        Form = Form with { DisabledFolders = Jira.SetFolderShown(Form.DisabledFolders, v, shown) };
        Edited();
    }

    /// <summary>A check box of the picker of the closed statuses.</summary>
    public void SetStatus(JiraStatusChoice choice, bool on)
    {
        ArgumentNullException.ThrowIfNull(choice);
        if (Saving)
        {
            return;
        }
        Form = Form with { ClosedStatuses = Jira.SetStatusSelected(ListedStatuses, Form.ClosedStatuses, choice, on) };
        Edited();
    }

    /// <summary>The notification mode's choice (an index of <see cref="Jira.NotificationModes"/>).</summary>
    public void SetNotificationIndex(int i)
    {
        if (Saving || i < 0 || i >= Jira.NotificationModes.Count || Jira.NotificationModes[i] == Form.NotificationMail)
        {
            return;
        }
        Form = Form with { NotificationMail = Jira.NotificationModes[i] };
        Edited();
    }

    /// <summary>
    /// Adds what the user typed to a list (<see cref="Jira.CheckEntry"/>).
    /// True when the field may be emptied: the entry was added, or there was
    /// nothing to add. Otherwise <see cref="Problem"/> says why not.
    /// </summary>
    public bool AddEntry(JiraListKind kind, string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        if (Saving)
        {
            return false;
        }
        var (entry, problem) = Jira.CheckEntry(kind, text, Entries(kind));
        if (entry.Length == 0)
        {
            if (problem.Length == 0)
            {
                problems.Remove(kind);
            }
            else
            {
                problems[kind] = problem;
            }
            Notify();
            return problem.Length == 0;
        }
        problems.Remove(kind);
        SetEntries(kind, [.. Entries(kind), entry]);
        Edited();
        return true;
    }

    /// <summary>Adds a suggested entry (<see cref="Suggestions"/>).</summary>
    public void AddSuggestion(JiraListKind kind, string value)
    {
        if (!Suggestions(kind).Any(s => s.Value == value))
        {
            return;
        }
        AddEntry(kind, value);
    }

    /// <summary>Removes the entry at <paramref name="index"/> of a list.</summary>
    public void RemoveEntry(JiraListKind kind, int index)
    {
        var list = Entries(kind);
        if (Saving || index < 0 || index >= list.Count)
        {
            return;
        }
        problems.Remove(kind);
        SetEntries(kind, [.. list.Take(index), .. list.Skip(index + 1)]);
        Edited();
    }

    /// <summary>The field of a list changed: its problem belongs to what was typed before.</summary>
    public void EntryTyped(JiraListKind kind)
    {
        if (!problems.Remove(kind))
        {
            return;
        }
        Notify();
    }

    /// <summary>"Replace Token…": the UI opens the account assistant.</summary>
    public void ReplaceToken()
    {
        if (IsClosed || Saving)
        {
            return;
        }
        scope.Raise(ReplaceTokenRequested, this, Account);
    }

    /// <summary>The account assistant stored a new token: the spaces and statuses are asked for again with it.</summary>
    public void TokenReplaced()
    {
        if (IsClosed || Saving)
        {
            return;
        }
        Load();
    }

    /// <summary>Save: <c>account.update</c> with the form, or nothing when the form changes nothing.</summary>
    public void Save()
    {
        if (IsClosed || Saving)
        {
            return;
        }
        var problem = Form.SettingsProblem();
        if (problem.Length > 0)
        {
            saveProblem = problem;
            ShowBanner();
            return;
        }
        var cfg = Form.Apply(Account.Config);
        if (!Jira.Changed(Account.Config, cfg))
        {
            RaiseEmpty(CloseRequested);
            return;
        }
        saveProblem = null;
        ShowBanner();
        Saving = true;
        SetBusy(Texts.Saving);
        Notify();
        var op = ++Op;
        var id = Account.Id;
        // Empty credentials: the daemon keeps the stored token.
        var parameters = new AccountUpdateParams { AccountId = id, Config = cfg, Credentials = new Credentials() };
        scope.Perform(Client, API.AccountUpdate, parameters, outcome =>
        {
            if (op != Op)
            {
                return;
            }
            Saving = false;
            SetBusy(null);
            if (outcome.Error is { } error)
            {
                saveProblem = Failure(JiraWizardStep.Save, error);
                ShowBanner();
                Notify();
                return;
            }
            LogSaved(logger, id.Value);
            scope.Raise(Done, this, (id, cfg));
        }, RpcTimeouts.Save);
    }

    // Internals

    private void SetEntries(JiraListKind kind, IReadOnlyList<string> list) => Form = kind switch
    {
        JiraListKind.BotNames => Form with { BotNames = list },
        JiraListKind.MetadataFilters => Form with { MetadataFilters = list },
        JiraListKind.AuthorPrefixes => Form with { AuthorPrefixes = list },
        _ => Form with { NotificationSenders = list },
    };

    // The form changed: what the last Save said is about another form.
    private void Edited()
    {
        saveProblem = null;
        ShowBanner();
        Notify();
    }

    private void SetBusy(string? text)
    {
        if (Progress == text)
        {
            return;
        }
        Progress = text;
        scope.Raise(BusyChanged, this, text);
    }

    // Tells the UI the banner when it changed.
    private void ShowBanner()
    {
        var text = Banner;
        if (shownBanner == text)
        {
            return;
        }
        shownBanner = text;
        scope.Raise(BannerChanged, this, text);
    }

    private void Notify() => RaiseEmpty(Changed);

    // Raises a parameterless event, every handler guarded (ControllerEvents).
    private void RaiseEmpty(EventHandler? handlers)
    {
        if (IsClosed || handlers is null)
        {
            return;
        }
        foreach (var handler in handlers.GetInvocationList())
        {
            scope.Guard(() => ((EventHandler)handler).Invoke(this, EventArgs.Empty));
        }
    }

    // The banner of a failed call: the step's own sentence (Jira.FailureOf:
    // a refused or missing token, an account that exists already), else the
    // client's for the error.
    private string Failure(JiraWizardStep step, Exception error)
    {
        var cls = Jira.Classify(error);
        LogStepFailed(logger, step, cls);
        var f = Jira.FailureOf(step, cls, Deployment, editing: true);
        return f.Banner.Length == 0 ? RpcErrorText.Text(f.What, error) : f.Banner;
    }

    // account.listSpaces for the stored account with its stored token.
    private void Load()
    {
        var parameters = new AccountListSpacesParams { AccountId = Account.Id, Config = Account.Config, Credentials = new Credentials(), Counts = false };
        loadProblem = null;
        ShowBanner();
        SetBusy(Texts.Loading);
        var op = ++Op;
        scope.Perform(Client, API.AccountListSpaces, parameters, outcome =>
        {
            if (op != Op)
            {
                return;
            }
            SetBusy(null);
            if (outcome.TryGetValue(out var res, out var error))
            {
                LogListed(logger, res.Spaces.Count, res.Statuses.Count);
                Listing = res;
            }
            else
            {
                // The page edits what is stored.
                loadProblem = Failure(JiraWizardStep.Spaces, error!);
                ShowBanner();
            }
            Notify();
        });
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "jira account step {Step} failed: class {Class}")]
    private static partial void LogStepFailed(ILogger logger, JiraWizardStep step, JiraErrorClass @class);

    [LoggerMessage(Level = LogLevel.Information, Message = "jira account listed: {Spaces} spaces, {Statuses} statuses")]
    private static partial void LogListed(ILogger logger, int spaces, int statuses);

    [LoggerMessage(Level = LogLevel.Information, Message = "jira account saved: {AccountId}")]
    private static partial void LogSaved(ILogger logger, string accountId);
}
