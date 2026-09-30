// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the error enums of macos/Sources/MalachiCore/Assistant/
// (Assistant.Failure in Assistant.swift, EventError in AssistantEvents.swift,
// RewriteError in AssistantRewrite.swift, SearchError in
// AssistantSearch.swift); GTK: the errors of ui/internal/assistant
// (errAction, errNoAccount, …, errMalformed, errNotObject, errRewrite,
// errNoPassage, errPassageTooLong, errNoInstruction, errNoWords,
// errWordsTooLong). One enum for all of them, so that one exception type
// carries them (AssistantException); the tests compare the kind, as Go's
// errors.Is does.

namespace Malachi.Core.Assistants;

/// <summary>What went wrong in <see cref="Assistant"/> (Go's errors of ui/internal/assistant).</summary>
public enum AssistantError
{
    /// <summary>errAction: not a message action (Unread has its own prompt, or an unknown action).</summary>
    NotAMessageAction,

    /// <summary>errNoAccount: no account id.</summary>
    NoAccount,

    /// <summary>errNoFolder: no folder id.</summary>
    NoFolder,

    /// <summary>errNoMessages: no message ids.</summary>
    NoMessages,

    /// <summary>errEmptyID: an empty message id.</summary>
    EmptyId,

    /// <summary>errTooLong: the prompt does not fit even with one id.</summary>
    TooLong,

    /// <summary>errPath: not a clean absolute path.</summary>
    NotACleanAbsolutePath,

    /// <summary>errMalformed: a stream-json line that is not JSON.</summary>
    Malformed,

    /// <summary>errNotObject: a stream-json line that is not an object.</summary>
    NotObject,

    /// <summary>errRewrite: not a rewrite.</summary>
    NotARewrite,

    /// <summary>errNoPassage: an empty passage.</summary>
    NoPassage,

    /// <summary>errPassageTooLong: the passage is too long.</summary>
    PassageTooLong,

    /// <summary>errNoInstruction: an empty instruction.</summary>
    NoInstruction,

    /// <summary>errNoWords: no words to search for.</summary>
    NoWords,

    /// <summary>errWordsTooLong: the words are too long.</summary>
    WordsTooLong,
}
