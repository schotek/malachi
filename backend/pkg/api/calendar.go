// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import "time"

// Calendars, events and invitations (docs/api.md §4.14–§4.16; the design
// is docs/calendar.md). Every string of these types that comes from a
// server, an .ics or another person (titles, locations, notes, names,
// calendar names) is untrusted display text: cleaned by the daemon and
// shown as plain text. No HTML of an event ever crosses the API.

// ---------------------------------------------------------------------------
// Limits (docs/calendar.md §4.8). Exceeding an input limit is
// invalidArgument; a server's data over a stored limit is truncated (and
// counted) or skipped. The Windows and Swift mirrors check these values.
// ---------------------------------------------------------------------------

const (
	MaxICSBytes             = 1 << 20 // one iCalendar object or calendar part
	MaxEventTitleBytes      = 1024
	MaxEventLocationBytes   = 1024
	MaxEventNotesBytes      = 32 << 10
	MaxMailTextBytes        = 4 << 10 // MailText.Subject, MailText.Text each
	MaxCommentBytes         = 2 << 10
	MaxEventAttendeesIn     = 100 // EventInput.Attendees
	MaxEventAttendeesStored = 500 // Event.Attendees (attendeeCount counts all)
	MaxInvitationAttendees  = 50  // InvitationCard.Attendees
	MaxEventAlerts          = 4
	MaxAlertMinutes         = 40320 // 4 weeks
	MaxEventAttachments     = 10
	MaxEventAttachmentBytes = 3 << 20
	MaxEventListDays        = 400
	MaxEventListResults     = 5000
	DefaultEventListResults = 2000
	MaxEventSearchResults   = 200
	MaxSeriesOccurrences    = 1000 // per series per query window
	MaxCalendarsPerAccount  = 200
	MaxEventsPerCalendar    = 50000
	MaxFreeBusyAttendees    = 20
	MaxFreeBusyDays         = 62
	MaxFreeSlots            = 50
	MaxInvitationList       = 500
	CalendarFutureDays      = 400 // the window's forward edge (docs/calendar.md §2.3)
	DefaultCalendarKeepDays = 365
	MaxRecurrenceInterval   = 99  // input
	MaxRecurrenceCount      = 999 // input
)

// ---------------------------------------------------------------------------
// Scalars and identifiers
// ---------------------------------------------------------------------------

// Date is a calendar date without a zone, "2026-10-14".
type Date string

// LocalDateTime is a wall-clock time, "2026-10-14T10:00:00", always with a
// separate TimeZone.
type LocalDateTime string

// TimeZone is an IANA zone name ("Europe/Prague"); "" means floating.
type TimeZone string

// DateOrTime is either an RFC 3339 UTC instant (a timed event) or a Date
// (an all-day event): a recurrence id, or the previous time of an update.
type DateOrTime string

// CalendarID names a stored calendar ("cal_…"), opaque.
type CalendarID string

// EventID names a stored event ("ev_…"), opaque; a series' instances share
// the id of their master and differ by recurrence id.
type EventID string

// AlertID names one alert of one occurrence ("al_…"), opaque.
type AlertID string

// ---------------------------------------------------------------------------
// Enumerations
// ---------------------------------------------------------------------------

// CalendarSource is where an account's calendars come from. Open enum: an
// unknown value is treated as absent.
type CalendarSource string

const (
	CalendarSourceGraph  CalendarSource = "graph"
	CalendarSourceCalDAV CalendarSource = "caldav"
	// CalendarSourceGoogle is reserved and not produced yet.
	CalendarSourceGoogle CalendarSource = "google"
)

// CalendarAccess is what the user may do with a calendar.
type CalendarAccess string

const (
	CalendarAccessOwner CalendarAccess = "owner"
	CalendarAccessWrite CalendarAccess = "write"
	CalendarAccessRead  CalendarAccess = "read"
)

// CalendarKind is what a calendar holds. Open enum: an unknown value is
// treated as absent.
type CalendarKind string

const (
	CalendarKindEvents    CalendarKind = "events"
	CalendarKindBirthdays CalendarKind = "birthdays"
	CalendarKindHolidays  CalendarKind = "holidays"
	CalendarKindOther     CalendarKind = "other"
)

// EventResponse is an attendee's answer (iCalendar PARTSTAT).
type EventResponse string

const (
	ResponseNeedsAction EventResponse = "needsAction"
	ResponseAccepted    EventResponse = "accepted"
	ResponseTentative   EventResponse = "tentative"
	ResponseDeclined    EventResponse = "declined"
	ResponseDelegated   EventResponse = "delegated"
)

// ShowAs is how an event counts for free/busy. An unknown value is treated
// as ShowAsUnknown.
type ShowAs string

const (
	ShowAsFree             ShowAs = "free"
	ShowAsTentative        ShowAs = "tentative"
	ShowAsBusy             ShowAs = "busy"
	ShowAsOOF              ShowAs = "oof"
	ShowAsWorkingElsewhere ShowAs = "workingElsewhere"
	ShowAsUnknown          ShowAs = "unknown"
)

// EventStatus is an event's iCalendar STATUS.
type EventStatus string

const (
	EventStatusConfirmed EventStatus = "confirmed"
	EventStatusTentative EventStatus = "tentative"
	EventStatusCancelled EventStatus = "cancelled"
)

// EditScope is what a change of a recurring event applies to
// (docs/calendar.md D19).
type EditScope string

const (
	EditScopeThis          EditScope = "this"          // the instance: an override / a Graph exception
	EditScopeThisAndFuture EditScope = "thisAndFuture" // the series is split at the instance
	EditScopeAll           EditScope = "all"           // the master
)

// InvitationMethod is the iTIP METHOD of an invitation's calendar part.
type InvitationMethod string

const (
	InvitationMethodRequest        InvitationMethod = "request"
	InvitationMethodReply          InvitationMethod = "reply"
	InvitationMethodCancel         InvitationMethod = "cancel"
	InvitationMethodCounter        InvitationMethod = "counter"
	InvitationMethodDeclineCounter InvitationMethod = "declineCounter"
	InvitationMethodRefresh        InvitationMethod = "refresh"
	InvitationMethodAdd            InvitationMethod = "add"
	InvitationMethodPublish        InvitationMethod = "publish"
	InvitationMethodOther          InvitationMethod = "other"
)

