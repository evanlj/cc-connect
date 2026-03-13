param(
  # A jsonl trace file OR a directory that contains jsonl trace files.
  [string]$Path = "",

  # Shortcut: instance name like instance-a / instance-b / instance-c.
  # When set, Path defaults to: <repo>/instances/<instance>/data/traces/codex
  [string]$Instance = "",

  # Start reading from file beginning. Default is to follow from end (like tail -f).
  [switch]$FromStart,

  # Print original text before Chinese translation.
  [switch]$ShowOriginal,

  # Poll interval for reading new data and scanning latest file.
  [int]$PollMs = 500,

  # If Path is a directory and -FollowLatest is enabled, how often to rescan for a newer *.jsonl.
  # (Rescanning too frequently can be expensive when the directory contains many trace files.)
  [int]$ScanLatestMs = 2000,

  # If Path is a directory, keep following the latest *.jsonl file (auto-switch on new file).
  [switch]$FollowLatest = $true,

  # Translate "reasoning" items.
  [switch]$Reasoning = $true,

  # Also translate "agent_message" items (usually the final assistant answer).
  [switch]$AgentMessage,

  # Optional output file. If set, translations will be appended.
  [string]$OutFile = "",

  # Debug / offline mode: do not call any translation API. (Useful for validating the "tail" logic.)
  [switch]$NoTranslate,

  # Auto-exit after N seconds (0 = never). Useful for quick tests.
  [int]$MaxSeconds = 0
)

Set-StrictMode -Version Latest

