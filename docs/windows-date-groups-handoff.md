# WinUI 3 inbox sections: verification handoff

The Windows port is implemented for native verification. It mirrors
`ui/internal/maildate` and `window/date_groups.go`, and macOS
`MailDateGroups.swift` / `MessageListViewController.swift`.

## Behavior and implementation

Only the normal Inbox list has sections. Flagged messages/conversations come
first, then Today, Yesterday, this/last week, this/last month, this year and
older years. Dates use the local timezone and the culture's first weekday.
Conversation members follow their parent, including its aggregate flag.
Search, other folders and the Board presentation are unchanged.

`MailDateGroups` is the pure calendar projection. `MailDateList` owns disclosure
state and retains `MessageRow` instances by key across flag moves and paging.
It exposes the visible message rows and `MailDateSectionRow` groups. WinUI's
`CollectionViewSource` uses `ItemsPath=Rows`; `GroupStyle.HidesIfEmpty=False`
keeps a collapsed group's header visible. Headers are buttons, not fake
message items, and receive their own Enter/Space handling.

Collapsing a section containing the selection clears the controller selection
before removing the visible rows. A selected row moved by a flag change reveals
its destination. Empty sections disappear; collapse state resets on a folder
change. Search temporarily disables grouping. Automatic pagination pauses while
any existing section is collapsed; Load More remains available if another page
exists. A timer active only while the pane is loaded checks local calendar
boundaries every 30 seconds and refreshes after midnight/timezone offset or
first-weekday changes.

All labels use the existing shared msgids and Czech catalog. Their nine Windows
parity exclusions have been removed. No API/backend changes or new dependencies.

## Verification performed on macOS

Static review of the source, references, selection/pagination paths, XML
well-formedness, translation keys, SPDX headers and `git diff --check`.
No .NET SDK is installed here. **No C# compilation, analyzer run, automated test
execution or native WinUI verification has been performed.** The user explicitly
requested native verification after handoff rather than local toolchain setup.

Prepared tests: `MailDateGroupsTests` covers date ranges, locale week starts,
year rollover, local midnight, spring/fall DST, flagged ordering and thread
aggregates/children. `MailDateListTests` covers stable identities, collapse and
selection, flag moves, paging, search/folder transitions, focus and midnight
using an injected clock.

## Windows agent

Run from the repository root on Windows with the normal project prerequisites:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File windows\build.ps1 build
powershell -NoProfile -ExecutionPolicy Bypass -File windows\build.ps1 test
powershell -NoProfile -ExecutionPolicy Bypass -File windows\build.ps1 lint
powershell -NoProfile -ExecutionPolicy Bypass -File windows\build.ps1 app
```

Fix any compiler/analyzer/XAML binding issues before the runtime walkthrough.
Pay particular attention to native grouped collection behavior:

1. In a Czech Inbox verify Označené, Dnes, Včera and the other translated headings;
   test English too. No empty groups, except headers deliberately kept collapsed.
2. Toggle each heading with mouse, Enter and Space. It must neither select/open
   a message nor run an action on the previously selected message. Test focus,
   Narrator announcements, high contrast and comfortable/compact density.
3. Collapse a selected row's group: selection and reading pane clear. Collapse
   another group: selection remains. With all sections collapsed headers and
   Load More must remain reachable.
4. Star/unstar the selected message, including when its destination is collapsed;
   selection follows the same message. Repeat for conversations with an older
   flagged member, expanded children and after receiving another page.
5. Load more with collapsed groups: no automatic fetching loop, older flagged
   rows appear in the first section once visible, no duplicates. Expand all:
   automatic viewport paging resumes. Test retry and loading spinner states.
6. Exercise list Up/Down, Enter, thread Left/Right, context menu and programmatic
   focus. Headers must not enter message counts, action targets or focus indices.
7. Switch Inbox/search/Sent/Board, folder and account; test empty Inbox, search
   results and failed/slow loading. Other list modes should retain their behavior.
8. Leave the Inbox open across local midnight (or use a controlled test clock),
   and inspect week/month/year edges and a Sunday-first locale.

The separate GTK verification remains documented in
[gtk-date-groups-handoff.md](gtk-date-groups-handoff.md); this work does not
claim to complete it.

Native grouping API references:
[CollectionViewSource](https://learn.microsoft.com/en-us/windows/windows-app-sdk/api/winrt/microsoft.ui.xaml.data.collectionviewsource)
and [GroupStyle.HidesIfEmpty](https://learn.microsoft.com/en-us/windows/windows-app-sdk/api/winrt/microsoft.ui.xaml.controls.groupstyle.hidesifempty).
