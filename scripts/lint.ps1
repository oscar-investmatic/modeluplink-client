# Run with PSScriptAnalyzer 1.24.0 installed; no application processes are started.
$ErrorActionPreference = 'Stop'
Import-Module PSScriptAnalyzer -RequiredVersion 1.24.0
$Root = Split-Path $PSScriptRoot -Parent
$Files = @(Get-ChildItem (Join-Path $Root 'windows') -Filter '*.ps1') + @(Get-Item $PSCommandPath)
$Findings = @()
foreach ($File in $Files) {
    $Findings += @(Invoke-ScriptAnalyzer -Path $File.FullName)
    $Source = Get-Content $File.FullName -Raw
    $Formatted = Invoke-Formatter -ScriptDefinition $Source -Settings CodeFormatting
    if ($Source -cne $Formatted) {
        throw "PowerShell formatting differs: $($File.Name)"
    }
}
if ($Findings.Count -gt 0) {
    $Findings | Format-Table -Wrap
    throw 'PowerShell analysis found issues'
}
Write-Output 'PowerShell analysis and formatting passed.'
