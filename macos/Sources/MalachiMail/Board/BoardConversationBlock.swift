// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// What the board's detail needs of Mail to show a message's HTML: the
/// loaded-message cache (message.body, and the `malachi-cid:` parts of the
/// web view), the text-zoom setting, and the registration with the message
/// windows' fan-out (`MessageWindows.track(display:)`), which tells a
/// display what the cache learns and gives it Mail's message actions (the
/// link policy). Set by the application (Integration+Board) for the
/// daemon's board and the samples alike; the samples' members have no ids,
/// so nothing is ever asked for them.
@MainActor
struct BoardMailBodies {
    let cache: MessageCache
    let settings: Settings
    let track: @MainActor (any MessageDisplay) -> Void
}

/// The conversation of the board's detail (`lower`): the heading with Show
/// in Mail, then the cards (`BoardMessageCardView`), or while the
/// conversation loads a spinner row instead, and the note with Try Again
/// when it could not be loaded.
///
/// It lives as long as the detail and is never rebuilt with it: a refresh
/// of the board (every autosave of the inline reply lists the board again)
/// changes the heading and the note in place and touches the cards only
/// when the case or its members changed (`Board.ConversationCards.apply`,
/// keyed by the case and each member's id and excerpt). Another case
/// starts over and lets every web view go; a new message adds its card
/// and keeps the others, with their web views, as they are.
///
/// Which card shows what is `Board.ConversationCards`'s: the newest open,
/// older ones folded to a preview; an open card of a message with an id
/// asks for its body through the cache (`fetchBody`, the variant the cache
/// entry shows, trimmed of its quoted history unless Mail revealed it) and
/// shows HTML in the web view of Mail's conversation cards while the block
/// is live (in a window and not hidden), at most `maxLiveWebViews` of them;
/// anything else keeps the excerpt. The block is a `MessageDisplay`: a body
/// that changes (remote images loaded in Mail) shows here too, and links go
/// through Mail's `openLink` of the delegate it is given.
///
/// The heading row also has the layout toggle of Mail's conversation
/// (`ConversationLayoutToggle`, left of Show in Mail), on the Mail key
/// conversation-chat-layout: a click only writes the setting, and the
/// block switches when the setting changes (`layoutChanged`), so a change
/// in Mail switches the board too. The chat (`chatColumn`) is one bubble
/// per member (`Board.chatRows`), oldest first like the cards, the user's
/// own on the right, with the text the case carries (`MessageCard.text`):
/// no body is fetched for it and no web view shown, and while it shows the
/// cards hold none either (`live`), keeping their folds and HTML for the
/// way back. A double click on a bubble opens the message in its own
/// window (`openFromBubble`). The detail keeps opening at its top.
///
/// With Mail's preference Render HTML in Chat (`conversation-chat-html`,
/// observed) the bubbles near the visible part of the detail's scroll view
/// ask message.body through the cache as the open cards do (the same stub
/// summary, the cache entry's variant) and one whose body renders HTML
/// (`Conversation.bubbleBody(_:snippet:html:)`) shows it in the bubble's
/// transparent web view as Mail's chat does: `Board.ChatBodies` keeps who
/// asked, `Board.ChatBodies.live` (Mail's rule, the board's cap of 4) who
/// holds a web view; the others, a hidden chat, the preference off and a
/// block out of the window read the board.get text. Heights change through
/// `onHeightChanging`, links go to the delegate's `openLink`, the link
/// under the pointer to `onHover`. With Shorten My / Others'
/// Messages in Chat (`conversation-chat-truncate-mine`, `-others`,
/// observed) every bubble of that side shows about three
/// lines until a click expands it (`expanded`, forgotten with the case).
@MainActor
final class BoardConversationBlock: NSView, MessageDisplay {
    weak var delegate: (any MessageActionDelegate)?
    var onOpenEmbedded: (@MainActor (_ containing: MessageSummary, _ attachment: Attachment, _ remote: Bool, _ chip: NSView?) -> Void)?

    /// Show in Mail and the conversation's Try Again.
    var onShowInMail: (() -> Void)?
    var onRetry: (() -> Void)?
    /// The link under the pointer in a card ("" when none).
    var onHover: ((String) -> Void)?
    /// Runs a change of a card's height that the user did not make, for
    /// the detail to keep what the user sees in place.
    var onHeightChanging: ((_ card: NSView, _ change: () -> Void) -> Void)?

    private let actions: BoardActions
    private let stack = FillStackView()
    private let heading = NSTextField(labelWithString: "")
    private let showButton = NSButton()
    /// The spinner row or the note, between the heading and the cards.
    private var statusView: NSView?
    private var status: Status?
    private let cardsColumn = FillStackView()
    /// Same indices as `state.members`.
    private(set) var cards: [BoardMessageCardView] = []
    private(set) var state = Board.ConversationCards()
    private var account: AccountID?
    private var registered = false
    private var zoomToken: Settings.ChangeToken?

