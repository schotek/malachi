// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only (docs/windows-port.md §6.1, §6.2): the names a web view goes
// by, the InPrivate profile it gets and the host of the documents it
// serves itself (malachi-doc://<host>/...).

namespace Malachi.Core.Presentation;

/// <summary>The profile and document host of each <see cref="WebViewKind"/>.</summary>
public static class WebViewKindExtensions
{
    extension(WebViewKind kind)
    {
        /// <summary>
        /// The InPrivate profile of the view (<c>viewer</c>, <c>editor</c>,
        /// <c>preview</c>), which is also the host of its documents: a
        /// document of one view is never a URL another view serves.
        /// </summary>
        public string ProfileName => kind switch
        {
            WebViewKind.Editor => "editor",
            WebViewKind.Preview => "preview",
            _ => "viewer",
        };
    }
}
