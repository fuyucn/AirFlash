using AirFlash.Core;
using Xunit;
namespace AirFlash.Tests;

public sealed class MonitorHistoryTests
{
    private static MonitorSample Sample(double seconds, double? latency = null, double? rtt = null,
        double? capture = null, double? backlog = null) => new(seconds, latency, rtt, capture, backlog);

    [Fact]
    public void KeepsOnlyTheMostRecentSamples()
    {
        var history = new MonitorHistory();
        for (var i = 0; i < MonitorHistory.Capacity + 25; i++) history.Add(Sample(i, latency: i));
        Assert.Equal(MonitorHistory.Capacity, history.Count);
        // The oldest samples were discarded, so the first retained one is offset.
        Assert.Equal(25, history.Samples.First().Seconds);
        Assert.Equal(MonitorHistory.Capacity + 24, history.Samples.Last().Seconds);
    }

    [Fact]
    public void PreservesGapsRatherThanInventingZero()
    {
        var history = new MonitorHistory();
        history.Add(Sample(0, latency: 120));
        history.Add(Sample(1));                 // no measurement this second
        history.Add(Sample(2, latency: 140));
        var values = history.Values(MonitorSeries.Latency);
        Assert.Equal([120d, null, 140d], values);
        Assert.Equal((120d, 140d), history.Range(MonitorSeries.Latency));
    }

    [Fact]
    public void SeriesAreIndependent()
    {
        var history = new MonitorHistory();
        history.Add(Sample(0, latency: 120, rtt: 4, capture: 10, backlog: 3));
        Assert.Equal([120d], history.Values(MonitorSeries.Latency));
        Assert.Equal([4d], history.Values(MonitorSeries.RoundTrip));
        Assert.Equal([10d], history.Values(MonitorSeries.Capture));
        Assert.Equal([3d], history.Values(MonitorSeries.Backlog));
    }

    [Fact]
    public void SpanAndRangeReportNothingWithoutSamples()
    {
        var history = new MonitorHistory();
        Assert.Equal(0, history.SpanSeconds);
        Assert.Null(history.Range(MonitorSeries.Latency));
        history.Add(Sample(5, latency: 100));
        Assert.Equal(0, history.SpanSeconds);   // one sample spans no time
        history.Add(Sample(12, latency: 100));
        Assert.Equal(7, history.SpanSeconds);
    }

    [Fact]
    public void ClearEmptiesTheBuffer()
    {
        var history = new MonitorHistory();
        history.Add(Sample(0, latency: 120));
        var added = history.Revision;
        history.Clear();
        Assert.Equal(0, history.Count);
        Assert.Empty(history.Values(MonitorSeries.Latency));
        Assert.True(history.Revision > added);
    }

    [Fact]
    public void RevisionChangesWhenAFullBufferReceivesANewSample()
    {
        var history = new MonitorHistory();
        for (var i = 0; i < MonitorHistory.Capacity; i++) history.Add(Sample(i, latency: i));
        var count = history.Count;
        var revision = history.Revision;
        history.Add(Sample(MonitorHistory.Capacity, latency: 200));
        Assert.Equal(count, history.Count);
        Assert.True(history.Revision > revision);
        Assert.Equal(200, history.Values(MonitorSeries.Latency).Last());
    }

    [Fact]
    public void NonFiniteValuesAreTreatedAsGaps()
    {
        var history = new MonitorHistory();
        history.Add(Sample(0, latency: double.NaN));
        history.Add(Sample(1, latency: 100));
        // The NaN sample must not corrupt the range of the real measurement.
        Assert.Equal((100d, 100d), history.Range(MonitorSeries.Latency));
    }
}
