namespace AirFlash.Core;

/// <summary>
/// Audible-delay estimate assembled from parts that are actually observed.
///
/// Every component is either reported by the receiver or measured locally. The
/// total is an estimate, not an acoustic measurement: it excludes the receiver's
/// own output path and any host audio processing before capture.
/// </summary>
public sealed record LatencyEstimate(double? Target, double? JitterBuffer, bool JitterBufferEstimated,
    double? CaptureDelay, double? NetworkRoundTrip, double? Total, int Members)
{
    /// <summary>True when no component has been observed yet.</summary>
    public bool IsEmpty => JitterBuffer is null && CaptureDelay is null && NetworkRoundTrip is null;

    /// <summary>
    /// Estimate the delay for a session.
    ///
    /// The jitter buffer is the receiver's own playback delay. The capture delay
    /// is the local queue the sender adds. Half the feedback round trip stands in
    /// for one-way network transit. Only observed parts contribute, so a missing
    /// measurement lowers the total instead of inventing a value.
    /// </summary>
    public static LatencyEstimate From(int targetLatency, StreamMetrics? capture, TransportMetrics? transport)
    {
        var members = transport?.Members ?? [];
        // The deepest buffer bounds audible delay when members differ.
        var deepest = members.Where(m => m.ReceiverLatencyMs is not null)
            .OrderByDescending(m => m.ReceiverLatencyMs!.Value)
            .FirstOrDefault();
        var buffer = deepest?.ReceiverLatencyMs;
        var rtt = members.Where(m => m.FeedbackRttMs is not null)
            .Select(m => m.FeedbackRttMs!.Value)
            .DefaultIfEmpty(double.NaN)
            .Max();
        var measuredRtt = double.IsNaN(rtt) ? (double?)null : rtt;
        var localCapture = capture?.CaptureToSendP95;
        double? total = buffer is null ? null : buffer;
        if (total is not null && localCapture is { } local) total += local;
        if (total is not null && measuredRtt is { } roundTrip) total += roundTrip / 2;
        return new(targetLatency > 0 ? targetLatency : null, buffer, deepest?.ReceiverLatencyEstimated == true,
            localCapture, measuredRtt, total, members.Count);
    }
}
