// AirFlash probe - WPF, the UI stack AirFlash uses today.
//
// Same measurement as the mygo app: the engine's 7.98 ms send cadence, run
// inside the UI process, so the two logs are directly comparable on Windows.
using System.Diagnostics;
using System.IO;
using System.Runtime.InteropServices;
using System.Text.Json;
using System.Globalization;
using System.Windows;
using System.Windows.Media;
using System.Windows.Controls;
using System.Windows.Shapes;

namespace WpfProbe;

internal static class Mem
{
    [StructLayout(LayoutKind.Sequential)]
    private struct Counters
    {
        public uint Cb;
        public uint PageFaultCount;
        public UIntPtr PeakWorkingSetSize;
        public UIntPtr WorkingSetSize;
        public UIntPtr QuotaPeakPagedPoolUsage;
        public UIntPtr QuotaPagedPoolUsage;
        public UIntPtr QuotaPeakNonPagedPoolUsage;
        public UIntPtr QuotaNonPagedPoolUsage;
        public UIntPtr PagefileUsage;
        public UIntPtr PeakPagefileUsage;
    }

    [DllImport("psapi.dll", SetLastError = true)]
    private static extern bool GetProcessMemoryInfo(IntPtr h, ref Counters c, uint cb);

    internal static (long workingSet, long priv) Current()
    {
        var c = new Counters { Cb = (uint)Marshal.SizeOf<Counters>() };
        if (!GetProcessMemoryInfo(Process.GetCurrentProcess().Handle, ref c, c.Cb))
            return (0, 0);
        return ((long)c.WorkingSetSize.ToUInt64(), (long)c.PagefileUsage.ToUInt64());
    }
}

internal sealed class AutoReport
{
    public string language { get; set; } = "csharp";
    public string ui { get; set; } = "WPF";
    public int cpus { get; set; }
    public long working_set_bytes { get; set; }
    public long private_bytes { get; set; }
    public double p50_ms { get; set; }
    public double p95_ms { get; set; }
    public double p99_ms { get; set; }
    public double max_ms { get; set; }
    public double over_one_slot_pct { get; set; }
    public int stalls_over_20ms { get; set; }
    public string note { get; set; } = "";
}

public partial class MainWindow : Window
{
    private const double SlotMs = 7.98;
    private const double SpinMs = 0.3;

    private readonly object gate = new();
    private readonly List<double> series = new();
    private volatile bool stopRequested;
    private volatile bool running;

    private double p50, p99, max, overPct;
    private int done, total;
    private string status = "idle";
    private int stalls;

    public MainWindow()
    {
        InitializeComponent();
        Loaded += OnLoaded;
        EnvText.Text = string.Format(CultureInfo.InvariantCulture,
            "{0} logical CPUs on {1} · WPF · .NET {2}",
            Environment.ProcessorCount, Environment.OSVersion.VersionString,
            Environment.Version);
        DrawChart();
    }

    // Auto mode for CI: open, settle, measure under load, report and exit.
    private async void OnLoaded(object sender, RoutedEventArgs e)
    {
        var args = Environment.GetCommandLineArgs();
        var idx = Array.IndexOf(args, "-auto");
        if (idx < 0 || idx + 1 >= args.Length) return;

        var outPath = args[idx + 1];
        var slots = 800;
        var sIdx = Array.IndexOf(args, "-slots");
        if (sIdx >= 0 && sIdx + 1 < args.Length) int.TryParse(args[sIdx + 1], out slots);
        var burn = Environment.ProcessorCount;
        var bIdx = Array.IndexOf(args, "-burn");
        if (bIdx >= 0 && bIdx + 1 < args.Length) int.TryParse(args[bIdx + 1], out burn);

        await Task.Delay(3000); // let the window paint
        var t = Task.Run(() => Measure(slots, burn, $"burn={burn}"));
        await t;
        await Dispatcher.BeginInvoke(() =>
        {
            var (ws, priv) = Mem.Current();
            var rep = new AutoReport
            {
                cpus = Environment.ProcessorCount,
                working_set_bytes = ws,
                private_bytes = priv,
                p50_ms = p50, p95_ms = p50, p99_ms = p99, max_ms = max,
                over_one_slot_pct = overPct, stalls_over_20ms = stalls,
                note = $"in-process with live UI, {done} slots, burn={burn}",
            };
            var json = JsonSerializer.Serialize(rep, new JsonSerializerOptions { WriteIndented = true });
            File.WriteAllText(outPath, json);
            Console.WriteLine(json);
            Application.Current.Shutdown();
        });
    }

    private void RunIdle_Click(object sender, RoutedEventArgs e) => Start(800, 0);

    private void RunBurn_Click(object sender, RoutedEventArgs e) =>
        Start(800, Environment.ProcessorCount);

    private void Stop_Click(object sender, RoutedEventArgs e) => stopRequested = true;

