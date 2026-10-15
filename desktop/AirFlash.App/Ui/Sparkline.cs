using System.Windows;
using System.Windows.Media;
using AirFlash.Core;

namespace AirFlash.App.Ui;

/// <summary>
/// Minimal trend line for a monitoring series.
///
/// Drawn directly rather than through a charting dependency: the surface is a
/// few hundred points at most, and a retained geometry keeps redraws cheap when
/// the panel is open during playback. Gaps stay gaps, so a missing measurement
/// is never drawn as a value of zero.
/// </summary>
public sealed class Sparkline : FrameworkElement
{
    public static readonly DependencyProperty ValuesProperty = DependencyProperty.Register(
        nameof(Values), typeof(IReadOnlyList<double?>), typeof(Sparkline),
        new FrameworkPropertyMetadata(null, FrameworkPropertyMetadataOptions.AffectsRender));
    public static readonly DependencyProperty StrokeProperty = DependencyProperty.Register(
        nameof(Stroke), typeof(Brush), typeof(Sparkline),
        new FrameworkPropertyMetadata(Brushes.SteelBlue, FrameworkPropertyMetadataOptions.AffectsRender));
    public static readonly DependencyProperty BaselineProperty = DependencyProperty.Register(
        nameof(Baseline), typeof(double?), typeof(Sparkline),
        new FrameworkPropertyMetadata(null, FrameworkPropertyMetadataOptions.AffectsRender));
    public static readonly DependencyProperty FillProperty = DependencyProperty.Register(
        nameof(Fill), typeof(Brush), typeof(Sparkline),
        new FrameworkPropertyMetadata(null, FrameworkPropertyMetadataOptions.AffectsRender));

    public IReadOnlyList<double?>? Values { get => (IReadOnlyList<double?>?)GetValue(ValuesProperty); set => SetValue(ValuesProperty, value); }
    public Brush Stroke { get => (Brush)GetValue(StrokeProperty); set => SetValue(StrokeProperty, value); }
    /// <summary>Optional reference line, such as the configured target latency.</summary>
    public double? Baseline { get => (double?)GetValue(BaselineProperty); set => SetValue(BaselineProperty, value); }
    public Brush? Fill { get => (Brush?)GetValue(FillProperty); set => SetValue(FillProperty, value); }

    protected override void OnRender(DrawingContext context)
    {
        var width = ActualWidth;
        var height = ActualHeight;
        if (width <= 1 || height <= 1) return;
        var values = Values;
        if (values is null || values.Count == 0) return;

        // A flat series still needs a visible band, so widen a zero-height range.
        var observed = values.Where(v => v is not null && double.IsFinite(v.Value)).Select(v => v!.Value).ToArray();
        if (observed.Length == 0) return;
        var min = observed.Min();
        var max = observed.Max();
        if (Baseline is { } baseline) { min = Math.Min(min, baseline); max = Math.Max(max, baseline); }
        if (max - min < 1) { max += 1; min -= 1; }
        else { var padding = (max - min) * 0.12; min -= padding; max += padding; }
        var span = max - min;

        double Y(double value) => height - (value - min) / span * height;
        // Inset the horizontal edges so the first and last points stay visible.
        var step = values.Count > 1 ? width / (values.Count - 1) : 0;

        if (Baseline is { } line)
        {
            var y = Y(line);
            var pen = new Pen(Stroke, 1) { DashStyle = new DashStyle([3, 3], 0) };
            pen.Freeze();
            context.DrawLine(pen, new Point(0, y), new Point(width, y));
        }

        // Break the path wherever a sample is missing instead of interpolating across it.
        var geometry = new StreamGeometry();
        using (var figure = geometry.Open())
        {
            var open = false;
            for (var i = 0; i < values.Count; i++)
            {
                if (values[i] is not { } value || !double.IsFinite(value)) { open = false; continue; }
                var point = new Point(i * step, Y(value));
                if (open) figure.LineTo(point, true, false);
                else { figure.BeginFigure(point, false, false); open = true; }
            }
        }
        geometry.Freeze();
        var linePen = new Pen(Stroke, 1.6) { LineJoin = PenLineJoin.Round, StartLineCap = PenLineCap.Round, EndLineCap = PenLineCap.Round };
        linePen.Freeze();
        context.DrawGeometry(null, linePen, geometry);
    }
}
