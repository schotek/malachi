// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The Board's smoke tests (docs/windows-port.md §12) over the published app
// with the sample cases (MALACHI_BOARD_SAMPLES=1, InMemoryBoardSource over
// BoardSamples; no account, no daemon's board): the title bar's switch into
// the Board and the page's "…" menu back to Mail (the search box again),
// the switch's third segment into the Calendar (its page, the search box
// that stays) and the calendar's "…" menu back to Mail,
// the three styles with a case selected and its detail (beside the List,
// in the sliding panel over Columns and Today, which Escape and Close
// close), the conversation's chat toggle (the bubbles on their sides, the
// cards back), Done and Move Back, a Remind preset that takes the case off the
// board, and the Board groups of the Preferences (General's Board View, Open
// at Launch, Show the Board and Keep cases for; AI's triage hidden while
// Register with Claude is off and shown once it is on, over the session's
// own Claude registration, BoardSmokeFixture). Cases are found by
// their sample titles, which are data, never translated; everything else by
// AutomationId.

using System;
using System.Linq;
using System.Windows.Automation;
using Xunit;

namespace Malachi.App.UiTests;

/// <summary>The Board over the sample cases.</summary>
[Collection(OneAppAtATime.Name)]
public sealed class BoardSmokeTests(BoardSmokeFixture fixture) : IClassFixture<BoardSmokeFixture>
{
    // Sample titles (BoardSamples.cs), each case used by one test only.
    private const string Contract = "Sign the contract renewal";
    private const string Onboarding = "Review the onboarding guide";
    private const string Trailer = "Say whether you can bring the trailer";
    private const string Maintenance = "Maintenance on Sunday morning";
    private const string Figures = "Send Chris the Q4 budget figures";
    private const string Laptops = "Quote for the new laptops from Jana";
    private const string Jana = "Jana Sample";

    private const byte VkEscape = 0x1B;

    private AppSession App
    {
        get
        {
            Assert.SkipWhen(fixture.SkipReason is not null, fixture.SkipReason ?? "");
            return fixture.Session;
        }
    }

    // The main window in either mode (AppSession.MainWindow's New Message
    // is the mail's sidebar, hidden while the board shows).
    private AutomationElement Main => Uia.WindowWith(App.ProcessId, "ModeMail", AppSession.StartTimeout);

    [Fact]
    public void TheSwitchShowsTheBoardAndItsMenuGivesTheMailBack()
    {
        var main = ShowBoard();
        // The search box hides with the mail; the board's bar is there.
        Uia.WaitFor(() => Uia.TryFind(main, "SearchBox") is null, "the search box to hide");
        Assert.NotNull(Uia.Find(main, "BoardStyleList"));
        Assert.NotNull(Uia.Find(main, "BoardAccountFilter"));
        Assert.Equal(ToggleState.On, ToggleOf(Uia.Find(main, "ModeBoard")));

        ShowMailFromTheMenu(main);
        Assert.Equal(ToggleState.On, ToggleOf(Uia.Find(main, "ModeMail")));
    }

    [Fact]
    public void TheSwitchShowsTheCalendarAndItsMenuGivesTheMailBack()
    {
        var main = Main;
        ShowMail(main);
        Uia.Toggle(Uia.Find(main, "ModeCalendar"));
        // The calendar's page with its "…"; the search box stays (the
        // calendar's search), the sidebar's primary menu goes.
        var menu = Uia.Find(main, "CalendarMenuButton");
        Assert.Equal(ToggleState.On, ToggleOf(Uia.Find(main, "ModeCalendar")));
        Assert.NotNull(Uia.Find(main, "SearchBox"));
        Uia.WaitFor(() => Uia.TryFind(main, "MainMenuButton") is null, "the mail's sidebar to hide");

        Uia.Invoke(menu);
        Choose(Uia.Find(main, "CalendarMenuMail"));
        Uia.WaitFor(() => Uia.TryFind(main, "CalendarMenuButton") is null, "the calendar to give way to the mail");
        Uia.Find(main, "MainMenuButton");
        Assert.Equal(ToggleState.On, ToggleOf(Uia.Find(main, "ModeMail")));
    }

