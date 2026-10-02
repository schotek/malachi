// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The detail of the selected case (`controller.view.detail`): in the
/// panel an action bar pinned over a scrolling, clamped column with the state, the "why"
/// box, the title, the deadline, the assistant's summary, tasks and
/// suggested reply, and the conversation; what the assistant wrote (its
/// title, its "why", its summary and tasks) carries the assistant's mark
/// and is spoken as the assistant's. The List style shows it beside
/// the list, its actions in the toolbar's section above it (`BoardToolbar`),
/// the other two styles as a sliding panel. Every string is
/// plain text set through `stringValue`; nothing from a case is markup.
///
/// The column has three parts. `upper` (the header line up to the tasks)
/// and `lower` (the conversation) are rebuilt from the detail whenever it
/// differs from what is shown; between them `replySlot`, the suggested
/// reply, is never touched by that rebuild. It follows the page's
/// `BoardReplyEditorHost` (`replySlotChanged`): the inline editor
/// (`ComposePane`) of the case's suggested reply while the host gives it to
/// this presentation, a loading row, a failure note with Try Again, else
/// Suggest Reply (`BoardSuggestReplyControl`), and for the invented samples
/// their static block. Every autosave of the reply bumps the case's
/// version and lists the board again; the slot keeps the same pane through
/// it, so the editor's caret and keyboard stay. The scroll position stays
/// unless another case is shown.
@MainActor
final class BoardDetailViewController: NSViewController {
    /// Where the detail is shown: the List style's pane beside the list,
    /// or the sliding panel over the other styles.
    enum Presentation {
        case pane
        case panel
    }

    private let controller: BoardController
    /// What the buttons do (shared with the toolbar and the menus).
    private let actions: BoardActions
    let presentation: Presentation

    /// The panel's Close button.
    var onClose: (() -> Void)?
    /// A short message over the page (set by the page; the actions' own
    /// go to `BoardActions.onToast`).
    var onToast: ((String) -> Void)?

    /// The state pill, the first control worth the keyboard.
    var focusTarget: NSView? { statePill }

    /// The panel's column: at its leading edge, under the action bar's
    /// buttons, up to here.
    private static let panelWidth: CGFloat = 760
    /// The pane's column: centred, up to the reader's maximum (the
    /// conversation's and the message's `ClampView`), so a case's
    /// conversation sits where Mail shows it.
    private static let paneWidth = CGFloat(ConversationLayout.Metrics.maxWidth)
    private static let gap: CGFloat = 14
    /// The content's leading and trailing inset.
    private static let inset: CGFloat = 20
    /// The action bar's buttons give way before the window does (below
    /// the window's holding priorities, 490 and up): a narrow panel cuts
    /// a title rather than keeping the window wide.
    private static let buttonResistance = NSLayoutConstraint.Priority(450)
    /// The state pill's accessibility name.
    private static var stateLabel: String { Board.Text.stateLabel }

    private let scroll = NSScrollView()
    /// The scroll view's document: the content column, as wide as the
    /// visible area up to `paneWidth` (centred) or `panelWidth` (at the
    /// leading edge).
    private let document = BoardDetailDocument()
    private let content = FillStackView()
    /// The header line up to the tasks: rebuilt with the detail.
    private let upper = FillStackView()
    /// The suggested reply: only `renderSlot` changes it.
    private let replySlot = FillStackView()
    /// The conversation: rebuilt with the detail.
    private let lower = FillStackView()
    private let actionBar = NSStackView()
    private let separator = NSBox()
    private let closeButton = NSButton()
    private let doneButton = NSButton()
    private let remindButton = NSButton()
    private let archiveButton = NSButton()
    private let replyButton = NSButton()
    /// The state pill of the shown case, while there is one.
    private var statePill: IssueStatusPill?
    /// The "Why is this here?" button of the shown case.
    private var whyButton: NSButton?

    /// The detail as last applied (the action buttons act on its case).
    private var shown: Board.Detail?
    /// `upper` and `lower` as built: the detail without what only the
    /// reply slot shows (`Self.rebuildKey`).
    private var built: Board.Detail?
    private var shownWhy = false
    /// Suggest Reply, in the place of the Suggested Reply block (kept
    /// across rebuilds: the field keeps what the user typed).
    private let suggestControl = BoardSuggestReplyControl()
    private lazy var suggestBox = BoardBox(suggestControl, fill: { .clear }, border: { .separatorColor })
    private var shownSuggest: Board.SuggestReplyView = .hidden
    private var suggestToken: BoardObserverToken?
    /// What the reply slot shows.
    private var shownSlot: SlotState = .hidden
    /// The conversation's cards the user folded or opened, by their place
    /// (oldest first), for `foldCase` only: a rebuild of the same case
    /// keeps them, another case starts over.
    private var folds: [Int: Bool] = [:]
    private var foldCase: Board.CaseID?
    /// The note under the live pane that what was typed could not be saved
    /// yet (`Board.Text.replyNotSaved`); shown and hidden in place, so the
    /// pane never leaves the window for it.
    private var unsavedNote: NSTextField?

