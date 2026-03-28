// Package migration provides a database migration framework for SQLite.
package migration

import (
	"sort"
	"sync"
)

// Registry maintains an in-memory registry of migrations.
// This allows programmatically registering migrations instead of loading from disk.
type Registry struct {
	mu          sync.RWMutex
	migrations  map[int]Migration
	orderedList []Migration // cached ordered list
}

// NewRegistry creates a new migration registry.
func NewRegistry() *Registry {
	return &Registry{
		migrations: make(map[int]Migration),
	}
}

// Register adds a migration to the registry.
// If a migration with the same version already exists, it will be overwritten.
func (r *Registry) Register(migration Migration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.migrations[migration.Version] = migration
	r.orderedList = nil // invalidate cache
}

// Get retrieves a migration by version.
// Returns false if the migration doesn't exist.
func (r *Registry) Get(version int) (Migration, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	migration, exists := r.migrations[version]
	return migration, exists
}

// GetAll returns all registered migrations sorted by version.
func (r *Registry) GetAll() []Migration {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Return cached list if available
	if r.orderedList != nil {
		return r.orderedList
	}

	// Build ordered list
	list := make([]Migration, 0, len(r.migrations))
	for _, migration := range r.migrations {
		list = append(list, migration)
	}

	// Sort by version
	sort.Slice(list, func(i, j int) bool {
		return list[i].Version < list[j].Version
	})

	// Cache the result
	r.orderedList = list

	return list
}

// Clear removes all migrations from the registry.
func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.migrations = make(map[int]Migration)
	r.orderedList = nil
}

// Count returns the number of registered migrations.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.migrations)
}

// HasMigration checks if a migration with the given version exists.
func (r *Registry) HasMigration(version int) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.migrations[version]
	return exists
}

// Versions returns all registered migration versions sorted.
func (r *Registry) Versions() []int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	versions := make([]int, 0, len(r.migrations))
	for version := range r.migrations {
		versions = append(versions, version)
	}

	// Sort versions
	sort.Ints(versions)

	return versions
}
