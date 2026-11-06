param(
 [string]$Version = "latest",
 [string]$InstallDir = (Join-Path $env:LOCALAPPDATA "Programs\ggrun-client"),
 [switch]$NoPath
)
$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$arch = 'amd64'
if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { $arch = 'arm64' }
$asset = "ggrun-client-windows-$arch.exe"
if ($Version -eq 'latest') {
 $release = 'https://github.com/raketenkater/ggrun-client/releases/latest/download'
} else {
 if ($Version -notmatch '^[a-zA-Z0-9._-]+$') { throw 'Invalid release version.' }
 $release = "https://github.com/raketenkater/ggrun-client/releases/download/$Version"
}
$tmp = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
 Invoke-WebRequest -UseBasicParsing "$release/$asset" -OutFile (Join-Path $tmp $asset)
 Invoke-WebRequest -UseBasicParsing "$release/SHA256SUMS" -OutFile (Join-Path $tmp 'SHA256SUMS')
 $line = @(Get-Content (Join-Path $tmp 'SHA256SUMS') | Where-Object { $_ -match ('\s+' + [regex]::Escape($asset) + '$') })
 if ($line.Count -ne 1) { throw 'Release checksum is missing or ambiguous.' }
 $expected = ($line[0] -split '\s+')[0]
 $actual = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp $asset)).Hash.ToLowerInvariant()
 if ($expected -ne $actual) { throw 'Release checksum mismatch.' }
 New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
 $binary = Join-Path $InstallDir 'ggrun-client.exe'
 Copy-Item (Join-Path $tmp $asset) $binary -Force
 & $binary version
 if ($LASTEXITCODE -ne 0) { throw 'Installed binary could not run.' }
 if (-not $NoPath) {
  $userPath = [Environment]::GetEnvironmentVariable('Path','User')
  if ($null -eq $userPath) { $userPath = '' }
  if (($userPath -split ';') -notcontains $InstallDir) {
   [Environment]::SetEnvironmentVariable('Path', ($userPath.TrimEnd(';') + ';' + $InstallDir), 'User')
  }
  if (($env:Path -split ';') -notcontains $InstallDir) { $env:Path += ';' + $InstallDir }
 }
 Write-Host "Installed in $InstallDir. Open a new terminal and run ggrun-client --help."
} finally { Remove-Item -Recurse -Force $tmp }
