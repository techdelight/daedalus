// Copyright (C) 2026 Techdelight BV

package platform

import (
	"net"
	"os"
	"strings"
)

// IsWSL2 returns true if the current environment is WSL2.
// It reads /proc/version and checks for the "microsoft" marker.
func IsWSL2() bool {
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	return ContainsWSL2Marker(string(data))
}

// ContainsWSL2Marker returns true if the version string contains
// the WSL2 kernel marker (case-insensitive "microsoft").
func ContainsWSL2Marker(version string) bool {
	return strings.Contains(strings.ToLower(version), "microsoft")
}

// WSL2IPAddress returns the first non-loopback IPv4 address,
// which is typically the WSL2 VM's address reachable from Windows.
// Returns an empty string if no suitable address is found.
func WSL2IPAddress() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipNet.IP
		if ip.IsLoopback() || ip.To4() == nil {
			continue
		}
		return ip.String()
	}
	return ""
}
