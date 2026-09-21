package history

import "encoding/json"

// Server-store support: the MySQL-backed store lives in
// internal/server/store (server-only — the CLI never links a SQL driver and
// talks to the server over HTTP). These thin exports give that package the
// same codec the SQLite store uses internally.

// CanonicalBody canonicalizes (sorted-key re-marshal) and hashes a body.
func CanonicalBody(body any) (raw json.RawMessage, hash []byte, err error) {
	return canonicalBody(body)
}

// DecodeByKind rehydrates a legacy entity from its marshaled JSON.
func DecodeByKind(kind string, raw []byte) (any, error) {
	return decodeByKind(kind, raw)
}

// LegacyEntityDrone wraps a graph entity as a persistable Drone.
func LegacyEntityDrone(obj Entity) (*Drone, error) {
	return legacyEntityDrone(obj)
}

// LoadEntitiesWithPatterns flattens a collective into its persistable
// entities, patterns included.
func LoadEntitiesWithPatterns(coll *Collective) []Entity {
	return loadEntitiesWithPatterns(coll)
}
