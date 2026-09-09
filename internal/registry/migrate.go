// Copyright (C) 2026 Techdelight BV

package registry

import (
	"fmt"

	"github.com/techdelight/daedalus/core"
)

// migrateFunc transforms registry data from one version to the next.
type migrateFunc func(data *core.RegistryData) error

// migrations maps source version → upgrade function.
var migrations = map[int]migrateFunc{
	1: migrateV1toV2,
	2: migrateV2toV3,
	3: migrateV3toV4,
}

// migrateV1toV2 upgrades the registry from v1 to v2.
// v2 adds DefaultFlags and Sessions fields to ProjectEntry.
// Zero values (nil) are correct — omitempty keeps JSON clean.
func migrateV1toV2(data *core.RegistryData) error {
	data.Version = 2
	return nil
}

// migrateV2toV3 upgrades the registry from v2 to v3.
// v3 adds ProgressPct, Vision, and ProjectVersion fields to ProjectEntry.
// Zero values (0/"") are correct — omitempty keeps JSON clean.
func migrateV2toV3(data *core.RegistryData) error {
	data.Version = 3
	return nil
}

// migrateV3toV4 upgrades the registry from v3 to v4.
// v4 adds the Mounts field to ProjectEntry — the extra host directories a
// project's container gets at /mnt/<name>.
// Zero value (nil) is correct: a project that configures none mounts none.
func migrateV3toV4(data *core.RegistryData) error {
	data.Version = 4
	return nil
}

// migrate applies all necessary migrations to bring data to CurrentRegistryVersion.
// Returns true if any migrations were applied.
func (r *Registry) migrate(data *core.RegistryData) (bool, error) {
	if data.Version > core.CurrentRegistryVersion {
		return false, fmt.Errorf("registry version %d is newer than supported version %d", data.Version, core.CurrentRegistryVersion)
	}
	changed := false
	for data.Version < core.CurrentRegistryVersion {
		fn, ok := migrations[data.Version]
		if !ok {
			return changed, fmt.Errorf("no migration from registry version %d", data.Version)
		}
		if err := fn(data); err != nil {
			return changed, fmt.Errorf("migrating from version %d: %w", data.Version, err)
		}
		changed = true
	}
	return changed, nil
}
