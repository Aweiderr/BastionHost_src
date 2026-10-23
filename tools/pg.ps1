# PostgreSQL 开发环境管理脚本
#
# 用法:
#   .\pg.ps1 start     启动
#   .\pg.ps1 stop      停止
#   .\pg.ps1 status    查看状态
#   .\pg.ps1 psql      打开 psql 交互终端
#   .\pg.ps1 reset     重建数据库（危险：清空全部数据）
$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8

$PG   = "D:\tools\pgsql\pgsql"
$DATA = "D:\tools\pgdata"
$PORT = 5432

function Get-PgProcess {
  Get-Process postgres -ErrorAction SilentlyContinue
}

switch ($args[0]) {
  "start" {
    if (Get-PgProcess) { Write-Host "PostgreSQL 已在运行" -ForegroundColor Yellow; break }
    Write-Host "启动 PostgreSQL ..." -ForegroundColor Cyan
    Start-Process -FilePath "$PG\bin\postgres.exe" `
      -ArgumentList "-D", $DATA, "-p", "$PORT" `
      -WindowStyle Hidden `
      -RedirectStandardOutput "$DATA\stdout.log" `
      -RedirectStandardError "$DATA\stderr.log"
    Start-Sleep -Seconds 4
    $c = Get-NetTCPConnection -LocalPort $PORT -State Listen -ErrorAction SilentlyContinue
    if ($c) { Write-Host "已启动，监听 $PORT" -ForegroundColor Green }
    else { Write-Host "启动失败，请查看 $DATA\stderr.log" -ForegroundColor Red }
  }
  "stop" {
    Write-Host "停止 PostgreSQL ..." -ForegroundColor Cyan
    & "$PG\bin\pg_ctl.exe" -D $DATA stop -m fast
  }
  "status" {
    $procs = Get-PgProcess
    if ($procs) {
      Write-Host "运行中（$($procs.Count) 个进程）" -ForegroundColor Green
      $c = Get-NetTCPConnection -LocalPort $PORT -State Listen -ErrorAction SilentlyContinue
      if ($c) { Write-Host "  监听端口: $PORT" }
      & "$PG\bin\psql.exe" -U postgres -h 127.0.0.1 -p $PORT -c "SELECT version();" 2>&1 | Select-Object -First 3
    } else {
      Write-Host "未运行" -ForegroundColor Yellow
    }
  }
  "psql" {
    $env:PGPASSWORD = "bastion"
    & "$PG\bin\psql.exe" -U bastion -d bastion -h 127.0.0.1 -p $PORT
  }
  "reset" {
    Write-Host "重建数据库 bastion（将清空全部数据）..." -ForegroundColor Red
    $confirm = Read-Host "确认? 输入 yes 继续"
    if ($confirm -ne "yes") { Write-Host "已取消"; break }
    & "$PG\bin\psql.exe" -U postgres -h 127.0.0.1 -p $PORT -c "DROP DATABASE IF EXISTS bastion;"
    & "$PG\bin\psql.exe" -U postgres -h 127.0.0.1 -p $PORT -c "CREATE DATABASE bastion OWNER bastion ENCODING 'UTF8';"
    Write-Host "已重建" -ForegroundColor Green
  }
  default {
    Write-Host "用法: .\pg.ps1 {start|stop|status|psql|reset}"
  }
}