    [Fact]
    public void TheListShowsTheSelectedCasesDetail()
    {
        var main = ShowBoard();
        SetStyle(main, "BoardStyleList");
        var list = Uia.Find(main, "BoardList");
        Uia.Select(Case(list, Contract));
        AssertDetailShows(main, Contract);
    }

    // The conversation block's toggle shows the case's messages as chat
    // bubbles (the user's own right of the block's middle, the other
    // person's left of it) and back as the cards. The toggle shares Mail's
    // conversation-chat-layout, which the session starts off; the test
    // leaves it off.
    [Fact]
    public void TheConversationToggleShowsTheChatAndTheCardsAgain()
    {
        var main = ShowBoard();
        SetStyle(main, "BoardStyleList");
        Uia.Select(Uia.Find(main, "BoardNavAll"));
        var list = Uia.Find(main, "BoardList");
        Uia.Select(Case(list, Laptops));
        AssertDetailShows(main, Laptops);
        var detail = Uia.Find(main, "BoardDetail");
        var block = Uia.Find(detail, "BoardConversation");
        var toggle = Uia.Find(block, "BoardChatToggle");
        Assert.Equal(ToggleState.Off, ToggleOf(toggle));
        var offName = toggle.Current.Name;
        // The cards: Jana's is named by her (the samples' cards have no
        // fold arrow, their excerpts are short).
        Uia.WaitFor(() => Named(block, Jana), "the cards");

        Uia.Toggle(toggle);
        try
        {
            // You, Jana, you: oldest first.
            var bubbles = Uia.Until(
                () =>
                {
                    AutomationElement?[] found =
                    [
                        Uia.TryFind(block, "BoardChatBubble:0"),
                        Uia.TryFind(block, "BoardChatBubble:1"),
                        Uia.TryFind(block, "BoardChatBubble:2"),
                    ];
                    return found.All(b => b is not null) ? found : null;
                },
                TimeSpan.FromSeconds(10),
                "the case's three bubbles");
            Uia.WaitFor(() => ToggleOf(toggle) == ToggleState.On, "the toggle to be on");
            Assert.NotEqual(offName, toggle.Current.Name);
            var r = block.Current.BoundingRectangle;
            var middle = r.Left + (r.Width / 2);
            double Centre(AutomationElement? b) => b!.Current.BoundingRectangle.Left + (b.Current.BoundingRectangle.Width / 2);
            Assert.True(Centre(bubbles[0]) > middle, "the user's first message on the right");
            Assert.True(Centre(bubbles[1]) < middle, "Jana's message on the left");
            Assert.True(Centre(bubbles[2]) > middle, "the user's reply on the right");
            Assert.Contains(Jana, bubbles[1]!.Current.Name, StringComparison.Ordinal);
        }
        finally
        {
            if (ToggleOf(toggle) == ToggleState.On)
            {
                Uia.Toggle(toggle);
            }
        }
        Uia.WaitFor(() => Uia.TryFind(block, "BoardChatBubble:0") is null, "the bubbles to go");
        Uia.WaitFor(() => Named(block, Jana), "the cards again");
        Assert.Equal(offName, toggle.Current.Name);
    }

    // Whether an element below root is named with text.
    private static bool Named(AutomationElement root, string text) =>
        root.FindAll(TreeScope.Descendants, Condition.TrueCondition).Cast<AutomationElement>()
            .Any(e => e.Current.Name.Contains(text, StringComparison.Ordinal));

    [Fact]
    public void ColumnsShowTheDetailInThePanelThatEscapeCloses()
    {
        var main = ShowBoard();
        SetStyle(main, "BoardStyleColumns");
        var column = Uia.Find(main, "BoardColumnInfo");
        Uia.Select(Case(column, Maintenance));
        var panel = Uia.Find(main, "BoardPanel");
        AssertDetailShows(panel, Maintenance);

        Uia.PressKey(Uia.Find(panel, "BoardDone"), App.ProcessId, VkEscape);
        Uia.WaitFor(() => Uia.TryFind(main, "BoardPanel") is null, "Escape to close the panel");
        SetStyle(main, "BoardStyleList");
    }

