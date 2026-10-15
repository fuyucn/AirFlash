using AirFlash.Core;
using Xunit;
namespace AirFlash.Tests;

public sealed class LatencyEstimateTests
{
    private static TransportMetrics Transport(params MemberTransportMetrics[] members) => new(1000, 0, 0, 0, members);
    private static MemberTransportMetrics Member(string host = "a", double? latency = 200, bool estimated = false, double? rtt = 4) =>
        new(host, 10, 0, 0, 0, 0, 0, 0, 0, 0, rtt, 0, false, 0, latency, estimated,
            null, null, null, null, null, null);

    [Fact]
    public void SumsObservedPartsAndHalvesTheRoundTrip()
    {
        var estimate = LatencyEstimate.From(120, new StreamMetrics(CaptureToSendP95: 10.8), Transport(Member(latency: 120, rtt: 4)));
        Assert.Equal(120, estimate.Target);
        Assert.Equal(120, estimate.JitterBuffer);
        Assert.False(estimate.JitterBufferEstimated);
        Assert.Equal(10.8, estimate.CaptureDelay);
        Assert.Equal(4, estimate.NetworkRoundTrip);
        // 120 buffer + 10.8 capture + 2 ms half round trip
        Assert.Equal(132.8, estimate.Total);
    }

    [Fact]
    public void MissingPartsLowerTheTotalInsteadOfInventingValues()
    {
        // No capture metrics and no feedback yet: only the receiver buffer is known.
        var estimate = LatencyEstimate.From(200, null, Transport(Member(latency: 200, rtt: null)));
        Assert.Equal(200, estimate.Total);
        Assert.Null(estimate.CaptureDelay);
        Assert.Null(estimate.NetworkRoundTrip);
        Assert.False(estimate.IsEmpty);
    }

    [Fact]
    public void NoObservationsMeansNoEstimate()
    {
        var estimate = LatencyEstimate.From(200, null, null);
        Assert.Null(estimate.Total);
        Assert.Null(estimate.JitterBuffer);
        Assert.True(estimate.IsEmpty);
    }

    [Fact]
    public void DeepestBufferBoundsDelayAcrossMembers()
    {
        // A stereo pair can negotiate different buffers; the larger one is audible.
        var estimate = LatencyEstimate.From(200, null, Transport(Member("a", 120), Member("b", 500)));
        Assert.Equal(500, estimate.JitterBuffer);
        Assert.Equal(2, estimate.Members);
    }

    [Fact]
    public void LargestRoundTripIsUsed()
    {
        var estimate = LatencyEstimate.From(200, null, Transport(Member("a", 200, rtt: 4), Member("b", 200, rtt: 40)));
        Assert.Equal(40, estimate.NetworkRoundTrip);
        Assert.Equal(220, estimate.Total);
    }

    [Fact]
    public void RequestedFallbackIsLabelledRatherThanPresentedAsMeasured()
    {
        var estimate = LatencyEstimate.From(200, null, Transport(Member(latency: 200, estimated: true, rtt: null)));
        Assert.True(estimate.JitterBufferEstimated);
        Assert.Equal(200, estimate.Total);
    }

    [Fact]
    public void AbsentTargetStaysUnknown()
    {
        Assert.Null(LatencyEstimate.From(0, null, Transport(Member())).Target);
    }
}
