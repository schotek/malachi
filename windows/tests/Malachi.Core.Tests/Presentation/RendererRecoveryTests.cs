// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The tests of RendererRecovery: a document whose renderer dies, hangs or
// takes the browser with it is shown again once; the same document failing
// again is given up, whoever loaded it again; a hang counts only when it is
// reported again, or the view's host script goes unanswered, without an
// answer in between.

using System;
using System.Text;
using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class RendererRecoveryTests
{
    private static readonly byte[] Body = Encoding.UTF8.GetBytes("<p>crash</p>");
    private static readonly byte[] Other = Encoding.UTF8.GetBytes("<p>other</p>");

    // A dead renderer: the document once more in the same control, then
    // never again automatically.
    [Fact]
    public void ADeadRendererReloadsOnce()
    {
        var recovery = new RendererRecovery();
        recovery.Loading(Body);
        Assert.Equal(RecoveryAction.Reload, recovery.Failed(RendererFailure.RendererExited));
        // The view loads it again (its own reload).
        recovery.Loading(Body);
        Assert.Equal(RecoveryAction.GiveUp, recovery.Failed(RendererFailure.RendererExited));
        // Given up: nothing is on display, nothing to do.
        Assert.Equal(RecoveryAction.None, recovery.Failed(RendererFailure.RendererExited));
    }

    // The same bytes loaded by the caller (the compose window after Crashed,
    // the user selecting the message again) are the same document: no
    // second automatic reload, so a caller that reloads cannot loop either.
    [Fact]
    public void TheSameDocumentIsKnownWhoeverLoadsIt()
    {
        var recovery = new RendererRecovery();
        recovery.Loading(Body);
        Assert.Equal(RecoveryAction.Reload, recovery.Failed(RendererFailure.RendererExited));
        recovery.Loading(Body);
        Assert.Equal(RecoveryAction.GiveUp, recovery.Failed(RendererFailure.RendererExited));
        recovery.Loading(Body.AsSpan().ToArray());
        Assert.Equal(RecoveryAction.GiveUp, recovery.Failed(RendererFailure.RendererExited));
    }

    // Another document has its own reload; coming back to the first one
    // after that gives it one again.
    [Fact]
    public void EveryDocumentHasItsOwnReload()
    {
        var recovery = new RendererRecovery();
        recovery.Loading(Body);
        Assert.Equal(RecoveryAction.Reload, recovery.Failed(RendererFailure.RendererExited));
        recovery.Loading(Other);
        Assert.Equal(RecoveryAction.Reload, recovery.Failed(RendererFailure.RendererExited));
        recovery.Loading(Body);
        Assert.Equal(RecoveryAction.Reload, recovery.Failed(RendererFailure.RendererExited));
    }

    // A document that loaded and was then replaced without failing does not
    // reset the count of the one that failed.
    [Fact]
    public void ALoadInBetweenDoesNotForgiveTheFailure()
    {
        var recovery = new RendererRecovery();
        recovery.Loading(Body);
        Assert.Equal(RecoveryAction.Reload, recovery.Failed(RendererFailure.RendererExited));
        recovery.Loading(Other);
        recovery.Loading(Body);
        Assert.Equal(RecoveryAction.GiveUp, recovery.Failed(RendererFailure.RendererExited));
    }

    // A dead browser takes the control: a new one, the document once more,
    // then a new control without it.
    [Fact]
    public void ADeadBrowserReplacesTheControl()
    {
        var recovery = new RendererRecovery();
        recovery.Loading(Body);
        Assert.Equal(RecoveryAction.ReplaceAndReload, recovery.Failed(RendererFailure.BrowserExited));
        recovery.Loading(Body);
        Assert.Equal(RecoveryAction.ReplaceAndGiveUp, recovery.Failed(RendererFailure.BrowserExited));
        // Without a document the control is still gone.
        Assert.Equal(RecoveryAction.Replace, recovery.Failed(RendererFailure.BrowserExited));
    }

    // The kinds count together: a document that killed its renderer and
    // then its browser has had its reload.
    [Fact]
    public void EveryKindOfFailureCountsForTheDocument()
    {
        var recovery = new RendererRecovery();
        recovery.Loading(Body);
        Assert.Equal(RecoveryAction.Reload, recovery.Failed(RendererFailure.RendererExited));
        recovery.Loading(Body);
        Assert.Equal(RecoveryAction.ReplaceAndGiveUp, recovery.Failed(RendererFailure.BrowserExited));
    }

    // The first report of a hang does nothing; the next one, without an
    // answer in between, replaces the control (which ends the renderer) and
    // shows the document again; the document hanging again is given up.
    [Fact]
    public void AHangIsActedOnWhenItIsReportedAgain()
    {
        Assert.Equal(2, RendererRecovery.UnresponsiveReports);
        var recovery = new RendererRecovery();
        recovery.Loading(Body);
        Assert.Equal(RecoveryAction.None, recovery.Failed(RendererFailure.Unresponsive));
        Assert.Equal(RecoveryAction.ReplaceAndReload, recovery.Failed(RendererFailure.Unresponsive));
        recovery.Loading(Body);
        Assert.Equal(RecoveryAction.None, recovery.Failed(RendererFailure.Unresponsive));
        Assert.Equal(RecoveryAction.ReplaceAndGiveUp, recovery.Failed(RendererFailure.Unresponsive));
    }

    // A renderer that answered after a report was only busy (a large
    // message laid out while the user clicks): its reports start again.
    [Fact]
    public void ARendererThatAnsweredIsNotHung()
    {
        var recovery = new RendererRecovery();
        recovery.Loading(Body);
        for (var i = 0; i < 5; i++)
        {
            Assert.Equal(RecoveryAction.None, recovery.Failed(RendererFailure.Unresponsive));
            recovery.Responsive();
        }
        // A new document starts the count again too.
        Assert.Equal(RecoveryAction.None, recovery.Failed(RendererFailure.Unresponsive));
        recovery.Loading(Other);
        Assert.Equal(RecoveryAction.None, recovery.Failed(RendererFailure.Unresponsive));
    }

    // Chromium reports a hang once and again only for further input
    // (measured): the view's host script that went unanswered for
    // AnswerTimeout after the report is the next report.
    [Fact]
    public void AnUnansweredScriptIsTheNextReport()
    {
        Assert.Equal(TimeSpan.FromSeconds(5), RendererRecovery.AnswerTimeout);
        var recovery = new RendererRecovery();
        recovery.Loading(Body);
        Assert.Equal(RecoveryAction.None, recovery.Failed(RendererFailure.Unresponsive));
        Assert.Equal(RecoveryAction.ReplaceAndReload, recovery.Unanswered());
        recovery.Loading(Body);
        Assert.Equal(RecoveryAction.None, recovery.Failed(RendererFailure.Unresponsive));
        Assert.Equal(RecoveryAction.ReplaceAndGiveUp, recovery.Unanswered());
    }

    // An unanswered script counts only for the hang it was sent for: not
    // after the renderer answered, the hang was acted on, or another
    // document started.
    [Fact]
    public void AnUnansweredScriptOfAnOldHangCountsForNothing()
    {
        var recovery = new RendererRecovery();
        Assert.Equal(RecoveryAction.None, recovery.Unanswered());
        recovery.Loading(Body);
        Assert.Equal(RecoveryAction.None, recovery.Unanswered());
        Assert.Equal(RecoveryAction.None, recovery.Failed(RendererFailure.Unresponsive));
        recovery.Responsive();
        Assert.Equal(RecoveryAction.None, recovery.Unanswered());
        Assert.Equal(RecoveryAction.None, recovery.Failed(RendererFailure.Unresponsive));
        Assert.Equal(RecoveryAction.ReplaceAndReload, recovery.Failed(RendererFailure.Unresponsive));
        Assert.Equal(RecoveryAction.None, recovery.Unanswered());
        recovery.Loading(Other);
        Assert.Equal(RecoveryAction.None, recovery.Failed(RendererFailure.Unresponsive));
        recovery.Loading(Body);
        Assert.Equal(RecoveryAction.None, recovery.Unanswered());
    }

    // A hung renderer with no document still ends with its control.
    [Fact]
    public void AHangWithoutADocumentReplacesTheControl()
    {
        var recovery = new RendererRecovery();
        recovery.Loading(Body);
        recovery.Dropped();
        Assert.Equal(RecoveryAction.None, recovery.Failed(RendererFailure.Unresponsive));
        Assert.Equal(RecoveryAction.Replace, recovery.Failed(RendererFailure.Unresponsive));
        // A dead renderer without a document: nothing to do.
        Assert.Equal(RecoveryAction.None, recovery.Failed(RendererFailure.RendererExited));
    }

    // A document dropped by the caller (the previewer's panel) is not
    // reloaded when the renderer of the empty page dies.
    [Fact]
    public void ADroppedDocumentIsNotReloaded()
    {
        var recovery = new RendererRecovery();
        recovery.Loading(Body);
        recovery.Dropped();
        Assert.Equal(RecoveryAction.None, recovery.Failed(RendererFailure.RendererExited));
        Assert.Equal(RecoveryAction.Replace, recovery.Failed(RendererFailure.BrowserExited));
    }

    [Fact]
    public void AnUnknownFailureIsRefused() =>
        Assert.Throws<ArgumentOutOfRangeException>(() => new RendererRecovery().Failed((RendererFailure)42));
}
