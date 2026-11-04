// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import "time"

// Mail rules (docs/api.md §4.21; the design is docs/plans/rules.md). A
// rule is conditions over a message's addresses, subject, text and
// classification, and actions on it. The daemon evaluates the enabled
// rules, in their order, on every message stored in the inbox of a mail
// account (imap, graph) once per Message-ID, never on mail older than
// the day before the rule was enabled; rules.apply runs them on stored
// mail by hand, with the local actions only. Nothing is read from or
// written to the server's own rules (Graph messageRules, Sieve).

// RuleMatch is how a rule's conditions combine.
type RuleMatch string

const (
	// RuleMatchAll: every condition holds.
	RuleMatchAll RuleMatch = "all"
	// RuleMatchAny: at least one condition holds.
	RuleMatchAny RuleMatch = "any"
)

// RuleField is what a condition looks at.
type RuleField string

const (
	// RuleFieldFrom: the From addresses (name and address).
	RuleFieldFrom RuleField = "from"
	// RuleFieldTo: the To addresses.
	RuleFieldTo RuleField = "to"
	// RuleFieldCc: the Cc addresses.
	RuleFieldCc RuleField = "cc"
	// RuleFieldToOrCc: the To and Cc addresses.
	RuleFieldToOrCc RuleField = "toOrCc"
	// RuleFieldSubject: the subject.
	RuleFieldSubject RuleField = "subject"
	// RuleFieldBody: the plain text of the message (the text part, else
	// the text of the HTML part), its first MaxRuleBodyBytes.
	RuleFieldBody RuleField = "body"
	// RuleFieldListID: the List-Id identifier as BulkInfo.ListID.
	RuleFieldListID RuleField = "listId"
	// RuleFieldBulk: the bulk classification; operators is and isNot with
	// a value of RuleBulkAny or a BulkKind.
	RuleFieldBulk RuleField = "bulk"
	// RuleFieldHasAttachments: operator is with the value "true" or
	// "false".
	RuleFieldHasAttachments RuleField = "hasAttachments"
)

// RuleBulkAny is the value of a bulk condition that holds for every kind.
const RuleBulkAny = "any"

// RuleOperator is how a condition compares. Text comparisons ignore case
// after NFC normalisation; an address field holds when any of its
// addresses does (notContains and isNot: when none does).
type RuleOperator string

const (
	RuleOpContains    RuleOperator = "contains"
	RuleOpNotContains RuleOperator = "notContains"
	RuleOpIs          RuleOperator = "is"
	RuleOpIsNot       RuleOperator = "isNot"
	RuleOpStartsWith  RuleOperator = "startsWith"
	RuleOpEndsWith    RuleOperator = "endsWith"
	// RuleOpDomainIs: the address's domain is the value or a subdomain of
	// it (address fields only).
	RuleOpDomainIs RuleOperator = "domainIs"
)

// RuleCondition is one condition of a rule.
type RuleCondition struct {
	Field RuleField    `json:"field"`
	Op    RuleOperator `json:"op"`
	Value string       `json:"value"`
}

// RuleActionKind is what an action does.
type RuleActionKind string

const (
	// RuleActionMove moves the message to FolderID (a selectable folder
	// of the rule's account, not the outbox or drafts); only a rule with
	// an AccountID has it.
	RuleActionMove RuleActionKind = "move"
	// RuleActionMarkRead marks the message read.
	RuleActionMarkRead RuleActionKind = "markRead"
	// RuleActionFlag stars the message.
	RuleActionFlag RuleActionKind = "flag"
	// RuleActionTrash moves the message to the account's trash.
	RuleActionTrash RuleActionKind = "trash"
	// RuleActionJunk moves the message to the account's junk folder.
	RuleActionJunk RuleActionKind = "junk"
	// RuleActionForward sends the message, attached whole, to To
	// (outgoing; docs/security.md).
	RuleActionForward RuleActionKind = "forward"
	// RuleActionAutoReply answers the sender with Text (outgoing; at
	// most once per sender in RuleReplyInterval).
	RuleActionAutoReply RuleActionKind = "autoReply"
)

// IsOutgoing reports whether the action sends mail.
func (k RuleActionKind) IsOutgoing() bool {
	return k == RuleActionForward || k == RuleActionAutoReply
}

// RuleAction is one action of a rule. Only the fields of its kind are
// set.
type RuleAction struct {
	Kind RuleActionKind `json:"kind"`
	// FolderID is the target of move.
	FolderID FolderID `json:"folderId,omitempty"`
	// To is the one address of forward.
	To string `json:"to,omitempty"`
	// Subject of autoReply; empty = "Re: " and the message's subject.
	Subject string `json:"subject,omitempty"`
	// Text is the plain text of autoReply.
	Text string `json:"text,omitempty"`
}

// RuleProblem is why the daemon stopped a rule (it is disabled then).
type RuleProblem string

const (
	// RuleProblemFolderGone: the folder of a move action is gone.
	RuleProblemFolderGone RuleProblem = "folderGone"
	// RuleProblemSendFailed: an outgoing action could not be queued.
	RuleProblemSendFailed RuleProblem = "sendFailed"
)

