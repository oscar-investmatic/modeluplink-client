# Runs only in a disposable Windows CI account. Real Windows 11 acceptance is separate.
$ErrorActionPreference = 'Stop'
if ($env:MODELUPLINK_WINDOWS_TASK_TEST -ne '1') { throw 'Requires a disposable Windows test account' }
$Root = Split-Path $PSScriptRoot -Parent
$Installer = (Get-ChildItem "$Root\dist\windows\*.unsigned.exe" | Select-Object -First 1).FullName
$InstallDir = Join-Path $env:RUNNER_TEMP 'Model Uplink install test'
$FixtureProfile = Join-Path $env:RUNNER_TEMP 'Model Uplink profile test'
$Models = Join-Path $env:RUNNER_TEMP 'Model Uplink models test'
$env:MODELUPLINK_CONFIG_DIR = $FixtureProfile
$env:OLLAMA_MODELS = $Models
New-Item -ItemType Directory -Force $Models | Out-Null
Set-Content (Join-Path $Models 'keep-model.bin') 'existing-model-test'
function Invoke-Installer([string]$Path, [string]$Arguments) {
    $p = Start-Process -FilePath $Path -ArgumentList $Arguments -PassThru -Wait
    if ($p.ExitCode -ne 0) { throw "Installer failed with exit code $($p.ExitCode)" }
}
$Arguments = '/VERYSILENT /SUPPRESSMSGBOXES /NORESTART /DIR="' + $InstallDir + '"'
Invoke-Installer $Installer $Arguments
if (-not (Test-Path "$InstallDir\modeluplink-app.exe")) { throw 'Desktop app was not installed' }
if (-not (Test-Path "$FixtureProfile\config.json")) { throw 'Installer finalization did not create protected settings' }
# Exercise an upgrade with no active endpoint; it must preserve the user profile.
Invoke-Installer $Installer $Arguments
Invoke-Installer "$InstallDir\unins000.exe" '/VERYSILENT /SUPPRESSMSGBOXES /NORESTART'
if (Test-Path "$InstallDir\modeluplink.exe") { throw 'Uninstaller left the helper behind' }
if (Test-Path "$FixtureProfile\config.json") { throw 'Uninstaller left local credentials behind' }
if (-not (Test-Path "$Models\keep-model.bin")) { throw 'Uninstaller deleted model files' }
Write-Output 'Native installer: clean install, upgrade, credential cleanup and model retention passed.'