// InvitationKind is the list pill of a message with an invitation.
type InvitationKind string

const (
	InvitationKindInvitation InvitationKind = "invitation" // a request
	InvitationKindUpdate     InvitationKind = "update"     // a request changing a known event, or SEQUENCE > 0 of a known UID
	InvitationKindCancelled  InvitationKind = "cancelled"  // a cancel
	InvitationKindReply      InvitationKind = "reply"      // a reply
	InvitationKindOther      InvitationKind = "other"      // everything else: no pill
)

// InvitationState says what is left to do about an invitation.
type InvitationState string

const (
	InvitationUnanswered InvitationState = "unanswered" // a request the user has not answered, its event not in the past
	InvitationAnswered   InvitationState = "answered"
	InvitationOutdated   InvitationState = "outdated"  // a newer SEQUENCE of the UID arrived, or the event ended
	InvitationCancelled  InvitationState = "cancelled" // a cancel, or a request a later cancel cancelled
	InvitationProcessed  InvitationState = "processed" // a reply or other message with nothing to ask
	InvitationInvalid    InvitationState = "invalid"   // the part could not be read: no card
)

// CalendarSyncStatus is the status of an account's calendar
// synchronisation (CalendarSyncState).
type CalendarSyncStatus string

const (
	CalendarSyncIdle            CalendarSyncStatus = "idle"
	CalendarSyncSyncing         CalendarSyncStatus = "syncing"
	CalendarSyncOffline         CalendarSyncStatus = "offline"
	CalendarSyncAuthRequired    CalendarSyncStatus = "authRequired"
	CalendarSyncConsentRequired CalendarSyncStatus = "consentRequired" // graph only: the sign-in lacks calendar access
	CalendarSyncError           CalendarSyncStatus = "error"
	CalendarSyncDisabled        CalendarSyncStatus = "disabled"
)

// InvitationsVia is who delivers the invitations, updates and replies of
// an event: the server, the daemon by iMIP mail through the outbox, or
// nobody.
type InvitationsVia string

const (
	InvitationsViaServer InvitationsVia = "server"
	InvitationsViaMail   InvitationsVia = "mail"
	InvitationsViaNone   InvitationsVia = "none"
)

// VideoProvider is the service of a video-call link. Open enum: an
// unknown value is treated as absent.
type VideoProvider string

const (
	VideoProviderTeams   VideoProvider = "teams"
	VideoProviderZoom    VideoProvider = "zoom"
	VideoProviderMeet    VideoProvider = "meet"
	VideoProviderWebex   VideoProvider = "webex"
	VideoProviderJitsi   VideoProvider = "jitsi"
	VideoProviderWhereby VideoProvider = "whereby"
	VideoProviderOther   VideoProvider = "other"
)

// CalDAVScheduling is who sends the invitations and replies of a caldav
// account's events (docs/calendar.md D8): auto follows the server's
// calendar-auto-schedule; account.listCalDAVCalendars reports server or
// client only.
type CalDAVScheduling string

const (
	CalDAVSchedulingAuto   CalDAVScheduling = "auto"
	CalDAVSchedulingServer CalDAVScheduling = "server"
	CalDAVSchedulingClient CalDAVScheduling = "client"
)

// AfterAnswer is what happens to an invitation message once the user
// answered it.
type AfterAnswer string

const (
	AfterAnswerKeep    AfterAnswer = "keep"
	AfterAnswerArchive AfterAnswer = "archive"
)

// WeekStart is the first day of the week of the calendar views.
type WeekStart string

const (
	WeekStartLocale   WeekStart = "locale"
	WeekStartMonday   WeekStart = "monday"
	WeekStartSunday   WeekStart = "sunday"
	WeekStartSaturday WeekStart = "saturday"
)

// RecurrenceFrequency is the FREQ of a Recurrence.
type RecurrenceFrequency string

const (
	FrequencyDaily   RecurrenceFrequency = "daily"
	FrequencyWeekly  RecurrenceFrequency = "weekly"
	FrequencyMonthly RecurrenceFrequency = "monthly"
	FrequencyYearly  RecurrenceFrequency = "yearly"
)

// AttendeeRole is an attendee's iCalendar ROLE.
type AttendeeRole string

const (
	AttendeeRequired       AttendeeRole = "required"
	AttendeeOptional       AttendeeRole = "optional"
	AttendeeChair          AttendeeRole = "chair"
	AttendeeNonParticipant AttendeeRole = "nonParticipant"
)

// AttendeeType is an attendee's iCalendar CUTYPE. An unknown value is
// treated as AttendeeTypeUnknown.
type AttendeeType string

const (
	AttendeeTypePerson   AttendeeType = "person"
	AttendeeTypeRoom     AttendeeType = "room"
	AttendeeTypeResource AttendeeType = "resource"
	AttendeeTypeGroup    AttendeeType = "group"
	AttendeeTypeUnknown  AttendeeType = "unknown"
)

// EventAlertState is the state notify.eventAlert announces.
type EventAlertState string

const (
	AlertDue       EventAlertState = "due"       // fired, or a snooze ended
	AlertDismissed EventAlertState = "dismissed" // a client dismissed it: withdraw the notification
)

// VideoCallMode is what EventInput.VideoCall asks for.
type VideoCallMode string

const (
	VideoCallNone   VideoCallMode = "none"
	VideoCallCreate VideoCallMode = "create" // the source creates a meeting (CalendarFeatures.OnlineMeetings)
	VideoCallLink   VideoCallMode = "link"   // a link the user typed
)

// CalDAVFoundVia is how account.discoverCalDAV found the server.
type CalDAVFoundVia string

const (
	CalDAVFoundWellKnown CalDAVFoundVia = "wellKnown"
	CalDAVFoundSRV       CalDAVFoundVia = "srv"
	CalDAVFoundURL       CalDAVFoundVia = "url"
)

// ---------------------------------------------------------------------------
// Accounts (docs/calendar.md §4.3)
// ---------------------------------------------------------------------------

