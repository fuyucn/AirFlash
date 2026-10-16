# Runs every probe in the same conditions and writes probe-log.txt.
# Usage:  powershell -ExecutionPolicy Bypass -File run-probes.ps1
$ErrorActionPreference = 'Continue'
$out = Join-Path $PSScriptRoot 'probe-log.txt'
$cores = [Environment]::ProcessorCount

function Line($s) { $s | Tee-Object -FilePath $out -Append }

"=== AirFlash latency probe ===" | Out-File $out
Line "time      : $(Get-Date -Format o)"
Line "cores     : $cores"
Line "os        : $((Get-CimInstance Win32_OperatingSystem).Caption) $((Get-CimInstance Win32_OperatingSystem).Version)"
Line "cpu       : $((Get-CimInstance Win32_Processor | Select-Object -First 1).Name)"
Line "power     : $((powercfg /getactivescheme) -join ' ')"
Line ""

foreach ($exe in 'probe-go.exe','probe-rust.exe','probe-csharp.exe') {
  $path = Join-Path $PSScriptRoot $exe
  if (-not (Test-Path $path)) { Line "MISSING $exe"; continue }

  Line "---------- $exe ----------"

  foreach ($case in @(
      @{ n='idle';          a=@('-slots','1500') },
      @{ n='idle+udp';      a=@('-slots','1500','-udp') },
      @{ n="burn=$cores";   a=@('-slots','1000','-burn',"$cores") },
      @{ n="burn+udp";      a=@('-slots','1000','-burn',"$cores",'-udp') }
  )) {
    Line "--- $($case.n) ---"
    & $path @($case.a) 2>&1 | Tee-Object -FilePath $out -Append
  }
  Line ""
}

Line "done."
Write-Host "Wrote $out"
