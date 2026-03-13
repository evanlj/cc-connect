param(
  [Parameter(Mandatory = $true)]
  [string]$TargetInstance,
  [string]$SourceInstance = "instance-a",
  [string]$InstancesRoot = "D:\ai-github\cc-connect\instances",
  [string]$TargetProjectName,
  [switch]$ClearFeishuCredentials,
  [switch]$DryRun
)

[Console]::InputEncoding = [Text.UTF8Encoding]::new($false)
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
$OutputEncoding = [Text.UTF8Encoding]::new($false)

function Normalize-TomlPath {
  param([string]$Path)
  return ($Path -replace "\\", "/")
}

function Replace-TomlValue {
  param(
    [string]$Text,
    [string]$Pattern,
    [string]$NewValue
  )
  return [regex]::Replace(
    $Text,
    $Pattern,
    ('$1"{0}"' -f $NewValue),
    [System.Text.RegularExpressions.RegexOptions]::IgnoreCase
  )
}

try {
  if (-not $TargetProjectName) { $TargetProjectName = "$TargetInstance-project" }

  $sourceDir = Join-Path $InstancesRoot $SourceInstance
  $targetDir = Join-Path $InstancesRoot $TargetInstance

  if (-not (Test-Path $sourceDir)) {
    throw "源实例不存在: $sourceDir"
  }
  if (Test-Path $targetDir) {
    throw "目标实例已存在: $targetDir"
  }

  Write-Output ("[PLAN] source={0}" -f $sourceDir)
  Write-Output ("[PLAN] target={0}" -f $targetDir)
  Write-Output ("[PLAN] target_project={0}" -f $TargetProjectName)
  Write-Output ("[PLAN] clear_feishu={0}" -f $ClearFeishuCredentials.IsPresent)

  if ($DryRun) {
    Write-Output "[DRYRUN] 未执行复制。"
    exit 0
  }

  New-Item -ItemType Directory -Path $targetDir -Force | Out-Null

  $excludeDirs = @(
    "data",
    "run",
    "crons"
  )
  $excludeDirPatterns = @(
    "data-parallel-*"
  )
  $excludeFilePatterns = @(
    ".tmp-*",
    "*.tmp",
    "*.sock",
    "stdout",
    "*.pid"
  )

  # Copy top-level entries manually so we can filter precisely.
  Get-ChildItem -LiteralPath $sourceDir -Force | ForEach-Object {
    $name = $_.Name
    $full = $_.FullName
    if ($_.PSIsContainer) {
      if ($excludeDirs -contains $name) { return }
      foreach ($p in $excludeDirPatterns) {
        if ($name -like $p) { return }
      }
      Copy-Item -LiteralPath $full -Destination (Join-Path $targetDir $name) -Recurse -Force
    }
    else {
      foreach ($fp in $excludeFilePatterns) {
        if ($name -like $fp) { return }
      }
      Copy-Item -LiteralPath $full -Destination (Join-Path $targetDir $name) -Force
    }
  }

  # Ensure fresh runtime dirs.
  New-Item -ItemType Directory -Path (Join-Path $targetDir "data") -Force | Out-Null

  $targetTomlPath = Normalize-TomlPath -Path $targetDir
  $tomls = Get-ChildItem -LiteralPath $targetDir -Filter *.toml -File -ErrorAction SilentlyContinue
  foreach ($toml in $tomls) {
    $raw = Get-Content -LiteralPath $toml.FullName -Raw -Encoding UTF8

    # Rewrite absolute paths and instance name fragments.
    $raw = $raw.Replace((Normalize-TomlPath -Path $sourceDir), $targetTomlPath)
    $raw = $raw.Replace($SourceInstance, $TargetInstance)

    # Force data_dir / project / work_dir to deterministic target values.
    $raw = Replace-TomlValue -Text $raw -Pattern '(^\s*data_dir\s*=\s*)"(.*?)"' -NewValue (Normalize-TomlPath -Path (Join-Path $targetDir "data"))
    $raw = Replace-TomlValue -Text $raw -Pattern '(^\s*name\s*=\s*)"(.*?)"' -NewValue $TargetProjectName
    $raw = Replace-TomlValue -Text $raw -Pattern '(^\s*work_dir\s*=\s*)"(.*?)"' -NewValue $targetTomlPath

    if ($ClearFeishuCredentials) {
      $raw = Replace-TomlValue -Text $raw -Pattern '(^\s*app_id\s*=\s*)"(.*?)"' -NewValue ""
      $raw = Replace-TomlValue -Text $raw -Pattern '(^\s*app_secret\s*=\s*)"(.*?)"' -NewValue ""
    }

    $raw | Set-Content -LiteralPath $toml.FullName -Encoding UTF8
  }

  # Rewrite BAT launchers if any.
  Get-ChildItem -LiteralPath $targetDir -Filter *.bat -File -ErrorAction SilentlyContinue | ForEach-Object {
    $text = Get-Content -LiteralPath $_.FullName -Raw -Encoding UTF8
    $text = $text.Replace($sourceDir, $targetDir)
    $text = $text.Replace($SourceInstance, $TargetInstance)
    $text = $text.Replace("$SourceInstance-project", $TargetProjectName)
    $text | Set-Content -LiteralPath $_.FullName -Encoding UTF8

    if ($_.Name -like "*$SourceInstance*") {
      $newName = $_.Name.Replace($SourceInstance, $TargetInstance)
      Rename-Item -LiteralPath $_.FullName -NewName $newName -Force
    }
  }

  Write-Output "[DONE] instance cloned."
  Write-Output ("[NEXT] 1) 检查配置: {0}" -f (Join-Path $targetDir "config.toml"))
  if (-not $ClearFeishuCredentials) {
    Write-Output "[WARN] 当前复制保留了源实例的飞书 app_id/app_secret。若要一机多机器人，请改为独立凭证。"
  }
  else {
    Write-Output "[NEXT] 请填写新的 app_id/app_secret 再启动。"
  }
  Write-Output ("[NEXT] 启动命令: D:\ai-github\cc-connect\cc-connect.exe -config {0}" -f (Join-Path $targetDir "config.toml"))
}
catch {
  Write-Error $_.Exception.Message
  exit 1
}
