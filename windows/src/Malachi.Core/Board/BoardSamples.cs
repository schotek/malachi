// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/BoardSamples.swift; GTK:
// ui/internal/board/samples.go (SampleSnapshot, samples), string for string.
//
// The dummy board's cases: invented sample data, no real person, company or
// address in it (names like "Ada Example", addresses only under
// example.invalid, issue keys DEMO-n). The dates are relative to now, so the
// board looks the same whenever it opens: every state, every deadline
// group, cases the assistant has and has not looked at, one it moved, one
// the user moved, an issue, a suggested reply, tasks, commitments and two
// done cases. English and not for translation (Windows-only data, as
// macOS's): they are a development aid (MALACHI_BOARD_SAMPLES=1) and test
// data. The account badges are FolderTree's capsules.

using System;
using System.Collections.Generic;
using System.Globalization;
using Malachi.Core.Api;
using Malachi.Core.IssueTrackers;

namespace Malachi.Core.Board;

public static partial class Board
{
    /// <summary>
    /// The invented sample board, dated relative to <paramref name="now"/>
    /// in <paramref name="timeZone"/> (the local one when null).
    /// </summary>
    public static Snapshot SampleSnapshot(DateTimeOffset now, TimeZoneInfo? timeZone = null)
    {
        // Windows-only strings: invented sample data, not for translation
        // (macOS's are macOS-only strings for the same reason).
        var s = new Samples(now, timeZone ?? TimeZoneInfo.Local);
        var work = new AccountId("sample-work");
        var home = new AccountId("sample-home");
        var club = new AccountId("sample-club");
        var jira = new AccountId("sample-jira");
        AccountInfo[] accounts =
        [
            new(work, "Contoso Ltd", "M365"),
            new(home, "Home", "GOOGLE"),
            new(club, "Rowing Club", "IMAP"),
            new(jira, "Demo Jira", Jira.KindBadge),
        ];
        static CaseMessage Msg(string from, DateTimeOffset date, string text) => new() { From = from, Date = date, Text = text };
        static CaseMessage Mine(DateTimeOffset date, string text) => new() { From = "You", Date = date, Text = text, Mine = true };
        static Visibility DoneNow() => Visibility.Done();

        var cases = new List<Case>
        {
            // Hot.
            new()
            {
                Id = Samples.Id(1), Account = work, Person = "Ada Example", Date = s.AgoMinutes(35),
                Subject = "Re: Contract renewal for 2027",
                Snippet = "Attached is the final version. We need the signed copy back today.",
                Unread = true, HasAttachments = true, MessageCount = 3, RuleState = State.You,
                RuleReason = BoardReason.YouRepliedToYou,
                Annotation = new()
                {
                    State = State.Hot, Title = "Sign the contract renewal",
                    Summary = "Ada sent the final contract for 2027. Legal has approved it; only your signature is missing.\n\n"
                        + "She needs the signed copy back by 5 pm today, or the old terms run on for another year.",
                    Why = "The deadline is today, and missing it renews the old terms.", Due = s.At(0, 17),
                    DueQuote = "We need the signed copy back by 5 pm today.",
                    Tasks = ["Sign page 4 and initial the appendix", "Confirm the start date of 1 January"],
                },
                Draft = new(new DraftId("sample-draft-1"), "Hi Ada,\n\nthanks for the final version. I will sign it and send it back this afternoon.\n\nBest regards"),
                Messages =
                [
                    Msg("Ada Example", s.AgoDays(3, 10),
                        "Hello,\n\nhere is the draft of the renewal for 2027. Could you look at the start date?\n\n"
                        + "A few things changed since last year. The notice period is now three months instead of two, "
                        + "and the price follows the yearly index from the second year on. The support hours stay as they "
                        + "are, but the response time for urgent requests goes down from eight hours to four.\n\n"
                        + "The appendix with the service levels is new. It lists what counts as an outage, how it is "
                        + "measured and what credit you get when a month falls short. Legal asked us to keep it separate "
                        + "so that it can be updated without a new contract.\n\n"
                        + "If anything in the draft does not work for you, mark it in the document and I will take it to "
                        + "our side this week.\n\nBest regards,\nAda"),
                    Mine(s.AgoDays(2, 15), "Thanks, Ada. The start date of 1 January works for us."),
                    Msg("Ada Example", s.AgoMinutes(35),
                        "Attached is the final version. Legal has approved it.\n\nWe need the signed copy back by 5 pm today.\n\n"
                        + "The start date is 1 January, as you confirmed. Page 4 needs your signature and the appendix your "
                        + "initials on each page. A scan is enough for now; the original can follow by post.\n\n"
                        + "If the copy is not back today, the old terms run on for another year and the new service levels "
                        + "would only start in 2028. I am at my desk until six if anything is unclear.\n\n"
                        + "Thank you, and best regards,\nAda"),
                ],
            },
            new()
            {
                Id = Samples.Id(2), Account = jira, Person = "Ben Placeholder", Date = s.AgoHours(2),
                Subject = "DEMO-14: Checkout fails for some card payments",
                Snippet = "Ben Placeholder raised the priority to Highest.", Unread = true, MessageCount = 4,
                Issue = new("DEMO-14", "In Progress", JiraStatusStyle.InProgress), RuleState = State.Hot,
                RuleReason = BoardReason.JiraAssigned,
                Annotation = new()
                {
                    State = State.Hot, Title = "Fix the failing card payments",
                    Summary = "Some card payments fail at the last step of the checkout since the last release. Ben asks for a fix before the sale starts.",
                    Why = "The issue blocks payments and was due two days ago.", Due = s.At(-2, 12),
                    DueQuote = "This has to be fixed within two days.",
                    Tasks = ["Find which card types fail", "Tell Ben when a fix is ready to test"],
                },
                Messages =
                [
                    Msg("Ben Placeholder", s.AgoDays(4, 9),
                        "Some card payments fail at the last step. This has to be fixed within two days."),
                    Mine(s.AgoDays(3, 11), "I can reproduce it with a test card. Looking into it."),
                    Msg("Ben Placeholder", s.AgoDays(1, 16), "Any news? The sale starts in a few days."),
                    Msg("Ben Placeholder", s.AgoHours(2), "Raised the priority to Highest."),
                ],
            },
            new()
            {
                Id = Samples.Id(3), Account = home, Person = "Northwind Insurance", Date = s.AgoHours(5),
                Subject = "Your car insurance ends soon", Snippet = "Renew now to stay covered without a break.",
                Unread = true, RuleState = State.Hot, RuleReason = BoardReason.HotImportant,
                Messages =
                [
                    Msg("Northwind Insurance", s.AgoHours(5),
                        "Your car insurance ends soon. Renew now to stay covered without a break."),
                ],
            },

            // Waiting for you.
            new()
            {
                Id = Samples.Id(4), Account = work, Person = "Chris Sample", Date = s.AgoHours(3),
                Subject = "Budget figures for Q4", Snippet = "Could you send me the updated figures?",
                MessageCount = 2, RuleState = State.You, RuleReason = BoardReason.YouRepliedToYou,
                Annotation = new()
                {
                    State = State.You, Title = "Send Chris the Q4 budget figures",
                    Summary = "Chris needs the updated Q4 figures for the board meeting tomorrow.",
                    Why = "Chris asked you directly and the meeting is tomorrow.", Due = s.At(1, 12),
                    DueQuote = "The board meets tomorrow at noon.",
                    Tasks = ["Update the travel line", "Send the sheet to Chris"],
                },
                Messages =
                [
                    Mine(s.AgoDays(1, 14), "I will send the updated figures before the meeting."),
                    Msg("Chris Sample", s.AgoHours(3),
                        "Could you send me the updated figures? The board meets tomorrow at noon."),
                ],
            },
            new()
            {
                Id = Samples.Id(5), Account = work, Person = "Dana Mock", Date = s.AgoHours(6),
                Subject = "Can you review the onboarding guide?",
                Snippet = "The guide is attached; comments in the document are fine.", HasAttachments = true,
                RuleState = State.You, RuleReason = BoardReason.YouAddressed,
                Annotation = new()
                {
                    State = State.You, Title = "Review the onboarding guide",
                    Summary = "Dana wants your comments on the new onboarding guide before it goes to the new starters.",
                    Why = "Dana asked you for a review within four days.", Due = s.At(4, 9),
                    DueQuote = "Comments within four days would be great.",
                },
                Draft = new(new DraftId("sample-draft-2"), "Hi Dana,\n\nI will read the guide and leave my comments in the document within four days."),
                Messages =
                [
                    Msg("Dana Mock", s.AgoHours(6),
                        "Hi,\n\nthe onboarding guide is attached. Comments within four days would be great."),
                ],
            },
            new()
            {
                Id = Samples.Id(6), Account = club, Person = "Erin Fictional", Date = s.AgoHours(8),
                Subject = "Saturday's regatta", Snippet = "Who brings the boat trailer?", MessageCount = 2,
                RuleState = State.Info, RuleReason = BoardReason.InfoNotAddressed,
                Annotation = new()
                {
                    State = State.You, Title = "Say whether you can bring the trailer",
                    Summary = "Erin asks the crew who can bring the boat trailer to Saturday's regatta.",
                    Why = "Erin asked you by name in the second message.",
                    Tasks = ["Check whether the car is free on Saturday", "Answer Erin"],
                },
                Messages =
                [
                    Msg("Erin Fictional", s.AgoDays(1, 19), "Hello crew, the regatta starts at 9 on Saturday."),
                    Msg("Erin Fictional", s.AgoHours(8), "Who brings the boat trailer? You had it last time, could you again?"),
                ],
            },
            new()
            {
                Id = Samples.Id(7), Account = home, Person = "Fabrikam Dental", Date = s.AgoHours(10),
                Subject = "Please confirm your appointment", Snippet = "Reply YES to confirm your appointment.",
                Unread = true, RuleState = State.You, RuleReason = BoardReason.YouAddressed,
                Messages =
                [
                    Msg("Fabrikam Dental", s.AgoHours(10), "Reply YES to confirm your appointment, or call us to move it."),
                ],
            },
            new()
            {
                Id = Samples.Id(8), Account = jira, Person = "Gus Invented", Date = s.AgoHours(20),
                Subject = "DEMO-21: Add export to CSV", Snippet = "Gus Invented asked: which columns should the export have?",
                MessageCount = 2, Issue = new("DEMO-21", "To Do", JiraStatusStyle.Todo), RuleState = State.You,
                RuleReason = BoardReason.JiraReporter,
                Annotation = new()
                {
                    State = State.You, Title = "Tell Gus which columns the export needs",
                    Summary = "Gus is ready to start on the CSV export and needs the list of columns from you.",
                    Why = "Gus mentioned you and is waiting before he starts.",
                },
                Messages =
                [
                    Msg("Gus Invented", s.AgoDays(2, 10), "Created the issue: the report should be exportable as CSV."),
                    Msg("Gus Invented", s.AgoHours(20), "Which columns should the export have? I will start once I know."),
                ],
            },
            new()
            {
                Id = Samples.Id(9), Account = work, Person = "Hana Demo", Date = s.AgoDays(1, 12),
                Subject = "Lunch next week?", Snippet = "Tuesday or Wednesday?", RuleState = State.You,
                RuleReason = BoardReason.YouAddressed,
                Annotation = new()
                {
                    State = State.You, Title = "Pick a day for lunch with Hana",
                    Summary = "Hana suggests lunch next Tuesday or Wednesday.", Why = "Hana waits for your choice.",
                },
                Messages =
                [
                    Msg("Hana Demo", s.AgoDays(1, 12), "Shall we have lunch next week? Tuesday or Wednesday?"),
                ],
            },
            new()
            {
                Id = Samples.Id(10), Account = home, Person = "Ivo Testcase", Date = s.AgoDays(2, 20),
                Subject = "Photos from the trip", Snippet = "Here are the photos, pick the ones for the album.",
                HasAttachments = true, RuleState = State.You, RuleReason = BoardReason.YouAddressed,
                Annotation = new()
                {
                    State = State.You, Title = "Pick photos for the album",
                    Summary = "Ivo sent the photos from the trip and asks which ones go into the printed album.",
                    Why = "Ivo orders the album once you have picked.", Due = s.At(12, 18),
                    DueQuote = "I would like to order the album in two weeks.",
                    Tasks = ["Pick about twenty photos"],
                },
                Messages =
                [
                    Msg("Ivo Testcase", s.AgoDays(2, 20),
                        "Here are the photos, pick the ones for the album. I would like to order the album in two weeks."),
                ],
            },

            // Waiting for them.
            new()
            {
                Id = Samples.Id(11), Account = work, Person = "Jana Sample", Date = s.AgoDays(1, 9),
                Subject = "Quote for the new laptops", Snippet = "You: Thank you, that works.",
                MessageCount = 3, RuleState = State.Them, RuleReason = BoardReason.ThemReplied,
                Annotation = new()
                {
                    State = State.Them, Title = "Quote for the new laptops from Jana",
                    Summary = "Jana promised a quote for twelve laptops within five days.",
                    Why = "Jana said she will send the quote.", Due = s.At(3, 17),
                    DueQuote = "You will have the quote within five days.",
                },
                Messages =
                [
                    Mine(s.AgoDays(3, 10), "Hello Jana, we need twelve laptops for the new team. Could you send a quote?"),
                    Msg("Jana Sample", s.AgoDays(2, 11),
                        "Of course. You will have the quote within five days. Orders go to orders@example.invalid."),
                    Mine(s.AgoDays(1, 9), "Thank you, that works."),
                ],
            },
            new()
            {
                Id = Samples.Id(12), Account = jira, Person = "Kai Mockup", Date = s.AgoDays(1, 15),
                Subject = "DEMO-9: Translate the login page", Snippet = "Kai Mockup moved the issue to In Review.",
                MessageCount = 2, Issue = new("DEMO-9", "In Review", JiraStatusStyle.InProgress),
                RuleState = State.Them, RuleReason = BoardReason.JiraYourComment,
                Annotation = new()
                {
                    State = State.Them, Title = "Login page translation in review",
                    Summary = "The translation is done and waits for Kai's review.",
                    Why = "Nothing to do until Kai finishes the review.",
                },
                Messages =
                [
                    Mine(s.AgoDays(2, 13), "The translation is ready for review."),
                    Msg("Kai Mockup", s.AgoDays(1, 15), "Moved to In Review. I will look at it."),
                ],
            },
            new()
            {
                Id = Samples.Id(13), Account = home, Person = "Litware Shop", Date = s.AgoDays(3, 11),
                Subject = "Your refund request", Snippet = "We have received your request and will reply within 10 days.",
                RuleState = State.Them, RuleReason = BoardReason.ThemAsked,
                Messages =
                [
                    Msg("Litware Shop", s.AgoDays(3, 11), "We have received your refund request and will reply within 10 days."),
                ],
            },
            new()
            {
                Id = Samples.Id(14), Account = club, Person = "Kim Example", Date = s.AgoDays(2, 18),
                Subject = "Venue for the annual dinner", Snippet = "I am asking two restaurants and will let you know.",
                RuleState = State.Info, RuleReason = BoardReason.InfoNotAddressed,
                Annotation = new()
                {
                    State = State.Info, Title = "Annual dinner venue",
                    Summary = "Kim is asking two restaurants about the annual dinner.",
                    Why = "Nothing asks you to act.",
                },
                UserState = State.Them,
                Messages =
                [
                    Msg("Kim Example", s.AgoDays(2, 18),
                        "I am asking two restaurants about the annual dinner and will let you know."),
                ],
            },

            // For your information.
            new()
            {
                Id = Samples.Id(15), Account = work, Person = "Contoso IT", Date = s.AgoHours(4),
                Subject = "Planned maintenance on Sunday", Snippet = "Mail and the file share are down from 6 to 8 am.",
                RuleState = State.Info, RuleReason = BoardReason.InfoNotAddressed,
                Annotation = new()
                {
                    State = State.Info, Title = "Maintenance on Sunday morning",
                    Summary = "Mail and the file share are down on Sunday from 6 to 8 am.",
                    Why = "An announcement; nothing to do.",
                },
                Messages =
                [
                    Msg("Contoso IT", s.AgoHours(4), "Mail and the file share are down on Sunday from 6 to 8 am."),
                ],
            },
            new()
            {
                Id = Samples.Id(16), Account = home, Person = "Northwind Bank", Date = s.AgoDays(1, 7),
                Subject = "Your monthly statement is ready", Snippet = "Your statement for September is attached.",
                HasAttachments = true, RuleState = State.Info, RuleReason = BoardReason.InfoNotAddressed,
                Messages =
                [
                    Msg("Northwind Bank", s.AgoDays(1, 7), "Your statement for September is attached."),
                ],
            },
            new()
            {
                Id = Samples.Id(17), Account = club, Person = "Rowing Club News", Date = s.AgoDays(2, 8),
                Subject = "October newsletter", Snippet = "New training times, the boathouse party and the results.",
                RuleState = State.Info, RuleReason = BoardReason.InfoNotAddressed,
                Annotation = new()
                {
                    State = State.Info, Title = "October newsletter",
                    Summary = "New training times from next week, a boathouse party and the results of the last race.",
                    Why = "A newsletter; nothing to do.",
                },
                Messages =
                [
                    Msg("Rowing Club News", s.AgoDays(2, 8),
                        "New training times, the boathouse party and the results of the last race."),
                ],
            },
            new()
            {
                Id = Samples.Id(18), Account = jira, Person = "Lou Sample", Date = s.AgoDays(1, 11),
                Subject = "DEMO-3: Update the dependency list", Snippet = "Lou Sample closed the issue.",
                MessageCount = 2, Issue = new("DEMO-3", "Done", JiraStatusStyle.Done), RuleState = State.You,
                RuleReason = BoardReason.JiraAssigned,
                Annotation = new()
                {
                    State = State.Info, Title = "Dependency list updated",
                    Summary = "Lou updated the list and closed the issue.",
                    Why = "The issue is closed; nothing is left for you.",
                },
                Messages =
                [
                    Mine(s.AgoDays(2, 9), "Could you update the list while you are at it?"),
                    Msg("Lou Sample", s.AgoDays(1, 11), "Done, closing the issue."),
                ],
            },
            new()
            {
                Id = Samples.Id(19), Account = work, Person = "Lee Placeholder", Date = s.AgoHours(1),
                Subject = "Notes from Tuesday's meeting", Snippet = "The notes are attached.", Unread = true,
                HasAttachments = true, RuleState = State.Info, RuleReason = BoardReason.InfoCcOnly,
                Annotation = new()
                {
                    State = State.Info, Title = "Notes from Tuesday's meeting",
                    Summary = "Lee's notes from Tuesday: the launch moves by a week; nothing is assigned to you.",
                    Why = "You are in Cc and nothing is assigned to you.",
                },
                Messages =
                [
                    Msg("Lee Placeholder", s.AgoHours(1), "The notes are attached. The launch moves by a week."),
                ],
            },

            // Done.
            new()
            {
                Id = Samples.Id(20), Account = work, Person = "Nia Demo", Date = s.AgoDays(1, 10),
                Subject = "Travel booking confirmed", Snippet = "Your train tickets are attached.", HasAttachments = true,
                RuleState = State.Info, RuleReason = BoardReason.InfoNotAddressed,
                Annotation = new()
                {
                    State = State.Info, Title = "Train tickets for the trip",
                    Summary = "The train tickets are booked and attached.", Why = "A confirmation; nothing to do.",
                },
                Visibility = DoneNow(),
                Messages =
                [
                    Msg("Nia Demo", s.AgoDays(1, 10), "Your train tickets are attached."),
                ],
            },
            new()
            {
                Id = Samples.Id(21), Account = home, Person = "Oskar Sample", Date = s.AgoDays(3, 17),
                Subject = "Thanks for the book", Snippet = "I will bring it back next week.", MessageCount = 2,
                RuleState = State.Them, RuleReason = BoardReason.ThemReplied,
                Annotation = new()
                {
                    State = State.Them, Title = "Oskar returns the book",
                    Summary = "Oskar will bring the book back next week.", Why = "Oskar said he will bring it.",
                },
                Visibility = DoneNow(),
                Messages =
                [
                    Msg("Oskar Sample", s.AgoDays(4, 12), "Thanks for the book! I will bring it back next week."),
                    Mine(s.AgoDays(3, 17), "No hurry."),
                ],
            },
        };

        Commitment[] commitments =
        [
            new()
            {
                Id = new("sample-promise-1"), CaseId = Samples.Id(1), Text = "Send Ada the signed contract",
                Quote = "I will sign it and send it back this afternoon.", Due = s.At(0, 17),
            },
            new()
            {
                Id = new("sample-promise-2"), CaseId = Samples.Id(4), Text = "Send Chris the updated Q4 figures",
                Quote = "I will send the updated figures before the meeting.", Due = s.At(1, 12),
            },
            new()
            {
                Id = new("sample-promise-3"), CaseId = Samples.Id(2), Text = "Tell Ben when a fix is ready to test",
                Quote = "Looking into it.",
            },
            new()
            {
                Id = new("sample-promise-4"), CaseId = Samples.Id(21), Text = "Remind Oskar about the book",
                Quote = "No hurry.", Due = s.At(6, 10),
            },
        ];

        return new Snapshot
        {
            Accounts = accounts,
            Cases = cases,
            Commitments = commitments,
            Annotated = true,
            Run = new Run { Model = "Claude", Date = s.AgoMinutes(20), Note = "19 cases sorted, 2 done" },
        };
    }

    // The dates and ids of the samples.
    private sealed class Samples(DateTimeOffset now, TimeZoneInfo zone)
    {
        public static BoardCaseId Id(int n) => new("sample-" + n.ToString(CultureInfo.InvariantCulture));

        public DateTimeOffset AgoMinutes(int minutes) => now.AddMinutes(-minutes);

        public DateTimeOffset AgoHours(int hours) => AgoMinutes(hours * 60);

        // days days before today at hour, never later than an hour ago.
        public DateTimeOffset AgoDays(int days, int hour)
        {
            var t = At(-days, hour);
            var hourAgo = AgoMinutes(60);
            return hourAgo < t ? hourAgo : t;
        }

        // days days from today at hour o'clock.
        public DateTimeOffset At(int days, int hour) => AtWallClock(LocalDate(now, zone).AddDays(days), hour, zone);
    }
}
