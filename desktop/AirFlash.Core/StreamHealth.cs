namespace AirFlash.Core;

/// <summary>Coarse stream condition. Order is meaningful: worse values rank higher.</summary>
public enum HealthLevel { Good, Attention, Degraded, Unknown }

/// <summary>
/// Whether the stream is healthy, derived only from conditions the sender can
/// actually observe.
///
/// Latency itself is never graded: a 500 ms profile that delivers 500 ms is
/// working as configured, so judging it would contradict the user's own choice.
/// What is graded is loss and recovery the sender can see directly — dropped
/// backlog, skipped slots, capture underruns, unresponsive feedback.
/// </summary>
public sealed record StreamHealth(HealthLevel Level, string Reason)
{
    public bool IsKnown => Level != HealthLevel.Unknown;

    public static StreamHealth Evaluate(PlaybackState state, StreamMetrics? capture, TransportMetrics? transport, bool feedbackDelayed)
    {
        if (state is not (PlaybackState.Streaming or PlaybackState.Standby)) return new(HealthLevel.Unknown, "Not playing");
        if (feedbackDelayed) return new(HealthLevel.Degraded, "Receiver feedback is not responding");

        var members = transport?.Members ?? [];
        var dropped = members.Sum(m => m.Dropped ?? 0);
        var sendErrors = members.Sum(m => m.SendErrors ?? 0);
        var skipped = transport?.SkippedPackets ?? 0;
        var recoveries = transport?.SenderLateRecoveries ?? 0;
        var underruns = capture?.Underruns ?? 0;
        var frameDrops = capture?.DroppedFrames ?? 0;

        // Loss the receiver never got is the condition that becomes audible.
        if (skipped > 0 || underruns > 0 || sendErrors > 0) return new(HealthLevel.Degraded, "Audio was lost and the timeline had to recover");
        if (dropped > 0 || frameDrops > 0 || recoveries > 0) return new(HealthLevel.Attention, "Some audio was discarded while recovering");

        var buffered = members.Any(m => (m.Backlogged ?? 0) > 0);
        if (buffered) return new(HealthLevel.Good, "Send path is briefly full; audio is being held, not lost");
        return new(HealthLevel.Good, "No loss detected");
    }
}