    /// The layout toggle, in the heading row left of Show in Mail.
    private let toggle = ConversationLayoutToggle(
        label: { Board.Text.chatToggleLabel(on: $0) }, tooltip: { Board.Text.chatToggleTooltip(on: $0) })
    /// The chat layout's bubbles, after `cardsColumn`; exactly one of the
    /// two shows (none while the conversation loads or failed).
    private let chatColumn = FillStackView()
    /// Same indices as `state.members` and `messages`.
    private(set) var chatRows: [ConversationChatRow] = []
    /// The members of the detail on show, for the bubbles' double click.
    private var messages: [Board.MessageCard] = []
    /// The chat shows (the setting, read when it changes).
    private(set) var chatShown = false
    /// The bubbles show the short date (`ConversationLayout.chatCompactDates`).
    private var compactDates = false
    private var compactPending = false
    /// The layout, text zoom and avatar settings the chat follows.
    private var chatTokens: [Settings.ChangeToken] = []
    /// Which bubbles asked message.body for Render HTML in Chat.
    private(set) var chatBodies = Board.ChatBodies()
    /// The web views' hosts of the bubbles (made with Mail's cache), one
    /// per row, so that a height change names its row to the detail.
    private var bubbleHosts: [ObjectIdentifier: BoardBubbleWebHost] = [:]
    /// The clip view whose scrolling the HTML bubbles follow.
    private weak var observedClip: NSClipView?
    private var chatLivePending = false
    /// The bubbles the user expanded in the case shown; a sample (no id)
    /// by its index.
    private var expanded = Conversation.Expanded()
    private var expandedSamples = Set<Int>()

    /// What the status row shows.
    private struct Status: Equatable {
        var loading: Bool
        var note: String
        var retry: Bool
    }

    init(actions: BoardActions) {
        self.actions = actions
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        heading.font = .systemFont(ofSize: Typo.bodySize, weight: .bold)
        heading.lineBreakMode = .byTruncatingTail
        heading.isSelectable = false
        heading.setAccessibilityRole(.staticText)
        heading.setContentHuggingPriority(.defaultLow, for: .horizontal)
        heading.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        showButton.title = Board.Text.showInMail
        showButton.bezelStyle = .rounded
        showButton.controlSize = .small
        showButton.font = .systemFont(ofSize: NSFont.systemFontSize(for: .small))
        showButton.target = self
        showButton.action = #selector(showInMailClicked(_:))
        showButton.setContentHuggingPriority(.required, for: .horizontal)
        showButton.setContentCompressionResistancePriority(NSLayoutConstraint.Priority(450), for: .horizontal)
        showButton.setAccessibilityLabel(Board.Text.showInMail)
        toggle.onToggle = { [weak self] on in
            guard let self else { return }
            // Only the setting: the block switches from its observer.
            guard let env = self.actions.mailBodies else {
                self.toggle.setChat(self.chatShown)
                return
            }
            env.settings.conversationChatLayout = on
        }
        // Without Mail's settings there is no layout to switch.
        toggle.isHidden = actions.mailBodies == nil
        let top = NSStackView(views: [heading, toggle, showButton])
        top.orientation = .horizontal
        top.alignment = .centerY
        top.distribution = .fill
        top.spacing = 8
        cardsColumn.spacing = 8
        stack.spacing = 8
        stack.addArrangedSubview(top)
        stack.addArrangedSubview(cardsColumn)
        chatColumn.spacing = 0
        chatColumn.isHidden = true
        stack.addArrangedSubview(chatColumn)
        addSubview(stack)
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: topAnchor),
            stack.bottomAnchor.constraint(equalTo: bottomAnchor),
            stack.leadingAnchor.constraint(equalTo: leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: trailingAnchor),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: Applying the detail

