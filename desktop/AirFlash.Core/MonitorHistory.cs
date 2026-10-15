using System.Collections.ObjectModel;

namespace AirFlash.Core;

/// <summary>One monitoring sample, taken about once per second during playback.</summary>
public sealed record MonitorSample(double Seconds, double? LatencyMs, double? RoundTripMs,
    double? CaptureMs, double? BacklogPackets);

/// <summary>Series a chart can draw. Missing samples stay null so gaps remain visible.</summary>
public enum MonitorSeries { Latency, RoundTrip, Capture, Backlog }

/// <summary>
/// Bounded rolling history of monitoring samples for the session in progress.
///
/// Kept deliberately small: the Monitor page draws a short trend, not a
/// recording, so the buffer is fixed and the oldest sample is discarded.
/// </summary>
public sealed class MonitorHistory
{
    public const int Capacity = 120;
    private readonly Queue<MonitorSample> _samples = new(Capacity);
    public IReadOnlyCollection<MonitorSample> Samples => _samples;
    public int Count => _samples.Count;

    /// <summary>Add one sample, discarding the oldest once the buffer is full.</summary>
    public void Add(MonitorSample sample)
    {
        while (_samples.Count >= Capacity) _samples.Dequeue();
        _samples.Enqueue(sample);
    }

    /// <summary>Drop everything; used when a new session starts.</summary>
    public void Clear() => _samples.Clear();

    /// <summary>Values for one series, oldest first, preserving gaps as null.</summary>
    public IReadOnlyList<double?> Values(MonitorSeries series) => series switch
    {
        MonitorSeries.Latency => _samples.Select(s => s.LatencyMs).ToArray(),
        MonitorSeries.RoundTrip => _samples.Select(s => s.RoundTripMs).ToArray(),
        MonitorSeries.Capture => _samples.Select(s => s.CaptureMs).ToArray(),
        _ => _samples.Select(s => s.BacklogPackets).ToArray(),
    };

    /// <summary>Seconds spanned by the retained samples, or zero when fewer than two.</summary>
    public double SpanSeconds => _samples.Count < 2 ? 0 : _samples.Last().Seconds - _samples.First().Seconds;

    /// <summary>Lowest and highest observed value for a series, ignoring gaps.</summary>
    public (double Min, double Max)? Range(MonitorSeries series)
    {
        double min = double.MaxValue, max = double.MinValue;
        var any = false;
        foreach (var value in Values(series))
        {
            if (value is not { } number || !double.IsFinite(number)) continue;
            min = Math.Min(min, number); max = Math.Max(max, number); any = true;
        }
        return any ? (min, max) : null;
    }
}
