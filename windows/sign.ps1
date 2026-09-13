param([Parameter(Mandatory = $true)][string]$Path)
$ErrorActionPreference = 'Stop'
$Thumbprint = $env:MODELUPLINK_SIGNING_THUMBPRINT
if ($Thumbprint -notmatch '^[A-Fa-f0-9]{40}$') { throw 'Set MODELUPLINK_SIGNING_THUMBPRINT to a certificate thumbprint' }
$Certificate = Get-Item "Cert:\CurrentUser\My\$Thumbprint"
if (-not $Certificate.HasPrivateKey -or $Certificate.NotAfter -lt (Get-Date)) { throw 'A valid signing certificate with a private key is required' }
$Result = Set-AuthenticodeSignature -FilePath $Path -Certificate $Certificate -HashAlgorithm SHA256 -TimestampServer 'http://timestamp.digicert.com'
if ($Result.Status -ne 'Valid' -or (Get-AuthenticodeSignature $Path).Status -ne 'Valid') { throw 'Authenticode signing or verification failed' }
