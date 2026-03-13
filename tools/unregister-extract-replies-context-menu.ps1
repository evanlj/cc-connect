function Remove-Menu {
  param([string]$KeyPath)
  if (Test-Path -LiteralPath $KeyPath) {
    Remove-Item -LiteralPath $KeyPath -Recurse -Force
  }
}

$fileKey = "HKCU:\Software\Classes\SystemFileAssociations\.jsonl\shell\cc-connect-extract-replies"
$dirKey  = "HKCU:\Software\Classes\Directory\shell\cc-connect-extract-replies"

Remove-Menu -KeyPath $fileKey
Remove-Menu -KeyPath $dirKey

Write-Host "[OK] Context menu removed."
