// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"fmt"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/jira"
)

// The dummy board's cases: invented sample data, no real person, company
// or address in it (names like "Ada Example", addresses only under
// example.invalid, issue keys DEMO-n). The dates are relative to now, so
// the board looks the same whenever it opens: every state, every deadline
// group, cases the assistant has and has not looked at, one it moved, one
// the user moved, an issue, a suggested reply, tasks, commitments and two
// done cases. English and not for translation: they are a development aid
// (MALACHI_BOARD_SAMPLES=1) and test data.
//
// The macOS client leads (MalachiCore/Board/BoardSamples.swift); this is
// its port, string for string.

// SampleSnapshot is the invented sample board, dated relative to now in
// loc (nil is time.Local).
func SampleSnapshot(now time.Time, loc *time.Location) Snapshot {
	if loc == nil {
		loc = time.Local
	}
	s := samples{now: now, loc: loc}
	work := api.AccountID("sample-work")
	home := api.AccountID("sample-home")
	club := api.AccountID("sample-club")
	jiraAccount := api.AccountID("sample-jira")
	accounts := []AccountInfo{
		{ID: work, Name: "Contoso Ltd", Badge: microsoftBadge, CanReply: true},
		{ID: home, Name: "Home", Badge: googleBadge, CanReply: true},
		{ID: club, Name: "Rowing Club", Badge: imapBadge, CanReply: true},
		{ID: jiraAccount, Name: "Demo Jira", Badge: jira.KindBadge, CanReply: true},
	}
	msg := func(from string, date time.Time, text string) CaseMessage {
		return CaseMessage{From: from, Date: date, Text: text}
	}
	mine := func(date time.Time, text string) CaseMessage {
		return CaseMessage{From: "You", Date: date, Text: text, Mine: true}
	}
	due := func(t time.Time) *time.Time { return &t }

	var cases []Case

	// Hot.
	cases = append(cases, Case{
		ID: s.id(1), Account: work, Person: "Ada Example", Date: s.agoMinutes(35),
		Subject: "Re: Contract renewal for 2027",
		Snippet: "Attached is the final version. We need the signed copy back today.",
		Unread:  true, HasAttachments: true, MessageCount: 3, RuleState: StateYou,
		RuleReason: api.BoardReasonYouRepliedToYou,
		Annotation: &Annotation{
			State: statePtr(StateHot), Title: "Sign the contract renewal",
			Summary: "Ada sent the final contract for 2027. Legal has approved it; only your signature is missing.\n\nShe needs the signed copy back by 5 pm today, or the old terms run on for another year.",
			Why:     "The deadline is today, and missing it renews the old terms.", Due: due(s.at(0, 17)),
			DueQuote: "We need the signed copy back by 5 pm today.",
			Tasks:    []string{"Sign page 4 and initial the appendix", "Confirm the start date of 1 January"},
		},
		Draft: &DraftLink{ID: "sample-draft-1", Text: "Hi Ada,\n\nthanks for the final version. I will sign it and send it back this afternoon.\n\nBest regards"},
		Messages: []CaseMessage{
			msg("Ada Example", s.agoDays(3, 10),
				"Hello,\n\nhere is the draft of the renewal for 2027. Could you look at the start date?\n\n"+
					"A few things changed since last year. The notice period is now three months instead of two, "+
					"and the price follows the yearly index from the second year on. The support hours stay as they "+
					"are, but the response time for urgent requests goes down from eight hours to four.\n\n"+
					"The appendix with the service levels is new. It lists what counts as an outage, how it is "+
					"measured and what credit you get when a month falls short. Legal asked us to keep it separate "+
					"so that it can be updated without a new contract.\n\n"+
					"If anything in the draft does not work for you, mark it in the document and I will take it to "+
					"our side this week.\n\nBest regards,\nAda"),
			mine(s.agoDays(2, 15), "Thanks, Ada. The start date of 1 January works for us."),
			msg("Ada Example", s.agoMinutes(35),
				"Attached is the final version. Legal has approved it.\n\nWe need the signed copy back by 5 pm today.\n\n"+
					"The start date is 1 January, as you confirmed. Page 4 needs your signature and the appendix your "+
					"initials on each page. A scan is enough for now; the original can follow by post.\n\n"+
					"If the copy is not back today, the old terms run on for another year and the new service levels "+
					"would only start in 2028. I am at my desk until six if anything is unclear.\n\n"+
					"Thank you, and best regards,\nAda"),
		},
		MessagesLoaded: true,
	})
	cases = append(cases, Case{
		ID: s.id(2), Account: jiraAccount, Person: "Ben Placeholder", Date: s.agoHours(2),
		Subject: "DEMO-14: Checkout fails for some card payments",
		Snippet: "Ben Placeholder raised the priority to Highest.", Unread: true, MessageCount: 4,
		Issue: &IssueInfo{Key: "DEMO-14", Status: "In Progress", Style: jira.StatusInProgress}, RuleState: StateHot,
		RuleReason: api.BoardReasonJiraAssigned,
		Annotation: &Annotation{
			State: statePtr(StateHot), Title: "Fix the failing card payments",
			Summary: "Some card payments fail at the last step of the checkout since the last release. Ben asks for a fix before the sale starts.",
			Why:     "The issue blocks payments and was due two days ago.", Due: due(s.at(-2, 12)),
			DueQuote: "This has to be fixed within two days.",
			Tasks:    []string{"Find which card types fail", "Tell Ben when a fix is ready to test"},
		},
		Messages: []CaseMessage{
			msg("Ben Placeholder", s.agoDays(4, 9),
				"Some card payments fail at the last step. This has to be fixed within two days."),
			mine(s.agoDays(3, 11), "I can reproduce it with a test card. Looking into it."),
			msg("Ben Placeholder", s.agoDays(1, 16), "Any news? The sale starts in a few days."),
			msg("Ben Placeholder", s.agoHours(2), "Raised the priority to Highest."),
		},
		MessagesLoaded: true,
	})
	cases = append(cases, Case{
		ID: s.id(3), Account: home, Person: "Northwind Insurance", Date: s.agoHours(5),
		Subject: "Your car insurance ends soon", Snippet: "Renew now to stay covered without a break.",
		Unread: true, MessageCount: 1, RuleState: StateHot, RuleReason: api.BoardReasonHotImportant,
		Messages: []CaseMessage{
			msg("Northwind Insurance", s.agoHours(5),
				"Your car insurance ends soon. Renew now to stay covered without a break."),
		},
		MessagesLoaded: true,
	})

	// Waiting for you.
	cases = append(cases, Case{
		ID: s.id(4), Account: work, Person: "Chris Sample", Date: s.agoHours(3),
		Subject: "Budget figures for Q4", Snippet: "Could you send me the updated figures?",
		MessageCount: 2, RuleState: StateYou, RuleReason: api.BoardReasonYouRepliedToYou,
		Annotation: &Annotation{
			State: statePtr(StateYou), Title: "Send Chris the Q4 budget figures",
			Summary: "Chris needs the updated Q4 figures for the board meeting tomorrow.",
			Why:     "Chris asked you directly and the meeting is tomorrow.", Due: due(s.at(1, 12)),
			DueQuote: "The board meets tomorrow at noon.",
			Tasks:    []string{"Update the travel line", "Send the sheet to Chris"},
		},
		Messages: []CaseMessage{
			mine(s.agoDays(1, 14), "I will send the updated figures before the meeting."),
			msg("Chris Sample", s.agoHours(3),
				"Could you send me the updated figures? The board meets tomorrow at noon."),
		},
		MessagesLoaded: true,
	})
	cases = append(cases, Case{
		ID: s.id(5), Account: work, Person: "Dana Mock", Date: s.agoHours(6),
		Subject: "Can you review the onboarding guide?",
		Snippet: "The guide is attached; comments in the document are fine.", HasAttachments: true,
		MessageCount: 1, RuleState: StateYou, RuleReason: api.BoardReasonYouAddressed,
		Annotation: &Annotation{
			State: statePtr(StateYou), Title: "Review the onboarding guide",
			Summary: "Dana wants your comments on the new onboarding guide before it goes to the new starters.",
			Why:     "Dana asked you for a review within four days.", Due: due(s.at(4, 9)),
			DueQuote: "Comments within four days would be great.",
		},
		Draft: &DraftLink{ID: "sample-draft-2", Text: "Hi Dana,\n\nI will read the guide and leave my comments in the document within four days."},
		Messages: []CaseMessage{
			msg("Dana Mock", s.agoHours(6),
				"Hi,\n\nthe onboarding guide is attached. Comments within four days would be great."),
		},
		MessagesLoaded: true,
	})
	cases = append(cases, Case{
		ID: s.id(6), Account: club, Person: "Erin Fictional", Date: s.agoHours(8),
		Subject: "Saturday's regatta", Snippet: "Who brings the boat trailer?", MessageCount: 2,
		RuleState: StateInfo, RuleReason: api.BoardReasonInfoNotAddressed,
		Annotation: &Annotation{
			State: statePtr(StateYou), Title: "Say whether you can bring the trailer",
			Summary: "Erin asks the crew who can bring the boat trailer to Saturday's regatta.",
			Why:     "Erin asked you by name in the second message.",
			Tasks:   []string{"Check whether the car is free on Saturday", "Answer Erin"},
		},
		Messages: []CaseMessage{
			msg("Erin Fictional", s.agoDays(1, 19), "Hello crew, the regatta starts at 9 on Saturday."),
			msg("Erin Fictional", s.agoHours(8),
				"Who brings the boat trailer? You had it last time, could you again?"),
		},
		MessagesLoaded: true,
	})
	cases = append(cases, Case{
		ID: s.id(7), Account: home, Person: "Fabrikam Dental", Date: s.agoHours(10),
		Subject: "Please confirm your appointment", Snippet: "Reply YES to confirm your appointment.",
		Unread: true, MessageCount: 1, RuleState: StateYou, RuleReason: api.BoardReasonYouAddressed,
		Messages: []CaseMessage{
			msg("Fabrikam Dental", s.agoHours(10),
				"Reply YES to confirm your appointment, or call us to move it."),
		},
		MessagesLoaded: true,
	})
	cases = append(cases, Case{
		ID: s.id(8), Account: jiraAccount, Person: "Gus Invented", Date: s.agoHours(20),
		Subject: "DEMO-21: Add export to CSV", Snippet: "Gus Invented asked: which columns should the export have?",
		MessageCount: 2, Issue: &IssueInfo{Key: "DEMO-21", Status: "To Do", Style: jira.StatusTodo},
		RuleState: StateYou, RuleReason: api.BoardReasonJiraReporter,
		Annotation: &Annotation{
			State: statePtr(StateYou), Title: "Tell Gus which columns the export needs",
			Summary: "Gus is ready to start on the CSV export and needs the list of columns from you.",
			Why:     "Gus mentioned you and is waiting before he starts.",
		},
		Messages: []CaseMessage{
			msg("Gus Invented", s.agoDays(2, 10), "Created the issue: the report should be exportable as CSV."),
			msg("Gus Invented", s.agoHours(20),
				"Which columns should the export have? I will start once I know."),
		},
		MessagesLoaded: true,
	})
	cases = append(cases, Case{
		ID: s.id(9), Account: work, Person: "Hana Demo", Date: s.agoDays(1, 12),
		Subject: "Lunch next week?", Snippet: "Tuesday or Wednesday?", MessageCount: 1, RuleState: StateYou,
		RuleReason: api.BoardReasonYouAddressed,
		Annotation: &Annotation{
			State: statePtr(StateYou), Title: "Pick a day for lunch with Hana",
			Summary: "Hana suggests lunch next Tuesday or Wednesday.", Why: "Hana waits for your choice.",
		},
		Messages: []CaseMessage{
			msg("Hana Demo", s.agoDays(1, 12), "Shall we have lunch next week? Tuesday or Wednesday?"),
		},
		MessagesLoaded: true,
	})
	cases = append(cases, Case{
		ID: s.id(10), Account: home, Person: "Ivo Testcase", Date: s.agoDays(2, 20),
		Subject: "Photos from the trip", Snippet: "Here are the photos, pick the ones for the album.",
		HasAttachments: true, MessageCount: 1, RuleState: StateYou, RuleReason: api.BoardReasonYouAddressed,
		Annotation: &Annotation{
			State: statePtr(StateYou), Title: "Pick photos for the album",
			Summary: "Ivo sent the photos from the trip and asks which ones go into the printed album.",
			Why:     "Ivo orders the album once you have picked.", Due: due(s.at(12, 18)),
			DueQuote: "I would like to order the album in two weeks.",
			Tasks:    []string{"Pick about twenty photos"},
		},
		Messages: []CaseMessage{
			msg("Ivo Testcase", s.agoDays(2, 20),
				"Here are the photos, pick the ones for the album. I would like to order the album in two weeks."),
		},
		MessagesLoaded: true,
	})

	// Waiting for them.
	cases = append(cases, Case{
		ID: s.id(11), Account: work, Person: "Jana Sample", Date: s.agoDays(1, 9),
		Subject: "Quote for the new laptops", Snippet: "You: Thank you, that works.",
		MessageCount: 3, RuleState: StateThem, RuleReason: api.BoardReasonThemReplied,
		Annotation: &Annotation{
			State: statePtr(StateThem), Title: "Quote for the new laptops from Jana",
			Summary: "Jana promised a quote for twelve laptops within five days.",
			Why:     "Jana said she will send the quote.", Due: due(s.at(3, 17)),
			DueQuote: "You will have the quote within five days.",
		},
		Messages: []CaseMessage{
			mine(s.agoDays(3, 10), "Hello Jana, we need twelve laptops for the new team. Could you send a quote?"),
			msg("Jana Sample", s.agoDays(2, 11),
				"Of course. You will have the quote within five days. Orders go to orders@example.invalid."),
			mine(s.agoDays(1, 9), "Thank you, that works."),
		},
		MessagesLoaded: true,
	})
	cases = append(cases, Case{
		ID: s.id(12), Account: jiraAccount, Person: "Kai Mockup", Date: s.agoDays(1, 15),
		Subject: "DEMO-9: Translate the login page", Snippet: "Kai Mockup moved the issue to In Review.",
		MessageCount: 2, Issue: &IssueInfo{Key: "DEMO-9", Status: "In Review", Style: jira.StatusInProgress},
		RuleState: StateThem, RuleReason: api.BoardReasonJiraYourComment,
		Annotation: &Annotation{
			State: statePtr(StateThem), Title: "Login page translation in review",
			Summary: "The translation is done and waits for Kai's review.",
			Why:     "Nothing to do until Kai finishes the review.",
		},
		Messages: []CaseMessage{
			mine(s.agoDays(2, 13), "The translation is ready for review."),
			msg("Kai Mockup", s.agoDays(1, 15), "Moved to In Review. I will look at it."),
		},
		MessagesLoaded: true,
	})
	cases = append(cases, Case{
		ID: s.id(13), Account: home, Person: "Litware Shop", Date: s.agoDays(3, 11),
		Subject: "Your refund request", Snippet: "We have received your request and will reply within 10 days.",
		MessageCount: 1, RuleState: StateThem, RuleReason: api.BoardReasonThemAsked,
		Messages: []CaseMessage{
			msg("Litware Shop", s.agoDays(3, 11),
				"We have received your refund request and will reply within 10 days."),
		},
		MessagesLoaded: true,
	})
	cases = append(cases, Case{
		ID: s.id(14), Account: club, Person: "Kim Example", Date: s.agoDays(2, 18),
		Subject: "Venue for the annual dinner", Snippet: "I am asking two restaurants and will let you know.",
		MessageCount: 1, RuleState: StateInfo, RuleReason: api.BoardReasonInfoNotAddressed,
		Annotation: &Annotation{
			State: statePtr(StateInfo), Title: "Annual dinner venue",
			Summary: "Kim is asking two restaurants about the annual dinner.",
			Why:     "Nothing asks you to act.",
		},
		UserState: statePtr(StateThem),
		Messages: []CaseMessage{
			msg("Kim Example", s.agoDays(2, 18),
				"I am asking two restaurants about the annual dinner and will let you know."),
		},
		MessagesLoaded: true,
	})

	// For your information.
	cases = append(cases, Case{
		ID: s.id(15), Account: work, Person: "Contoso IT", Date: s.agoHours(4),
		Subject: "Planned maintenance on Sunday", Snippet: "Mail and the file share are down from 6 to 8 am.",
		MessageCount: 1, RuleState: StateInfo, RuleReason: api.BoardReasonInfoNotAddressed,
		Annotation: &Annotation{
			State: statePtr(StateInfo), Title: "Maintenance on Sunday morning",
			Summary: "Mail and the file share are down on Sunday from 6 to 8 am.",
			Why:     "An announcement; nothing to do.",
		},
		Messages: []CaseMessage{
			msg("Contoso IT", s.agoHours(4), "Mail and the file share are down on Sunday from 6 to 8 am."),
		},
		MessagesLoaded: true,
	})
	cases = append(cases, Case{
		ID: s.id(16), Account: home, Person: "Northwind Bank", Date: s.agoDays(1, 7),
		Subject: "Your monthly statement is ready", Snippet: "Your statement for September is attached.",
		HasAttachments: true, MessageCount: 1, RuleState: StateInfo, RuleReason: api.BoardReasonInfoNotAddressed,
		Messages: []CaseMessage{
			msg("Northwind Bank", s.agoDays(1, 7), "Your statement for September is attached."),
		},
		MessagesLoaded: true,
	})
	cases = append(cases, Case{
		ID: s.id(17), Account: club, Person: "Rowing Club News", Date: s.agoDays(2, 8),
		Subject: "October newsletter", Snippet: "New training times, the boathouse party and the results.",
		MessageCount: 1, RuleState: StateInfo, RuleReason: api.BoardReasonInfoNotAddressed,
		Annotation: &Annotation{
			State: statePtr(StateInfo), Title: "October newsletter",
			Summary: "New training times from next week, a boathouse party and the results of the last race.",
			Why:     "A newsletter; nothing to do.",
		},
		Messages: []CaseMessage{
			msg("Rowing Club News", s.agoDays(2, 8),
				"New training times, the boathouse party and the results of the last race."),
		},
		MessagesLoaded: true,
	})
	cases = append(cases, Case{
		ID: s.id(18), Account: jiraAccount, Person: "Lou Sample", Date: s.agoDays(1, 11),
		Subject: "DEMO-3: Update the dependency list", Snippet: "Lou Sample closed the issue.",
		MessageCount: 2, Issue: &IssueInfo{Key: "DEMO-3", Status: "Done", Style: jira.StatusDone},
		RuleState: StateYou, RuleReason: api.BoardReasonJiraAssigned,
		Annotation: &Annotation{
			State: statePtr(StateInfo), Title: "Dependency list updated",
			Summary: "Lou updated the list and closed the issue.",
			Why:     "The issue is closed; nothing is left for you.",
		},
		Messages: []CaseMessage{
			mine(s.agoDays(2, 9), "Could you update the list while you are at it?"),
			msg("Lou Sample", s.agoDays(1, 11), "Done, closing the issue."),
		},
		MessagesLoaded: true,
	})
	cases = append(cases, Case{
		ID: s.id(19), Account: work, Person: "Lee Placeholder", Date: s.agoHours(1),
		Subject: "Notes from Tuesday's meeting", Snippet: "The notes are attached.", Unread: true,
		HasAttachments: true, MessageCount: 1, RuleState: StateInfo, RuleReason: api.BoardReasonInfoCcOnly,
		Annotation: &Annotation{
			State: statePtr(StateInfo), Title: "Notes from Tuesday's meeting",
			Summary: "Lee's notes from Tuesday: the launch moves by a week; nothing is assigned to you.",
			Why:     "You are in Cc and nothing is assigned to you.",
		},
		Messages: []CaseMessage{
			msg("Lee Placeholder", s.agoHours(1), "The notes are attached. The launch moves by a week."),
		},
		MessagesLoaded: true,
	})

	// Done.
	cases = append(cases, Case{
		ID: s.id(20), Account: work, Person: "Nia Demo", Date: s.agoDays(1, 10),
		Subject: "Travel booking confirmed", Snippet: "Your train tickets are attached.", HasAttachments: true,
		MessageCount: 1, RuleState: StateInfo, RuleReason: api.BoardReasonInfoNotAddressed,
		Annotation: &Annotation{
			State: statePtr(StateInfo), Title: "Train tickets for the trip",
			Summary: "The train tickets are booked and attached.", Why: "A confirmation; nothing to do.",
		},
		Visibility: Visibility{Kind: VisibleDone},
		Messages: []CaseMessage{
			msg("Nia Demo", s.agoDays(1, 10), "Your train tickets are attached."),
		},
		MessagesLoaded: true,
	})
	cases = append(cases, Case{
		ID: s.id(21), Account: home, Person: "Oskar Sample", Date: s.agoDays(3, 17),
		Subject: "Thanks for the book", Snippet: "I will bring it back next week.", MessageCount: 2,
		RuleState: StateThem, RuleReason: api.BoardReasonThemReplied,
		Annotation: &Annotation{
			State: statePtr(StateThem), Title: "Oskar returns the book",
			Summary: "Oskar will bring the book back next week.", Why: "Oskar said he will bring it.",
		},
		Visibility: Visibility{Kind: VisibleDone},
		Messages: []CaseMessage{
			msg("Oskar Sample", s.agoDays(4, 12), "Thanks for the book! I will bring it back next week."),
			mine(s.agoDays(3, 17), "No hurry."),
		},
		MessagesLoaded: true,
	})

	commitments := []Commitment{
		{
			ID: "sample-promise-1", CaseID: s.id(1), Text: "Send Ada the signed contract",
			Quote: "I will sign it and send it back this afternoon.", Due: due(s.at(0, 17)),
		},
		{
			ID: "sample-promise-2", CaseID: s.id(4), Text: "Send Chris the updated Q4 figures",
			Quote: "I will send the updated figures before the meeting.", Due: due(s.at(1, 12)),
		},
		{
			ID: "sample-promise-3", CaseID: s.id(2), Text: "Tell Ben when a fix is ready to test",
			Quote: "Looking into it.",
		},
		{
			ID: "sample-promise-4", CaseID: s.id(21), Text: "Remind Oskar about the book",
			Quote: "No hurry.", Due: due(s.at(6, 10)),
		},
	}

	return Snapshot{
		Accounts: accounts, Cases: cases, Commitments: commitments, Annotated: true,
		Run:   &Run{Model: "Claude", Date: s.agoMinutes(20), Note: "19 cases sorted, 2 done"},
		Phase: PhaseReady,
	}
}

// samples are the dates and ids of the samples.
type samples struct {
	now time.Time
	loc *time.Location
}

func (s samples) id(n int) CaseID { return CaseID(fmt.Sprintf("sample-%d", n)) }

func (s samples) agoMinutes(m int) time.Time { return s.now.Add(-time.Duration(m) * time.Minute) }

func (s samples) agoHours(h int) time.Time { return s.agoMinutes(h * 60) }

// agoDays is days days before today at hour, never later than an hour
// ago.
func (s samples) agoDays(days, hour int) time.Time {
	t := s.at(-days, hour)
	if hour := s.agoMinutes(60); hour.Before(t) {
		return hour
	}
	return t
}

// at is days days from today at hour o'clock.
func (s samples) at(days, hour int) time.Time {
	y, m, d := s.now.In(s.loc).Date()
	return time.Date(y, m, d+days, hour, 0, 0, 0, s.loc)
}
