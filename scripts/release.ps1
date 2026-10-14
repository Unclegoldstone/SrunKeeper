param([string]$Version = '0.1.0')
$ErrorActionPreference = 'Stop'
if ($Version -notmatch '^\d+\.\d+\.\d+([-.][a-zA-Z0-9.]+)?$') { throw 'Invalid version' }
$projectRoot = Split-Path -Parent $PSScriptRoot
Push-Location $projectRoot
$previousGOOS = $env:GOOS
$previousGOARCH = $env:GOARCH
$previousCGO = $env:CGO_ENABLED
try {
    & go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Tests failed' }
    & go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'Vet failed' }
    $packageName = "SrunKeeper-$Version-windows-amd64"
    $packageDir = Join-Path $projectRoot "dist\$packageName"
    if (Test-Path -LiteralPath $packageDir) { throw 'Package directory exists. Use a new version or remove it manually.' }
    New-Item -ItemType Directory -Path (Join-Path $packageDir 'config') -Force | Out-Null
    $env:GOOS = 'windows'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
    & go build -trimpath -ldflags "-s -w -X main.version=$Version" -o (Join-Path $packageDir 'SrunKeeper.exe') ./cmd/srunkeeper
    if ($LASTEXITCODE -ne 0) { throw 'Build failed' }
    & (Join-Path $packageDir 'SrunKeeper.exe') -init
    if ($LASTEXITCODE -ne 0) { throw 'Config initialization failed' }
    Copy-Item -LiteralPath (Join-Path $projectRoot 'README.md') -Destination $packageDir
    $utf8 = [Text.UTF8Encoding]::new($false)
    [IO.File]::WriteAllText((Join-Path $packageDir '启动网页界面.cmd'), "@echo off`r`nchcp 65001 >nul`r`ncd /d `"%~dp0`"`r`nSrunKeeper.exe -web`r`npause`r`n", $utf8)
    [IO.File]::WriteAllText((Join-Path $packageDir '启动命令行.cmd'), "@echo off`r`nchcp 65001 >nul`r`ncd /d `"%~dp0`"`r`nSrunKeeper.exe`r`npause`r`n", $utf8)
    $archive = Join-Path $projectRoot "dist\$packageName.zip"
    if (Test-Path -LiteralPath $archive) { throw 'Archive already exists' }
    Compress-Archive -Path $packageDir -DestinationPath $archive
    $hash = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLower()
    [IO.File]::WriteAllText("$archive.sha256", "$hash  $packageName.zip`n", $utf8)
    Write-Output $archive
} finally {
    $env:GOOS = $previousGOOS; $env:GOARCH = $previousGOARCH; $env:CGO_ENABLED = $previousCGO
    Pop-Location
}