// RuleInput is a rule as the user edits it.
type RuleInput struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// AccountID limits the rule to one mail account; empty = every
	// enabled mail account (no move action then).
	AccountID  AccountID       `json:"accountId,omitempty"`
	Match      RuleMatch       `json:"match"`
	Conditions []RuleCondition `json:"conditions"`
	Actions    []RuleAction    `json:"actions"`
	// Stop: no later rule runs on a message this rule matched.
	Stop bool `json:"stop"`
}

// Rule is a stored rule.
type Rule struct {
	ID string `json:"id"`
	RuleInput
	// Outgoing: an action sends mail (forward, autoReply).
	Outgoing bool `json:"outgoing"`
	// Problem is set when the daemon disabled the rule.
	Problem   RuleProblem `json:"problem,omitempty"`
	CreatedAt time.Time   `json:"createdAt"`
	UpdatedAt time.Time   `json:"updatedAt"`
	// Applied counts the messages the rule acted on (automatically and
	// by rules.apply); LastAppliedAt is the latest.
	Applied       int        `json:"applied"`
	LastAppliedAt *time.Time `json:"lastAppliedAt,omitempty"`
	// Skipped counts outgoing actions the protections refused
	// (docs/security.md): bulk or automatic mail, own address, limits.
	Skipped int `json:"skipped"`
}

// Limits of rules.
const (
	MaxRules            = 200
	MaxRuleConditions   = 20
	MaxRuleActions      = 10
	MaxRuleNameBytes    = 200
	MaxRuleValueBytes   = 500
	MaxRuleReplyBytes   = 8192
	MaxRuleBodyBytes    = 64 * 1024
	MaxRuleTestMessages = 50
	// RuleOutgoingPerHour bounds what one rule sends in an hour.
	RuleOutgoingPerHour = 20
)

// RuleReplyInterval is how long autoReply leaves a sender alone after
// answering it (RFC 3834 §2).
const RuleReplyInterval = 4 * 24 * time.Hour

// RuleInvalidData is error.data of invalidArgument for a rule the daemon
// refuses: the field ("name", "accountId", "conditions", "actions",
// "confirmOutgoing") and, for conditions and actions, the index.
type RuleInvalidData struct {
	Field string `json:"field"`
	Index int    `json:"index,omitempty"`
}

// RulesListParams has no fields.
type RulesListParams struct{}

// RulesListResult is every rule in its order.
type RulesListResult struct {
	Rules []Rule `json:"rules"`
}

// RulesCreateParams adds a rule at the end. ConfirmOutgoing is required
// when the rule is enabled with an outgoing action: the client asked the
// user.
type RulesCreateParams struct {
	Rule            RuleInput `json:"rule"`
	ConfirmOutgoing bool      `json:"confirmOutgoing,omitempty"`
}

// RulesCreateResult is the new rule.
type RulesCreateResult struct {
	Rule Rule `json:"rule"`
}

// RulesUpdateParams replaces a rule's input. ConfirmOutgoing is required
// when the updated rule is enabled with an outgoing action and was not
// before, or an outgoing action's To, Subject or Text changed.
type RulesUpdateParams struct {
	ID              string    `json:"id"`
	Rule            RuleInput `json:"rule"`
	ConfirmOutgoing bool      `json:"confirmOutgoing,omitempty"`
}

// RulesUpdateResult is the rule after the update.
type RulesUpdateResult struct {
	Rule Rule `json:"rule"`
}

// RulesDeleteParams names the rule to delete.
type RulesDeleteParams struct {
	ID string `json:"id"`
}

// RulesDeleteResult has no fields.
type RulesDeleteResult struct{}

// RulesReorderParams is the whole new order: every rule's id once.
type RulesReorderParams struct {
	IDs []string `json:"ids"`
}

// RulesReorderResult is every rule in the new order.
type RulesReorderResult struct {
	Rules []Rule `json:"rules"`
}

// RulesTestParams asks which stored messages of a folder a rule, as
// edited and not saved, matches.
type RulesTestParams struct {
	Rule      RuleInput `json:"rule"`
	AccountID AccountID `json:"accountId"`
	FolderID  FolderID  `json:"folderId"`
}

// RulesTestResult is how many stored messages of the folder match and
// the newest MaxRuleTestMessages of them.
type RulesTestResult struct {
	Count    int              `json:"count"`
	Messages []MessageSummary `json:"messages"`
}

// RulesApplyParams runs rules by hand on stored mail: a folder's
// messages (FolderID) or MessageIDs of one account, never both. RuleIDs
// picks the rules; empty = every enabled rule of the account. Only the
// local actions run (move, markRead, flag, trash, junk).
type RulesApplyParams struct {
	AccountID  AccountID   `json:"accountId"`
	FolderID   FolderID    `json:"folderId,omitempty"`
	MessageIDs []MessageID `json:"messageIds,omitempty"`
	RuleIDs    []string    `json:"ruleIds,omitempty"`
}

// RulesApplyResult counts what rules.apply did.
type RulesApplyResult struct {
	// Examined: messages looked at; Matched: messages a rule matched;
	// Changed: messages an action changed.
	Examined int `json:"examined"`
	Matched  int `json:"matched"`
	Changed  int `json:"changed"`
	// SkippedOutgoing counts the outgoing actions left out.
	SkippedOutgoing int `json:"skippedOutgoing"`
}

// RulesChangedNotification is notify.rulesChanged: a rule was added,
// changed, reordered, deleted or disabled by the daemon, or its counts
// moved. The client calls rules.list again.
type RulesChangedNotification struct{}
