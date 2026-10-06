// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/BoardModelTests.swift (EditorHeightTests);
// GTK: ui/internal/board/model_test.go (TestEditorHeightClamps,
// TestEditorHeightOddInput). The inline reply
// editor's height: the content's, clamped to [160, min(480, 0.6 × visible)].

using Malachi.Core.Board;
using Xunit;

namespace Malachi.Core.Tests.Board;

public sealed class EditorHeightTests
{
    [Fact]
    public void Clamps()
    {
        const double Tall = 1000;
        // Short content: the minimum, no scrolling.
        Assert.Equal(new EditorHeight(0, Tall), new EditorHeight(40, Tall));
        Assert.Equal(160, new EditorHeight(40, Tall).Height);
        Assert.False(new EditorHeight(40, Tall).Scrolls);
        // In between: the content's.
        Assert.Equal(300, new EditorHeight(300, Tall).Height);
        Assert.False(new EditorHeight(300, Tall).Scrolls);
        // 0.6 × 1000 = 600 > 480: the maximum caps it.
        Assert.Equal(480, new EditorHeight(700, Tall).Height);
        Assert.True(new EditorHeight(700, Tall).Scrolls);
        // A small detail: 0.6 × 500 = 300.
        Assert.Equal(300, new EditorHeight(700, 500).Height);
        Assert.Equal(300, new EditorHeight(300, 500).Height);
        Assert.False(new EditorHeight(300, 500).Scrolls);
        // Tiny: never below the minimum.
        Assert.Equal(160, new EditorHeight(700, 100).Height);
        Assert.True(new EditorHeight(700, 100).Scrolls);
        Assert.Equal(160, EditorHeight.Cap(100));
        Assert.Equal(420, EditorHeight.Cap(700), 9);
    }

    [Fact]
    public void OddInput()
    {
        Assert.Equal(160, new EditorHeight(double.NaN, 1000).Height);
        Assert.Equal(160, new EditorHeight(double.PositiveInfinity, 1000).Height);
        Assert.False(new EditorHeight(double.PositiveInfinity, 1000).Scrolls);
        Assert.Equal(160, new EditorHeight(-5, 1000).Height);
        Assert.Equal(480, EditorHeight.Cap(0));
        Assert.Equal(480, EditorHeight.Cap(double.NaN));
        Assert.Equal(480, EditorHeight.Cap(double.PositiveInfinity));
        Assert.Equal(480, EditorHeight.Cap(-1));
    }

    [Fact]
    public void TheBoundsAreTheReferences()
    {
        Assert.Equal(160, EditorHeight.Minimum);
        Assert.Equal(480, EditorHeight.Maximum);
        Assert.Equal(0.6, EditorHeight.VisibleShare);
    }
}
