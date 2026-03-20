// Copyright (C) 2026 Techdelight BV

package platform

// DisplayArgs returns Docker volume mount and environment flags for X11/Wayland
// display forwarding. Parameters are raw environment variable values (empty
// string means unset). Returns extra args for docker compose run and any
// warning messages.
func DisplayArgs(display, waylandDisplay, xdgRuntimeDir string) (extraArgs []string, warnings []string) {
	hasX11 := display != ""
	hasWayland := waylandDisplay != "" && xdgRuntimeDir != ""

	if display == "" && waylandDisplay == "" {
		warnings = append(warnings, "neither DISPLAY nor WAYLAND_DISPLAY is set; GUI applications may not work")
		return
	}

	// X11 forwarding
	if hasX11 {
		extraArgs = append(extraArgs,
			"-v", "/tmp/.X11-unix:/tmp/.X11-unix",
			"-e", "DISPLAY="+display,
		)
	}

	// Wayland forwarding
	if hasWayland {
		socketPath := xdgRuntimeDir + "/" + waylandDisplay
		extraArgs = append(extraArgs,
			"-v", socketPath+":/tmp/"+waylandDisplay,
			"-e", "WAYLAND_DISPLAY="+waylandDisplay,
			"-e", "XDG_RUNTIME_DIR=/tmp",
		)
	} else if waylandDisplay != "" && xdgRuntimeDir == "" {
		warnings = append(warnings, "WAYLAND_DISPLAY is set but XDG_RUNTIME_DIR is empty; Wayland socket cannot be mounted")
	}

	return
}
