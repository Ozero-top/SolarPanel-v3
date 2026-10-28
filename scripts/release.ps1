<#
.SYNOPSIS
  SolarPanel V3 一键发版：版本号同步 → 构建校验 → 提交推送 → 打 tag → GitHub Release → 三架构镜像推送

.DESCRIPTION
  发版前请先在 updates.md 顶部补好对应版本小节（如「## v3.3.6（2026-10-25）」），
  脚本会从该小节自动提取 Release 说明。执行前要求工作区干净。

.EXAMPLE
  .\scripts\release.ps1 3.3.6

.EXAMPLE
  .\scripts\release.ps1 v3.3.6 -Title "v3.3.6 — 壁纸优化"

.EXAMPLE
  .\scripts\release.ps1 3.3.6 -SkipDocker    # 只发 git 与 Release，不推镜像

.EXAMPLE
  .\scripts\release.ps1 3.3.6 -SkipGit       # 代码已提交过，只补推镜像
#>
[CmdletBinding()]
param(
  [Parameter(Mandatory = $true, Position = 0)]
  [string]$Version,

  [string]$Title = "",

  [string]$Image = "ovitor/solarpanel-v3",

  [string]$Builder = "multiarch",

  [string[]]$Platforms = @("linux/amd64", "linux/arm64", "linux/arm/v7"),

  [switch]$SkipDocker,

  [switch]$SkipGit
)

$ErrorActionPreference = 'Stop'

function Fail([string]$msg) {
  Write-Host "[x] $msg" -ForegroundColor Red
  exit 1
}

function Step([string]$msg) {
  Write-Host ""
  Write-Host "==> $msg" -ForegroundColor Cyan
}

function Run([string]$exe, [string[]]$exeArgs) {
  Write-Host ("    " + $exe + " " + ($exeArgs -join ' ')) -ForegroundColor DarkGray
  & $exe @exeArgs
  if ($LASTEXITCODE -ne 0) { Fail "$exe 执行失败（exit $LASTEXITCODE）" }
}

# 保留原文件的 BOM 状态，避免整文件编码被改动
function Save-Text([string]$path, [string]$text) {
  $bytes = [IO.File]::ReadAllBytes($path)
  $hasBom = ($bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF)
  [IO.File]::WriteAllText($path, $text, (New-Object System.Text.UTF8Encoding($hasBom)))
}

# ===== 参数与前置校验 =====
$clean = $Version.Trim().TrimStart('v', 'V')
if ($clean -notmatch '^\d+\.\d+\.\d+$') {
  Fail "版本号需形如 3.3.6（可带 v 前缀），当前收到：$Version"
}
$tag = "v$clean"
if (-not $Title) { $Title = $tag }