    /// Brings the block up to `d` (nil: no case; every card goes).
    func apply(_ d: Board.Detail?) {
        guard let d else {
            removeAllCards()
            removeAllChatRows()
            state = Board.ConversationCards()
            chatBodies = Board.ChatBodies()
            resetExpanded()
            account = nil
            messages = []
            return
        }
        account = d.accountID
        if let env = actions.mailBodies {
            observeChat(env)
            // The delegate a bubble's double click opens through.
            register(env)
        }
        if heading.stringValue != d.conversationTitle {
            heading.stringValue = d.conversationTitle
        }
        let canShow = actions.canShowInMail(d.id)
        if showButton.isEnabled != canShow {
            showButton.isEnabled = canShow
        }
        let s = Status(loading: d.messagesLoading, note: d.messagesNote, retry: d.messagesRetry)
        applyStatus(s.loading || !s.note.isEmpty ? s : nil)
        if s.loading || !s.note.isEmpty {
            // The cards of the same case wait hidden for the conversation;
            // another case's go.
            if state.caseID != d.id {
                removeAllCards()
                removeAllChatRows()
                state = Board.ConversationCards()
                chatBodies = Board.ChatBodies()
                resetExpanded()
                messages = []
            }
            cardsColumn.isHidden = true
            chatColumn.isHidden = true
            return
        }
        cardsColumn.isHidden = chatShown
        chatColumn.isHidden = !chatShown
        var reloads: [Int] = []
        let change = state.apply(Board.ConversationCards.key(d))
        if change == .reset {
            resetExpanded()
        }
        messages = d.messages
        chatBodies.apply(state.members)
        applyChat(change, d.messages)
        scheduleChatLive()
        switch change {
        case .none:
            break
        case .reset:
            removeAllCards()
            cards = d.messages.map { makeCard($0) }
            for card in cards {
                cardsColumn.addArrangedSubview(card)
            }
        case .members(let kept, let reload):
            var next: [BoardMessageCardView] = []
            var stays = Set<ObjectIdentifier>()
            for (i, m) in d.messages.enumerated() {
                if let old = kept[i] {
                    // The same card, whatever the excerpt now says.
                    cards[old].update(m)
                    next.append(cards[old])
                    stays.insert(ObjectIdentifier(cards[old]))
                } else {
                    next.append(makeCard(m))
                }
            }
            var hadRemoved = false
            for card in cards where !stays.contains(ObjectIdentifier(card)) {
                hadRemoved = true
                card.onHeightChanging = nil
                card.apply(.text, folded: false, web: nil) // lets its web view go
                cardsColumn.removeArrangedSubview(card)
                card.removeFromSuperview()
            }
            // In place: a card that stays is never taken out of the
            // window, so its web view keeps its document.
            for (i, card) in next.enumerated() {
                let arranged = cardsColumn.arrangedSubviews
                if i < arranged.count, arranged[i] === card {
                    continue
                }
                if arranged.contains(where: { $0 === card }) {
                    cardsColumn.removeArrangedSubview(card)
                }
                cardsColumn.insertArrangedSubview(card, at: i)
            }
            cards = next
            if hadRemoved {
                onHover?("")
            }
            reloads = reload
        }
        refreshCards()
        fetchBodies()
        for i in reloads {
            fetchBody(i)
        }
    }

