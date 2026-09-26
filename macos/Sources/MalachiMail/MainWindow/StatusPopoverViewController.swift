// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The status popover (window.blp `status_popover`, status.go
/// `refreshStatusPopover`): a boxed list with one row per account, paused
/// ones included, with the account's state as a whole sentence and the
/// button that helps (`accountStatuses`), a row under it that leads to the
/// account's unsent messages, and the daemon at the foot. The rows are
/// updated in place: progress arrives every half second and must neither
/// rebuild the list nor take the keyboard focus away; they are rebuilt only
/// when the accounts change (`sameAccounts`). Account names and the
/// backend's error details are plain text.
@MainActor
final class StatusPopoverViewController: NSViewController {
    /// The content width (window.blp: a clamp of 320).
    static let width: CGFloat = 320
    /// The margin around the content: the Blueprint's 6 plus the padding a
    /// GTK popover has of its own, which an NSPopover lacks.
    static let margin: CGFloat = 12

    /// The action of an account's row, with the state it showed.
    var onAction: (@MainActor (AccountStatus) -> Void)?
    /// The unsent-messages row of an account was activated.
    var onShowOutbox: (@MainActor (AccountID) -> Void)?

    private let group = PreferencesGroupView()
    private let daemonLabel = PrefsWrappingLabel("", color: .secondaryLabelColor)
    /// The rows by account, and the accounts they were built for, in order.
    private var rows: [AccountID: StatusRowViews] = [:]
    private var order: [AccountID] = []
    private var daemonText = ""

    init() {
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func loadView() {
        let v = NSView()
        v.translatesAutoresizingMaskIntoConstraints = false
        daemonLabel.font = Typo.caption
        let m = Self.margin
        let stack = prefsColumn(spacing: 12, insets: NSEdgeInsets(top: m, left: m, bottom: m, right: m))
        prefsAddFilling(group, to: stack)
        prefsAddFilling(daemonLabel, to: stack)
        v.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: v.topAnchor),
            stack.bottomAnchor.constraint(equalTo: v.bottomAnchor),
            stack.leadingAnchor.constraint(equalTo: v.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: v.trailingAnchor),
            v.widthAnchor.constraint(equalToConstant: Self.width),
        ])
        view = v
        group.isHidden = true
        applyDaemon()
    }

    /// The foot (window.blp `status_daemon`): the daemon's version and pid,
    /// or that system.info failed; "" hides it.
    func setDaemon(_ text: String) {
        daemonText = text
        if isViewLoaded {
            applyDaemon()
        }
    }

    private func applyDaemon() {
        daemonLabel.stringValue = daemonText
        daemonLabel.isHidden = daemonText.isEmpty
    }

    /// Shows `list` (status.go `refreshStatusPopover`): the rows are rebuilt
    /// when the accounts changed and updated in place otherwise, so a row
    /// keeps the keyboard focus while its account syncs. `outbox` says
    /// whether an account's outbox can be shown (`MailModel.outboxKey`).
    func update(_ list: [AccountStatus], outbox: (AccountID) -> Bool) {
        _ = view
        if !sameAccounts(order, list) {
            rows = [:]
            order = []
            var views: [NSView] = []
            for st in list {
                let r = StatusRowViews(account: st.account)
                r.onAction = { [weak self] st in self?.onAction?(st) }
                r.onShowOutbox = { [weak self] acc in self?.onShowOutbox?(acc) }
                rows[st.account] = r
                order.append(st.account)
                views.append(r.row)
                views.append(r.failed)
            }
            group.setRows(views)
        }
        for st in list {
            guard let r = rows[st.account] else { continue }
            r.apply(st, outbox: outbox(st.account))
            group.setRow(r.failed, hidden: st.failed <= 0)
        }
        group.isHidden = list.isEmpty
    }
}

/// One account's part of the status popover (status.go `statusRow`): the
/// account's row with its action buttons (at most one of them shown), and
/// the row that leads to its unsent messages, hidden while there are none.
@MainActor
private final class StatusRowViews: NSObject {
    let account: AccountID
    let row: PreferenceRowView
    /// `.check`: an icon.
    let check: NSButton
    /// The other actions: a label (`statusButtonLabel`).
    let button: NSButton
    let failed: StatusLinkRow

    var onAction: (@MainActor (AccountStatus) -> Void)?
    var onShowOutbox: (@MainActor (AccountID) -> Void)?

    /// What the rows show; nil until the first `apply`.
    private(set) var status: AccountStatus?

