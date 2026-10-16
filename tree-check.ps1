# Verifies whether WebView2 shows up as child processes we should count.
$p = Start-Process -FilePath 'artifacts/probe/AirFlash Probe.exe' -ArgumentList @('-auto','artifacts/probe/tree-test.json','-slots','300','-burn','0') -PassThru
Start-Sleep -Seconds 4
$all = Get-CimInstance Win32_Process | Select-Object ProcessId,ParentProcessId,Name,WorkingSetSize
function Descendants($root, $all) {
  $out = @()
  $queue = @($root)
  while ($queue.Count -gt 0) {
    $cur = $queue[0]; $queue = @($queue[1..($queue.Count-1)])
    foreach ($c in $all | Where-Object { $_.ParentProcessId -eq $cur }) {
      $out += $c
      $queue += $c.ProcessId
    }
  }
  return $out
}
$kids = Descendants $p.Id $all
"root pid: $($p.Id) $($all | Where-Object ProcessId -eq $p.Id | ForEach-Object { $_.Name })"
foreach ($k in $kids) { "  child pid=$($k.ProcessId) $($k.Name) ws=$([math]::Round($k.WorkingSetSize/1MB,1)) MB" }
$sum = ($kids | Measure-Object WorkingSetSize -Sum).Sum
"children total: $([math]::Round($sum/1MB,1)) MB"
$root = ($all | Where-Object ProcessId -eq $p.Id).WorkingSetSize
"root: $([math]::Round($root/1MB,1)) MB"
"TREE TOTAL: $([math]::Round(($root+$sum)/1MB,1)) MB"
$p.WaitForExit(120000) | Out-Null