    private void Start(int slots, int burn)
    {
        if (running) return;
        running = true;
        stopRequested = false;
        lock (gate) { series.Clear(); }
        stalls = 0;
        _ = Task.Run(() => Measure(slots, burn, $"burn={burn}"));
    }

    private void Measure(int slots, int burn, string phase)
    {
        using var done = new CancellationTokenSource();
        var threads = new List<Thread>();
        for (var i = 0; i < burn; i++)
        {
            var t = new Thread(() =>
            {
                long x = 0;
                while (!done.IsCancellationRequested)
                    for (long k = 0; k < 20000; k++) x += k * k;
                if (x == 1) Console.Error.WriteLine(x);
            })
            { IsBackground = true };
            t.Start();
            threads.Add(t);
        }
        Thread.Sleep(200);

        var late = new List<double>(slots);
        // The audio loop pins itself to one OS thread; do the same.
        var loop = new Thread(() =>
        {
            var sw = Stopwatch.StartNew();
            var deadline = sw.Elapsed.TotalMilliseconds;
            for (var i = 0; i < slots && !stopRequested; i++)
            {
                deadline += SlotMs;
                while (true)
                {
                    var d = deadline - sw.Elapsed.TotalMilliseconds;
                    if (d <= 0) break;
                    if (d > SpinMs) Thread.Sleep(TimeSpan.FromMilliseconds(Math.Min(d - SpinMs, 0.5)));
                }
                var l = sw.Elapsed.TotalMilliseconds - deadline;
                if (l > 20) Interlocked.Increment(ref stalls);
                late.Add(l);

                if (i % 20 == 0)
                {
                    var snap = new List<double>(late);
                    Publish(phase, snap, i, slots);
                }
            }
        });
        loop.Start();
        loop.Join();

        done.Cancel();
        foreach (var t in threads) t.Join(2000);

        Publish(phase + " DONE", late, late.Count, slots);
        running = false;
    }

    private void Publish(string phase, List<double> late, int doneCount, int totalCount)
    {
        var sorted = new List<double>(late);
        sorted.Sort();
        double At(double q) => sorted.Count == 0 ? 0 : sorted[(int)Math.Round(q * (sorted.Count - 1))];
        var over = sorted.Count(v => v > SlotMs);
        var pct = sorted.Count == 0 ? 0 : 100.0 * over / sorted.Count;

        Dispatcher.BeginInvoke(() =>
        {
            done = doneCount;
            total = totalCount;
            p50 = At(0.50);
            p99 = At(0.99);
            max = At(1);
            overPct = pct;
            status = $"{phase} · {done}/{total} slots · {stalls} stalls > 20 ms";

            StatsText.Text = string.Format(CultureInfo.InvariantCulture,
                "p50 {0:F3} ms   p99 {1:F3} ms   max {2:F3} ms   late {3:F1}%",
                p50, p99, max, overPct);
            StatusText.Text = status;

            lock (gate)
            {
                series.Add(sorted.Count > 0 ? sorted[^1] : 0);
                if (series.Count > 300) series.RemoveAt(0);
            }
            DrawChart();
        });
    }

    // Redraws the cadence; anything over the 7.98 ms deadline is drawn red.
    private void DrawChart()
    {
        Chart.Children.Clear();
        double[] data;
        lock (gate) { data = series.ToArray(); }

        var w = Math.Max(200, ActualWidth - 60);
        const double h = 160;
        const double ceiling = 12.0;

        // The 7.98 ms deadline, so a miss is visible at a glance.
        var deadlineY = h - (SlotMs / ceiling) * h;
        Chart.Children.Add(new Line
        {
            X1 = 0, X2 = w, Y1 = deadlineY, Y2 = deadlineY,
            Stroke = new SolidColorBrush(Color.FromRgb(0x99, 0x99, 0x99)),
            StrokeDashArray = new DoubleCollection { 6, 4 },
            StrokeThickness = 1,
        });
        var label = new TextBlock
        {
            Text = $"{SlotMs} ms deadline",
            FontSize = 11,
            Foreground = new SolidColorBrush(Color.FromRgb(0x77, 0x77, 0x77)),
        };
        Canvas.SetLeft(label, 2);
        Canvas.SetTop(label, Math.Max(0, deadlineY - 16));
        Chart.Children.Add(label);

        if (data.Length == 0) return;

        var step = w / Math.Max(1, data.Length);
        var barW = Math.Max(1.0, Math.Min(4.0, step - 1));
        for (var i = 0; i < data.Length; i++)
        {
            var v = Math.Min(data[i], ceiling);
            var barH = Math.Max(2.0, (v / ceiling) * h);
            var missed = data[i] > SlotMs;
            var bar = new Rectangle
            {
                Width = barW,
                Height = barH,
                Fill = new SolidColorBrush(missed
                    ? Color.FromRgb(0xDC, 0x26, 0x26)
                    : Color.FromRgb(0x25, 0x63, 0xEB)),
            };
            Canvas.SetLeft(bar, i * step);
            Canvas.SetTop(bar, h - barH);
            Chart.Children.Add(bar);
        }
    }
}
