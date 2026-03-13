param(
  [Parameter(Mandatory = $true)]
  [string]$Target,
  [string]$Out = ""
)

function Get-TextFields {
  param([object]$obj)
  if ($null -eq $obj) { return }
  if ($obj -is [System.Collections.IDictionary]) {
    foreach ($key in $obj.Keys) {
      $val = $obj[$key]
      if ($key -eq 'text' -and $val -is [string]) { $val }
      Get-TextFields $val
    }
    return
  }
  if ($obj -is [pscustomobject]) {
    foreach ($prop in $obj.PSObject.Properties) {
      $val = $prop.Value
      if ($prop.Name -eq 'text' -and $val -is [string]) { $val }
      Get-TextFields $val
    }
    return
  }
  if ($obj -is [System.Collections.IEnumerable] -and -not ($obj -is [string])) {
    foreach ($item in $obj) { Get-TextFields $item }
    return
  }
}

function Process-File {
  param(
    [string]$FilePath,
    [string]$OutPath
  )
  $texts = @(
    Get-Content -LiteralPath $FilePath | ForEach-Object {
      try {
        $obj = $_ | ConvertFrom-Json
        Get-TextFields $obj
      } catch {}
    }
  )

  $joined = ($texts -join "`n")
  $joined = $joined -replace '\\r', "`r" -replace '\\n', "`n"
  Set-Content -LiteralPath $OutPath -Value $joined -Encoding UTF8
  Write-Host "[OK] $FilePath -> $OutPath"
}

if (Test-Path -LiteralPath $Target -PathType Container) {
  $files = Get-ChildItem -LiteralPath $Target -Filter *.jsonl
  if ($files.Count -eq 0) {
    Write-Host "[WARN] No .jsonl files found in $Target"
    exit 0
  }
  foreach ($f in $files) {
    $outFile = $f.FullName -replace '\.jsonl$', '.replies.txt'
    Process-File -FilePath $f.FullName -OutPath $outFile
  }
  exit 0
}

if (-not (Test-Path -LiteralPath $Target)) {
  Write-Error "File not found: $Target"
  exit 1
}

$outFile = $Out
if (-not $outFile) {
  $outFile = $Target -replace '\.jsonl$', '.replies.txt'
}

Process-File -FilePath $Target -OutPath $outFile