$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
  if (-not (Test-Path "updates.md")) { Fail "未找到 updates.md，请在仓库根目录运行" }
  if (-not (Test-Path "Dockerfile")) { Fail "未找到 Dockerfile，请在仓库根目录运行" }

  # 更新日志小节必须存在：Release 说明从这里取
  $lines = Get-Content "updates.md" -Encoding UTF8
  $start = -1
  for ($i = 0; $i -lt $lines.Count; $i++) {
    if ($lines[$i] -match ("^##\s+" + [regex]::Escape($tag) + "\b")) { $start = $i; break }
  }
  if ($start -lt 0) {
    Fail "updates.md 里没有「## $tag」小节，请先补好更新日志再发版"
  }
  $end = $lines.Count
  for ($i = $start + 1; $i -lt $lines.Count; $i++) {
    if ($lines[$i] -match '^##\s+') { $end = $i; break }
  }
  $notes = (($lines[($start + 1)..($end - 1)]) -join "`n").Trim()
  if (-not $notes) { Fail "updates.md 的「## $tag」小节内容为空" }

  if (-not $SkipGit) {
    $dirty = @(git status --porcelain)
    if ($dirty.Count -gt 0) {
      Write-Host "工作区存在未提交改动：" -ForegroundColor Yellow
      $dirty | ForEach-Object { Write-Host "    $_" -ForegroundColor Yellow }
      Fail "请先提交或还原上述改动，保证发版提交里只有版本相关变更"
    }
    if (git rev-parse -q --verify "refs/tags/$tag" 2>$null) {
      Fail "tag $tag 已存在，若只是补推镜像请加 -SkipGit"
    }
    $branch = (git rev-parse --abbrev-ref HEAD).Trim()
    if ($branch -ne 'main') {
      Fail "当前分支是 $branch，请切到 main 再发版"
    }
  }

  # ===== 1. 同步版本号 =====
  Step "同步版本号到 $tag"

  $apiPath = "frontend/assets/js/api.js"
  $api = Get-Content $apiPath -Raw -Encoding UTF8
  $apiNew = [regex]::Replace($api, "const APP_VERSION = 'v[^']+';", "const APP_VERSION = '$tag';")
  if ($apiNew -eq $api) { Write-Host "    api.js 已是 $tag" -ForegroundColor DarkGray }
  else { Save-Text $apiPath $apiNew; Write-Host "    api.js → $tag" -ForegroundColor Green }

  $readme = Get-Content "README.md" -Raw -Encoding UTF8
  $readmeNew = [regex]::Replace($readme, 'Version-v\d+\.\d+\.\d+-', "Version-$tag-")
  if ($readmeNew -eq $readme) { Write-Host "    README 徽章已是 $tag" -ForegroundColor DarkGray }
  else { Save-Text "README.md" $readmeNew; Write-Host "    README 徽章 → $tag" -ForegroundColor Green }

  # ===== 2. 构建与校验 =====
  Step "前端资源戳 + 编译校验"
  Run "node" @("scripts/build-frontend.js")
  Run "node" @("scripts/build-clipper.js")
  Run "go" @("build", "./...")
  Run "go" @("vet", "./...")

  # ===== 3. 提交与推送 =====
  if (-not $SkipGit) {
    Step "提交并推送代码"
    # 前置已保证工作区干净，此处的改动只可能来自上面的版本同步与资源戳构建
    Run "git" @("add", "-A")
    Run "git" @("commit", "-m", "chore(release): $tag")
    Run "git" @("push", "origin", "main")

    Step "打 tag 并推送"
    Run "git" @("tag", $tag)
    Run "git" @("push", "origin", $tag)

    Step "创建 GitHub Release"
    $notesFile = Join-Path $env:TEMP "sp-release-$clean.md"
    [IO.File]::WriteAllText($notesFile, $notes, (New-Object System.Text.UTF8Encoding($false)))
    $ghArgs = @("release", "create", $tag, "--title", $Title, "--notes-file", $notesFile)
    $clipZip = Get-ChildItem "dist/solarpanel-clipper-v*.zip" -ErrorAction SilentlyContinue |
      Sort-Object LastWriteTime -Descending | Select-Object -First 1
    if ($clipZip) {
      $ghArgs += $clipZip.FullName
      $sums = Join-Path "dist" "SHA256SUMS.txt"
      if (Test-Path $sums) { $ghArgs += (Resolve-Path $sums).Path }
      Write-Host "    附扩展包：$($clipZip.Name)" -ForegroundColor Green
    }
    Run "gh" $ghArgs
  }

  # ===== 4. 三架构镜像 =====
  if (-not $SkipDocker) {
    Step "构建并推送多架构镜像：$tag + latest"
    $plat = ($Platforms -join ',')
    Run "docker" @(
      "buildx", "build",
      "--builder", $Builder,
      "--platform", $plat,
      "--tag", "${Image}:$tag",
      "--tag", "${Image}:latest",
      "--push", "."
    )

    Step "校验镜像架构"
    docker buildx imagetools inspect "${Image}:$tag" 2>&1 |
      Select-String "Digest:|Platform:"
  }

  # ===== 完成 =====
  Write-Host ""
  Write-Host "发版完成：$tag" -ForegroundColor Green
  if (-not $SkipGit) {
    Write-Host "  Release: https://github.com/Ozero-top/SolarPanel-v3/releases/tag/$tag"
  }
  if (-not $SkipDocker) {
    Write-Host "  镜像: https://hub.docker.com/r/$Image/tags"
  }
  Write-Host ""
  Write-Host "提示：已部署实例按默认 6 小时节奏自动发现新版本，可在后台「更新日志」手动检查。" -ForegroundColor DarkGray
}
finally {
  Pop-Location
}
