param(
  [ValidateSet("lint", "update", "template")]
  [string]$Action = "lint",

  [string]$HtmlFile,
  [string]$OutFile,

  [string]$WorkspaceId,
  [string]$StoryId,
  [ValidateSet("stories", "tasks")]
  [string]$EntityType = "stories",

  [string]$TapdScriptPath
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Write-Info {
  param([string]$Message)
  Write-Host "[INFO] $Message"
}

function Write-Pass {
  param([string]$Message)
  Write-Host "[PASS] $Message" -ForegroundColor Green
}

function Write-Warn {
  param([string]$Message)
  Write-Host "[WARN] $Message" -ForegroundColor Yellow
}

function Write-Fail {
  param([string]$Message)
  Write-Host "[FAIL] $Message" -ForegroundColor Red
}

function Resolve-TapdScriptPath {
  param([string]$InputPath)

  $candidates = @()
  if (-not [string]::IsNullOrWhiteSpace($InputPath)) {
    $candidates += $InputPath
  }
  if (-not [string]::IsNullOrWhiteSpace($env:TAPD_SCRIPT_PATH)) {
    $candidates += $env:TAPD_SCRIPT_PATH
  }
  $candidates += "C:\Users\Xverse\.agents\skills\tapd\scripts\tapd.py"

  foreach ($p in $candidates) {
    if (-not [string]::IsNullOrWhiteSpace($p) -and (Test-Path -LiteralPath $p)) {
      return (Resolve-Path -LiteralPath $p).Path
    }
  }

  throw "未找到 tapd.py。请通过 -TapdScriptPath 指定，或设置 TAPD_SCRIPT_PATH。"
}

function Get-HtmlTemplatePath {
  $repoRoot = Split-Path -Parent $PSScriptRoot
  return (Join-Path $repoRoot "docs\tapd\templates\story-description.template.html")
}

function Assert-FileExists {
  param([string]$Path, [string]$Hint)
  if ([string]::IsNullOrWhiteSpace($Path) -or -not (Test-Path -LiteralPath $Path -PathType Leaf)) {
    throw "文件不存在：$Path`n$Hint"
  }
}

function Get-FileTextUtf8 {
  param([string]$Path)
  return [System.IO.File]::ReadAllText((Resolve-Path -LiteralPath $Path), [System.Text.Encoding]::UTF8)
}

function Test-TapdRichText {
  param([string]$Html)

  $errors = New-Object System.Collections.Generic.List[string]
  $warnings = New-Object System.Collections.Generic.List[string]

  if ([string]::IsNullOrWhiteSpace($Html)) {
    $errors.Add("内容为空。")
    return [pscustomobject]@{
      Pass = $false
      Errors = $errors
      Warnings = $warnings
    }
  }

  # Hard requirements: TAPD 富文本至少要有段落标签，建议含结构化列表。
  if ($Html -notmatch '<p(\s|>)') {
    $errors.Add("缺少 <p> 段落标签。")
  }
  if ($Html -notmatch '<(ul|ol)(\s|>)') {
    $warnings.Add("未检测到 <ul>/<ol> 列表；建议使用列表展示目标与 DoD。")
  }
  if ($Html -notmatch '<(strong|b)(\s|>)') {
    $warnings.Add("未检测到强调标签（<strong>/<b>）；建议对小节标题加粗。")
  }

  # Markdown anti-patterns（常见导致 TAPD 折叠换行的输入）
  $mdChecks = @(
    @{ Pattern = '(?m)^\s{0,3}#{1,6}\s+'; Name = 'Markdown 标题（#）' },
    @{ Pattern = '(?m)^\s*[-*+]\s+'; Name = 'Markdown 无序列表（-/*/+）' },
    @{ Pattern = '(?m)^\s*\d+\.\s+'; Name = 'Markdown 有序列表（1.）' },
    @{ Pattern = '```'; Name = 'Markdown 代码块（```）' }
  )
  foreach ($check in $mdChecks) {
    if ($Html -match $check.Pattern) {
      $errors.Add("检测到未转换的 $($check.Name)。请先转成 HTML 再提交。")
    }
  }

  # 段落数量过少时提示（可能仍是大段粘贴）
  $pCount = ([regex]::Matches($Html, '<p(\s|>)')).Count
  if ($pCount -lt 2) {
    $warnings.Add("段落数量较少（<p> < 2），请确认是否已结构化排版。")
  }

  return [pscustomobject]@{
    Pass = ($errors.Count -eq 0)
    Errors = $errors
    Warnings = $warnings
  }
}

function Show-LintResult {
  param([object]$Lint)

  foreach ($w in $Lint.Warnings) { Write-Warn $w }
  foreach ($e in $Lint.Errors) { Write-Fail $e }

  if ($Lint.Pass) {
    Write-Pass "HTML 预检通过。"
  } else {
    throw "HTML 预检未通过（见上方错误）。"
  }
}

function Invoke-TapdJson {
  param(
    [string]$TapdPyPath,
    [string[]]$Arguments
  )

  $output = & python $TapdPyPath @Arguments 2>&1
  $exitCode = $LASTEXITCODE
  $text = ($output | Out-String).Trim()

  if ($exitCode -ne 0) {
    throw "tapd.py 执行失败（exit=$exitCode）：`n$text"
  }
  if ([string]::IsNullOrWhiteSpace($text)) {
    throw "tapd.py 返回为空。"
  }

  try {
    return $text | ConvertFrom-Json -Depth 100
  } catch {
    throw "tapd.py 返回非 JSON：`n$text"
  }
}

function Get-StoryDescriptionFromQueryResult {
  param(
    [object]$Result,
    [string]$EntityType
  )

  if ($null -eq $Result -or $null -eq $Result.data) {
    throw "查询结果缺少 data 字段。"
  }
  $items = @($Result.data)
  if ($items.Count -eq 0) {
    throw "未查询到目标实体。"
  }

  $first = $items[0]
  if ($EntityType -eq "tasks") {
    if ($null -ne $first.Task) { return [string]$first.Task.description }
    if ($null -ne $first.Story) { return [string]$first.Story.description }
  } else {
    if ($null -ne $first.Story) { return [string]$first.Story.description }
    if ($null -ne $first.Task) { return [string]$first.Task.description }
  }

  throw "查询结果中找不到 Story/Task 对象。"
}

function Normalize-ForCompare {
  param([string]$Text)
  if ($null -eq $Text) { return "" }
  $s = $Text.Replace("`r`n", "`n").Trim()
  return $s
}

function Run-Lint {
  param([string]$HtmlPath)
  Assert-FileExists -Path $HtmlPath -Hint "请先准备 HTML 文件，再执行 lint。"
  $html = Get-FileTextUtf8 -Path $HtmlPath
  Write-Info "开始 HTML 预检：$HtmlPath"
  $lint = Test-TapdRichText -Html $html
  Show-LintResult -Lint $lint
}

function Run-Update {
  param(
    [string]$HtmlPath,
    [string]$Workspace,
    [string]$Story,
    [string]$Entity,
    [string]$TapdPyPath
  )

  if ([string]::IsNullOrWhiteSpace($Workspace)) { throw "缺少 -WorkspaceId" }
  if ([string]::IsNullOrWhiteSpace($Story)) { throw "缺少 -StoryId" }
  Assert-FileExists -Path $HtmlPath -Hint "请传入排版后的 HTML 文件。"

  $html = Get-FileTextUtf8 -Path $HtmlPath
  Write-Info "开始提交前 HTML 预检..."
  $lint = Test-TapdRichText -Html $html
  Show-LintResult -Lint $lint

  Write-Info "调用 TAPD 更新需求（workspace=$Workspace, id=$Story, entity=$Entity）..."
  [void](Invoke-TapdJson -TapdPyPath $TapdPyPath -Arguments @(
      "update_story_or_task",
      "--workspace_id", $Workspace,
      "--entity_type", $Entity,
      "--id", $Story,
      "--description", $html
    ))
  Write-Pass "TAPD 更新请求已成功返回。"

  Write-Info "执行回读校验..."
  $query = Invoke-TapdJson -TapdPyPath $TapdPyPath -Arguments @(
      "get_stories_or_tasks",
      "--workspace_id", $Workspace,
      "--entity_type", $Entity,
      "--id", $Story,
      "--fields", "id,name,description",
      "--no_count"
    )

  $remoteDesc = Get-StoryDescriptionFromQueryResult -Result $query -EntityType $Entity
  $localNorm = Normalize-ForCompare -Text $html
  $remoteNorm = Normalize-ForCompare -Text $remoteDesc

  if ($localNorm -ne $remoteNorm) {
    Write-Warn "本地与 TAPD 回读内容不完全一致。通常是平台清洗空白造成，继续做结构校验。"
  }

  $postLint = Test-TapdRichText -Html $remoteDesc
  if (-not $postLint.Pass) {
    foreach ($e in $postLint.Errors) { Write-Fail "回读校验失败：$e" }
    throw "回读校验失败：TAPD description 未满足富文本门禁。"
  }

  Write-Pass "回读校验通过：TAPD description 已满足富文本排版要求。"
  Write-Host ""
  Write-Host ("TAPD_LINK: https://www.tapd.cn/{0}/prong/stories/view/{1}" -f $Workspace, $Story)
}

function Write-Template {
  param([string]$Destination)

  $template = Get-HtmlTemplatePath
  Assert-FileExists -Path $template -Hint "模板文件缺失，请检查仓库文件。"

  if ([string]::IsNullOrWhiteSpace($Destination)) {
    $Destination = Join-Path (Get-Location) "tapd-story-description.html"
  }

  Copy-Item -LiteralPath $template -Destination $Destination -Force
  Write-Pass "模板已生成：$Destination"
}

try {
  switch ($Action) {
    "template" {
      Write-Template -Destination $OutFile
      break
    }
    "lint" {
      if ([string]::IsNullOrWhiteSpace($HtmlFile)) {
        throw "lint 模式必须提供 -HtmlFile <path>"
      }
      Run-Lint -HtmlPath $HtmlFile
      break
    }
    "update" {
      if ([string]::IsNullOrWhiteSpace($HtmlFile)) { throw "update 模式必须提供 -HtmlFile <path>" }
      $tapdPy = Resolve-TapdScriptPath -InputPath $TapdScriptPath
      Write-Info "tapd.py: $tapdPy"
      Run-Update -HtmlPath $HtmlFile -Workspace $WorkspaceId -Story $StoryId -Entity $EntityType -TapdPyPath $tapdPy
      break
    }
    default {
      throw "不支持的 Action: $Action"
    }
  }
} catch {
  Write-Fail $_.Exception.Message
  exit 1
}

