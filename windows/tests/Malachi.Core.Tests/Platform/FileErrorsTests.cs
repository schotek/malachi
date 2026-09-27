// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: file-system errors without their path (FileErrors.cs); no
// Go or Swift counterpart (AttachmentActions.swift logs such errors as
// private instead).

using System;
using System.IO;
using Malachi.Core.Platform;
using Xunit;

namespace Malachi.Core.Tests.Platform;

public sealed class FileErrorsTests
{
    public static readonly TheoryData<Exception> Errors = new()
    {
        new FileNotFoundException("Could not find file 'C:\\open\\secret-name.pdf'.", "C:\\open\\secret-name.pdf"),
        new DirectoryNotFoundException("Could not find a part of the path 'C:\\open\\secret-name.pdf'."),
        new PathTooLongException("The path 'C:\\open\\secret-name.pdf' is too long."),
        new IOException("The file 'C:\\open\\secret-name.pdf' already exists.", unchecked((int)0x80070050)),
        new UnauthorizedAccessException("Access to the path 'C:\\open\\secret-name.pdf' is denied."),
    };

    [Theory]
    [MemberData(nameof(Errors))]
    public void TheKindAndTheCodeStayAndThePathGoes(Exception error)
    {
        var bare = FileErrors.WithoutPath(error, "could not write a file for opening");

        Assert.Equal(error.GetType(), bare.GetType());
        Assert.Equal(error.HResult, bare.HResult);
        Assert.Equal("could not write a file for opening", bare.Message);
        Assert.Null(bare.InnerException);
        Assert.DoesNotContain("secret-name", bare.ToString(), StringComparison.Ordinal);
    }

    [Fact]
    public void OtherErrorsStayAsTheyAre()
    {
        var error = new InvalidOperationException("x");

        Assert.Same(error, FileErrors.WithoutPath(error, "y"));
    }
}