    private func applyStatus(_ s: Status?) {
        guard s != status else { return }
        status = s
        if let v = statusView {
            stack.removeArrangedSubview(v)
            v.removeFromSuperview()
            statusView = nil
        }
        guard let s else { return }
        let v: NSView
        if s.loading {
            let spinner = Spinner(size: 16)
            spinner.start()
            let note = NSTextField(labelWithString: s.note)
            note.font = Typo.caption
            note.textColor = Tint.secondary
            note.isSelectable = false
            let row = NSStackView(views: [spinner, note])
            row.orientation = .horizontal
            row.alignment = .centerY
            row.spacing = 6
            v = row
        } else {
            let note = PrefsWrappingLabel.board(s.note, font: Typo.caption, color: Tint.secondary)
            if s.retry {
                let retry = NSButton(title: Board.Text.tryAgain, target: self, action: #selector(retryClicked(_:)))
                retry.isBordered = false
                retry.bezelStyle = .inline
                retry.setButtonType(.momentaryChange)
                retry.font = Typo.caption
                retry.contentTintColor = .linkColor
                retry.setAccessibilityLabel(Board.Text.tryAgain)
                // The note as wide as the column, the link at its own width
                // under it (as the deadline's quote under its chip).
                let column = NSStackView(views: [note, retry])
                column.orientation = .vertical
                column.alignment = .leading
                column.spacing = 2
                column.translatesAutoresizingMaskIntoConstraints = false
                note.widthAnchor.constraint(equalTo: column.widthAnchor).isActive = true
                v = column
            } else {
                v = note
            }
        }
        stack.insertArrangedSubview(v, at: 1)
        statusView = v
    }

    private func makeCard(_ m: Board.MessageCard) -> BoardMessageCardView {
        let card = BoardMessageCardView(m)
        card.onFold = { [weak self, weak card] folded in
            guard let self, let card else { return }
            self.userFolded(card, folded)
        }
        card.onUnavailable = { [weak self, weak card] in
            guard let self, let card, let i = self.cards.firstIndex(where: { $0 === card }) else { return }
            _ = self.state.answered(i, html: false)
            self.refreshCards()
        }
        card.onHeightChanging = { [weak self] card, change in
            if let self, let host = self.onHeightChanging {
                host(card, change)
            } else {
                change()
            }
        }
        return card
    }

    private func removeAllCards() {
        for card in cards {
            card.onHeightChanging = nil
            card.apply(.text, folded: false, web: nil) // lets its web view go
            cardsColumn.removeArrangedSubview(card)
            card.removeFromSuperview()
        }
        cards = []
        onHover?("")
    }

    // MARK: What the cards show

    /// The block may hold web views now: in a window, not hidden, with
    /// Mail's cache at hand, and the cards shown (the chat holds none).
    private var live: Bool {
        actions.mailBodies != nil && window != nil && !isHiddenOrHasHiddenAncestor && !chatShown
    }

    private var web: BoardCardWeb? {
        guard let env = actions.mailBodies else { return nil }
        let settings = env.settings
        return BoardCardWeb(
            cache: env.cache, zoom: { settings.textZoom },
            openLink: { [weak self] link, links, window in
                self?.delegate?.openLink(link.href, links: links, from: window)
            },
            hover: { [weak self] href in self?.onHover?(href) })
    }

    /// Applies the state to every card: its arrow, its fold, the text or
    /// the web view.
    private func refreshCards() {
        let live = live
        let canFetch = actions.mailBodies != nil
        let web = web
        if web != nil, zoomToken == nil, let env = actions.mailBodies {
            zoomToken = env.settings.onChange(.textZoom) { [weak self] in
                guard let self, let env = self.actions.mailBodies else { return }
                for card in self.cards {
                    card.setZoom(env.settings.textZoom)
                }
            }
        }
        for (i, card) in cards.enumerated() {
            card.configure(foldable: state.foldable(i), arrowAlways: state.arrow(i, long: false, canFetch: canFetch))
            card.apply(state.shows(i, live: live), folded: state.isFolded(i), web: web)
        }
    }

    /// Asks for the bodies the open cards need, while the block is live.
    private func fetchBodies() {
        guard live, let env = actions.mailBodies, account != nil else { return }
        register(env)
        for i in state.members.indices where state.needsBody(i) {
            state.asked(i)
            fetchBody(i)
        }
    }

    /// message.body for card `i` through the cache; the answer shows in
    /// place (`loaded`). Also the reload of an open card whose message the
    /// daemon rebuilt (the cache forgot it then, so this asks again).
    private func fetchBody(_ i: Int) {
        guard live, let env = actions.mailBodies, let account, state.members.indices.contains(i),
              let id = state.members[i].id
        else { return }
        register(env)
        let member = state.members[i]
        env.cache.fetchBody(Self.summary(id, account: account)) { [weak self] lm in
            self?.loaded(member, lm)
        }
    }

    /// The summary message.body needs: the ids. Nothing else of it is read
    /// (`MessageCache.fetchBody`).
    private static func summary(_ id: MessageID, account: AccountID) -> MessageSummary {
        MessageSummary(
            id: id, accountId: account, folderId: FolderID(""), from: [], subject: "", date: .goZero, snippet: "",
            flags: [], hasAttachments: false, size: 0)
    }

    private func register(_ env: BoardMailBodies) {
        guard !registered else { return }
        registered = true
        env.track(self)
    }

    /// What the cache holds for `member` now: HTML for the web view, or
    /// anything else for the excerpt.
    private func loaded(_ member: Board.ConversationCards.Member, _ lm: LoadedMessage) {
        guard lm.bodySettled, !lm.fetching, let i = state.members.firstIndex(of: member), i < cards.count else { return }
        let html: String? = lm.err == nil && showsHTML(lm.body) ? lm.body?.html : nil
        bodyArrived(i, html: html, links: lm.body?.links ?? [])
    }

    /// The body of card `i` is `html` (nil: none to show, the excerpt
    /// stays). The one way a card gets HTML: message.body's answer, and the
    /// development hook's invented document.
    private func bodyArrived(_ i: Int, html: String?, links: [Link]) {
        let card = cards[i]
        card.setHTML(html, links: links)
        _ = state.answered(i, html: html != nil)
        refreshCards()
    }

    /// The card's arrow: the state decides, the limit may fold another.
    private func userFolded(_ card: BoardMessageCardView, _ folded: Bool) {
        guard let i = cards.firstIndex(where: { $0 === card }) else { return }
        _ = state.setFolded(i, folded)
        refreshCards()
        fetchBodies()
    }

    // MARK: The chat

    /// Follows the layout setting (and the chat's fonts and avatars) from
    /// the first detail on.
    private func observeChat(_ env: BoardMailBodies) {
        guard chatTokens.isEmpty else { return }
        let settings = env.settings
        chatShown = settings.conversationChatLayout
        toggle.setChat(chatShown)
        chatTokens.append(settings.onChange(.conversationChatLayout) { [weak self] in self?.layoutChanged() })
        chatTokens.append(settings.onChange(.textZoom) { [weak self] in self?.applyChatFonts() })
        chatTokens.append(settings.onChange(.monochromeAvatars) { [weak self] in self?.applyChatAvatars() })
        chatTokens.append(settings.onChange(.conversationChatHTML) { [weak self] in self?.chatHTMLChanged() })
        chatTokens.append(settings.onChange(.conversationChatTruncateMine) { [weak self] in self?.chatShorteningChanged() })
        chatTokens.append(settings.onChange(.conversationChatTruncateOthers) { [weak self] in self?.chatShorteningChanged() })
    }

    /// The setting switched the layout (a click here, or in Mail): the
    /// other column shows; the cards let their web views go or get them
    /// back (`live`), on the next turn as any change of `live`.
    private func layoutChanged() {
        guard let env = actions.mailBodies else { return }
        let on = env.settings.conversationChatLayout
        toggle.setChat(on)
        guard on != chatShown else { return }
        chatShown = on
        if status == nil, account != nil {
            cardsColumn.isHidden = on
            chatColumn.isHidden = !on
        }
        if on {
            // The rows were left as they were while the cards showed.
            for (i, row) in chatRows.enumerated() where messages.indices.contains(i) {
                renderChatRow(row, messages[i], at: i)
            }
            scheduleCompactCheck()
        }
        liveChanged()
    }

    /// The bubbles' fonts at the text zoom; the board's text is an
    /// excerpt, never shown monospaced (as Mail's snippet).
    private var chatFonts: (normal: NSFont, monospace: NSFont) {
        let zoom = actions.mailBodies?.settings.textZoom ?? 1
        return (Typo.body(zoom: zoom, monospace: false), Typo.body(zoom: zoom, monospace: true))
    }

    private var monochrome: Bool { actions.mailBodies?.settings.monochromeAvatars ?? false }

    /// The chat's rows after the same change as the cards: another case
    /// builds them anew, new members add theirs and the others stay (a row
    /// whose side changed is made anew: the side is its layout).
    private func applyChat(_ change: Board.ConversationCards.Change, _ ms: [Board.MessageCard]) {
        switch change {
        case .none:
            for (i, m) in ms.enumerated() where i < chatRows.count {
                if chatRows[i].right == m.mine {
                    if chatShown {
                        renderChatRow(chatRows[i], m, at: i)
                    }
                } else {
                    replaceChatRow(i, with: makeChatRow(m))
                }
            }
        case .reset:
            removeAllChatRows()
            chatRows = ms.map { makeChatRow($0) }
            for row in chatRows {
                chatColumn.addArrangedSubview(row)
            }
        case .members(let kept, _):
            var next: [ConversationChatRow] = []
            var stays = Set<ObjectIdentifier>()
            for (i, m) in ms.enumerated() {
                if let old = kept[i], old < chatRows.count, chatRows[old].right == m.mine {
                    if chatShown {
                        renderChatRow(chatRows[old], m, at: i)
                    }
                    next.append(chatRows[old])
                    stays.insert(ObjectIdentifier(chatRows[old]))
                } else {
                    next.append(makeChatRow(m))
                }
            }
            for row in chatRows where !stays.contains(ObjectIdentifier(row)) {
                row.bubble.setLive(false)
                bubbleHosts[ObjectIdentifier(row)] = nil
                chatColumn.removeArrangedSubview(row)
                row.removeFromSuperview()
            }
            for (i, row) in next.enumerated() {
                let arranged = chatColumn.arrangedSubviews
                if i < arranged.count, arranged[i] === row {
                    continue
                }
                if arranged.contains(where: { $0 === row }) {
                    chatColumn.removeArrangedSubview(row)
                }
                chatColumn.insertArrangedSubview(row, at: i)
            }
            chatRows = next
        }
    }

    private func replaceChatRow(_ i: Int, with row: ConversationChatRow) {
        let old = chatRows[i]
        old.bubble.setLive(false)
        bubbleHosts[ObjectIdentifier(old)] = nil
        chatColumn.removeArrangedSubview(old)
        old.removeFromSuperview()
        chatColumn.insertArrangedSubview(row, at: i)
        chatRows[i] = row
    }

    private func makeChatRow(_ m: Board.MessageCard) -> ConversationChatRow {
        let row = ConversationChatRow(id: m.id, draftID: nil, right: m.mine, fonts: chatFonts)
        row.bubble.compactDate = compactDates
        if let env = actions.mailBodies {
            let host = BoardBubbleWebHost(env: env, block: self, row: row)
            bubbleHosts[ObjectIdentifier(row)] = host
            row.bubble.webHost = host
        }
        row.bubble.onExpand = { [weak self, weak row] in
            guard let self, let row, let i = self.chatRows.firstIndex(where: { $0 === row }) else { return }
            self.expandBubble(i)
        }
        row.bubble.onDoubleClick = { [weak self, weak row] in
            guard let self, let row, let i = self.chatRows.firstIndex(where: { $0 === row }),
                  self.messages.indices.contains(i)
            else { return }
            self.openFromBubble(self.messages[i])
        }
        renderChatRow(row, m, at: nil)
        return row
    }

    /// A bubble shows `m` (member `i`, nil: a new row, nothing asked yet):
    /// the avatar of its real sender, the header of its card's sender (You
    /// for the user's own) and date, its text, or with Render HTML in Chat
    /// the HTML its body has (`chatBody`).
    private func renderChatRow(_ row: ConversationChatRow, _ m: Board.MessageCard, at i: Int?) {
        row.showAvatar(name: m.sender, monochrome: monochrome)
        row.bubble.showPlain(sender: m.from, date: m.date == .distantPast ? nil : m.date)
        row.bubble.setShortened(shortened(m, at: i))
        row.bubble.showBody(chatBody(m, at: i), monospace: false)
    }

    /// Whether the bubble of `m` (member `i`) shows the shortened preview
    /// (`Conversation.bubbleShortened`).
    private func shortened(_ m: Board.MessageCard, at i: Int?) -> Bool {
        let was = m.id.map { expanded.is($0) } ?? i.map { expandedSamples.contains($0) } ?? false
        let settings = actions.mailBodies?.settings
        return Conversation.bubbleShortened(
            mineOn: settings?.conversationChatTruncateMine ?? false,
            othersOn: settings?.conversationChatTruncateOthers ?? false, mine: m.mine, expanded: was)
    }

    private func resetExpanded() {
        expanded.reset()
        expandedSamples = []
    }

    /// A click on the shortened bubble of member `i`: the whole message
    /// while the case shows.
    private func expandBubble(_ i: Int) {
        guard messages.indices.contains(i), i < chatRows.count else { return }
        let m = messages[i]
        if let id = m.id {
            expanded.expand(id)
        } else {
            expandedSamples.insert(i)
        }
        let row = chatRows[i]
        change(row) { row.bubble.setShortened(shortened(m, at: i)) }
        scheduleChatLive()
    }

    /// Shorten My / Others' Messages in Chat switched: every bubble anew.
    private func chatShorteningChanged() {
        for (i, row) in chatRows.enumerated() where messages.indices.contains(i) {
            let m = messages[i]
            change(row) { row.bubble.setShortened(self.shortened(m, at: i)) }
        }
        scheduleChatLive()
    }

    /// What the bubble of member `i` shows: the board.get text, unless the
    /// preference is on, the bubble asked and the cache's entry renders
    /// HTML (`Conversation.bubbleBody`); the text stays its fallback (no
    /// placeholder: it is the trimmed text already).
    private func chatBody(_ m: Board.MessageCard, at i: Int?) -> Conversation.BubbleBody {
        let text = Conversation.bubbleText(m.text)
        guard htmlOn, let i, chatBodies.wasAsked(i), let id = m.id, let lm = actions.mailBodies?.cache.loaded(id),
              case .html(let b, _) = Conversation.bubbleBody(lm, snippet: m.text, html: true)
        else { return .text(text) }
        return .html(body: b, text: text)
    }

    /// Render HTML in Chat.
    private var htmlOn: Bool { actions.mailBodies?.settings.conversationChatHTML ?? false }

    private func removeAllChatRows() {
        bubbleHosts = [:]
        for row in chatRows {
            row.bubble.setLive(false)
            chatColumn.removeArrangedSubview(row)
            row.removeFromSuperview()
        }
        chatRows = []
    }

    private func applyChatFonts() {
        let f = chatFonts
        let zoom = actions.mailBodies?.settings.textZoom
        for row in chatRows {
            row.bubble.setFonts(normal: f.normal, monospace: f.monospace, zoom: zoom)
        }
    }

    private func applyChatAvatars() {
        for (i, row) in chatRows.enumerated() where messages.indices.contains(i) {
            let m = messages[i]
            row.showAvatar(name: m.sender, monochrome: monochrome)
        }
    }

    /// A double click on the bubble of `m` opens the message in its own
    /// window as Mail's chat does, with the summary message.get gives (the
    /// samples have none: the preview says it cannot).
    private func openFromBubble(_ m: Board.MessageCard) {
        guard Board.chatOpens(m), let id = m.id, let account, let env = actions.mailBodies else {
            if actions.samples {
                actions.onToast?(Board.Text.later)
            }
            return
        }
        register(env)
        let caseID = state.caseID
        env.cache.lookUpOutcome(accountId: account, id: id) { [weak self] outcome in
            // A late answer after the user went elsewhere (Mail, another
            // case, the block out of the window) does nothing.
            guard let self, self.window != nil, !self.isHiddenOrHasHiddenAncestor, self.account == account,
                  self.state.caseID == caseID, self.chatShown
            else { return }
            switch outcome {
            case .found(let s):
                self.delegate?.openMessage(s)
            case .gone:
                self.actions.onToast?(Board.Text.showInMailGone)
            case .failed:
                self.actions.onToast?(Board.Text.showInMailFailed)
            }
        }
    }

    override func layout() {
        super.layout()
        scheduleCompactCheck()
    }

    /// Whether the bubbles show the short date, from the block's own width
    /// (the stack, and with it the chat's column, is pinned edge to edge;
    /// the column itself is 0 while hidden and sized after this pass), on
    /// the next turn: never from inside the layout pass, as a date's label
    /// changes width.
    private func scheduleCompactCheck() {
        guard !compactPending else { return }
        compactPending = true
        Task { @MainActor [weak self] in
            guard let self else { return }
            self.compactPending = false
            let width = self.bounds.width
            guard width > 0 else { return }
            let compact = ConversationLayout.chatCompactDates(column: Double(width))
            guard compact != self.compactDates else { return }
            self.compactDates = compact
            for row in self.chatRows {
                row.bubble.compactDate = compact
            }
        }
    }

    // MARK: Live

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        liveChanged()
    }

