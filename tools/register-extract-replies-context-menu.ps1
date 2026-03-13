param(
  [string]$ScriptRoot = $PSScriptRoot
)

$bat = Join-Path $ScriptRoot "extract-replies.bat"
if (-not (Test-Path -LiteralPath $bat)) {
  Write-Error "Missing extract-replies.bat at $bat"
  exit 1
}

$menuName = "Extract Replies (cc-connect)"

function Set-Menu {
  param(
    [string]$KeyPath,
    [string]$Command
  )
  New-Item -Path $KeyPath -Force | Out-Null
  Set-ItemProperty -Path $KeyPath -Name "MUIVerb" -Value $menuName
  Set-ItemProperty -Path $KeyPath -Name "Icon" -Value $bat
  New-Item -Path (Join-Path $KeyPath "command") -Force | Out-Null
  Set-ItemProperty -Path (Join-Path $KeyPath "command") -Name "(default)" -Value $Command
}

$fileKey = "HKCU:\Software\Classes\SystemFileAssociations\.jsonl\shell\cc-connect-extract-replies"
$dirKey  = "HKCU:\Software\Classes\Directory\shell\cc-connect-extract-replies"

$cmdFile = "`"$bat`" `"%1`""
$cmdDir  = "`"$bat`" `"%1`""

Set-Menu -KeyPath $fileKey -Command $cmdFile
Set-Menu -KeyPath $dirKey -Command $cmdDir

Write-Host "[OK] Context menu registered (HKCU) for .jsonl and directories."
