// Copyright (C) 2026 Techdelight BV

package platform

import (
	"net"
	"testing"
)

func TestContainsWSL2Marker_RealKernel(t *testing.T) {
	version := "Linux version 5.15.90.1-microsoft-standard-WSL2 (oe-user@oe-host)"
	if !ContainsWSL2Marker(version) {
		t.Error("expected true for WSL2 kernel string")
	}
}

func TestContainsWSL2Marker_MixedCase(t *testing.T) {
	version := "Linux version 5.15.90.1-Microsoft-Standard-WSL2"
	if !ContainsWSL2Marker(version) {
		t.Error("expected true for mixed-case Microsoft")
	}
}

func TestContainsWSL2Marker_RegularLinux(t *testing.T) {
	version := "Linux version 6.1.0-18-amd64 (debian-kernel@lists.debian.org)"
	if ContainsWSL2Marker(version) {
		t.Error("expected false for regular Linux kernel")
	}
}

func TestContainsWSL2Marker_Empty(t *testing.T) {
	if ContainsWSL2Marker("") {
		t.Error("expected false for empty string")
	}
}

func TestWSL2IPAddress_NoPanic(t *testing.T) {
	ip := WSL2IPAddress()
	if ip == "" {
		return // no non-loopback IPv4 — acceptable in CI
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		t.Errorf("WSL2IPAddress() = %q, not a valid IP", ip)
	}
	if parsed.To4() == nil {
		t.Errorf("WSL2IPAddress() = %q, expected IPv4", ip)
	}
}