    override func viewDidHide() {
        super.viewDidHide()
        liveChanged()
    }

    override func viewDidUnhide() {
        super.viewDidUnhide()
        liveChanged()
    }

    /// Whether the block is in a window and shown changes while AppKit
    /// lays the window out (a panel hiding, a style switch), and
    /// `refreshCards` can change a card's height, which the detail measures
    /// with `layoutSubtreeIfNeeded`: never from inside a layout pass, so the
    /// refresh waits for the next turn of the main queue (what `live` says
    /// then is what counts).
    private func liveChanged() {
        guard !liveChangePending else { return }
        liveChangePending = true
        Task { @MainActor [weak self] in
            guard let self else { return }
            self.liveChangePending = false
            self.refreshCards()
            self.fetchBodies()
            self.updateChatLive()
        }
    }

    private var liveChangePending = false

    // MARK: MessageDisplay

    func displays(_ id: MessageID) -> Bool {
        state.index(of: id) != nil
    }

    func showLoaded(_ id: MessageID, _ lm: LoadedMessage) {
        if let i = chatBodies.members.firstIndex(where: { $0.id == id }), chatBodies.wasAsked(i) {
            chatLoaded(chatBodies.members[i], lm)
        }
        guard let i = state.index(of: id), state.body(i) != .unknown else { return }
        loaded(state.members[i], lm)
    }