    [Fact]
    public void TodayShowsTheDetailInThePanelThatCloseCloses()
    {
        var main = ShowBoard();
        SetStyle(main, "BoardStyleToday");
        var today = Uia.Find(main, "BoardTodayList");
        Uia.Select(Case(today, Figures));
        var panel = Uia.Find(main, "BoardPanel");
        AssertDetailShows(panel, Figures);

        Uia.Invoke(Uia.Find(panel, "BoardPanelClose"));
        Uia.WaitFor(() => Uia.TryFind(main, "BoardPanel") is null, "Close to close the panel");
        SetStyle(main, "BoardStyleList");
    }

    [Fact]
    public void DoneTakesTheCaseToDoneAndMoveBackReturnsIt()
    {
        var main = ShowBoard();
        SetStyle(main, "BoardStyleList");
        Uia.Select(Uia.Find(main, "BoardNavAll"));
        var list = Uia.Find(main, "BoardList");
        Uia.Select(Case(list, Onboarding));
        AssertDetailShows(main, Onboarding);
        var doneName = Uia.Find(Uia.Find(main, "BoardDetail"), "BoardDone").Current.Name;

        Uia.Invoke(Uia.Find(Uia.Find(main, "BoardDetail"), "BoardDone"));
        Uia.WaitFor(() => TryCase(list, Onboarding) is null, "the done case to leave the board");

        // Under Done, its detail offers Move Back (the same button, renamed).
        Uia.Select(Uia.Find(main, "BoardNavDone"));
        Uia.Select(Case(list, Onboarding));
        AssertDetailShows(main, Onboarding);
        var moveBack = Uia.Find(Uia.Find(main, "BoardDetail"), "BoardDone");
        Uia.WaitFor(() => moveBack.Current.Name != doneName, "Done to become Move Back");
        Uia.Invoke(moveBack);
        Uia.WaitFor(() => TryCase(list, Onboarding) is null, "the case to leave Done");

        Uia.Select(Uia.Find(main, "BoardNavAll"));
        Assert.NotNull(Case(list, Onboarding));
    }

    [Fact]
    public void ARemindPresetTakesTheCaseOffTheBoard()
    {
        var main = ShowBoard();
        SetStyle(main, "BoardStyleList");
        Uia.Select(Uia.Find(main, "BoardNavAll"));
        var list = Uia.Find(main, "BoardList");
        Uia.Select(Case(list, Trailer));
        AssertDetailShows(main, Trailer);

        Uia.Invoke(Uia.Find(Uia.Find(main, "BoardDetail"), "BoardRemind"));
        // The flyout is part of the window's tree while it is open.
        Uia.Invoke(Uia.Find(main, "BoardRemindPreset0"));
        Uia.WaitFor(() => TryCase(list, Trailer) is null, "the reminded case to leave the board");
    }

    [Fact]
    public void ThePreferencesShowTheBoardGroups()
    {
        var app = App;
        ShowMail(Main);
        app.MenuItem("MenuPreferences");
        var preferences = Uia.WindowWith(app.ProcessId, "PreferencesNavigation");
        try
        {
            Uia.Select(Uia.Find(preferences, "GeneralPage"));
            Assert.NotNull(Uia.Find(preferences, "BoardDefaultStyle"));
            Assert.NotNull(Uia.Find(preferences, "BoardStartMode"));
            Assert.NotNull(Uia.Find(preferences, "BoardShow"));
            Assert.NotNull(Uia.Find(preferences, "BoardWindowHot"));

            // The triage's group only once the app is registered with Claude
            // (Open In is In App). The session's Claude Code is present and
            // not registered (BoardSmokeFixture): the group is absent, then
            // the switch registers the bridge in the session's ~/.claude.json
            // and the group shows, then the switch takes it back.
            Uia.Select(Uia.Find(preferences, "AiPage"));
            var register = Uia.Find(preferences, "RegisterWithClaude");
            Uia.WaitFor(() => register.Current.IsEnabled, "Register with Claude to know the status");
            Assert.Equal(ToggleState.Off, ToggleOf(register));
            Assert.Null(Uia.TryFind(preferences, "BoardTriageConsent"));
            Assert.Null(Uia.TryFind(preferences, "BoardTriageAuto"));

            Uia.Toggle(register);
            Uia.WaitFor(() => ToggleOf(Uia.Find(preferences, "RegisterWithClaude")) == ToggleState.On, "the bridge to be registered");
            Assert.NotNull(Uia.Find(preferences, "BoardTriageConsent"));
            Assert.NotNull(Uia.Find(preferences, "BoardTriageAuto"));
            Assert.NotNull(Uia.Find(preferences, "BoardTriageAccountsSubtitle"));

            Uia.Toggle(Uia.Find(preferences, "RegisterWithClaude"));
            Uia.WaitFor(() => ToggleOf(Uia.Find(preferences, "RegisterWithClaude")) == ToggleState.Off, "the bridge to be unregistered");
            Uia.WaitFor(() => Uia.TryFind(preferences, "BoardTriageConsent") is null, "the triage's group to hide");
        }
        finally
        {
            Uia.Close(preferences);
            Uia.WaitGone(preferences, what: "the Preferences");
        }
    }

