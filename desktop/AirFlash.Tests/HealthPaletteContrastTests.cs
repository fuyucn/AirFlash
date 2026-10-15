using AirFlash.Core;
using Xunit;
namespace AirFlash.Tests;

/// <summary>
/// The health chip carries meaning, so its label must stay legible on the tinted
/// background. These thresholds are the WCAG AA contrast floor; a colour that
/// fails is not usable as a status indicator.
/// </summary>
public sealed class HealthPaletteContrastTests
{
    // Palette values mirrored from ThemeService for both themes.
    private static readonly (string Name, double R, double G, double B)[] Dark =
    [
        ("Success", 0x5F, 0xD3, 0x9A), ("Warning", 0xE0, 0xA4, 0x58), ("Error", 0xFF, 0x9A, 0xA5),
    ];
    private static readonly (string Name, double R, double G, double B)[] Light =
    [
        ("Success", 0x12, 0x6B, 0x3E), ("Warning", 0x9A, 0x5B, 0x00), ("Error", 0xC3, 0x2D, 0x40),
    ];
    private const double DarkSurfaceR = 0x1B, DarkSurfaceG = 0x22, DarkSurfaceB = 0x31;
    private const double LightSurface = 255;
    private static readonly double DarkTintAlpha = 0x2E / 255d;
    private static readonly double LightTintAlpha = 0x1F / 255d;

    [Theory]
    [InlineData(true)]
    [InlineData(false)]
    public void EveryHealthColourIsLegibleOnItsOwnChip(bool dark)
    {
        var entries = dark ? Dark : Light;
        var surface = dark ? (DarkSurfaceR, DarkSurfaceG, DarkSurfaceB) : (LightSurface, LightSurface, LightSurface);
        var alpha = dark ? DarkTintAlpha : LightTintAlpha;
        foreach (var (name, r, g, b) in entries)
        {
            var foreground = (r, g, b);
            // The chip background is the accent at chip alpha composited over the card.
            var background = (r * alpha + surface.Item1 * (1 - alpha),
                              g * alpha + surface.Item2 * (1 - alpha),
                              b * alpha + surface.Item3 * (1 - alpha));
            var contrast = Contrast(foreground, background);
            Assert.True(contrast >= 4.5, $"{name} contrast in {(dark ? "dark" : "light")} is {contrast:F2}:1");
        }
    }

    [Fact]
    public void HealthGradesLossButNotLatency()
    {
        // Guards the design decision: a deep buffer with no loss stays healthy.
        var slowButClean = new MemberTransportMetrics("a", 100, 0, 0, 0, 0, 0, 0, 0, 0, 120, 0, false, 0, 500, false,
            null, null, null, null, null, null);
        var transport = new TransportMetrics(1000, 0, 0, 0, [slowButClean]);
        Assert.Equal(HealthLevel.Good, StreamHealth.Evaluate(PlaybackState.Streaming, new StreamMetrics(), transport, false).Level);
    }

    private static double Contrast((double R, double G, double B) a, (double R, double G, double B) b)
    {
        var la = Luminance(a);
        var lb = Luminance(b);
        return (Math.Max(la, lb) + 0.05) / (Math.Min(la, lb) + 0.05);
    }

    private static double Luminance((double R, double G, double B) color) =>
        0.2126 * Channel(color.R) + 0.7152 * Channel(color.G) + 0.0722 * Channel(color.B);

    private static double Channel(double value)
    {
        var v = value / 255d;
        return v <= 0.03928 ? v / 12.92 : Math.Pow((v + 0.055) / 1.055, 2.4);
    }
}