    func refreshRemoteBar(_ id: MessageID, _ lm: LoadedMessage) {}

    func refreshBulk(_ id: MessageID, _ lm: LoadedMessage) {}

    func refreshChips(_ id: MessageID, _ lm: LoadedMessage?) {}

    func showOutboxState(_ id: MessageID, _ m: Message?) {}

    // MARK: Actions

    @objc private func showInMailClicked(_ sender: Any?) {
        onShowInMail?()
    }

    @objc private func retryClicked(_ sender: Any?) {
        onRetry?()
    }

    // MARK: Development aid

    /// DEVELOPMENT AID (`MALACHI_START`, samples only): card `i` gets
    /// `html` as message.body's HTML answer would give it (`bodyArrived`).
    func developmentFeed(_ i: Int, html: String) {
        guard actions.samples, cards.indices.contains(i) else { return }
        bodyArrived(i, html: html, links: [])
    }

    /// DEVELOPMENT AID (samples only): card `i` opened as by its arrow.
    func developmentOpen(_ i: Int) {
        guard actions.samples, cards.indices.contains(i) else { return }
        userFolded(cards[i], false)
    }

    /// DEVELOPMENT AID: the layout toggle's label.
    var developmentToggleLabel: String { toggle.title }

    /// DEVELOPMENT AID: the members on show (the bubbles' texts and names).
    var developmentMessages: [Board.MessageCard] { messages }

