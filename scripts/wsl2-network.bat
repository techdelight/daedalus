@echo off
rem Copyright (C) 2026 Techdelight BV
rem
rem Enable or disable LAN access to Daedalus Web UI running inside WSL2.
rem
rem Usage (run as Administrator):
rem   wsl2-network.bat enable  [port]
rem   wsl2-network.bat disable [port]
rem
rem Default port: 3000

setlocal enabledelayedexpansion

rem --- Check admin privileges ---
net session >nul 2>&1
if %errorlevel% neq 0 (
    echo Error: This script must be run as Administrator.
    exit /b 1
)

rem --- Parse arguments ---
set "ACTION=%~1"
set "PORT=%~2"
if "%ACTION%"=="" (
    echo Usage: %~nx0 enable [port]
    echo        %~nx0 disable [port]
    exit /b 1
)
if "%PORT%"=="" set "PORT=3000"

set "RULENAME=Daedalus Web UI (port %PORT%)"

if /i "%ACTION%"=="enable" goto :enable
if /i "%ACTION%"=="disable" goto :disable
echo Error: Unknown action "%ACTION%". Use "enable" or "disable".
exit /b 1

rem ============================================================
:enable
rem ============================================================

rem --- Get WSL2 IP ---
for /f "tokens=1" %%i in ('wsl hostname -I 2^>nul') do set "WSL_IP=%%i"
if not defined WSL_IP (
    echo Error: Could not detect WSL2 IP. Is WSL2 running?
    exit /b 1
)

rem --- Remove existing port proxy (idempotent update) ---
netsh interface portproxy delete v4tov4 listenport=%PORT% listenaddress=0.0.0.0 >nul 2>&1

rem --- Add port proxy ---
netsh interface portproxy add v4tov4 listenport=%PORT% listenaddress=0.0.0.0 connectport=%PORT% connectaddress=%WSL_IP% >nul
echo Port proxy: 0.0.0.0:%PORT% -^> %WSL_IP%:%PORT%

rem --- Add firewall rule if missing ---
netsh advfirewall firewall show rule name="%RULENAME%" >nul 2>&1
if %errorlevel% neq 0 (
    netsh advfirewall firewall add rule name="%RULENAME%" dir=in action=allow protocol=TCP localport=%PORT% >nul
    echo Firewall rule added: %RULENAME%
) else (
    echo Firewall rule already exists.
)

echo.
echo LAN access enabled.
echo Note: WSL2's IP changes on reboot. Re-run this script after restarting WSL2.
goto :eof

rem ============================================================
:disable
rem ============================================================

rem --- Remove port proxy ---
netsh interface portproxy show v4tov4 | findstr /c:"0.0.0.0" | findstr /c:"%PORT%" >nul 2>&1
if %errorlevel% equ 0 (
    netsh interface portproxy delete v4tov4 listenport=%PORT% listenaddress=0.0.0.0 >nul
    echo Port proxy removed.
) else (
    echo No port proxy found for port %PORT%.
)

rem --- Remove firewall rule ---
netsh advfirewall firewall show rule name="%RULENAME%" >nul 2>&1
if %errorlevel% equ 0 (
    netsh advfirewall firewall delete rule name="%RULENAME%" >nul
    echo Firewall rule removed: %RULENAME%
) else (
    echo No firewall rule found: %RULENAME%
)

echo.
echo LAN access disabled.
goto :eof