    init(actions: BoardActions, presentation: Presentation) {
        self.actions = actions
        controller = actions.controller
        self.presentation = presentation
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: View

    override func loadView() {
        let root = ContentBackgroundView()
        root.translatesAutoresizingMaskIntoConstraints = false
        view = root

        configure(closeButton, title: Board.Text.close, action: #selector(closeClicked(_:)))
        configure(doneButton, title: Board.Text.done, action: #selector(doneClicked(_:)))
        configure(remindButton, title: Board.Text.remind, action: #selector(remindClicked(_:)))
        configure(archiveButton, title: Board.Text.archive, action: #selector(archiveClicked(_:)))
        configure(replyButton, title: Board.Text.reply, action: #selector(replyClicked(_:)))
        replyButton.bezelColor = .controlAccentColor
        closeButton.isHidden = presentation != .panel
        let spacer = NSView()
        spacer.setContentHuggingPriority(.defaultLow, for: .horizontal)
        spacer.setContentCompressionResistancePriority(NSLayoutConstraint.Priority(1), for: .horizontal)
        actionBar.setViews([closeButton, doneButton, remindButton, archiveButton, spacer, replyButton], in: .leading)
        actionBar.orientation = .horizontal
        actionBar.alignment = .centerY
        actionBar.spacing = 8
        actionBar.edgeInsets = NSEdgeInsets(top: 8, left: 16, bottom: 8, right: 16)
        actionBar.translatesAutoresizingMaskIntoConstraints = false
        separator.boxType = .separator
        separator.translatesAutoresizingMaskIntoConstraints = false

        content.spacing = Self.gap
        content.edgeInsets = NSEdgeInsets(top: 16, left: Self.inset, bottom: 24, right: Self.inset)
        upper.spacing = Self.gap
        replySlot.spacing = Self.gap
        lower.spacing = Self.gap
        for part in [upper, replySlot, lower] {
            part.isHidden = true
            content.addArrangedSubview(part)
        }
        document.addSubview(content)
        scroll.documentView = document
        scroll.hasVerticalScroller = true
        scroll.hasHorizontalScroller = false
        scroll.autohidesScrollers = true
        scroll.drawsBackground = false
        scroll.translatesAutoresizingMaskIntoConstraints = false

        root.addSubview(scroll)
        let top = root.safeAreaLayoutGuide.topAnchor
        if presentation == .panel {
            root.addSubview(separator)
            root.addSubview(actionBar)
            NSLayoutConstraint.activate([
                actionBar.topAnchor.constraint(equalTo: top),
                actionBar.leadingAnchor.constraint(equalTo: root.leadingAnchor),
                actionBar.trailingAnchor.constraint(equalTo: root.trailingAnchor),
                separator.topAnchor.constraint(equalTo: actionBar.bottomAnchor),
                separator.leadingAnchor.constraint(equalTo: root.leadingAnchor),
                separator.trailingAnchor.constraint(equalTo: root.trailingAnchor),
                scroll.topAnchor.constraint(equalTo: separator.bottomAnchor),
            ])
        } else {
            // The pane's actions are the toolbar's, over the pane.
            scroll.topAnchor.constraint(equalTo: top).isActive = true
        }
        NSLayoutConstraint.activate([
            scroll.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            scroll.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            scroll.bottomAnchor.constraint(equalTo: root.bottomAnchor),
            document.widthAnchor.constraint(equalTo: scroll.contentView.widthAnchor),
        ])
        // In the pane the column is centred and stops at the reader's
        // maximum, as Mail's reading pane; in the panel it starts at the
        // leading edge, like the action bar's buttons. As wide as the
        // document up to the cap either way; the texts stay leading within
        // it. The document sets the column's width from its own (as
        // `ClampView` does): a constraint between the two would pull the
        // pane down to the cap whenever it is wider.
        if presentation == .pane {
            document.setColumn(content, maximum: Self.paneWidth, centred: true)
        } else {
            document.setColumn(content, maximum: Self.panelWidth, centred: false)
        }
        root.setAccessibilityElement(true)
        root.setAccessibilityRole(.group)
        suggestControl.onSuggest = { [weak self] text in
            guard let self, let d = self.shown else { return }
            self.actions.suggestReply(d.id, instruction: text)
        }
        suggestControl.onStop = { [weak self] in
            self?.actions.stopSuggestedReply()
        }
        suggestToken = actions.suggestion?.observe { [weak self] in
            self?.render()
        }
        actions.replyHost?.attach(self)
        render()
    }

    override func viewWillAppear() {
        super.viewWillAppear()
        render()
    }

    /// Archive and then Remind…, the least needed buttons, give way when
    /// the bar has no room for all of them at their full titles (a narrow
    /// panel, "Move Back to Board"). Measured with both counted in, so
    /// hiding them cannot bring them back on the next pass. The context
    /// menu keeps both.
    override func viewDidLayout() {
        super.viewDidLayout()
        guard presentation == .panel, !actionBar.isHidden else { return }
        let optional = [archiveButton, remindButton]
        let buttons = [closeButton, doneButton, remindButton, archiveButton, replyButton]
            .filter { optional.contains($0) || !$0.isHidden }
        let width = { (bs: [NSButton]) -> CGFloat in
            bs.reduce(CGFloat(0)) { $0 + $1.fittingSize.width } + self.actionBar.spacing * CGFloat(bs.count)
                + self.actionBar.edgeInsets.left + self.actionBar.edgeInsets.right
        }
        let available = view.bounds.width
        let hideArchive = width(buttons) > available
        let hideRemind = hideArchive && width(buttons.filter { $0 !== archiveButton }) > available
        if archiveButton.isHidden != hideArchive {
            archiveButton.isHidden = hideArchive
        }
        if remindButton.isHidden != hideRemind {
            remindButton.isHidden = hideRemind
        }
    }

    // MARK: Changes

    /// Brings the detail up to date: `.selection` (another case, or the
    /// "why" box) and `.content` (the case itself changed) both go through
    /// `render`, which compares with what is shown.
    func apply(_ changes: BoardController.Changes) {
        guard changes.contains(.selection) || changes.contains(.content) else { return }
        guard isViewLoaded else { return }
        render()
    }

    private func render() {
        guard isViewLoaded else { return }
        let detail = controller.view.detail
        let why = controller.state.revealsWhy
        shown = detail
        let key = detail.map(Self.rebuildKey)
        if key != built || why != shownWhy {
            let sameCase = detail?.id == built?.id
            built = key
            shownWhy = why
            let focus = focusedControl()
            rebuild(detail, why: why)
            restore(focus)
            renderSlot()
            if !sameCase {
                scroll.contentView.scroll(to: .zero)
                scroll.reflectScrolledClipView(scroll.contentView)
            }
            return
        }
        renderSlot()
    }

    /// The detail as far as `upper` and `lower` show it: the suggested
    /// reply's text is the slot's (the samples' block), and an autosave
    /// that changed only it rebuilds nothing.
    private static func rebuildKey(_ d: Board.Detail) -> Board.Detail {
        var d = d
        d.draft = ""
        return d
    }

    /// Rebuilds `upper` and `lower`; the reply slot between them stays.
    private func rebuild(_ detail: Board.Detail?, why: Bool) {
        for part in [upper, lower] {
            for v in part.arrangedSubviews {
                part.removeArrangedSubview(v)
                v.removeFromSuperview()
            }
        }
        statePill = nil
        whyButton = nil
        guard let d = detail else {
            upper.isHidden = true
            lower.isHidden = true
            actionBar.isHidden = true
            separator.isHidden = true
            view.setAccessibilityLabel(nil)
            return
        }
        upper.isHidden = false
        lower.isHidden = false
        actionBar.isHidden = false
        separator.isHidden = false
        view.setAccessibilityLabel(d.spokenTitle)
        doneButton.title = d.isDone ? Board.Text.notDone : Board.Text.done
        doneButton.setAccessibilityLabel(doneButton.title)
        archiveButton.isEnabled = actions.canArchive(d.id)
        replyButton.isEnabled = actions.canReply(d.id)

        upper.addArrangedSubview(headerLine(d))
        if why {
            upper.addArrangedSubview(whyBox(d))
        }
        upper.addArrangedSubview(titleBlock(d))
        if !d.due.isEmpty {
            upper.addArrangedSubview(deadlineLine(d))
        }
        if !d.staleNote.isEmpty {
            // Where the summary would be, quietly: the notes no longer count.
            upper.addArrangedSubview(wrappingLabel(d.staleNote, font: Typo.caption, color: Tint.secondary))
        }
        if !d.summary.isEmpty {
            upper.addArrangedSubview(summaryBox(d))
        }
        if !d.tasks.isEmpty {
            upper.addArrangedSubview(tasksBlock(d))
        }
        lower.addArrangedSubview(conversationBlock(d))
    }

    // MARK: The reply slot

    /// What the reply slot shows.
    private enum SlotState: Equatable {
        case hidden
        /// The samples' static block with the suggested reply's text.
        case staticDraft(String)
        /// Suggest Reply (its view is applied in place).
        case suggest
        case loading
        case failed(retry: Bool)
        /// The host's live pane.
        case editor(ObjectIdentifier)
    }

    /// The host's slot changed (a pane made, moved, released; the draft
    /// loading or failed).
    func replySlotChanged() {
        guard isViewLoaded else { return }
        renderSlot()
    }

    private func slotState(_ d: Board.Detail?) -> (SlotState, ComposePane?, unsaved: Bool) {
        guard let d else { return (.hidden, nil, false) }
        switch actions.replyHost?.slot(for: d.id, in: presentation) ?? .none {
        case .pane(let p, let unsaved): return (.editor(ObjectIdentifier(p)), p, unsaved)
        case .elsewhere: return (.hidden, nil, false)
        case .loading: return (.loading, nil, false)
        case .failed(let retry): return (.failed(retry: retry), nil, false)
        case .none:
            if actions.samples, !d.draft.isEmpty {
                return (.staticDraft(d.draft), nil, false)
            }
            return (actions.suggestReplyView(d.id).shown ? .suggest : .hidden, nil, false)
        }
    }

    /// Brings the reply slot up to `slotState`; the same state changes
    /// nothing (the pane stays where it is), Suggest Reply is updated in
    /// place. The keyboard in what goes moves to the state pill.
    private func renderSlot() {
        let d = controller.view.detail
        let (state, pane, unsaved) = slotState(d)
        let suggest = d.map { actions.suggestReplyView($0.id) } ?? .hidden
        defer { unsavedNote?.isHidden = !unsaved }
        if state == shownSlot, pane.map({ $0.view.isDescendant(of: replySlot) }) ?? true {
            if state == .suggest, let d, suggest != shownSuggest {
                shownSuggest = suggest
                suggestControl.apply(suggest, case: d.id)
            }
            return
        }
        let hadFocus = owns(focusIn: replySlot)
        for v in replySlot.arrangedSubviews where v.superview === replySlot {
            replySlot.removeArrangedSubview(v)
            v.removeFromSuperview()
        }
        shownSlot = state
        unsavedNote = nil
        replySlot.isHidden = state == .hidden
        if let d {
            switch state {
            case .hidden:
                break
            case .staticDraft(let text):
                replySlot.addArrangedSubview(draftBox(text))
            case .suggest:
                shownSuggest = suggest
                suggestControl.apply(suggest, case: d.id)
                replySlot.addArrangedSubview(suggestBox)
            case .loading:
                replySlot.addArrangedSubview(replyNoteBox(loading: true, retry: false))
            case .failed(let retry):
                replySlot.addArrangedSubview(replyNoteBox(loading: false, retry: retry))
            case .editor:
                if let pane {
                    replySlot.addArrangedSubview(editorBox(pane))
                }
            }
        }
        if hadFocus, !owns(focusIn: replySlot), let window = view.window, let statePill,
           window.firstResponder === window || window.firstResponder == nil
        {
            window.makeFirstResponder(statePill)
        }
    }

    /// The suggested reply edited in place: the heading, the note that it
    /// stays on the board until sent, and the host's pane.
    private func editorBox(_ pane: ComposePane) -> NSView {
        let paneView = pane.view
        paneView.removeFromSuperview()
        let unsaved = wrappingLabel(Board.Text.replyNotSaved, font: Typo.caption, color: .systemOrange)
        unsaved.isHidden = true
        unsavedNote = unsaved
        let column = FillStackView(fillingViews: [replyHeading(), replyNote(), unsaved, paneView])
        column.spacing = 4
        column.setCustomSpacing(10, after: unsaved)
        return BoardBox(column, fill: { .clear }, border: { .separatorColor })
    }

    /// A reply pane that had the keyboard ended (Send, Discard, its draft
    /// gone): the keyboard goes where the detail's focus rule puts it, the
    /// state pill, unless something else took it meanwhile.
    func takeKeyboardAfterReply() {
        guard isViewLoaded, let window = view.window, let statePill, !statePill.isHiddenOrHasHiddenAncestor,
              window.firstResponder === window || window.firstResponder == nil
        else { return }
        window.makeFirstResponder(statePill)
    }

    /// The suggested reply loading (a spinner row) or not opened (the
    /// note, Try Again for a failure that may pass).
    private func replyNoteBox(loading: Bool, retry: Bool) -> NSView {
        var views: [NSView] = [replyHeading()]
        if loading {
            let spinner = Spinner(size: 16)
            spinner.start()
            let note = NSTextField(labelWithString: Board.Text.replyLoading)
            note.font = Typo.caption
            note.textColor = Tint.secondary
            note.isSelectable = false
            let row = NSStackView(views: [spinner, note])
            row.orientation = .horizontal
            row.alignment = .centerY
            row.spacing = 6
            views.append(row)
        } else {
            views.append(wrappingLabel(Board.Text.replyLoadFailed, font: Typo.caption, color: Tint.secondary))
            if retry {
                views.append(linkButton(Board.Text.tryAgain, action: #selector(replyRetryClicked(_:))))
            }
        }
        let column = FillStackView(fillingViews: views)
        column.spacing = 6
        return BoardBox(column, fill: { .clear }, border: { .separatorColor })
    }

    private func replyHeading() -> NSTextField {
        let heading = NSTextField(labelWithString: Board.Text.draftHeading)
        heading.font = .systemFont(ofSize: Typo.bodySize, weight: .bold)
        heading.isSelectable = false
        return heading
    }

    private func replyNote() -> NSTextField {
        let note = NSTextField(labelWithString: Board.Text.draftNote)
        note.font = Typo.caption
        note.textColor = Tint.secondary
        note.lineBreakMode = .byTruncatingTail
        note.isSelectable = false
        note.setContentCompressionResistancePriority(NSLayoutConstraint.Priority(250), for: .horizontal)
        return note
    }

    /// A link-style button in the caption's font (Why is this here?, Try
    /// Again, Unstar).
    private func linkButton(_ title: String, action: Selector, symbol: String? = nil) -> NSButton {
        let b = NSButton(title: title, target: self, action: action)
        b.isBordered = false
        b.bezelStyle = .inline
        b.setButtonType(.momentaryChange)
        b.font = Typo.caption
        b.contentTintColor = .linkColor
        if let symbol {
            b.image = Icon.symbol(symbol, size: .small, description: nil)
            b.imagePosition = .imageLeading
        }
        b.setAccessibilityLabel(title)
        return b
    }

    // MARK: Blocks

    private func headerLine(_ d: Board.Detail) -> NSView {
        let flow = FlowView(spacing: 8, lineSpacing: 6)
        let pill = IssueStatusPill()
        pill.text = d.stateTitle
        // A done case has no Move To (as its context menu): a new state
        // would not take it out of Done.
        pill.menuIndicator = !d.isDone
        pill.paint(BoardPalette.colours(d.state))
        pill.setLabel(Self.stateLabel)
        let id = d.id
        if !d.isDone {
            pill.onClick = { [weak self, weak pill] in
                guard let self, let pill else { return }
                // Under the pill, as `IssueTransitionMenu.popUp(under:)` does.
                BoardCaseMenu.stateMenu(for: id, controller: self.controller)
                    .popUp(positioning: nil, at: NSPoint(x: 0, y: 0), in: pill)
            }
        }
        statePill = pill
        flow.addView(sized(pill))
        if !d.remindText.isEmpty {
            // "Back on the board Tomorrow 09:00", beside the state.
            let remind = NSTextField(labelWithString: d.remindText)
            remind.font = Typo.caption
            remind.textColor = Tint.secondary
            remind.isSelectable = false
            flow.addView(sized(remind))
        }
        if !d.account.isEmpty {
            // A pill cut at a fixed length (the full name in its tooltip),
            // as in the rows, so a long name cannot outgrow a narrow panel.
            flow.addView(sized(BoardTag.account(d.account)))
        }
        if let issue = d.issue {
            let tag = BoardTag.issue(key: issue.key, status: issue.status)
            BoardTag.paint(tag, IssuePill.colours(issue.style), emphasized: false)
            flow.addView(sized(tag))
        }
        let why = linkButton(Board.Text.whyLink, action: #selector(whyClicked(_:)))
        whyButton = why
        flow.addView(sized(why))
        if d.canUnstar {
            // On the board because of a star: take it off here (the rules
            // then decide where the case goes).
            flow.addView(sized(linkButton(Board.Text.unstar, action: #selector(unstarClicked(_:)), symbol: "star.slash")))
        }
        return flow
    }

    private func whyBox(_ d: Board.Detail) -> NSView {
        let why = wrappingLabel(d.why)
        var views: [NSView] = [d.whyIsAssistant ? marked(why, spoken: Board.Text.spokenAssistant(d.why)) : why]
        if !d.sourceText.isEmpty {
            views.append(wrappingLabel(d.sourceText, font: Typo.caption, color: Tint.secondary))
        }
        let column = FillStackView(fillingViews: views)
        column.spacing = 4
        let card = CalloutCard()
        column.translatesAutoresizingMaskIntoConstraints = false
        card.addSubview(column)
        let pad = CalloutCard.padding
        NSLayoutConstraint.activate([
            column.topAnchor.constraint(equalTo: card.topAnchor, constant: pad.top),
            card.bottomAnchor.constraint(equalTo: column.bottomAnchor, constant: pad.bottom),
            column.leadingAnchor.constraint(equalTo: card.leadingAnchor, constant: pad.left),
            card.trailingAnchor.constraint(equalTo: column.trailingAnchor, constant: pad.right),
        ])
        return card
    }

    private func titleBlock(_ d: Board.Detail) -> NSView {
        let meta = [d.person, d.time].filter { !$0.isEmpty }.joined(separator: " · ")
        var views: [NSView] = []
        if !meta.isEmpty {
            views.append(wrappingLabel(meta, font: Typo.caption, color: Tint.secondary))
        }
        let title = wrappingLabel(d.title, font: Typo.title2Bold)
        views.append(d.titleIsAssistant ? marked(title, spoken: d.spokenTitle) : title)
        if !d.subject.isEmpty {
            views.append(wrappingLabel(d.subject, font: Typo.caption, color: Tint.secondary))
        }
        let column = FillStackView(fillingViews: views)
        column.spacing = 4
        return column
    }

    /// The due chip, and the quote it comes from on its own wrapping
    /// line under it (a quote is up to 300 bytes; beside the chip it would
    /// outgrow a narrow panel).
    private func deadlineLine(_ d: Board.Detail) -> NSView {
        let stack = NSStackView(views: [BoardTag.due(d.due)])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 4
        stack.translatesAutoresizingMaskIntoConstraints = false
        if !d.dueQuote.isEmpty {
            let quote = wrappingLabel(
                Board.Text.quoted(d.dueQuote), font: NSFontManager.shared.convert(Typo.body, toHaveTrait: .italicFontMask),
                color: Tint.secondary)
            stack.addArrangedSubview(quote)
            quote.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
        }
        return stack
    }

    private func summaryBox(_ d: Board.Detail) -> NSView {
        let heading = NSTextField(labelWithString: Board.Text.summaryHeading)
        heading.font = .systemFont(ofSize: Typo.captionSize, weight: .semibold)
        heading.textColor = BoardPalette.assistant
        heading.isSelectable = false
        let column = FillStackView(fillingViews: [heading, wrappingLabel(d.summary)])
        column.spacing = 4
        return BoardBox(column, fill: { BoardPalette.assistantFill }, border: { BoardPalette.assistantBorder })
    }

    private func tasksBlock(_ d: Board.Detail) -> NSView {
        // The assistant's, as the summary's heading.
        let heading = NSTextField(labelWithString: Board.Text.tasksHeading)
        heading.font = .systemFont(ofSize: Typo.bodySize, weight: .bold)
        heading.textColor = BoardPalette.assistant
        heading.isSelectable = false
        var views: [NSView] = [heading]
        for task in d.tasks {
            views.append(taskRow(task))
        }
        let column = FillStackView(fillingViews: views)
        column.spacing = 6
        return column
    }

    private func taskRow(_ text: String) -> NSView {
        let ring = BoardRingView()
        let label = wrappingLabel(text)
        let row = NSView()
        row.translatesAutoresizingMaskIntoConstraints = false
        row.addSubview(ring)
        row.addSubview(label)
        let line = BoardMetrics.lineHeight(Typo.body)
        NSLayoutConstraint.activate([
            ring.leadingAnchor.constraint(equalTo: row.leadingAnchor),
            ring.centerYAnchor.constraint(equalTo: row.topAnchor, constant: line / 2),
            ring.widthAnchor.constraint(equalToConstant: BoardMetrics.ringSize),
            ring.heightAnchor.constraint(equalToConstant: BoardMetrics.ringSize),
            label.leadingAnchor.constraint(equalTo: ring.trailingAnchor, constant: 8),
            label.trailingAnchor.constraint(equalTo: row.trailingAnchor),
            label.topAnchor.constraint(equalTo: row.topAnchor),
            label.bottomAnchor.constraint(equalTo: row.bottomAnchor),
        ])
        return row
    }

    /// The samples' suggested reply: its text, Discard and the note (the
    /// samples have no draft to edit).
    private func draftBox(_ text: String) -> NSView {
        let discard = NSButton()
        configure(discard, title: Board.Text.discard, action: #selector(discardClicked(_:)))
        let note = replyNote()
        note.setContentHuggingPriority(.defaultLow, for: .horizontal)
        let spacer = NSView()
        spacer.setContentHuggingPriority(NSLayoutConstraint.Priority(1), for: .horizontal)
        spacer.setContentCompressionResistancePriority(NSLayoutConstraint.Priority(1), for: .horizontal)
        let buttons = NSStackView(views: [discard, spacer, note])
        buttons.orientation = .horizontal
        buttons.alignment = .centerY
        buttons.spacing = 8
        let column = FillStackView(fillingViews: [replyHeading(), wrappingLabel(text), buttons])
        column.spacing = 8
        return BoardBox(column, fill: { .clear }, border: { .separatorColor })
    }

    /// The heading with Show in Mail at its end, then the cards (every
    /// message's whole text, older ones folded to a few lines; the column
    /// scrolls, never a card); while the conversation loads a spinner row
    /// instead, and the note when it could not be loaded.
    private func conversationBlock(_ d: Board.Detail) -> NSView {
        var views: [NSView] = []
        let heading = NSTextField(labelWithString: d.conversationTitle)
        heading.font = .systemFont(ofSize: Typo.bodySize, weight: .bold)
        heading.lineBreakMode = .byTruncatingTail
        heading.isSelectable = false
        heading.setAccessibilityRole(.staticText)
        heading.setContentHuggingPriority(.defaultLow, for: .horizontal)
        heading.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        let show = NSButton()
        configure(show, title: Board.Text.showInMail, action: #selector(showInMailClicked(_:)))
        show.controlSize = .small
        show.font = .systemFont(ofSize: NSFont.systemFontSize(for: .small))
        show.isEnabled = actions.canShowInMail(d.id)
        let top = NSStackView(views: [heading, show])
        top.orientation = .horizontal
        top.alignment = .centerY
        top.distribution = .fill
        top.spacing = 8
        views.append(top)
        if d.messagesLoading {
            let spinner = Spinner(size: 16)
            spinner.start()
            let note = NSTextField(labelWithString: d.messagesNote)
            note.font = Typo.caption
            note.textColor = Tint.secondary
            note.isSelectable = false
            let row = NSStackView(views: [spinner, note])
            row.orientation = .horizontal
            row.alignment = .centerY
            row.spacing = 6
            views.append(row)
        } else if !d.messagesNote.isEmpty {
            let note = wrappingLabel(d.messagesNote, font: Typo.caption, color: Tint.secondary)
            if d.messagesRetry {
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
                views.append(column)
            } else {
                views.append(note)
            }
        } else {
            // The newest message in full; an older one of a longer
            // conversation starts folded to a few lines (the card offers
            // its arrow only when its text is longer than that).
            if foldCase != d.id {
                foldCase = d.id
                folds = [:]
            }
            let newest = d.messages.count - 1
            for (i, m) in d.messages.enumerated() {
                let foldable = i < newest
                let card = BoardMessageCardView(m, foldable: foldable, folded: folds[i] ?? foldable)
                card.onFold = { [weak self] folded in
                    self?.folds[i] = folded
                }
                views.append(card)
            }
        }
        let column = FillStackView(fillingViews: views)
        column.spacing = 8
        return column
    }

    /// `label` after the assistant's mark, spoken as `spoken` ("Assistant:
    /// …"): text the assistant wrote.
    private func marked(_ label: NSTextField, spoken: String) -> NSView {
        let mark = BoardAssistantMark.label(font: label.font ?? Typo.body)
        label.setAccessibilityLabel(spoken)
        let row = NSStackView(views: [mark, label])
        row.orientation = .horizontal
        row.alignment = .firstBaseline
        row.distribution = .fill
        row.spacing = BoardAssistantMark.gap
        row.setHuggingPriority(.defaultLow, for: .horizontal)
        row.translatesAutoresizingMaskIntoConstraints = false
        label.setContentHuggingPriority(.defaultLow, for: .horizontal)
        label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        return row
    }

    /// A wrapping, selectable label of the detail (`PrefsWrappingLabel`,
    /// whose wrapping width follows the width it is given).
    private func wrappingLabel(_ text: String, font: NSFont = Typo.body, color: NSColor = .labelColor) -> NSTextField {
        PrefsWrappingLabel.board(text, font: font, color: color)
    }

    /// FlowView places its children at their fitting size.
    private func sized(_ v: NSView) -> NSView {
        v.translatesAutoresizingMaskIntoConstraints = true
        v.setFrameSize(v.fittingSize)
        return v
    }

    private func configure(_ button: NSButton, title: String, action: Selector) {
        button.title = title
        button.bezelStyle = .rounded
        button.controlSize = .regular
        button.target = self
        button.action = action
        button.setContentHuggingPriority(.required, for: .horizontal)
        button.setContentCompressionResistancePriority(Self.buttonResistance, for: .horizontal)
        button.setAccessibilityLabel(title)
    }

    // MARK: Focus across a rebuild

    /// The control of the content that has the keyboard, by its part.
    private enum FocusedControl {
        case none
        case statePill
        case why
        case other
    }

    /// Which control of `upper` or `lower` has the keyboard (a field's
    /// editor counts as its field), before a rebuild removes it. The reply
    /// slot is not rebuilt: the keyboard there (the inline editor, Suggest
    /// Reply's field) stays where it is.
    private func focusedControl() -> FocusedControl {
        guard var responder = view.window?.firstResponder else { return .none }
        if let editor = responder as? NSTextView, editor.isFieldEditor, let field = editor.delegate as? NSResponder {
            responder = field
        }
        guard let v = responder as? NSView, v.isDescendant(of: content), !v.isDescendant(of: replySlot) else {
            return .none
        }
        if let statePill, v.isDescendant(of: statePill) {
            return .statePill
        }
        if let whyButton, v === whyButton {
            return .why
        }
        return .other
    }

    /// Gives the keyboard to the rebuilt counterpart of what had it, the
    /// state pill for anything else in the content; the window would keep
    /// it otherwise.
    private func restore(_ focus: FocusedControl) {
        guard let window = view.window else { return }
        let target: NSView?
        switch focus {
        case .none: return
        case .why: target = whyButton ?? statePill
        case .statePill, .other: target = statePill
        }
        if let target {
            window.makeFirstResponder(target)
        }
    }

    /// Whether the keyboard is in `container` (a field's editor counts as
    /// its field).
    private func owns(focusIn container: NSView) -> Bool {
        guard var responder = view.window?.firstResponder else { return false }
        if let editor = responder as? NSTextView, editor.isFieldEditor, let field = editor.delegate as? NSResponder {
            responder = field
        }
        guard let v = responder as? NSView else { return false }
        return v.isDescendant(of: container)
    }

    // MARK: Actions

    @objc private func closeClicked(_ sender: Any?) {
        onClose?()
    }

    @objc private func doneClicked(_ sender: Any?) {
        guard let d = shown else { return }
        actions.toggleDone(d.id)
    }

    /// Remind…'s menu under the button.
    @objc private func remindClicked(_ sender: Any?) {
        guard let d = shown else { return }
        BoardActions.popUp(actions.remindMenu(for: d.id), under: remindButton)
    }

    @objc private func archiveClicked(_ sender: Any?) {
        guard let d = shown else { return }
        actions.archive(d.id)
    }

    @objc private func replyClicked(_ sender: Any?) {
        guard let d = shown else { return }
        actions.reply(d.id)
    }

    /// Unstar beside Why is this here?.
    @objc private func unstarClicked(_ sender: Any?) {
        guard let d = shown else { return }
        actions.unstar(d.id)
    }

    /// The reply slot's Try Again.
    @objc private func replyRetryClicked(_ sender: Any?) {
        actions.replyHost?.retry()
    }

    @objc private func showInMailClicked(_ sender: Any?) {
        guard let d = shown else { return }
        actions.showInMail(d.id)
    }

    @objc private func discardClicked(_ sender: Any?) {
        guard let d = shown else { return }
        actions.discardDraft(d.id)
    }

    @objc private func whyClicked(_ sender: Any?) {
        controller.toggleWhy()
    }

    /// The conversation's Try Again.
    @objc private func retryClicked(_ sender: Any?) {
        controller.retryMessages()
    }
}

/// A rounded box around `content` with a fill and a border whose colours
/// are resolved when it draws (dynamic, no cached CGColors).
@MainActor
final class BoardBox: NSView {
    private let fill: () -> NSColor
    private let border: () -> NSColor

    init(_ content: NSView, fill: @escaping () -> NSColor, border: @escaping () -> NSColor) {
        self.fill = fill
        self.border = border
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        wantsLayer = true
        layer?.cornerRadius = BoardMetrics.cardRadius
        layer?.cornerCurve = .continuous
        layer?.borderWidth = 1
        content.translatesAutoresizingMaskIntoConstraints = false
        addSubview(content)
        NSLayoutConstraint.activate([
            content.topAnchor.constraint(equalTo: topAnchor, constant: 10),
            bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: 10),
            content.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 12),
            trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: 12),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var wantsUpdateLayer: Bool { true }

    override func updateLayer() {
        effectiveAppearance.performAsCurrentDrawingAppearance {
            layer?.backgroundColor = fill().cgColor
            layer?.borderColor = border().cgColor
        }
    }
}

/// The detail's scroll document: top-left origin, so a column shorter than
/// the visible area sits at its top and scrolling to the origin shows the
/// start (as `ClampView` does for the reader).
@MainActor
private final class BoardDetailDocument: NSView {
    override var isFlipped: Bool { true }

    private var maximum: CGFloat = 0
    /// The column's width; its constant follows the document's own width,
    /// up to `maximum`, so nothing ties the document's width to the column.
    private var columnWidth: NSLayoutConstraint?

    override init(frame: NSRect) {
        super.init(frame: frame)
        translatesAutoresizingMaskIntoConstraints = false
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Pins `column` to the top and the bottom, centred (as `ClampView`)
    /// or at the leading edge, as wide as the document up to `maximum`.
    func setColumn(_ column: NSView, maximum: CGFloat, centred: Bool) {
        self.maximum = maximum
        column.translatesAutoresizingMaskIntoConstraints = false
        if column.superview !== self {
            addSubview(column)
        }
        let width = column.widthAnchor.constraint(equalToConstant: min(bounds.width, maximum))
        columnWidth = width
        NSLayoutConstraint.activate([
            width,
            column.topAnchor.constraint(equalTo: topAnchor),
            column.bottomAnchor.constraint(equalTo: bottomAnchor),
            centred
                ? column.centerXAnchor.constraint(equalTo: centerXAnchor)
                : column.leadingAnchor.constraint(equalTo: leadingAnchor),
        ])
    }

    override func layout() {
        let w = max(0, min(bounds.width, maximum))
        if let c = columnWidth, c.constant != w {
            c.constant = w
        }
        super.layout()
    }
}

/// The small ring before a task.
@MainActor
private final class BoardRingView: NSView {
    override init(frame: NSRect) {
        super.init(frame: frame)
        translatesAutoresizingMaskIntoConstraints = false
        setAccessibilityElement(false)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func draw(_ dirtyRect: NSRect) {
        BoardPalette.assistant.setStroke()
        let path = NSBezierPath(ovalIn: bounds.insetBy(dx: BoardMetrics.ringWidth / 2, dy: BoardMetrics.ringWidth / 2))
        path.lineWidth = BoardMetrics.ringWidth
        path.stroke()
    }
}