// CalDAVConfig is the connection of a caldav account. The password is
// Credentials.Password, kept in the keyring.
type CalDAVConfig struct {
	ServerURL    string `json:"serverUrl"`              // normalised (account.discoverCalDAV)
	PrincipalURL string `json:"principalUrl,omitempty"` // from account.listCalDAVCalendars
	HomeSetURL   string `json:"homeSetUrl,omitempty"`
	Username     string `json:"username"`
	// CertificateSHA256 pins the server's leaf certificate, as
	// ServerConfig.CertificateSHA256; not allowed with an http URL.
	CertificateSHA256 string `json:"certificateSha256,omitempty"`
	// MailAccountID is the mail account invitations and replies leave
	// from when the daemon schedules (client scheduling); empty: events
	// with attendees answer invitationsUnavailable.
	MailAccountID AccountID        `json:"mailAccountId,omitempty"`
	Scheduling    CalDAVScheduling `json:"scheduling,omitempty"` // "" = auto
}

// CalendarConfig is the calendar part of a graph or caldav account.
type CalendarConfig struct {
	// Sync: synchronise the calendars. Graph default false; caldav always
	// true.
	Sync bool `json:"sync"`
	// DisabledCalendars are remote ids (graph) or hrefs (caldav) not
	// synchronised; ≤ 200 entries of ≤ 2048 bytes.
	DisabledCalendars []string `json:"disabledCalendars,omitempty"`
	// DefaultCalendar is the remote id or href new events of this
	// account go to; "" = the server's default.
	DefaultCalendar string `json:"defaultCalendar,omitempty"`
	// KeepDays is how far back events are kept: 30, 90, 180, 365 or 730;
	// 0 = DefaultCalendarKeepDays.
	KeepDays int `json:"keepDays,omitempty"`
	// ShowUnanswered draws unanswered invitations dashed; nil = true.
	ShowUnanswered *bool `json:"showUnanswered,omitempty"`
	// InvitationCard shows the invitation card in Mail; nil = true.
	InvitationCard *bool       `json:"invitationCard,omitempty"`
	AfterAnswer    AfterAnswer `json:"afterAnswer,omitempty"` // "" = keep
}

// ShowsUnanswered resolves ShowUnanswered's default.
func (c CalendarConfig) ShowsUnanswered() bool { return c.ShowUnanswered == nil || *c.ShowUnanswered }

// ShowsInvitationCard resolves InvitationCard's default.
func (c CalendarConfig) ShowsInvitationCard() bool {
	return c.InvitationCard == nil || *c.InvitationCard
}

// CalDAVCalendar is a VEVENT collection of a CalDAV server
// (account.listCalDAVCalendars).
type CalDAVCalendar struct {
	Href   string         `json:"href"`
	Name   string         `json:"name"`
	Color  string         `json:"color,omitempty"` // #rrggbb
	Access CalendarAccess `json:"access"`
	// IsDefault: the server's schedule-default-calendar-URL.
	IsDefault bool `json:"isDefault,omitempty"`
}

// AccountDiscoverCalDAVParams: URL is a host or a full URL; https assumed.
// CertificateSHA256 optionally pins the server's leaf certificate (64 hex
// digits, normalised by NormalizeCertificateSHA256 as the account
// config's pin), so that discovery can run again after the user trusted
// the certificate; it applies only to the typed URL's origin, never to a
// host a redirect names.
type AccountDiscoverCalDAVParams struct {
	URL               string `json:"url"`
	CertificateSHA256 string `json:"certificateSha256,omitempty"`
}

// AccountDiscoverCalDAVResult names the server found. ServerURL may name
// another host than the one typed (a .well-known redirect); Certificate
// describes the verified certificate.
type AccountDiscoverCalDAVResult struct {
	ServerURL        string           `json:"serverUrl"`
	FoundVia         CalDAVFoundVia   `json:"foundVia"`
	NeedsCredentials bool             `json:"needsCredentials"`
	Certificate      *CertificateInfo `json:"certificate,omitempty"`
}

// AccountListCalDAVCalendarsParams: with AccountID and no password the
// stored one is used, only for a caldav account of the same realm.
type AccountListCalDAVCalendarsParams struct {
	AccountID   AccountID     `json:"accountId,omitempty"`
	Config      AccountConfig `json:"config"`
	Credentials Credentials   `json:"credentials"`
}

type AccountListCalDAVCalendarsResult struct {
	PrincipalURL string `json:"principalUrl"`
	HomeSetURL   string `json:"homeSetUrl"`
	DisplayName  string `json:"displayName,omitempty"`
	// Addresses are the mailto: values of calendar-user-address-set.
	Addresses  []string         `json:"addresses"`
	Scheduling CalDAVScheduling `json:"scheduling"` // server or client
	Calendars  []CalDAVCalendar `json:"calendars"`  // ≤ MaxCalendarsPerAccount
}

// ---------------------------------------------------------------------------
// Common types (docs/calendar.md §4.2)
// ---------------------------------------------------------------------------

// Calendar is one stored calendar.
type Calendar struct {
	ID        CalendarID `json:"id"`
	AccountID AccountID  `json:"accountId"`
	Name      string     `json:"name"`
	// RemoteID is the Graph calendar id or the CalDAV collection href:
	// the value CalendarConfig.DisabledCalendars and DefaultCalendar use.
	// Always present.
	RemoteID string `json:"remoteId"`
	// Disabled: the calendar is in the account's DisabledCalendars; listed
	// by calendar.list so that Preferences can offer it, with no events
	// stored. Visible and Color still apply.
	Disabled bool `json:"disabled,omitempty"`
	// Color is the user's colour if set, else the server's, else a palette
	// colour by id; "#rrggbb", lower case, always valid.
	Color       string `json:"color"`
	ServerColor string `json:"serverColor,omitempty"`
	UserColor   string `json:"userColor,omitempty"`
	// Visible: shown in the views (calendar.update).
	Visible bool           `json:"visible"`
	Access  CalendarAccess `json:"access"`
	Kind    CalendarKind   `json:"kind"`
	// Owner is set for a calendar shared with the user.
	Owner *Address `json:"owner,omitempty"`
	// IsDefault: the account's calendar for new events.
	IsDefault bool     `json:"isDefault,omitempty"`
	TimeZone  TimeZone `json:"timeZone,omitempty"`
	// OnlineMeetingProviders: Graph allowedOnlineMeetingProviders, an open
	// enum ("teamsForBusiness").
	OnlineMeetingProviders []string `json:"onlineMeetingProviders,omitempty"`
}

