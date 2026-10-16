// AirFlash latency probe - C# implementation.
//
// Same cadence and measurement as the Go and Rust probes. `burn` here means
// competing .NET threads. The C# layer is AirFlash's UI host, so this shows
// what the UI process itself does to a shared deadline.
using System.Diagnostics;
using System.Globalization;
using System.Net.Sockets;
using System.Text.Json;

const double SlotMs = 7.98;
const double SpinMs = 0.3;
const int Packet = 352 * 2 * 2;

int slots = 1500, burn = 0;
var mode = "sleep";
var udp = false;
var json = false;
for (var i = 0; i < args.Length; i++)
{
    switch (args[i])
    {
        case "-slots": slots = int.Parse(args[++i], CultureInfo.InvariantCulture); break;
        case "-burn": burn = int.Parse(args[++i], CultureInfo.InvariantCulture); break;
        case "-mode": mode = args[++i]; break;
        case "-udp": udp = true; break;
        case "-json": json = true; break;
    }
}

var stop = new CancellationTokenSource();
var threads = new List<Thread>();
for (var i = 0; i < burn; i++)
{
    var t = new Thread(() =>
    {
        long x = 0;
        while (!stop.IsCancellationRequested)
            for (long k = 0; k < 20000; k++) x += k * k;
        if (x == 1) Console.Error.WriteLine(x);
    })
    { IsBackground = true };
    t.Start();
    threads.Add(t);
}
Thread.Sleep(200);

// A real local listener, so the send path matches the Go probe exactly and
// no ICMP port-unreachable can turn into a spurious send error.
UdpClient? sender = null;
if (udp)
{
    var receiver = new UdpClient(new System.Net.IPEndPoint(System.Net.IPAddress.Loopback, 0));
    var receiverAddr = (System.Net.IPEndPoint)receiver.Client.LocalEndPoint!;
    _ = Task.Run(() =>
    {
        var remote = new System.Net.IPEndPoint(System.Net.IPAddress.Any, 0);
        while (true)
        {
            try { receiver.Receive(ref remote); }
            catch { return; }
        }
    });
    sender = new UdpClient(new System.Net.IPEndPoint(System.Net.IPAddress.Loopback, 0));
    sender.Connect(receiverAddr);
}

var payload = new byte[Packet];
var late = new List<double>(slots);
var stalls = 0;
var sw = Stopwatch.StartNew();
var deadline = sw.Elapsed.TotalMilliseconds;

for (var i = 0; i < slots; i++)
{
    deadline += SlotMs;
    if (mode == "spin")
    {
        while (sw.Elapsed.TotalMilliseconds < deadline) { }
    }
    else
    {
        while (true)
        {
            var d = deadline - sw.Elapsed.TotalMilliseconds;
            if (d <= 0) break;
            if (d > SpinMs) Thread.Sleep(TimeSpan.FromMilliseconds(Math.Min(d - SpinMs, 0.5)));
        }
    }
    if (sender is not null) sender.Send(payload);
    var l = sw.Elapsed.TotalMilliseconds - deadline;
    if (l > 20.0) stalls++;
    late.Add(l);
}

stop.Cancel();
foreach (var t in threads) t.Join(2000);
sender?.Dispose();

late.Sort();
double At(double q) => late[(int)(q * (late.Count - 1))];
var over = late.Count(l => l > SlotMs);
var pct = 100.0 * over / late.Count;

if (json)
{
    var result = new
    {
        language = "csharp",
        os = Environment.OSVersion.ToString(),
        cpus = Environment.ProcessorCount,
        mode, burn, udp, slots,
        p50_ms = At(0.50), p95_ms = At(0.95), p99_ms = At(0.99),
        max_ms = late[^1],
        over_one_slot_pct = pct,
        stalls_over_20ms = stalls,
    };
    Console.WriteLine(JsonSerializer.Serialize(result,
        new JsonSerializerOptions { WriteIndented = true }));
}
else
{
    Console.WriteLine(string.Format(CultureInfo.InvariantCulture,
        "c#   mode={0,-5} burn={1,-3} udp={2,-5} | p50={3,7:F3} p95={4,7:F3} p99={5,7:F3} max={6,8:F3} ms | late>1slot {7,5:F1}% | stalls>20ms {8}",
        mode, burn, udp, At(0.50), At(0.95), At(0.99), late[^1], pct, stalls));
}
