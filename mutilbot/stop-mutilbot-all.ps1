[Console]::InputEncoding  = [Text.UTF8Encoding]::new($false)
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)

$ErrorActionPreference = "SilentlyContinue"

$scriptDir = Split-Path -Parent $PSCommandPath

Write-Host "[INFO] Stopping all mutilbot instances..."

foreach ($i in 1..5) {
    $instanceName = "mutilbot$i"
    $instancePath = (Join-Path $scriptDir "instance-mutilbot$i").ToLowerInvariant()

    $targets = @(Get-CimInstance Win32_Process | Where-Object {
        if (-not $_.CommandLine) { return $false }

        $cmd = $_.CommandLine.ToLowerInvariant()

        $isInstanceConnect = $_.Name -ieq "cc-connect.exe" -and $cmd.Contains($instancePath)
        $isInstanceCmd = $_.Name -ieq "cmd.exe" `
            -and $cmd.Contains($instancePath) `
            -and ($cmd.Contains("start.bat") -or $cmd.Contains("start-rightcode.bat"))

        return $isInstanceConnect -or $isInstanceCmd
    })

    if ($targets.Count -eq 0) {
        Write-Host "[INFO] $instanceName is not running."
        continue
    }

    $stopped = 0
    foreach ($proc in $targets) {
        try {
            Stop-Process -Id $proc.ProcessId -Force -ErrorAction Stop
            $stopped++
        } catch {
            # ignore and continue stopping other matched processes
        }
    }

    if ($stopped -gt 0) {
        Write-Host "[OK] $instanceName stopped. (killed $stopped process(es))"
    } else {
        Write-Host "[WARN] $instanceName matched process(es), but stop failed."
    }
}

Write-Host "[DONE] stop commands sent."
exit 0