// CalendarFeatures is what an account's source can do.
type CalendarFeatures struct {
	FreeBusy       bool           `json:"freeBusy"`       // calendar.freeBusy can ask about other people
	ProposeNewTime bool           `json:"proposeNewTime"` // some events may offer it
	Invitations    InvitationsVia `json:"invitations"`
	MailAccountID  AccountID      `json:"mailAccountId,omitempty"` // invitations "mail": the account they leave from
	OnlineMeetings bool           `json:"onlineMeetings"`          // "Add video call" can create a meeting
}

// CalendarSyncState is an account's calendar synchronisation state. A
// Graph account's is separate from its mail SyncState.
type CalendarSyncState struct {
	AccountID AccountID          `json:"accountId"`
	Status    CalendarSyncStatus `json:"status"`
	Progress  int                `json:"progress"` // 0–100, -1 unknown
	LastSync  *time.Time         `json:"lastSync,omitempty"`
	Error     *Error             `json:"error,omitempty"`
}

// CalendarWindow is the span of an account's stored events.
type CalendarWindow struct {
	From Date `json:"from"`
	To   Date `json:"to"`
}

// CalendarAccount is an account with CapabilityCalendar (calendar.list).
type CalendarAccount struct {
	AccountID AccountID         `json:"accountId"`
	Source    CalendarSource    `json:"source"`
	Sync      bool              `json:"sync"` // CalendarConfig.Sync (always true for caldav)
	State     CalendarSyncState `json:"state"`
	Features  CalendarFeatures  `json:"features"`
	Window    CalendarWindow    `json:"window"`
	// Calendars in server order; empty (never null) while sync is off.
	Calendars []Calendar `json:"calendars"`
}

// Recurrence is a recurrence rule in the shape the editor shows.
type Recurrence struct {
	Frequency RecurrenceFrequency `json:"frequency"`
	// Interval is 1–MaxRecurrenceInterval in input; up to 1000 read.
	Interval int `json:"interval"`
	// ByDay: weekly the days ("MO"); monthly/yearly with an ordinal
	// ("2TU", "-1FR").
	ByDay      []string `json:"byDay,omitempty"`
	ByMonthDay []int    `json:"byMonthDay,omitempty"` // -31..31, not 0
	ByMonth    []int    `json:"byMonth,omitempty"`    // 1–12
	// Until and Count: at most one; neither = no end.
	Until     Date   `json:"until,omitempty"`
	Count     int    `json:"count,omitempty"`
	WeekStart string `json:"weekStart,omitempty"` // "MO"…"SU"
	// Complex (read only): the rule has more than this shape; shown as
	// "Custom", edited only with EditScopeAll and kept unless replaced.
	Complex bool `json:"complex,omitempty"`
	// Exceptions (read only): modified or removed instances in the window.
	Exceptions int `json:"exceptions,omitempty"`
}

// Attendee is one attendee of an event or an invitation.
type Attendee struct {
	Name      string        `json:"name,omitempty"`
	Address   string        `json:"address"`
	Role      AttendeeRole  `json:"role"`
	Response  EventResponse `json:"response"`
	Type      AttendeeType  `json:"type"`
	Me        bool          `json:"me,omitempty"`
	Organizer bool          `json:"organizer,omitempty"`
}

// VideoCall is a video-call link found in an event; opened only through
// the client's "Open this link?".
type VideoCall struct {
	URL      string        `json:"url"`
	Provider VideoProvider `json:"provider"`
	Host     string        `json:"host"`
}

// MessageRef names a stored message: an invitation of an occurrence, or
// the invitation messages of an event.
type MessageRef struct {
	AccountID AccountID `json:"accountId"`
	MessageID MessageID `json:"messageId"`
}

// Occurrence is one instance in a view (event.list, event.search). Timed:
// Start and End are UTC instants; all-day: StartDate and EndDate
// (exclusive) instead.
type Occurrence struct {
	EventID      EventID     `json:"eventId,omitempty"`      // absent for an invitation not in any calendar
	RecurrenceID DateOrTime  `json:"recurrenceId,omitempty"` // instance of a series: its original start
	SeriesID     EventID     `json:"seriesId,omitempty"`     // the master of a series instance
	CalendarID   CalendarID  `json:"calendarId,omitempty"`
	AccountID    AccountID   `json:"accountId"`
	Title        string      `json:"title"` // "" when private and not the user's
	Start        *time.Time  `json:"start,omitempty"`
	End          *time.Time  `json:"end,omitempty"`
	StartDate    Date        `json:"startDate,omitempty"`
	EndDate      Date        `json:"endDate,omitempty"`
	AllDay       bool        `json:"allDay"`
	Floating     bool        `json:"floating,omitempty"`
	TimeZone     TimeZone    `json:"timeZone,omitempty"`
	Location     string      `json:"location,omitempty"`
	Status       EventStatus `json:"status"`
	ShowAs       ShowAs      `json:"showAs"`
	// MyResponse is absent when the user is not an attendee.
	MyResponse    EventResponse `json:"myResponse,omitempty"`
	Organizer     bool          `json:"organizer,omitempty"` // the user organises it
	AttendeeCount int           `json:"attendeeCount,omitempty"`
	Recurring     bool          `json:"recurring,omitempty"`
	VideoCall     bool          `json:"videoCall,omitempty"`
	Private       bool          `json:"private,omitempty"`
	Alert         bool          `json:"alert,omitempty"` // an alert is set
	ReadOnly      bool          `json:"readOnly,omitempty"`
	// Invitation: an unanswered invitation shown tentatively, not in a
	// calendar.
	Invitation *MessageRef `json:"invitation,omitempty"`
}