    /// DEVELOPMENT AID: the cards and the chat's bubbles holding a web
    /// view now.
    var developmentLiveWebViews: Int {
        cards.filter { $0.webView != nil }.count + chatRows.filter(\.bubble.holdsWebView).count
    }

    // MARK: HTML in the chat

    /// The chat may hold web views and ask bodies now: Mail's cache at
    /// hand, in a window, shown, the chat shown with its members.
    private var chatLive: Bool {
        actions.mailBodies != nil && window != nil && !isHiddenOrHasHiddenAncestor && chatShown && status == nil
            && account != nil
    }

    /// Render HTML in Chat switched: off, every web view goes, nothing is
    /// asked any more and the bubbles read the board.get text; on, the near
    /// bubbles ask (`updateChatLive`).
    private func chatHTMLChanged() {
        if !htmlOn {
            chatBodies.forget()
        }
        for (i, row) in chatRows.enumerated() where messages.indices.contains(i) {
            let m = messages[i]
            change(row) {
                if !self.htmlOn {
                    row.bubble.setLive(false)
                }
                self.renderChatRow(row, m, at: i)
            }
        }
        scheduleChatLive()
    }

    /// Runs `change` of row's height through the detail.
    private func change(_ row: NSView, _ change: () -> Void) {
        if let host = onHeightChanging {
            host(row, change)
        } else {
            change()
        }
    }

