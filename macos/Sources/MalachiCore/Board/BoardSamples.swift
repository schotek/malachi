// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The dummy board's cases: invented sample data, no real person, company
// or address in it (names like "Ada Example", addresses only under
// example.invalid, issue keys DEMO-n). The dates are relative to `now`, so
// the board looks the same whenever it opens: every state, every deadline
// group, cases the assistant has and has not looked at, one it moved, one
// the user moved, an issue, a suggested reply, tasks, commitments and two
// done cases.
//
// Swift-first: written here first; the GTK window ports it to
// ui/internal/board with the board itself.

import Foundation

extension Board {
    /// The invented sample board, dated relative to `now`.
    public static func sampleSnapshot(now: Date, calendar: Calendar = .current) -> Snapshot {
        // macOS-only strings: invented sample data, not for translation.
        let s = Samples(now: now, calendar: calendar)
        let work = AccountID(rawValue: "sample-work")
        let home = AccountID(rawValue: "sample-home")
        let club = AccountID(rawValue: "sample-club")
        let jira = AccountID(rawValue: "sample-jira")
        let accounts = [
            AccountInfo(id: work, name: "Contoso Ltd", badge: microsoftBadge),
            AccountInfo(id: home, name: "Home", badge: googleBadge),
            AccountInfo(id: club, name: "Rowing Club", badge: imapBadge),
            AccountInfo(id: jira, name: "Demo Jira", badge: Jira.kindBadge),
        ]

        var cases: [Case] = []

        // Hot.
        cases.append(
            Case(
                id: s.id(1), account: work, person: "Ada Example", date: s.ago(minutes: 35),
                subject: "Re: Contract renewal for 2027",
                snippet: "Attached is the final version. We need the signed copy back today.",
                unread: true, hasAttachments: true, messageCount: 3, ruleState: .you,
                ruleReason: .youRepliedToYou,
                annotation: Annotation(
                    state: .hot, title: "Sign the contract renewal",
                    summary: "Ada sent the final contract for 2027. Legal has approved it; only your signature is missing.\n\nShe needs the signed copy back by 5 pm today, or the old terms run on for another year.",
                    why: "The deadline is today, and missing it renews the old terms.", due: s.at(0, 17),
                    dueQuote: "We need the signed copy back by 5 pm today.",
                    tasks: ["Sign page 4 and initial the appendix", "Confirm the start date of 1 January"]),
                draft: DraftLink(id: "sample-draft-1", text: "Hi Ada,\n\nthanks for the final version. I will sign it and send it back this afternoon.\n\nBest regards"),
                messages: [
                    CaseMessage(
                        from: "Ada Example", date: s.ago(days: 3, hour: 10),
                        text: "Hello,\n\nhere is the draft of the renewal for 2027. Could you look at the start date?\n\n"
                            + "A few things changed since last year. The notice period is now three months instead of two, "
                            + "and the price follows the yearly index from the second year on. The support hours stay as they "
                            + "are, but the response time for urgent requests goes down from eight hours to four.\n\n"
                            + "The appendix with the service levels is new. It lists what counts as an outage, how it is "
                            + "measured and what credit you get when a month falls short. Legal asked us to keep it separate "
                            + "so that it can be updated without a new contract.\n\n"
                            + "If anything in the draft does not work for you, mark it in the document and I will take it to "
                            + "our side this week.\n\nBest regards,\nAda"),
                    CaseMessage(
                        from: "You", date: s.ago(days: 2, hour: 15),
                        text: "Thanks, Ada. The start date of 1 January works for us.", mine: true),
                    CaseMessage(
                        from: "Ada Example", date: s.ago(minutes: 35),
                        text: "Attached is the final version. Legal has approved it.\n\nWe need the signed copy back by 5 pm today.\n\n"
                            + "The start date is 1 January, as you confirmed. Page 4 needs your signature and the appendix your "
                            + "initials on each page. A scan is enough for now; the original can follow by post.\n\n"
                            + "If the copy is not back today, the old terms run on for another year and the new service levels "
                            + "would only start in 2028. I am at my desk until six if anything is unclear.\n\n"
                            + "Thank you, and best regards,\nAda"),
                ]))
        cases.append(
            Case(
                id: s.id(2), account: jira, person: "Ben Placeholder", date: s.ago(hours: 2),
                subject: "DEMO-14: Checkout fails for some card payments",
                snippet: "Ben Placeholder raised the priority to Highest.", unread: true, messageCount: 4,
                issue: IssueInfo(key: "DEMO-14", status: "In Progress", style: .inProgress), ruleState: .hot,
                ruleReason: .jiraAssigned,
                annotation: Annotation(
                    state: .hot, title: "Fix the failing card payments",
                    summary: "Some card payments fail at the last step of the checkout since the last release. Ben asks for a fix before the sale starts.",
                    why: "The issue blocks payments and was due two days ago.", due: s.at(-2, 12),
                    dueQuote: "This has to be fixed within two days.",
                    tasks: ["Find which card types fail", "Tell Ben when a fix is ready to test"]),
                messages: [
                    CaseMessage(
                        from: "Ben Placeholder", date: s.ago(days: 4, hour: 9),
                        text: "Some card payments fail at the last step. This has to be fixed within two days."),
                    CaseMessage(
                        from: "You", date: s.ago(days: 3, hour: 11),
                        text: "I can reproduce it with a test card. Looking into it.", mine: true),
                    CaseMessage(
                        from: "Ben Placeholder", date: s.ago(days: 1, hour: 16),
                        text: "Any news? The sale starts in a few days."),
                    CaseMessage(
                        from: "Ben Placeholder", date: s.ago(hours: 2),
                        text: "Raised the priority to Highest."),
                ]))
        cases.append(
            Case(
                id: s.id(3), account: home, person: "Northwind Insurance", date: s.ago(hours: 5),
                subject: "Your car insurance ends soon", snippet: "Renew now to stay covered without a break.",
                unread: true, ruleState: .hot, ruleReason: .hotImportant,
                messages: [
                    CaseMessage(
                        from: "Northwind Insurance", date: s.ago(hours: 5),
                        text: "Your car insurance ends soon. Renew now to stay covered without a break.")
                ]))

        // Waiting for you.
        cases.append(
            Case(
                id: s.id(4), account: work, person: "Chris Sample", date: s.ago(hours: 3),
                subject: "Budget figures for Q4", snippet: "Could you send me the updated figures?",
                messageCount: 2, ruleState: .you, ruleReason: .youRepliedToYou,
                annotation: Annotation(
                    state: .you, title: "Send Chris the Q4 budget figures",
                    summary: "Chris needs the updated Q4 figures for the board meeting tomorrow.",
                    why: "Chris asked you directly and the meeting is tomorrow.", due: s.at(1, 12),
                    dueQuote: "The board meets tomorrow at noon.",
                    tasks: ["Update the travel line", "Send the sheet to Chris"]),
                messages: [
                    CaseMessage(
                        from: "You", date: s.ago(days: 1, hour: 14),
                        text: "I will send the updated figures before the meeting.", mine: true),
                    CaseMessage(
                        from: "Chris Sample", date: s.ago(hours: 3),
                        text: "Could you send me the updated figures? The board meets tomorrow at noon."),
                ]))
        cases.append(
            Case(
                id: s.id(5), account: work, person: "Dana Mock", date: s.ago(hours: 6),
                subject: "Can you review the onboarding guide?",
                snippet: "The guide is attached; comments in the document are fine.", hasAttachments: true,
                ruleState: .you, ruleReason: .youAddressed,
                annotation: Annotation(
                    state: .you, title: "Review the onboarding guide",
                    summary: "Dana wants your comments on the new onboarding guide before it goes to the new starters.",
                    why: "Dana asked you for a review within four days.", due: s.at(4, 9),
                    dueQuote: "Comments within four days would be great."),
                draft: DraftLink(id: "sample-draft-2", text: "Hi Dana,\n\nI will read the guide and leave my comments in the document within four days."),
                messages: [
                    CaseMessage(
                        from: "Dana Mock", date: s.ago(hours: 6),
                        text: "Hi,\n\nthe onboarding guide is attached. Comments within four days would be great.")
                ]))
        cases.append(
            Case(
                id: s.id(6), account: club, person: "Erin Fictional", date: s.ago(hours: 8),
                subject: "Saturday's regatta", snippet: "Who brings the boat trailer?", messageCount: 2,
                ruleState: .info, ruleReason: .infoNotAddressed,
                annotation: Annotation(
                    state: .you, title: "Say whether you can bring the trailer",
                    summary: "Erin asks the crew who can bring the boat trailer to Saturday's regatta.",
                    why: "Erin asked you by name in the second message.",
                    tasks: ["Check whether the car is free on Saturday", "Answer Erin"]),
                messages: [
                    CaseMessage(
                        from: "Erin Fictional", date: s.ago(days: 1, hour: 19),
                        text: "Hello crew, the regatta starts at 9 on Saturday."),
                    CaseMessage(
                        from: "Erin Fictional", date: s.ago(hours: 8),
                        text: "Who brings the boat trailer? You had it last time, could you again?"),
                ]))
        cases.append(
            Case(
                id: s.id(7), account: home, person: "Fabrikam Dental", date: s.ago(hours: 10),
                subject: "Please confirm your appointment", snippet: "Reply YES to confirm your appointment.",
                unread: true, ruleState: .you, ruleReason: .youAddressed,
                messages: [
                    CaseMessage(
                        from: "Fabrikam Dental", date: s.ago(hours: 10),
                        text: "Reply YES to confirm your appointment, or call us to move it.")
                ]))
        cases.append(
            Case(
                id: s.id(8), account: jira, person: "Gus Invented", date: s.ago(hours: 20),
                subject: "DEMO-21: Add export to CSV", snippet: "Gus Invented asked: which columns should the export have?",
                messageCount: 2, issue: IssueInfo(key: "DEMO-21", status: "To Do", style: .todo), ruleState: .you,
                ruleReason: .jiraReporter,
                annotation: Annotation(
                    state: .you, title: "Tell Gus which columns the export needs",
                    summary: "Gus is ready to start on the CSV export and needs the list of columns from you.",
                    why: "Gus mentioned you and is waiting before he starts."),
                messages: [
                    CaseMessage(
                        from: "Gus Invented", date: s.ago(days: 2, hour: 10),
                        text: "Created the issue: the report should be exportable as CSV."),
                    CaseMessage(
                        from: "Gus Invented", date: s.ago(hours: 20),
                        text: "Which columns should the export have? I will start once I know."),
                ]))
        cases.append(
            Case(
                id: s.id(9), account: work, person: "Hana Demo", date: s.ago(days: 1, hour: 12),
                subject: "Lunch next week?", snippet: "Tuesday or Wednesday?", ruleState: .you,
                ruleReason: .youAddressed,
                annotation: Annotation(
                    state: .you, title: "Pick a day for lunch with Hana",
                    summary: "Hana suggests lunch next Tuesday or Wednesday.", why: "Hana waits for your choice."),
                messages: [
                    CaseMessage(
                        from: "Hana Demo", date: s.ago(days: 1, hour: 12),
                        text: "Shall we have lunch next week? Tuesday or Wednesday?")
                ]))
        cases.append(
            Case(
                id: s.id(10), account: home, person: "Ivo Testcase", date: s.ago(days: 2, hour: 20),
                subject: "Photos from the trip", snippet: "Here are the photos, pick the ones for the album.",
                hasAttachments: true, ruleState: .you, ruleReason: .youAddressed,
                annotation: Annotation(
                    state: .you, title: "Pick photos for the album",
                    summary: "Ivo sent the photos from the trip and asks which ones go into the printed album.",
                    why: "Ivo orders the album once you have picked.", due: s.at(12, 18),
                    dueQuote: "I would like to order the album in two weeks.",
                    tasks: ["Pick about twenty photos"]),
                messages: [
                    CaseMessage(
                        from: "Ivo Testcase", date: s.ago(days: 2, hour: 20),
                        text: "Here are the photos, pick the ones for the album. I would like to order the album in two weeks.")
                ]))

        // Waiting for them.
        cases.append(
            Case(
                id: s.id(11), account: work, person: "Jana Sample", date: s.ago(days: 1, hour: 9),
                subject: "Quote for the new laptops", snippet: "You: Thank you, that works.",
                messageCount: 3, ruleState: .them, ruleReason: .themReplied,
                annotation: Annotation(
                    state: .them, title: "Quote for the new laptops from Jana",
                    summary: "Jana promised a quote for twelve laptops within five days.",
                    why: "Jana said she will send the quote.", due: s.at(3, 17),
                    dueQuote: "You will have the quote within five days."),
                messages: [
                    CaseMessage(
                        from: "You", date: s.ago(days: 3, hour: 10),
                        text: "Hello Jana, we need twelve laptops for the new team. Could you send a quote?", mine: true),
                    CaseMessage(
                        from: "Jana Sample", date: s.ago(days: 2, hour: 11),
                        text: "Of course. You will have the quote within five days. Orders go to orders@example.invalid."),
                    CaseMessage(
                        from: "You", date: s.ago(days: 1, hour: 9), text: "Thank you, that works.", mine: true),
                ]))
        cases.append(
            Case(
                id: s.id(12), account: jira, person: "Kai Mockup", date: s.ago(days: 1, hour: 15),
                subject: "DEMO-9: Translate the login page", snippet: "Kai Mockup moved the issue to In Review.",
                messageCount: 2, issue: IssueInfo(key: "DEMO-9", status: "In Review", style: .inProgress),
                ruleState: .them, ruleReason: .jiraYourComment,
                annotation: Annotation(
                    state: .them, title: "Login page translation in review",
                    summary: "The translation is done and waits for Kai's review.",
                    why: "Nothing to do until Kai finishes the review."),
                messages: [
                    CaseMessage(
                        from: "You", date: s.ago(days: 2, hour: 13), text: "The translation is ready for review.",
                        mine: true),
                    CaseMessage(
                        from: "Kai Mockup", date: s.ago(days: 1, hour: 15), text: "Moved to In Review. I will look at it."),
                ]))
        cases.append(
            Case(
                id: s.id(13), account: home, person: "Litware Shop", date: s.ago(days: 3, hour: 11),
                subject: "Your refund request", snippet: "We have received your request and will reply within 10 days.",
                ruleState: .them, ruleReason: .themAsked,
                messages: [
                    CaseMessage(
                        from: "Litware Shop", date: s.ago(days: 3, hour: 11),
                        text: "We have received your refund request and will reply within 10 days.")
                ]))
        cases.append(
            Case(
                id: s.id(14), account: club, person: "Kim Example", date: s.ago(days: 2, hour: 18),
                subject: "Venue for the annual dinner", snippet: "I am asking two restaurants and will let you know.",
                ruleState: .info, ruleReason: .infoNotAddressed,
                annotation: Annotation(
                    state: .info, title: "Annual dinner venue",
                    summary: "Kim is asking two restaurants about the annual dinner.",
                    why: "Nothing asks you to act."),
                userState: .them,
                messages: [
                    CaseMessage(
                        from: "Kim Example", date: s.ago(days: 2, hour: 18),
                        text: "I am asking two restaurants about the annual dinner and will let you know.")
                ]))

        // For your information.
        cases.append(
            Case(
                id: s.id(15), account: work, person: "Contoso IT", date: s.ago(hours: 4),
                subject: "Planned maintenance on Sunday", snippet: "Mail and the file share are down from 6 to 8 am.",
                ruleState: .info, ruleReason: .infoNotAddressed,
                annotation: Annotation(
                    state: .info, title: "Maintenance on Sunday morning",
                    summary: "Mail and the file share are down on Sunday from 6 to 8 am.",
                    why: "An announcement; nothing to do."),
                messages: [
                    CaseMessage(
                        from: "Contoso IT", date: s.ago(hours: 4),
                        text: "Mail and the file share are down on Sunday from 6 to 8 am.")
                ]))
        cases.append(
            Case(
                id: s.id(16), account: home, person: "Northwind Bank", date: s.ago(days: 1, hour: 7),
                subject: "Your monthly statement is ready", snippet: "Your statement for September is attached.",
                hasAttachments: true, ruleState: .info, ruleReason: .infoNotAddressed,
                messages: [
                    CaseMessage(
                        from: "Northwind Bank", date: s.ago(days: 1, hour: 7),
                        text: "Your statement for September is attached.")
                ]))
        cases.append(
            Case(
                id: s.id(17), account: club, person: "Rowing Club News", date: s.ago(days: 2, hour: 8),
                subject: "October newsletter", snippet: "New training times, the boathouse party and the results.",
                ruleState: .info, ruleReason: .infoNotAddressed,
                annotation: Annotation(
                    state: .info, title: "October newsletter",
                    summary: "New training times from next week, a boathouse party and the results of the last race.",
                    why: "A newsletter; nothing to do."),
                messages: [
                    CaseMessage(
                        from: "Rowing Club News", date: s.ago(days: 2, hour: 8),
                        text: "New training times, the boathouse party and the results of the last race.")
                ]))
        cases.append(
            Case(
                id: s.id(18), account: jira, person: "Lou Sample", date: s.ago(days: 1, hour: 11),
                subject: "DEMO-3: Update the dependency list", snippet: "Lou Sample closed the issue.",
                messageCount: 2, issue: IssueInfo(key: "DEMO-3", status: "Done", style: .done), ruleState: .you,
                ruleReason: .jiraAssigned,
                annotation: Annotation(
                    state: .info, title: "Dependency list updated",
                    summary: "Lou updated the list and closed the issue.",
                    why: "The issue is closed; nothing is left for you."),
                messages: [
                    CaseMessage(
                        from: "You", date: s.ago(days: 2, hour: 9), text: "Could you update the list while you are at it?",
                        mine: true),
                    CaseMessage(from: "Lou Sample", date: s.ago(days: 1, hour: 11), text: "Done, closing the issue."),
                ]))
        cases.append(
            Case(
                id: s.id(19), account: work, person: "Lee Placeholder", date: s.ago(hours: 1),
                subject: "Notes from Tuesday's meeting", snippet: "The notes are attached.", unread: true,
                hasAttachments: true, ruleState: .info, ruleReason: .infoCcOnly,
                annotation: Annotation(
                    state: .info, title: "Notes from Tuesday's meeting",
                    summary: "Lee's notes from Tuesday: the launch moves by a week; nothing is assigned to you.",
                    why: "You are in Cc and nothing is assigned to you."),
                messages: [
                    CaseMessage(
                        from: "Lee Placeholder", date: s.ago(hours: 1),
                        text: "The notes are attached. The launch moves by a week.")
                ]))

        // Done.
        cases.append(
            Case(
                id: s.id(20), account: work, person: "Nia Demo", date: s.ago(days: 1, hour: 10),
                subject: "Travel booking confirmed", snippet: "Your train tickets are attached.", hasAttachments: true,
                ruleState: .info, ruleReason: .infoNotAddressed,
                annotation: Annotation(
                    state: .info, title: "Train tickets for the trip",
                    summary: "The train tickets are booked and attached.", why: "A confirmation; nothing to do."),
                visibility: .done(at: nil),
                messages: [
                    CaseMessage(
                        from: "Nia Demo", date: s.ago(days: 1, hour: 10), text: "Your train tickets are attached.")
                ]))
        cases.append(
            Case(
                id: s.id(21), account: home, person: "Oskar Sample", date: s.ago(days: 3, hour: 17),
                subject: "Thanks for the book", snippet: "I will bring it back next week.", messageCount: 2,
                ruleState: .them, ruleReason: .themReplied,
                annotation: Annotation(
                    state: .them, title: "Oskar returns the book",
                    summary: "Oskar will bring the book back next week.", why: "Oskar said he will bring it."),
                visibility: .done(at: nil),
                messages: [
                    CaseMessage(
                        from: "Oskar Sample", date: s.ago(days: 4, hour: 12), text: "Thanks for the book! I will bring it back next week."),
                    CaseMessage(from: "You", date: s.ago(days: 3, hour: 17), text: "No hurry.", mine: true),
                ]))

        let commitments = [
            Commitment(
                id: "sample-promise-1", caseID: s.id(1), text: "Send Ada the signed contract",
                quote: "I will sign it and send it back this afternoon.", due: s.at(0, 17)),
            Commitment(
                id: "sample-promise-2", caseID: s.id(4), text: "Send Chris the updated Q4 figures",
                quote: "I will send the updated figures before the meeting.", due: s.at(1, 12)),
            Commitment(
                id: "sample-promise-3", caseID: s.id(2), text: "Tell Ben when a fix is ready to test",
                quote: "Looking into it."),
            Commitment(
                id: "sample-promise-4", caseID: s.id(21), text: "Remind Oskar about the book",
                quote: "No hurry.", due: s.at(6, 10)),
        ]

        return Snapshot(
            accounts: accounts, cases: cases, commitments: commitments, annotated: true,
            run: Run(model: "Claude", date: s.ago(minutes: 20), note: "19 cases sorted, 2 done"))
    }

    /// The dates and ids of the samples.
    private struct Samples {
        let now: Date
        let calendar: Calendar

        func id(_ n: Int) -> CaseID {
            CaseID(rawValue: "sample-\(n)")
        }

        func ago(minutes: Int) -> Date {
            now.addingTimeInterval(-Double(minutes) * 60)
        }

        func ago(hours: Int) -> Date {
            ago(minutes: hours * 60)
        }

        /// `days` days before today at `hour`, never later than an hour ago.
        func ago(days: Int, hour: Int) -> Date {
            min(at(-days, hour), ago(minutes: 60))
        }

        /// `days` days from today at `hour` o'clock.
        func at(_ days: Int, _ hour: Int) -> Date {
            let day = calendar.date(byAdding: .day, value: days, to: calendar.startOfDay(for: now)) ?? now
            return calendar.date(bySettingHour: hour, minute: 0, second: 0, of: day) ?? day
        }
    }
}
