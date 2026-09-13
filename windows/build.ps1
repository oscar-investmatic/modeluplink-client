param([string]$Version='', [switch]$Unsigned, [string]$CertificateThumbprint=$env:MODELUPLINK_SIGNING_THUMBPRINT)
$ErrorActionPreference='Stop'
$Root=Split-Path $PSScriptRoot -Parent
if(-not $Version){$Version=(Get-Content (Join-Path $Root 'desktop\VERSION') -Raw).Trim()}
if($Version -notmatch '^\d+\.\d+\.\d+$'){throw 'Use a numeric release version'}
$Output=Join-Path $Root 'dist\windows'
New-Item -ItemType Directory -Force $Output | Out-Null
if(-not $Unsigned -and -not $CertificateThumbprint){throw 'Install an Authenticode certificate and set MODELUPLINK_SIGNING_THUMBPRINT. -Unsigned is only for internal build checks.'}
if(-not $Unsigned -and $CertificateThumbprint -notmatch '^[A-Fa-f0-9]{40}$'){throw 'Invalid certificate thumbprint'}
$env:MODELUPLINK_SIGNING_THUMBPRINT=$CertificateThumbprint
function Sign-And-Verify([string]$Path){
 if(-not $Unsigned){ & "$PSScriptRoot\sign.ps1" -Path $Path }
}
Push-Location $Root
try {
 $Revision=python desktop/source.py --field revision
 if($LASTEXITCODE -ne 0){throw "Clean source verification failed"}
 go run ./windows/resources -version $Version
 if($LASTEXITCODE -ne 0){throw 'Windows resource compilation failed'}
 $env:GOOS='windows';$env:GOARCH='amd64';$env:CGO_ENABLED='1'
 go build -mod=readonly -trimpath -ldflags "-s -w -H=windowsgui -X github.com/oscar-investmatic/modeluplink-client/internal/buildinfo.Revision=$Revision -X github.com/oscar-investmatic/modeluplink-client/pkg/agent.Version=$Version" -o "$Output\modeluplink.exe" ./cmd/modeluplink
 if($LASTEXITCODE -ne 0){throw 'Windows helper build failed'}
 go build -mod=readonly -trimpath -ldflags "-s -w -H=windowsgui -X main.Version=$Version -X github.com/oscar-investmatic/modeluplink-client/internal/buildinfo.Revision=$Revision" -o "$Output\modeluplink-app.exe" ./cmd/modeluplink-app
 if($LASTEXITCODE -ne 0){throw 'Windows app build failed'}
 Sign-And-Verify "$Output\modeluplink.exe"
 Sign-And-Verify "$Output\modeluplink-app.exe"
 $Compiler=Get-Command ISCC.exe -ErrorAction SilentlyContinue
 if($Compiler){$ISCC=$Compiler.Source}else{$ISCC=Join-Path ${env:ProgramFiles(x86)} 'Inno Setup 6\ISCC.exe'}
 if(-not (Test-Path $ISCC)){throw 'Install Inno Setup 6 to create the installer'}
 $CompilerArgs=@("/DAppVersion=$Version", "/DBuildDir=$Output")
 if(-not $Unsigned){
  $Signer='powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File $q'+$PSScriptRoot+'\sign.ps1$q -Path $f'
  $CompilerArgs+=@('/DSignedBuild=1', "/SModelUplink=$Signer")
 }
 & $ISCC @CompilerArgs "$PSScriptRoot\installer.iss"
 if($LASTEXITCODE -ne 0){throw 'Installer compilation failed'}
 $Installer=Join-Path $Output "modeluplink_${Version}_windows_amd64.exe"
 Sign-And-Verify $Installer
 if($Unsigned){Move-Item -Force $Installer ($Installer -replace '\.exe$','.unsigned.exe');Write-Output 'Internal unsigned build only; not eligible for website publication.'}
 else {
  $Name=Split-Path $Installer -Leaf
  $Hash=(Get-FileHash $Installer -Algorithm SHA256).Hash.ToLowerInvariant()
  [IO.File]::WriteAllText((Join-Path $Output 'checksums.txt'),"$Hash  $Name`n",[Text.UTF8Encoding]::new($false))
  $ChecksumHash=(Get-FileHash (Join-Path $Output 'checksums.txt') -Algorithm SHA256).Hash.ToLowerInvariant()
  $Manifest=@{version=$Version;files=@{$Name=$Hash;'checksums.txt'=$ChecksumHash}}
  [IO.File]::WriteAllText((Join-Path $Output 'release.json'),($Manifest|ConvertTo-Json),[Text.UTF8Encoding]::new($false))
 }
} finally {
 Remove-Item -ErrorAction SilentlyContinue "$Root\cmd\modeluplink\resource_windows_amd64.syso", "$Root\cmd\modeluplink-app\resource_windows_amd64.syso"
 Pop-Location
}
