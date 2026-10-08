# release.ps1 - 打包发布版本（注入版本到 exe）
# 产出:
#   dist\FileServer.exe                 构建产物（内含版本/commit/构建时间）
#   dist\FileServer-lite-<Version>.zip  单 exe（约 7MB）
#   dist\FileServer-full-<Version>.zip  单 exe + ffmpeg 组件（完整视频支持）
#   dist\SHA256SUMS.txt                 上述产物 + exe 的 SHA256
param(
    [string]$Version = "1.0.0"
)
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$dist = Join-Path $root 'dist'
$exe = Join-Path $dist 'FileServer.exe'
$module = 'github.com/YLing2024/FileServer'

# 构建工具：优先仓库内 .tools 的 Go，否则用系统 go
$go = Join-Path $root '.tools\go\bin\go.exe'
if (-not (Test-Path $go)) { $go = 'go' }

$commit = ''
try { $commit = (& git -C $root rev-parse --short HEAD).Trim() } catch { $commit = '' }
$buildDate = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
$ldflags = "-s -w -X $module/internal/version.Version=$Version -X $module/internal/version.Commit=$commit -X $module/internal/version.BuildDate=$buildDate"

New-Item -ItemType Directory -Path $dist -Force | Out-Null

# ---- 构建（版本真正注入 exe，而不只是改 zip 名）----
Push-Location $root
try {
    & $go build -trimpath -ldflags $ldflags -o $exe .\cmd\fileserver
    if ($LASTEXITCODE -ne 0) { throw "go build failed (exit $LASTEXITCODE)" }
} finally {
    Pop-Location
}
Write-Host "exe: $exe (version $Version, commit $commit, $buildDate)"

$ffSrc = Join-Path $root '.tools\ffmpeg'
$tmp = Join-Path $dist ".release-tmp"
if (Test-Path $tmp) { Remove-Item $tmp -Recurse -Force }
New-Item -ItemType Directory -Path $tmp -Force | Out-Null

# ---- lite ----
Copy-Item $exe (Join-Path $tmp 'FileServer.exe')
$liteZip = Join-Path $dist "FileServer-lite-$Version.zip"
Compress-Archive -Path (Join-Path $tmp '*') -DestinationPath $liteZip -Force
Write-Host "lite: $liteZip"

# ---- full（ffmpeg 二进制不进仓库，仅本地打包）----
$fullZip = $null
if (Test-Path (Join-Path $ffSrc 'ffmpeg.exe')) {
    $ffDir = Join-Path $tmp 'ffmpeg'
    New-Item -ItemType Directory -Path $ffDir -Force | Out-Null
    Copy-Item (Join-Path $ffSrc 'ffmpeg.exe') $ffDir
    Copy-Item (Join-Path $ffSrc 'ffprobe.exe') $ffDir
    $fullZip = Join-Path $dist "FileServer-full-$Version.zip"
    Compress-Archive -Path (Join-Path $tmp '*') -DestinationPath $fullZip -Force
    Write-Host "full: $fullZip"
} else {
    Write-Host "未找到 .tools\ffmpeg，跳过 full 包"
}
Remove-Item $tmp -Recurse -Force

# ---- SHA256 ----
$sums = Join-Path $dist 'SHA256SUMS.txt'
$targets = @($exe, $liteZip)
if ($fullZip) { $targets += $fullZip }
$lines = foreach ($f in $targets) {
    if (Test-Path $f) {
        $h = (Get-FileHash -Algorithm SHA256 -Path $f).Hash.ToLower()
        "$h  $(Split-Path -Leaf $f)"
    }
}
Set-Content -Path $sums -Value $lines -Encoding ascii
Write-Host "sums: $sums"
Write-Host '完成。'
