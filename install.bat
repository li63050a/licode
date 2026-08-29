@echo off
REM licode 一键安装脚本 (Windows)
REM 用法: 双击运行，或  install.bat [github|gitee]
setlocal EnableDelayedExpansion

set "VERSION=v0.1.0"
set "SOURCE=%~1"
if "%SOURCE%"=="" set "SOURCE=github"

set "FILE=api-gateway-windows-amd64.exe"
if "%SOURCE%"=="gitee" (
  set "BASE=https://gitee.com/li63050a/licode/releases/download"
) else (
  set "BASE=https://github.com/li63050a/licode/releases/download"
)
set "URL=%BASE%/%VERSION%/%FILE%"

set "DEST=%USERPROFILE%\bin"
if not exist "%DEST%" mkdir "%DEST%"

echo ==^> 下载 %URL%
curl -fSL "%URL%" -o "%DEST%\licode.exe"
if errorlevel 1 (
  echo curl 失败，尝试 PowerShell 下载...
  powershell -NoProfile -Command "Invoke-WebRequest -Uri '%URL%' -OutFile '%DEST%\licode.exe'"
  if errorlevel 1 (
    echo 下载失败，请手动下载: %URL%
    exit /b 1
  )
)

echo ==^> 已安装到 %DEST%\licode.exe
echo ==^> 直接运行 licode 进入 TUI（如提示找不到命令，请把 %DEST% 加入 PATH）
"%DEST%\licode.exe" --help
endlocal