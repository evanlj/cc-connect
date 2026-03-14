param(
  [Parameter(Mandatory = $true)]
  [string]$Target,
  [string]$Out = ""
)

function Try-ReadTurnMeta {
  param([string]$FilePath)

  # Fast path: cc-connect Codex traces always write turn_meta as the first line.
  try {
    $firstLine = Get-Content -LiteralPath $FilePath -TotalCount 1
    if ($firstLine) {
      # IMPORTANT: keep date-like strings (e.g. RFC3339 ts) as strings for fidelity.
      $obj = $firstLine | ConvertFrom-Json -DateKind String
      if ($obj -and $obj.type -eq 'turn_meta') {
        return $obj
      }
    }
  } catch {}

  # Fallback: scan the first few lines in case the file is non-standard.
  try {
    $head = Get-Content -LiteralPath $FilePath -TotalCount 50
    foreach ($line in $head) {
      if (-not $line) { continue }
      try {
        # IMPORTANT: keep date-like strings (e.g. RFC3339 ts) as strings for fidelity.
        $obj = $line | ConvertFrom-Json -DateKind String
        if ($obj -and $obj.type -eq 'turn_meta') {
          return $obj
        }
      } catch {}
    }
  } catch {}

  return $null
}

function Format-TurnMetaHeader {
  param([object]$meta)

  if ($null -eq $meta) { return "" }

  $lines = @()
  $lines += "# --- cc-connect trace meta ---"

  if ($meta.ts)    { $lines += ("# ts: {0}" -f $meta.ts) }
  if ($meta.model) { $lines += ("# model: {0}" -f $meta.model) }
  if ($meta.mode)  { $lines += ("# mode: {0}" -f $meta.mode) }

  # Prompt can be multiline; prefix each line to keep the header compact & skippable.
  if ($meta.prompt_preview) {
    $lines += "# prompt:"
    foreach ($pl in ($meta.prompt_preview -split "\r?\n")) {
      if ($pl -eq "") {
        $lines += "#"
      } else {
        $lines += ("#   {0}" -f $pl)
      }
    }
  }

  $lines += "# --- end trace meta ---"

  # Add a blank line after the header so the extracted text starts cleanly.
  return ($lines -join "`n") + "`n`n"
}

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
  $meta = Try-ReadTurnMeta -FilePath $FilePath
  $header = Format-TurnMetaHeader -meta $meta

  $texts = @(
    Get-Content -LiteralPath $FilePath | ForEach-Object {
      try {
        # IMPORTANT: keep date-like strings (e.g. RFC3339 ts) as strings for fidelity.
        $obj = $_ | ConvertFrom-Json -DateKind String
        Get-TextFields $obj
      } catch {}
    }
  )

  $joined = ($texts -join "`n")
  $joined = $joined -replace '\\r', "`r" -replace '\\n', "`n"
  Set-Content -LiteralPath $OutPath -Value ($header + $joined) -Encoding UTF8
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
