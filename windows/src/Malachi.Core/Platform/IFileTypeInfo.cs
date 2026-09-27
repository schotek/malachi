// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only (docs/windows-port.md §6.6): what the shell knows about a type
// of file, for the previewer's panel (PreviewPanel). Looked up by the
// extension alone (SHGetFileInfo with SHGFI_USEFILEATTRIBUTES), so no file
// exists and no handler reads one.

namespace Malachi.Core.Platform;

/// <summary>The shell's name and icon for an extension.</summary>
public interface IFileTypeInfo
{
    /// <summary>
    /// What Windows calls files of <paramref name="extension"/> (".pdf" →
    /// "PDF Document"), in the user's language; null when it has no name.
    /// "" asks for a file without an extension.
    /// </summary>
    string? TypeName(string extension);

    /// <summary>
    /// Windows' icon for files of <paramref name="extension"/>, about
    /// <paramref name="size"/> pixels square; null when there is none.
    /// </summary>
    FileIcon? Icon(string extension, int size);
}