// Event is an occurrence with everything the detail shows (event.get and
// the results of the writes).
type Event struct {
	Occurrence
	ETag               string      `json:"etag"`            // pass back on update/delete/respond
	Notes              string      `json:"notes,omitempty"` // plain text
	Links              []string    `json:"links,omitempty"` // https links found in Notes
	VideoCallLink      *VideoCall  `json:"videoCallLink,omitempty"`
	OrganizerAddress   *Address    `json:"organizerAddress,omitempty"`
	Attendees          []Attendee  `json:"attendees,omitempty"` // ≤ MaxEventAttendeesStored
	AttendeesTruncated bool        `json:"attendeesTruncated,omitempty"`
	Recurrence         *Recurrence `json:"recurrence,omitempty"` // of the series (on any instance)
	Alerts             []int       `json:"alerts,omitempty"`     // minutes before start
	ResponseRequested  bool        `json:"responseRequested,omitempty"`
	CanEdit            bool        `json:"canEdit"`
	CanDelete          bool        `json:"canDelete"`
	CanRespond         bool        `json:"canRespond"`
	CanProposeNewTime  bool        `json:"canProposeNewTime"`
	// RemoveIsDelete: Remove from Calendar deletes the event for everyone
	// (the user organises it).
	RemoveIsDelete     bool         `json:"removeIsDelete"`
	InvitationMessages []MessageRef `json:"invitationMessages,omitempty"` // Show Invitation in Mail
	Created            *time.Time   `json:"created,omitempty"`
	Updated            *time.Time   `json:"updated,omitempty"`
}

// EventInputAttendee is an attendee as the editor gives it.
type EventInputAttendee struct {
	Name    string       `json:"name,omitempty"`
	Address string       `json:"address"`
	Role    AttendeeRole `json:"role,omitempty"` // required or optional; "" = required
}

// VideoCallInput is the editor's video call.
type VideoCallInput struct {
	Mode     VideoCallMode `json:"mode"`
	Provider string        `json:"provider,omitempty"` // create: an onlineMeetingProviders value
	URL      string        `json:"url,omitempty"`      // link: https
}

// EventInput is an event as event.create and event.update take it: timed
// (Start, End, TimeZone required, "" refused) or all-day (StartDate,
// EndDate exclusive and after StartDate).
type EventInput struct {
	Title     string        `json:"title"` // ≤ MaxEventTitleBytes after cleaning; may be empty
	AllDay    bool          `json:"allDay"`
	Start     LocalDateTime `json:"start,omitempty"`
	End       LocalDateTime `json:"end,omitempty"`
	TimeZone  TimeZone      `json:"timeZone,omitempty"`
	StartDate Date          `json:"startDate,omitempty"`
	EndDate   Date          `json:"endDate,omitempty"`
	Location  string        `json:"location,omitempty"` // ≤ MaxEventLocationBytes
	Notes     string        `json:"notes,omitempty"`    // plain text, ≤ MaxEventNotesBytes
	// Recurrence without Complex and Exceptions; nil = none.
	Recurrence *Recurrence `json:"recurrence,omitempty"`
	// KeepRecurrence: an update of a complex rule keeps it as it is
	// (Recurrence ignored).
	KeepRecurrence bool `json:"keepRecurrence,omitempty"`
	// Alerts in minutes before start, ≤ MaxEventAlerts values of
	// 0–MaxAlertMinutes. nil (absent or null) = the default alert of
	// calendar.preferences; empty = none. Not omitempty, so that an empty
	// list survives encoding.
	Alerts    []int                `json:"alerts"`
	ShowAs    ShowAs               `json:"showAs,omitempty"`
	Private   bool                 `json:"private,omitempty"`
	Attendees []EventInputAttendee `json:"attendees,omitempty"` // ≤ MaxEventAttendeesIn
	VideoCall *VideoCallInput      `json:"videoCall,omitempty"`
	// Attachments are attachment.import ids, ≤ MaxEventAttachments, each
	// ≤ MaxEventAttachmentBytes.
	Attachments []string `json:"attachments,omitempty"`
}

// MailText is a client's localised iMIP subject and body (the daemon is
// language-neutral); each ≤ MaxMailTextBytes.
type MailText struct {
	Subject string `json:"subject"`
	Text    string `json:"text"`
}

// BusyInterval is a busy span of an Availability.
type BusyInterval struct {
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	ShowAs ShowAs    `json:"showAs"`
}

// Availability is one person's free/busy (calendar.freeBusy).
type Availability struct {
	Address string         `json:"address"`
	Known   bool           `json:"known"` // false: no free/busy for this person
	Busy    []BusyInterval `json:"busy"`  // never null
}

// TimeSlot is a free slot (calendar.findFreeTime).
type TimeSlot struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// EventAlert is an alert notify.eventAlert announces.
type EventAlert struct {
	AlertID       AlertID         `json:"alertId"`
	State         EventAlertState `json:"state"`
	EventID       EventID         `json:"eventId"`
	RecurrenceID  DateOrTime      `json:"recurrenceId,omitempty"`
	AccountID     AccountID       `json:"accountId"`
	CalendarID    CalendarID      `json:"calendarId"`
	Title         string          `json:"title"`
	Start         *time.Time      `json:"start,omitempty"`
	End           *time.Time      `json:"end,omitempty"`
	StartDate     Date            `json:"startDate,omitempty"`
	AllDay        bool            `json:"allDay"`
	Location      string          `json:"location,omitempty"`
	VideoCall     bool            `json:"videoCall,omitempty"`
	MinutesBefore int             `json:"minutesBefore"`
}

// WorkingHours are the user's working days and hours, "HH:MM" in the
// daemon's local zone.
type WorkingHours struct {
	Days  []string `json:"days"` // "MO"…"SU", at least one
	Start string   `json:"start"`
	End   string   `json:"end"` // after Start
}

