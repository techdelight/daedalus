// Copyright (C) 2026 Techdelight BV

package runner

import "fmt"

// adapters is the registry of built-in adapters keyed by canonical name.
var adapters = map[string]Adapter{
	"claude":  ClaudeAdapter{},
	"copilot": CopilotAdapter{},
}

// Lookup returns the adapter registered under name. Unknown names
// produce an error listing the supported set so misconfigurations
// surface early rather than booting with the wrong runner.
func Lookup(name string) (Adapter, error) {
	a, ok := adapters[name]
	if !ok {
		return nil, fmt.Errorf("unknown runner %q (known: %v)", name, Names())
	}
	return a, nil
}

// Names returns the sorted-stable list of registered adapter names.
// Used in error messages and `daedalus runners list`.
func Names() []string {
	return []string{"claude", "copilot"}
}