    // The main window in Board mode, entered through the title bar's switch.
    private AutomationElement ShowBoard()
    {
        var main = Main;
        if (!ShowsBoard(main))
        {
            Uia.Toggle(Uia.Find(main, "ModeBoard"));
        }
        // The board's bar (BoardPage itself, a UserControl, has no element).
        Uia.Find(main, "BoardStyleList");
        return main;
    }

    private static bool ShowsBoard(AutomationElement main) => Uia.TryFind(main, "BoardStyleList") is not null;

    // Back to Mail through the title bar's switch (from the Board or the Calendar).
    private static void ShowMail(AutomationElement main)
    {
        if (ShowsBoard(main) || Uia.TryFind(main, "CalendarMenuButton") is not null)
        {
            Uia.Toggle(Uia.Find(main, "ModeMail"));
        }
        Uia.Find(main, "MainMenuButton");
    }

    // A mode's item of a "…" menu (a radio item: invoked, or toggled).
    private static void Choose(AutomationElement item)
    {
        if (item.TryGetCurrentPattern(InvokePattern.Pattern, out var invoke))
        {
            ((InvokePattern)invoke).Invoke();
        }
        else
        {
            Uia.Toggle(item);
        }
    }

    // Back to Mail through the board's "…" menu: the search box is back.
    private static void ShowMailFromTheMenu(AutomationElement main)
    {
        Uia.Invoke(Uia.Find(main, "BoardPageMenu"));
        Choose(Uia.Find(main, "BoardMenuMail"));
        Uia.WaitFor(() => !ShowsBoard(main), "the board to give way to the mail");
        var search = Uia.Find(main, "SearchBox");
        Uia.WaitFor(() => !search.Current.IsOffscreen, "the search box to show again");
    }

    private static void SetStyle(AutomationElement main, string automationId)
    {
        var button = Uia.Find(main, automationId);
        if (ToggleOf(button) != ToggleState.On)
        {
            Uia.Toggle(button);
        }
        Uia.WaitFor(() => ToggleOf(Uia.Find(main, automationId)) == ToggleState.On, automationId + " to be the style");
    }

    private static ToggleState ToggleOf(AutomationElement element) =>
        ((TogglePattern)element.GetCurrentPattern(TogglePattern.Pattern)).Current.ToggleState;

    // A case's row (or card) below root whose spoken name carries its title.
    private static AutomationElement Case(AutomationElement root, string title) =>
        Uia.Until(() => TryCase(root, title), null, "the case " + title);

    private static AutomationElement? TryCase(AutomationElement root, string title) =>
        root.FindAll(TreeScope.Descendants, new PropertyCondition(AutomationElement.AutomationIdProperty, "BoardCase"))
            .Cast<AutomationElement>()
            .FirstOrDefault(r => r.Current.Name.Contains(title, StringComparison.Ordinal));

    // The detail below root shows the case titled title.
    private static void AssertDetailShows(AutomationElement root, string title)
    {
        var detail = Uia.Find(root, "BoardDetail");
        Uia.WaitFor(
            () => Uia.All(detail, ControlType.Text).Cast<AutomationElement>()
                .Any(t => t.Current.Name.Contains(title, StringComparison.Ordinal)),
            "the detail of " + title);
    }
}