    init(account: AccountID) {
        let check = NSButton(image: Icon.image("view-refresh-symbolic", size: .regular), target: nil, action: nil)
        let button = NSButton(title: "", target: nil, action: nil)
        let actions = NSStackView(views: [check, button])
        actions.orientation = .horizontal
        actions.alignment = .centerY
        actions.spacing = 6
        self.account = account
        self.check = check
        self.button = button
        row = PreferenceRowView(title: "", subtitle: "", trailing: actions)
        failed = StatusLinkRow()
        super.init()

        check.isBordered = false
        check.imagePosition = .imageOnly
        check.toolTip = L10n.T("Check for New Mail")
        check.setAccessibilityLabel(L10n.T("Check for New Mail"))
        check.isHidden = true
        check.target = self
        check.action = #selector(actionClicked(_:))

        button.bezelStyle = .rounded
        button.isHidden = true
        button.target = self
        button.action = #selector(actionClicked(_:))

        failed.onActivate = { [weak self] in
            guard let self else { return }
            self.onShowOutbox?(self.account)
        }
    }

    /// Shows `st` on the rows (status.go `statusRow.apply`). The buttons are
    /// only touched when the action changed, so one that has the focus
    /// keeps it while the detail moves. `outbox` says whether the account's
    /// outbox can be shown: a paused account's cannot.
    func apply(_ st: AccountStatus, outbox: Bool) {
        row.title = st.title
        row.subtitle = st.detail
        if status == nil || st.action != status?.action || st.signIn != status?.signIn {
            check.isHidden = st.action != .check
            let label = statusButtonLabel(st)
            if !label.isEmpty {
                // Only "_Edit Account…" carries a mnemonic.
                button.title = st.action == .edit ? mn(label) : label
            }
            button.isHidden = label.isEmpty
        }
        if st.failed > 0 {
            failed.title = notSentText(st.failed)
        }
        failed.isActivatable = outbox
        status = st
    }

    @objc private func actionClicked(_ sender: Any?) {
        guard let status else { return }
        onAction?(status)
    }
}

/// An activatable row of the boxed list with a trailing arrow (an
/// `Adw.ActionRow` with `activatable` and a `go-next-symbolic` suffix): the
/// text is a borderless button over the row's width, so a click, the
/// keyboard and VoiceOver all activate it. Without a target
/// (`isActivatable` false) the arrow goes and the row is only text.
@MainActor
private final class StatusLinkRow: NSView {
    var onActivate: (@MainActor () -> Void)?

    private let button = NSButton(title: "", target: nil, action: nil)
    private let arrow = NSImageView()

    var title: String {
        get { button.title }
        set { button.title = newValue }
    }

    /// Whether the row leads anywhere. Without a target the text stays as
    /// it is (a disabled button would dim it, which GTK's inactive row does
    /// not); the arrow goes and the row is no stop for the keyboard.
    var isActivatable = true {
        didSet {
            arrow.isHidden = !isActivatable
            button.refusesFirstResponder = !isActivatable
        }
    }

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        button.isBordered = false
        button.imagePosition = .noImage
        button.alignment = .left
        button.lineBreakMode = .byTruncatingTail
        button.font = .systemFont(ofSize: 13)
        button.contentTintColor = .labelColor
        button.target = self
        button.action = #selector(clicked(_:))
        button.translatesAutoresizingMaskIntoConstraints = false
        button.setContentHuggingPriority(.defaultLow, for: .horizontal)
        button.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)

        arrow.image = Icon.image("go-next-symbolic", size: .regular)
        arrow.imageScaling = .scaleNone
        arrow.contentTintColor = .secondaryLabelColor
        // Decoration: VoiceOver would read the GTK icon name.
        arrow.setAccessibilityElement(false)
        arrow.translatesAutoresizingMaskIntoConstraints = false
        arrow.setContentHuggingPriority(.required, for: .horizontal)
        arrow.setContentCompressionResistancePriority(.required, for: .horizontal)

        addSubview(button)
        addSubview(arrow)
        let inset = PreferenceRowView.inset
        NSLayoutConstraint.activate([
            heightAnchor.constraint(greaterThanOrEqualToConstant: PreferenceRowView.minHeight),
            button.leadingAnchor.constraint(equalTo: leadingAnchor, constant: inset),
            button.topAnchor.constraint(equalTo: topAnchor),
            button.bottomAnchor.constraint(equalTo: bottomAnchor),
            button.trailingAnchor.constraint(equalTo: arrow.leadingAnchor, constant: -inset),
            arrow.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -inset),
            arrow.centerYAnchor.constraint(equalTo: centerYAnchor),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    @objc private func clicked(_ sender: Any?) {
        guard isActivatable else { return }
        onActivate?()
    }
}