function Resolve-RepoRoot {
  # Heuristic: this script lives under <repo>/tools/
  return (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
}

function Resolve-PathToWatch {
  param([string]$pathArg, [string]$instanceArg)

  if ($instanceArg) {
    $root = Resolve-RepoRoot
    $candidate = Join-Path $root (Join-Path ("instances\" + $instanceArg) "data\\traces\\codex")
    if (Test-Path -LiteralPath $candidate) { return $candidate }
    throw "Instance trace root not found: $candidate"
  }

  if ($pathArg) { return $pathArg }

  throw "Path is required (or -Instance)."
}

function Get-LatestJsonlFile {
  param([string]$dir)
  if (-not (Test-Path -LiteralPath $dir)) { return $null }
  $f = Get-ChildItem -LiteralPath $dir -Recurse -File -Filter *.jsonl -ErrorAction SilentlyContinue |
    Sort-Object LastWriteTime -Descending |
    Select-Object -First 1
  return $f
}

function Find-InstanceNameFromPath {
  param([string]$p)
  $m = [regex]::Match($p, '[\\/]+instances[\\/]+(?<inst>[^\\/]+)[\\/]')
  if ($m.Success) { return $m.Groups["inst"].Value }
  return ""
}

function Resolve-ConfigPathFromTrace {
  param([string]$tracePath)
  $inst = Find-InstanceNameFromPath $tracePath
  if (-not $inst) { return "" }
  $root = Resolve-RepoRoot
  $cfg = Join-Path $root (Join-Path ("instances\" + $inst) "config.toml")
  if (Test-Path -LiteralPath $cfg) { return $cfg }
  return ""
}

function Resolve-ProviderFromConfig {
  param(
    [string]$configPath,
    [string]$projectName = ""
  )
  if (-not $configPath -or -not (Test-Path -LiteralPath $configPath)) { return $null }

  $lines = Get-Content -LiteralPath $configPath
  $projects = @()
  $cur = $null
  $section = ""
  $curProvider = $null

  foreach ($raw in $lines) {
    $line = $raw.Trim()
    if ($line -eq "" -or $line.StartsWith("#")) { continue }

    if ($line -match '^\[\[projects\]\]') {
      $cur = [ordered]@{ name = $null; providerName = $null; providers = @() }
      $projects += $cur
      $section = "project"
      $curProvider = $null
      continue
    }

    if ($line -match '^\[projects\.agent\.options\]') {
      $section = "agent_options"
      $curProvider = $null
      continue
    }

    if ($line -match '^\[\[projects\.agent\.providers\]\]') {
      $section = "provider"
      $curProvider = [ordered]@{}
      if ($cur -ne $null) { $cur.providers += $curProvider }
      continue
    }

    if ($line -match '^\[.*\]') {
      $section = ""
      $curProvider = $null
      continue
    }

    if ($section -eq "project" -and $cur -ne $null -and -not $cur.name) {
      if ($line -match '^name\s*=\s*"([^"]+)"') { $cur.name = $matches[1]; continue }
    }

    if ($section -eq "agent_options" -and $cur -ne $null) {
      if ($line -match '^provider\s*=\s*"([^"]+)"') { $cur.providerName = $matches[1]; continue }
    }

    if ($section -eq "provider" -and $curProvider -ne $null) {
      if ($line -match '^(name|api_key|base_url|model)\s*=\s*"([^"]*)"') {
        $curProvider[$matches[1]] = $matches[2]
        continue
      }
    }
  }

  if ($projects.Count -eq 0) { return $null }
  $proj = $null
  if ($projectName) {
    $proj = $projects | Where-Object { $_.name -eq $projectName } | Select-Object -First 1
  }
  if (-not $proj) {
    $proj = $projects | Select-Object -First 1
  }
  if (-not $proj) { return $null }

  $prov = $null
  if ($proj.providerName) {
    $prov = $proj.providers | Where-Object { $_.name -eq $proj.providerName } | Select-Object -First 1
  }
  if (-not $prov) { $prov = $proj.providers | Select-Object -First 1 }
  return $prov
}

function Get-TraceProjectName {
  param([string]$traceFilePath)
  try {
    foreach ($line in (Get-Content -LiteralPath $traceFilePath -TotalCount 50 -Encoding UTF8)) {
      if (-not $line) { continue }
      try { $obj = $line | ConvertFrom-Json } catch { continue }
      if ($obj -and $obj.type -eq "turn_meta" -and $obj.cc_project) {
        return [string]$obj.cc_project
      }
    }
  } catch {}
  return ""
}

function Extract-ResponseText {
  param($resp)
  if ($null -eq $resp) { return "" }

  # Responses API sometimes provides "output_text" (depends on gateway).
  if ($resp.PSObject.Properties.Name -contains "output_text") {
    $t = $resp.output_text
    if ($t -is [string] -and $t) { return $t }
  }

  # Standard Responses API: output[].content[].text
  if ($resp.PSObject.Properties.Name -contains "output") {
    $parts = New-Object System.Collections.Generic.List[string]
    foreach ($o in $resp.output) {
      if ($null -eq $o) { continue }
      if ($o.PSObject.Properties.Name -contains "content") {
        foreach ($c in $o.content) {
          if ($null -eq $c) { continue }
          if ($c.PSObject.Properties.Name -contains "text" -and $c.text) { $parts.Add([string]$c.text) }
        }
      }
    }
    if ($parts.Count -gt 0) { return ($parts -join "") }
  }

  # Chat completions fallback: choices[0].message.content
  if ($resp.PSObject.Properties.Name -contains "choices") {
    try {
      $t = $resp.choices[0].message.content
      if ($t) { return [string]$t }
    } catch {}
  }

  return ""
}

function Invoke-TranslateToZh {
  param(
    [string]$text,
    [hashtable]$provider
  )

  if (-not $text) { return "" }

  if ($NoTranslate) {
    # Debug: keep the pipeline working without requiring network/API credentials.
    return $text
  }

  # If already Chinese (and basically no English), skip API call to save tokens.
  if (($text -match '[\u4e00-\u9fff]') -and -not ($text -match '[A-Za-z]')) {
    return $text
  }

  $apiKey = $null
  $baseUrl = $null
  $model = $null

  if ($provider) {
    $apiKey = $provider.api_key
    $baseUrl = $provider.base_url
    $model = $provider.model
  }

  # Optional overrides (useful for debugging or cost control). Not required.
  if ($env:CC_TRANSLATE_API_KEY) { $apiKey = $env:CC_TRANSLATE_API_KEY }
  if ($env:CC_TRANSLATE_BASE_URL) { $baseUrl = $env:CC_TRANSLATE_BASE_URL }
  if ($env:CC_TRANSLATE_MODEL) { $model = $env:CC_TRANSLATE_MODEL }

  if (-not $apiKey) { $apiKey = $env:OPENAI_API_KEY }
  if (-not $baseUrl) { $baseUrl = $env:OPENAI_BASE_URL }
  if (-not $model) { $model = $env:OPENAI_MODEL }
  if (-not $baseUrl) { $baseUrl = "https://api.openai.com/v1" }
  if (-not $model) { $model = "gpt-4o-mini" }

  if (-not $apiKey) {
    Write-Warning "translate skipped: no api_key found (instance config.toml or env vars)."
    return ""
  }

  $prompt = "请把下面内容翻译成中文，保留 Markdown/换行格式，仅输出中文译文：`n`n$text"

  $headers = @{ Authorization = "Bearer $apiKey" }

  # 1) Try Responses API first (works with codex-style gateways).
  $uri = ($baseUrl.TrimEnd("/") + "/responses")
  $body = @{
    model = $model
    input = $prompt
    temperature = 0.2
  } | ConvertTo-Json -Depth 8

  try {
    $resp = Invoke-RestMethod -Method Post -Uri $uri -Headers $headers -ContentType "application/json" -Body $body -TimeoutSec 60
    $out = Extract-ResponseText $resp
    if ($out) { return $out }
  } catch {
    # Continue to chat fallback.
  }

  # 2) Fallback to Chat Completions.
  $uri2 = ($baseUrl.TrimEnd("/") + "/chat/completions")
  $body2 = @{
    model = $model
    messages = @(
      @{ role = "user"; content = $prompt }
    )
    temperature = 0.2
  } | ConvertTo-Json -Depth 8

  try {
    $resp2 = Invoke-RestMethod -Method Post -Uri $uri2 -Headers $headers -ContentType "application/json" -Body $body2 -TimeoutSec 60
    $out2 = Extract-ResponseText $resp2
    return $out2
  } catch {
    $msg = $_.Exception.Message
    if (-not $msg) { $msg = "$_" }
    Write-Warning "translate failed: $msg"
    return ""
  }
}

function Append-Output {
  param([string]$outPath, [string]$content)
  if (-not $outPath) { return }
  $dir = Split-Path -Parent $outPath
  if ($dir -and -not (Test-Path -LiteralPath $dir)) {
    New-Item -ItemType Directory -Path $dir -Force | Out-Null
  }
  Add-Content -LiteralPath $outPath -Value $content -Encoding UTF8
}

function Should-ShowItem {
  param([string]$itemType)
  if (-not $itemType) { return $false }
  if ($Reasoning -and $itemType -eq "reasoning") { return $true }
  if ($AgentMessage -and $itemType -eq "agent_message") { return $true }
  return $false
}

function Read-NewChunk {
  param(
    [string]$filePath,
    [long]$pos
  )

  try {
    $fs = New-Object System.IO.FileStream($filePath, [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read, [System.IO.FileShare]::ReadWrite)
    $null = $fs.Seek($pos, [System.IO.SeekOrigin]::Begin)
    $sr = New-Object System.IO.StreamReader($fs, [Text.Encoding]::UTF8, $true, 4096, $true)
    $txt = $sr.ReadToEnd()
    $newPos = $fs.Position
    $sr.Dispose()
    $fs.Dispose()
    return [pscustomobject]@{ Text = $txt; Pos = $newPos }
  } catch {
    return [pscustomobject]@{ Text = ""; Pos = $pos }
  }
}

function Process-JsonlLine {
  param(
    [string]$line,
    [hashtable]$provider
  )
  if (-not $line) { return }

  try { $obj = $line | ConvertFrom-Json } catch { return }
  if (-not $obj) { return }

  if ($obj.type -ne "item.completed") { return }
  if (-not $obj.item) { return }

  $itemType = $obj.item.type
  if (-not (Should-ShowItem -itemType $itemType)) { return }

  $text = $obj.item.text
  if (-not $text) { return }

  $id = $obj.item.id
  $tag = if ($itemType -eq "reasoning") { "THINK" } else { "MSG" }

  if ($script:SeenItems -and $id) {
    $k = "$tag|$id"
    if (-not $script:SeenItems.Add($k)) { return }
  }

  if ($ShowOriginal) {
    Write-Host ""
    Write-Host ("[{0}] {1}" -f $tag, $id) -ForegroundColor DarkGray
    Write-Host $text
  }

  $zh = Invoke-TranslateToZh -text ([string]$text) -provider $provider
  if ($zh) {
    Write-Host ""
    Write-Host $zh
    Append-Output -outPath $OutFile -content ("`n{0}`n" -f $zh)
  } elseif (-not $ShowOriginal) {
    # Translation failed/skipped: still show original text so the user doesn't see "nothing".
    Write-Host ""
    Write-Host $text
    Append-Output -outPath $OutFile -content ("`n{0}`n" -f $text)
  }
}

# ────────────────────────────────────────────────────────────────

$watch = Resolve-PathToWatch -pathArg $Path -instanceArg $Instance

Write-Host "[INFO] Watching: $watch" -ForegroundColor DarkGreen

$current = $null
$pos = 0L
$buffer = ""
$provider = $null
$script:SeenItems = New-Object System.Collections.Generic.HashSet[string]

$startedAt = Get-Date
$nextScanAt = Get-Date

while ($true) {
  if ($MaxSeconds -gt 0) {
    $elapsed = (New-TimeSpan -Start $startedAt -End (Get-Date)).TotalSeconds
    if ($elapsed -ge $MaxSeconds) { break }
  }

  # Decide whether we should (re)scan for the latest file.
  $shouldScan = $false
  if (-not $current) { $shouldScan = $true }
  elseif ((Get-Date) -ge $nextScanAt) { $shouldScan = $true }

  if ($shouldScan) {
    $targetFile = $null
    if (Test-Path -LiteralPath $watch -PathType Leaf) {
      $targetFile = Get-Item -LiteralPath $watch
    } else {
      if ($FollowLatest) {
        $targetFile = Get-LatestJsonlFile -dir $watch
      } else {
        throw "Path is a directory. Use -FollowLatest or pass a specific .jsonl file."
      }
    }

    if ($targetFile) {
      if ($null -eq $current -or $current.FullName -ne $targetFile.FullName) {
        $isFirst = ($null -eq $current)
        $current = $targetFile
        $buffer = ""
        $script:SeenItems.Clear()

        Write-Host ""
        Write-Host ("[INFO] Now following: {0}" -f $current.FullName) -ForegroundColor Yellow

        $cfg = Resolve-ConfigPathFromTrace -tracePath $current.FullName
        $projName = Get-TraceProjectName -traceFilePath $current.FullName
        if ($cfg) {
          $provider = Resolve-ProviderFromConfig -configPath $cfg -projectName $projName
          if ($provider) {
            Write-Host ("[INFO] Provider: {0} | base_url={1} | model={2}" -f $provider.name, $provider.base_url, $provider.model) -ForegroundColor DarkGray
          } else {
            Write-Host ("[WARN] Failed to parse provider from config: {0}" -f $cfg) -ForegroundColor DarkYellow
          }
        } else {
          Write-Host "[WARN] Cannot infer instance config from trace path; will use env vars for API." -ForegroundColor DarkYellow
        }

        if ($isFirst) {
          # First attach: honor -FromStart. Default is to tail from end.
          $pos = 0L
          if (-not $FromStart) {
            try { $pos = (Get-Item -LiteralPath $current.FullName).Length } catch { $pos = 0L }
          }
        } else {
          # New file: start from beginning to avoid missing early lines of the new turn.
          $pos = 0L
        }
      }
    }

    # Plan next scan time.
    $nextScanAt = (Get-Date).AddMilliseconds([Math]::Max(200, $ScanLatestMs))
  }

  if (-not $current) {
    Start-Sleep -Milliseconds $PollMs
    continue
  }

  # If the file was truncated/replaced, reset position.
  try {
    $lenNow = (Get-Item -LiteralPath $current.FullName).Length
    if ($lenNow -lt $pos) {
      $pos = 0L
      $buffer = ""
      $script:SeenItems.Clear()
    }
  } catch {}

  $chunk = Read-NewChunk -filePath $current.FullName -pos $pos
  if ($chunk) {
    $pos = [long]$chunk.Pos
    if ($chunk.Text) { $buffer += [string]$chunk.Text }
  }

  # Process complete lines; keep the last (possibly partial) line in $buffer.
  if ($buffer -and $buffer.Contains("`n")) {
    $parts = $buffer.Split([char]10)
    for ($i = 0; $i -lt ($parts.Length - 1); $i++) {
      $line = $parts[$i].TrimEnd([char]13)
      Process-JsonlLine -line $line -provider $provider
    }
    $buffer = $parts[$parts.Length - 1]
  }

  Start-Sleep -Milliseconds $PollMs
}