    private func scheduleChatLive() {
        guard !chatLivePending else { return }
        chatLivePending = true
        Task { @MainActor [weak self] in
            guard let self else { return }
            self.chatLivePending = false
            self.updateChatLive()
        }
    }

    /// Follows the detail's scrolling while HTML bubbles may show.
    private func observeClip(_ on: Bool) {
        let clip = on ? enclosingScrollView?.contentView : nil
        guard clip !== observedClip else { return }
        if let old = observedClip {
            NotificationCenter.default.removeObserver(self, name: NSView.boundsDidChangeNotification, object: old)
        }
        observedClip = clip
        if let clip {
            clip.postsBoundsChangedNotifications = true
            NotificationCenter.default.addObserver(
                self, selector: #selector(clipScrolled(_:)), name: NSView.boundsDidChangeNotification, object: clip)
        }
    }

    @objc private func clipScrolled(_ note: Notification) {
        scheduleChatLive()
    }

    /// The near bubbles ask their bodies, the nearest HTML ones (at most
    /// the board's cap) hold web views, every other lets its view go
    /// (`Board.ChatBodies.live` over the rows' frames against the detail's
    /// visible part). Outside `chatLive` or with the preference off, none.
    private func updateChatLive() {
        let on = chatLive && htmlOn
        observeClip(on)
        guard on, let clip = observedClip else {
            for row in chatRows where row.bubble.live {
                change(row) { row.bubble.setLive(false) }
            }
            return
        }
        let v = convert(clip.bounds, from: clip)
        let visible = ConversationLayout.Span(Double(v.minY), Double(v.maxY))
        var frames: [ConversationLayout.Span] = []
        var html: [Bool] = []
        for row in chatRows {
            let f = row.convert(row.bounds, to: self)
            frames.append(ConversationLayout.Span(Double(f.minY), Double(f.maxY)))
            html.append(row.bubble.rendersHTML)
        }
        let live = Board.ChatBodies.live(frames: frames, html: html, visible: visible)
        for i in live.near.sorted() where chatBodies.needsBody(i, near: true, setting: true) {
            chatBodies.asked(i)
            chatFetch(i)
        }
        for (i, row) in chatRows.enumerated() where row.bubble.live != live.web.contains(i) {
            change(row) { row.bubble.setLive(live.web.contains(i)) }
        }
    }

    /// message.body for bubble `i` through the cache, as a card asks.
    private func chatFetch(_ i: Int) {
        guard let env = actions.mailBodies, let account, chatBodies.members.indices.contains(i),
              let id = chatBodies.members[i].id
        else { return }
        register(env)
        let member = chatBodies.members[i]
        env.cache.fetchBody(Self.summary(id, account: account)) { [weak self] lm in
            self?.chatLoaded(member, lm)
        }
    }

    /// The cache's answer for a bubble's member: the bubble shows it (HTML
    /// or the text) and the web views are looked at again.
    private func chatLoaded(_ member: Board.ConversationCards.Member, _ lm: LoadedMessage) {
        guard lm.bodySettled, !lm.fetching, let i = chatBodies.members.firstIndex(of: member),
              chatBodies.wasAsked(i), i < chatRows.count, messages.indices.contains(i)
        else { return }
        let row = chatRows[i]
        let m = messages[i]
        change(row) { renderChatRow(row, m, at: i) }
        scheduleChatLive()
    }
}

/// The web views' host of the board chat's bubbles: Mail's cache and text
/// zoom, links through the block's delegate (one per row) (the cards' "Open this
/// link?"), hover and height changes through the block.
@MainActor
final class BoardBubbleWebHost: ConversationBubbleWebHost {
    private let env: BoardMailBodies
    private weak var block: BoardConversationBlock?
    private weak var row: NSView?

    init(env: BoardMailBodies, block: BoardConversationBlock, row: NSView) {
        self.env = env
        self.block = block
        self.row = row
    }

    var cache: MessageCache { env.cache }

    var textZoom: Int { env.settings.textZoom }

    func openLink(_ href: String, links: [Link], from window: NSWindow?) {
        block?.delegate?.openLink(href, links: links, from: window)
    }

    func hover(_ href: String) {
        block?.onHover?(href)
    }

    func bubbleHeightChanging(_ change: () -> Void) {
        guard let block, let row, let host = block.onHeightChanging else {
            change()
            return
        }
        host(row, change)
    }
}
