# 堡垒机项目构建脚本
#
# 用法:
#   .\build.ps1              构建全部 (manager + server)
#   .\build.ps1 manager      仅构建管理服务
#   .\build.ps1 server       仅构建服务端
#   .\build.ps1 linux        交叉编译到 Linux (amd64)
#   .\build.ps1 cross        交叉编译全部平台 (windows/linux/darwin x amd64/arm64)
#   .\build.ps1 dist         构建发布包（各平台产物打包到 dist/）
#   .\build.ps1 test         运行全部测试
#   .\build.ps1 vet          静态检查
#   .\build.ps1 migrate      仅构建数据库迁移工具
#   .\build.ps1 db-init      构建并初始化数据库（首次部署）
#   .\build.ps1 db-status    查看迁移状态
#   .\build.ps1 db-reset     清空并重建数据库（恢复出厂 / 测试重建）
#
# 说明: 三个项目是独立的 Go module, 通过 go.mod 的 replace 指令引用本地 shared.
#       不使用 go.work, 因为 workspace 模式在含中文的路径下解析异常.
param(
  [string]$Target = "all"
)

$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$env:Path = "C:\Program Files\Go\bin;$env:Path"
Set-Location $PSScriptRoot
New-Item -ItemType Directory -Path "bin" -Force | Out-Null

function Build-App {
  param(
    [string]$Name,
    [string]$Module,
    [string]$Pkg,
    [string]$Ext = ".exe",
    [string]$GOOS = "",
    [string]$GOARCH = ""
  )
  Write-Host "构建 $Name ..." -ForegroundColor Cyan
  if ($GOOS) { $env:GOOS = $GOOS; $env:GOARCH = $GOARCH }
  else { Remove-Item Env:GOOS -ErrorAction SilentlyContinue; Remove-Item Env:GOARCH -ErrorAction SilentlyContinue }
  $out = Join-Path $PSScriptRoot "bin/$Name$Ext"
  go build -C $Module -o $out $Pkg
  if ($LASTEXITCODE -ne 0) { throw "$Name 构建失败" }
  Write-Host "  -> bin/$Name$Ext" -ForegroundColor Green
}

# 支持的交叉编译目标（Go 原生支持，无需 CGO）
$script:CrossTargets = @(
  @{ OS = "windows"; Arch = "amd64"; Ext = ".exe" },
  @{ OS = "linux";   Arch = "amd64"; Ext = "" },
  @{ OS = "linux";   Arch = "arm64"; Ext = "" },
  @{ OS = "darwin";  Arch = "amd64"; Ext = "" },
  @{ OS = "darwin";  Arch = "arm64"; Ext = "" }
)
$script:Apps = @(
  @{ Name = "manager"; Module = "manager"; Pkg = "./cmd/manager" },
  @{ Name = "server";  Module = "server";  Pkg = "./cmd/server" },
  @{ Name = "migrate"; Module = "manager"; Pkg = "./cmd/migrate" }
)

function Build-Cross {
  $env:CGO_ENABLED = "0"   # 纯静态编译，产物无 libc 依赖
  foreach ($t in $script:CrossTargets) {
    $env:GOOS = $t.OS
    $env:GOARCH = $t.Arch
    $dir = Join-Path $PSScriptRoot "dist/$($t.OS)-$($t.Arch)"
    New-Item -ItemType Directory -Path $dir -Force | Out-Null
    Write-Host "交叉编译 $($t.OS)/$($t.Arch) ..." -ForegroundColor Cyan
    foreach ($a in $script:Apps) {
      $out = Join-Path $dir "$($a.Name)$($t.Ext)"
      go build -C $a.Module -o $out $a.Pkg
      if ($LASTEXITCODE -ne 0) { throw "$($a.Name) 在 $($t.OS)/$($t.Arch) 构建失败" }
    }
    $size = (Get-ChildItem $dir -File | Measure-Object -Property Length -Sum).Sum / 1MB
    Write-Host ("  -> dist/$($t.OS)-$($t.Arch)  {0:N1} MB" -f $size) -ForegroundColor Green
  }
  Remove-Item Env:GOOS -ErrorAction SilentlyContinue
  Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
  Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue
}

function Run-Tests {
  Write-Host "运行测试 ..." -ForegroundColor Cyan
  $failed = $false
  foreach ($m in @("shared", "manager", "server")) {
    Write-Host "--- $m ---" -ForegroundColor DarkGray
    go test -C $m ./...
    if ($LASTEXITCODE -ne 0) { $failed = $true }
  }
  if ($failed) { throw "测试失败" }
}

switch ($Target.ToLower()) {
  "manager" { Build-App -Name "manager" -Module "manager" -Pkg "./cmd/manager" }
  "server"  { Build-App -Name "server"  -Module "server"  -Pkg "./cmd/server" }
  "linux" {
    Build-App -Name "manager-linux-amd64" -Module "manager" -Pkg "./cmd/manager" -Ext "" -GOOS "linux" -GOARCH "amd64"
    Build-App -Name "server-linux-amd64"  -Module "server"  -Pkg "./cmd/server"  -Ext "" -GOOS "linux" -GOARCH "amd64"
  }
  "test" { Run-Tests }
  "vet" {
    Write-Host "静态检查 ..." -ForegroundColor Cyan
    foreach ($m in @("shared", "manager", "server")) { go vet -C $m ./... }
  }
  "cross" { Build-Cross }
  "dist"  { Build-Cross; Write-Host "发布产物位于 dist/ 目录" -ForegroundColor Green }
  "migrate" { Build-App -Name "migrate" -Module "manager" -Pkg "./cmd/migrate" }
  # 端到端测试工具（仅用于开发与验证，不随生产发布）
  "e2e" {
    Build-App -Name "e2e-ssh"        -Module "server" -Pkg "./cmd/e2e-ssh"
    Build-App -Name "record-verify"  -Module "server" -Pkg "./cmd/record-verify"
  }
  "db-init" {
    Build-App -Name "migrate" -Module "manager" -Pkg "./cmd/migrate"
    & "$PSScriptRoot/bin/migrate.exe" init
  }
  "db-status" {
    if (-not (Test-Path "$PSScriptRoot/bin/migrate.exe")) { Build-App -Name "migrate" -Module "manager" -Pkg "./cmd/migrate" }
    & "$PSScriptRoot/bin/migrate.exe" status
  }
  "db-reset" {
    if (-not (Test-Path "$PSScriptRoot/bin/migrate.exe")) { Build-App -Name "migrate" -Module "manager" -Pkg "./cmd/migrate" }
    & "$PSScriptRoot/bin/migrate.exe" reset --yes
  }
  default {
    Build-App -Name "manager" -Module "manager" -Pkg "./cmd/manager"
    Build-App -Name "server"  -Module "server"  -Pkg "./cmd/server"
    Build-App -Name "migrate" -Module "manager" -Pkg "./cmd/migrate"
  }
}

Write-Host "完成" -ForegroundColor Green
