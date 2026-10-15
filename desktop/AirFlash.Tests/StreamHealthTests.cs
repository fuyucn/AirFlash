using AirFlash.Core;
using Xunit;
namespace AirFlash.Tests;

public sealed class StreamHealthTests
{
    private static TransportMetrics Transport(params MemberTransportMetrics[] members) => new(1000, 0, 0, 0, members);
    private static MemberTransportMetrics Member(long? sendErrors = null, long? dropped = null, long? backlogged = null, bool? feedbackDelayed = null) =>
        new("a", 100, sendErrors, 0, 0, 0, 0, 0, 0, 0, 4, 0, feedbackDelayed, 0, 150, false,
            backlogged, dropped, null, null, null, null);

    [Fact]
    public void NotPlayingIsUnknownRatherThanHealthy()
    {
        var health = StreamHealth.Evaluate(PlaybackState.Idle, null, null, false);
        Assert.Equal(HealthLevel.Unknown, health.Level);
        Assert.False(health.IsKnown);
    }

    [Fact]
    public void CleanStreamIsGood()
    {
        var health = StreamHealth.Evaluate(PlaybackState.Streaming, new StreamMetrics(), Transport(Member()), false);
        Assert.Equal(HealthLevel.Good, health.Level);
    }

    [Fact]
    public void LatencyAloneNeverDegradesHealth()
    {
        // A 500 ms profile delivering 500 ms is working as configured, so a deep
        // buffer with no loss must still read as healthy.
        var slow = new MemberTransportMetrics("a", 100, 0, 0, 0, 0, 0, 0, 0, 0, 120, 0, false, 0, 500, false,
            null, null, null, null, null, null);
        var health = StreamHealth.Evaluate(PlaybackState.Streaming, new StreamMetrics(), Transport(slow), false);
        Assert.Equal(HealthLevel.Good, health.Level);
    }

    [Fact]
    public void HeldBacklogIsStillGoodBecauseNothingWasLost()
    {
        var health = StreamHealth.Evaluate(PlaybackState.Streaming, new StreamMetrics(), Transport(Member(backlogged: 7)), false);
        Assert.Equal(HealthLevel.Good, health.Level);
        Assert.Contains("held", health.Reason, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public void DiscardedAudioRaisesAttention()
    {
        var health = StreamHealth.Evaluate(PlaybackState.Streaming, new StreamMetrics(), Transport(Member(dropped: 3)), false);
        Assert.Equal(HealthLevel.Attention, health.Level);
    }

    [Fact]
    public void LostAudioDegrades()
    {
        Assert.Equal(HealthLevel.Degraded, StreamHealth.Evaluate(PlaybackState.Streaming, new StreamMetrics(), Transport(Member(sendErrors: 2)), false).Level);
        Assert.Equal(HealthLevel.Degraded, StreamHealth.Evaluate(PlaybackState.Streaming, new StreamMetrics(Underruns: 1), Transport(Member()), false).Level);
        Assert.Equal(HealthLevel.Degraded, StreamHealth.Evaluate(PlaybackState.Streaming, null, new TransportMetrics(1000, 2, 5, 0, [Member()]), false).Level);
    }

    [Fact]
    public void UnresponsiveFeedbackDegradesRegardlessOfCounters()
    {
        var health = StreamHealth.Evaluate(PlaybackState.Streaming, new StreamMetrics(), Transport(Member()), true);
        Assert.Equal(HealthLevel.Degraded, health.Level);
        Assert.Contains("feedback", health.Reason, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public void StandbyIsStillGraded()
    {
        var health = StreamHealth.Evaluate(PlaybackState.Standby, new StreamMetrics(), Transport(Member()), false);
        Assert.Equal(HealthLevel.Good, health.Level);
    }

    [Fact]
    public void WorstConditionWins()
    {
        // Loss outranks discarded audio, so the reason names the graver condition.
        var health = StreamHealth.Evaluate(PlaybackState.Streaming, new StreamMetrics(Underruns: 1), Transport(Member(dropped: 9)), false);
        Assert.Equal(HealthLevel.Degraded, health.Level);
    }
}