// CalendarPreferences are the calendar's daemon-side preferences
// (calendar.preferences; meta calendar.prefs).
type CalendarPreferences struct {
	WeekStart              WeekStart    `json:"weekStart"`
	DefaultDurationMinutes int          `json:"defaultDurationMinutes"` // 5–1440
	DefaultAlertMinutes    int          `json:"defaultAlertMinutes"`    // -1 none, 0–MaxAlertMinutes
	ShowDeclined           bool         `json:"showDeclined"`
	WorkingHours           WorkingHours `json:"workingHours"`
}

// DefaultCalendarPreferences returns the preferences of a store that
// never had any set.
func DefaultCalendarPreferences() CalendarPreferences {
	return CalendarPreferences{
		WeekStart:              WeekStartLocale,
		DefaultDurationMinutes: 60,
		DefaultAlertMinutes:    15,
		WorkingHours:           WorkingHours{Days: []string{"MO", "TU", "WE", "TH", "FR"}, Start: "08:00", End: "17:00"},
	}
}

// CalendarPreferencesUpdate is CalendarPreferences as
// calendar.setPreferences takes it: a missing (nil) field keeps its value;
// WorkingHours, when given, is replaced whole.
type CalendarPreferencesUpdate struct {
	WeekStart              *WeekStart    `json:"weekStart,omitempty"`
	DefaultDurationMinutes *int          `json:"defaultDurationMinutes,omitempty"`
	DefaultAlertMinutes    *int          `json:"defaultAlertMinutes,omitempty"`
	ShowDeclined           *bool         `json:"showDeclined,omitempty"`
	WorkingHours           *WorkingHours `json:"workingHours,omitempty"`
}

// ---------------------------------------------------------------------------
// Error data
// ---------------------------------------------------------------------------

// CalendarConsentRequiredData is Error.Data of calendarConsentRequired.
// SignIn: account.oauthStart with Calendar can fix it (source daemon);
// false for GOA (the desktop's Online Accounts settings).
type CalendarConsentRequiredData struct {
	AccountID AccountID `json:"accountId"`
	SignIn    bool      `json:"signIn"`
}

// ConflictEventData is Error.Data of conflict for an event write whose
// etag is not the server's current one: the current event, as event.get
// returns it.
type ConflictEventData struct {
	Event Event `json:"event"`
}

// ---------------------------------------------------------------------------
// calendar.* (docs/api.md §4.14)
// ---------------------------------------------------------------------------

type CalendarListParams struct {
	AccountID AccountID `json:"accountId,omitempty"` // empty = every account with the capability
}

type CalendarListResult struct {
	Accounts []CalendarAccount `json:"accounts"`
	// Unanswered counts the invitations of enabled accounts waiting for an
	// answer.
	Unanswered int `json:"unanswered"`
}

// CalendarUpdateParams: the user's own settings of one calendar; nil
// fields are left as they are, UserColor "" clears it.
type CalendarUpdateParams struct {
	CalendarID CalendarID `json:"calendarId"`
	Visible    *bool      `json:"visible,omitempty"`
	UserColor  *string    `json:"userColor,omitempty"`
}

type CalendarUpdateResult struct {
	Calendar Calendar `json:"calendar"`
}

type CalendarSyncParams struct {
	AccountID  AccountID  `json:"accountId,omitempty"`  // empty = every account
	CalendarID CalendarID `json:"calendarId,omitempty"` // empty = the account's calendars
}

type CalendarSyncResult struct{}

type CalendarPreferencesParams struct{}

type CalendarPreferencesResult struct {
	Preferences CalendarPreferences `json:"preferences"`
}

type CalendarSetPreferencesParams struct {
	Preferences CalendarPreferencesUpdate `json:"preferences"`
}

type CalendarSetPreferencesResult struct {
	Preferences CalendarPreferences `json:"preferences"` // as stored
}

// CalendarFreeBusyParams: ≤ MaxFreeBusyAttendees, To − From ≤
// MaxFreeBusyDays; IntervalMinutes 15, 30 or 60 (0 = 30).
type CalendarFreeBusyParams struct {
	AccountID       AccountID `json:"accountId"`
	Attendees       []string  `json:"attendees"`
	From            time.Time `json:"from"`
	To              time.Time `json:"to"`
	IntervalMinutes int       `json:"intervalMinutes,omitempty"`
}

// CalendarFreeBusyResult: Attendees in the order asked.
type CalendarFreeBusyResult struct {
	Me        Availability   `json:"me"`
	Attendees []Availability `json:"attendees"`
}

// CalendarFindFreeTimeParams: WorkingHoursOnly nil = true; Limit 0 = 5,
// ≤ MaxFreeSlots.
type CalendarFindFreeTimeParams struct {
	AccountID        AccountID `json:"accountId,omitempty"`
	Attendees        []string  `json:"attendees,omitempty"`
	DurationMinutes  int       `json:"durationMinutes"`
	From             time.Time `json:"from"`
	To               time.Time `json:"to"`
	WorkingHoursOnly *bool     `json:"workingHoursOnly,omitempty"`
	Limit            int       `json:"limit,omitempty"`
}

type CalendarFindFreeTimeResult struct {
	Slots   []TimeSlot `json:"slots"`
	Unknown []string   `json:"unknown"` // attendees without free/busy
}

// ---------------------------------------------------------------------------
// event.* (docs/api.md §4.15)
// ---------------------------------------------------------------------------

// EventListParams: To − From ≤ MaxEventListDays; Limit 0 =
// DefaultEventListResults, ≤ MaxEventListResults. IncludeDeclined nil =
// the showDeclined preference; IncludeInvitations nil = true.
type EventListParams struct {
	From               time.Time    `json:"from"`
	To                 time.Time    `json:"to"`
	TimeZone           TimeZone     `json:"timeZone"`
	CalendarIDs        []CalendarID `json:"calendarIds,omitempty"`
	AccountIDs         []AccountID  `json:"accountIds,omitempty"`
	IncludeHidden      bool         `json:"includeHidden,omitempty"`
	IncludeDeclined    *bool        `json:"includeDeclined,omitempty"`
	IncludeInvitations *bool        `json:"includeInvitations,omitempty"`
	Limit              int          `json:"limit,omitempty"`
}

