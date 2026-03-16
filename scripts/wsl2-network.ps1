# Copyright (C) 2026 Techdelight BV
#
# Enable or disable LAN access to Daedalus Web UI running inside WSL2.
#
# Usage (run as Administrator):
#   .\wsl2-network.ps1 enable  [port]
#   .\wsl2-network.ps1 disable [port]
#
# Default port: 3000

param(
    [Parameter(Position = 0, Mandatory = $true)]
    [ValidateSet("enable", "disable")]
    [string]$Action,

    [Parameter(Position = 1)]
    [int]$Port = 3000
)

$RuleName = "Daedalus Web UI (port $Port)"

function Test-Administrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]$identity
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

if (-not (Test-Administrator)) {
    Write-Error "This script must be run as Administrator."
    exit 1
}

function Get-WSL2IP {
    $ip = (wsl hostname -I 2>$null)
    if ($ip) { return ($ip -split '\s+')[0].Trim() }
    return $null
}

function Test-PortProxy {
    $existing = netsh interface portproxy show v4tov4 2>$null | Select-String ":$Port\s"
    return ($null -ne $existing)
}

function Test-FirewallRule {
    return ($null -ne (Get-NetFirewallRule -DisplayName $RuleName -ErrorAction SilentlyContinue))
}

if ($Action -eq "enable") {
    $wslIP = Get-WSL2IP
    if (-not $wslIP) {
        Write-Error "Could not detect WSL2 IP. Is WSL2 running?"
        exit 1
    }

    if (Test-PortProxy) {
        # Update existing rule with current WSL2 IP.
        netsh interface portproxy delete v4tov4 listenport=$Port listenaddress=0.0.0.0 >$null
    }
    netsh interface portproxy add v4tov4 listenport=$Port listenaddress=0.0.0.0 connectport=$Port connectaddress=$wslIP >$null
    Write-Host "Port proxy: 0.0.0.0:$Port -> ${wslIP}:$Port"

    if (-not (Test-FirewallRule)) {
        New-NetFirewallRule -DisplayName $RuleName -Direction Inbound -LocalPort $Port -Protocol TCP -Action Allow >$null
        Write-Host "Firewall rule added: $RuleName"
    } else {
        Write-Host "Firewall rule already exists."
    }

    Write-Host ""
    Write-Host "LAN access enabled. Other machines can connect to http://$((Get-NetIPAddress -AddressFamily IPv4 | Where-Object { $_.InterfaceAlias -notmatch 'Loopback|vEthernet' -and $_.IPAddress -notmatch '^169\.' } | Select-Object -First 1).IPAddress):$Port"
    Write-Host ""
    Write-Host "Note: WSL2's IP changes on reboot. Re-run this script after restarting WSL2."

} elseif ($Action -eq "disable") {
    if (Test-PortProxy) {
        netsh interface portproxy delete v4tov4 listenport=$Port listenaddress=0.0.0.0 >$null
        Write-Host "Port proxy removed."
    } else {
        Write-Host "No port proxy found for port $Port."
    }

    if (Test-FirewallRule) {
        Remove-NetFirewallRule -DisplayName $RuleName >$null
        Write-Host "Firewall rule removed: $RuleName"
    } else {
        Write-Host "No firewall rule found: $RuleName"
    }

    Write-Host ""
    Write-Host "LAN access disabled."
}