type EventListResult struct {
	Occurrences []Occurrence `json:"occurrences"`
	Truncated   bool         `json:"truncated"` // Limit reached
	// Complete false: a part of the range outside an account's window could
	// not be fetched; what is stored is returned.
	Complete bool `json:"complete"`
}

type EventGetParams struct {
	EventID      EventID    `json:"eventId"`
	RecurrenceID DateOrTime `json:"recurrenceId,omitempty"`
	TimeZone     TimeZone   `json:"timeZone,omitempty"`
}

type EventGetResult struct {
	Event Event `json:"event"`
}

// EventSearchParams: Query 1–256 bytes; Limit 0 = 50, ≤
// MaxEventSearchResults.
type EventSearchParams struct {
	Query       string       `json:"query"`
	From        *time.Time   `json:"from,omitempty"`
	To          *time.Time   `json:"to,omitempty"`
	CalendarIDs []CalendarID `json:"calendarIds,omitempty"`
	Limit       int          `json:"limit,omitempty"`
}

type EventSearchResult struct {
	Occurrences []Occurrence `json:"occurrences"`
	Total       int          `json:"total"` // ≤ 1000
}

// InvitationsSent says how the invitations, updates or cancellations of a
// write left.
type InvitationsSent struct {
	Via   InvitationsVia `json:"via"`
	Count int            `json:"count"`
}

// EventCreateParams: SendInvitations nil = true.
type EventCreateParams struct {
	CalendarID      CalendarID `json:"calendarId"`
	Event           EventInput `json:"event"`
	SendInvitations *bool      `json:"sendInvitations,omitempty"`
	Mail            *MailText  `json:"mail,omitempty"`
}

type EventCreateResult struct {
	Event       Event           `json:"event"`
	Invitations InvitationsSent `json:"invitations"`
}

// EventUpdateParams: Event replaces every field of the scope's target.
// SendUpdates nil = true.
type EventUpdateParams struct {
	EventID      EventID    `json:"eventId"`
	RecurrenceID DateOrTime `json:"recurrenceId,omitempty"`
	Scope        EditScope  `json:"scope"`
	ETag         string     `json:"etag"`
	Event        EventInput `json:"event"`
	SendUpdates  *bool      `json:"sendUpdates,omitempty"`
	Mail         *MailText  `json:"mail,omitempty"`
}

type EventUpdateResult struct {
	Event Event `json:"event"`
	// DroppedExceptions: overrides that no longer fit the changed rule.
	DroppedExceptions int             `json:"droppedExceptions"`
	Invitations       InvitationsSent `json:"invitations"`
}

// EventDeleteParams: Notify nil = true; Comment ≤ MaxCommentBytes.
type EventDeleteParams struct {
	EventID      EventID    `json:"eventId"`
	RecurrenceID DateOrTime `json:"recurrenceId,omitempty"`
	Scope        EditScope  `json:"scope"`
	ETag         string     `json:"etag"`
	Notify       *bool      `json:"notify,omitempty"`
	Comment      string     `json:"comment,omitempty"`
	Mail         *MailText  `json:"mail,omitempty"`
}

type EventDeleteResult struct {
	Invitations InvitationsSent `json:"invitations"`
}

// EventRespondParams: Scope this or all; Response accepted, tentative or
// declined; SendResponse nil = true; a proposed time needs both ends and
// Event.CanProposeNewTime.
type EventRespondParams struct {
	EventID       EventID       `json:"eventId"`
	RecurrenceID  DateOrTime    `json:"recurrenceId,omitempty"`
	Scope         EditScope     `json:"scope"`
	ETag          string        `json:"etag"`
	Response      EventResponse `json:"response"`
	SendResponse  *bool         `json:"sendResponse,omitempty"`
	Comment       string        `json:"comment,omitempty"`
	ProposedStart *time.Time    `json:"proposedStart,omitempty"`
	ProposedEnd   *time.Time    `json:"proposedEnd,omitempty"`
	Mail          *MailText     `json:"mail,omitempty"`
}

type EventRespondResult struct {
	Event Event          `json:"event"`
	Via   InvitationsVia `json:"via"`
}

// EventSnoozeAlertParams: Minutes 1–1440.
type EventSnoozeAlertParams struct {
	AlertID AlertID `json:"alertId"`
	Minutes int     `json:"minutes"`
}

type EventSnoozeAlertResult struct{}

type EventDismissAlertParams struct {
	AlertID AlertID `json:"alertId"`
}

type EventDismissAlertResult struct{}

// ---------------------------------------------------------------------------
// invitation.* (docs/api.md §4.16)
// ---------------------------------------------------------------------------

// InvitationSummary rides on a listed message (MessageSummary.Invitation).
type InvitationSummary struct {
	Method    InvitationMethod `json:"method"`
	Kind      InvitationKind   `json:"kind"`
	State     InvitationState  `json:"state"`
	Title     string           `json:"title"`
	Start     *time.Time       `json:"start,omitempty"`
	End       *time.Time       `json:"end,omitempty"`
	StartDate Date             `json:"startDate,omitempty"`
	AllDay    bool             `json:"allDay,omitempty"`
	Organizer *Address         `json:"organizer,omitempty"`
	ReplyFrom *Address         `json:"replyFrom,omitempty"` // reply: the attendee who answered
	// OwnEvent: the organiser is one of the user's addresses (the user's
	// own invitation, its Sent copy): a request with it is processed.
	OwnEvent bool `json:"ownEvent,omitempty"`
}

// InvitationMe is the user on an invitation's attendee list.
type InvitationMe struct {
	Address  string        `json:"address"`
	Response EventResponse `json:"response"`
}

// InvitationPrevious is what the stored event had before an update:
// Start and End only when the time changed, Location when it changed and
// was not empty, Recurrence (the previous rule, until and count included)
// when the rule changed or is gone.
type InvitationPrevious struct {
	Start      DateOrTime  `json:"start,omitempty"`
	End        DateOrTime  `json:"end,omitempty"`
	Location   string      `json:"location,omitempty"`
	Recurrence *Recurrence `json:"recurrence,omitempty"`
}

// InvitationConflict is a stored occurrence overlapping an invitation.
type InvitationConflict struct {
	Title      string     `json:"title,omitempty"` // absent for a private event of a shared calendar
	Start      time.Time  `json:"start"`
	End        time.Time  `json:"end"`
	CalendarID CalendarID `json:"calendarId"`
	EventID    EventID    `json:"eventId"`
}

// DayBusy is a busy span of an invitation's mini day strip.
type DayBusy struct {
	Start      time.Time  `json:"start"`
	End        time.Time  `json:"end"`
	CalendarID CalendarID `json:"calendarId,omitempty"`
}

// InvitationDay is the mini day strip of an invitation card.
type InvitationDay struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	Busy []DayBusy `json:"busy"` // never null
}

// InvitationTarget is a calendar the invitation's event can go to.
type InvitationTarget struct {
	CalendarID CalendarID `json:"calendarId"`
	IsDefault  bool       `json:"isDefault,omitempty"`
}

// InvitationCard is the invitation card of a message (Message.Invitation),
// rendered by a client from these fields, never from HTML.
type InvitationCard struct {
	InvitationSummary
	PartID            string               `json:"partId"`
	UID               string               `json:"uid"`
	RecurrenceID      DateOrTime           `json:"recurrenceId,omitempty"`
	Sequence          int                  `json:"sequence"`
	EndDate           Date                 `json:"endDate,omitempty"`
	TimeZone          TimeZone             `json:"timeZone,omitempty"`
	Recurrence        *Recurrence          `json:"recurrence,omitempty"`
	Location          string               `json:"location,omitempty"`
	VideoCall         *VideoCall           `json:"videoCall,omitempty"`
	Notes             string               `json:"notes,omitempty"`             // plain text
	OrganizerMismatch bool                 `json:"organizerMismatch,omitempty"` // the organiser's domain is not the From domain
	AttendeeCount     int                  `json:"attendeeCount"`
	Attendees         []Attendee           `json:"attendees,omitempty"` // ≤ MaxInvitationAttendees
	Me                *InvitationMe        `json:"me,omitempty"`        // absent: the user is not on the list
	Response          EventResponse        `json:"response,omitempty"`  // the user's answer
	RespondedAt       *time.Time           `json:"respondedAt,omitempty"`
	EventID           EventID              `json:"eventId,omitempty"` // the stored event it is linked to
	CalendarID        CalendarID           `json:"calendarId,omitempty"`
	Previous          *InvitationPrevious  `json:"previous,omitempty"`
	Conflicts         []InvitationConflict `json:"conflicts"` // ≤ 3, never null
	Day               InvitationDay        `json:"day"`
	// Targets: "Add to:", the account's default first; empty → the answer
	// only. Never null.
	Targets []InvitationTarget `json:"targets"`
	// FixedTarget: the server chose the calendar (Graph).
	FixedTarget       bool           `json:"fixedTarget,omitempty"`
	CanRespond        bool           `json:"canRespond"`
	CanProposeNewTime bool           `json:"canProposeNewTime"`
	ReplyVia          InvitationsVia `json:"replyVia"`
	ReplyAccountID    AccountID      `json:"replyAccountId,omitempty"` // the account the answer leaves from
	ReplyTo           *Address       `json:"replyTo,omitempty"`        // the organiser it goes to
	// ReplyApplied (a reply): the attendee's answer was applied to the
	// user's event.
	ReplyApplied bool `json:"replyApplied,omitempty"`
}

// InvitationListParams: Limit 0 = 200, ≤ MaxInvitationList.
type InvitationListParams struct {
	AccountIDs []AccountID     `json:"accountIds,omitempty"`
	State      InvitationState `json:"state,omitempty"`
	Limit      int             `json:"limit,omitempty"`
}

// InvitationListItem is one invitation (the newest message of its UID).
type InvitationListItem struct {
	AccountID AccountID         `json:"accountId"`
	MessageID MessageID         `json:"messageId"`
	Summary   InvitationSummary `json:"summary"`
}

type InvitationListResult struct {
	Invitations []InvitationListItem `json:"invitations"`
}

// InvitationRespondParams: Response accepted, tentative or declined;
// CalendarID one of the card's Targets; SendResponse nil = true.
type InvitationRespondParams struct {
	AccountID     AccountID     `json:"accountId"`
	MessageID     MessageID     `json:"messageId"`
	Response      EventResponse `json:"response"`
	CalendarID    CalendarID    `json:"calendarId,omitempty"`
	SendResponse  *bool         `json:"sendResponse,omitempty"`
	Comment       string        `json:"comment,omitempty"`
	ProposedStart *time.Time    `json:"proposedStart,omitempty"`
	ProposedEnd   *time.Time    `json:"proposedEnd,omitempty"`
	Mail          *MailText     `json:"mail,omitempty"`
}

type InvitationRespondResult struct {
	Invitation InvitationCard `json:"invitation"`
	EventID    EventID        `json:"eventId,omitempty"`
	MovedTo    FolderID       `json:"movedTo,omitempty"` // the message was archived (afterAnswer)
}

type InvitationApplyCancelParams struct {
	AccountID AccountID `json:"accountId"`
	MessageID MessageID `json:"messageId"`
}

type InvitationApplyCancelResult struct {
	Removed bool `json:"removed"` // false: no stored event was linked
}

// ---------------------------------------------------------------------------
// Notifications (docs/api.md §5)
// ---------------------------------------------------------------------------

// CalendarChangedNotification: stored calendars or occurrences changed;
// From/To bound the changed occurrences when known. Without calendars: the
// preferences changed.
type CalendarChangedNotification struct {
	AccountIDs  []AccountID  `json:"accountIds,omitempty"`
	CalendarIDs []CalendarID `json:"calendarIds,omitempty"`
	From        *time.Time   `json:"from,omitempty"`
	To          *time.Time   `json:"to,omitempty"`
}

type CalendarStateNotification struct {
	State CalendarSyncState `json:"state"`
}

type EventAlertNotification struct {
	Alert EventAlert `json:"alert"`
}

// InvitationsChangedNotification: the unanswered invitations of these
// accounts (absent = any) changed.
type InvitationsChangedNotification struct {
	AccountIDs []AccountID `json:"accountIds,omitempty"`
}
