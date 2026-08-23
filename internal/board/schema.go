package board

import _ "embed"

// CurrentVersion is the only Board schema version accepted for persistence.
const CurrentVersion = 1

// boardSchemaV1 is the machine-readable counterpart of the Go contract.
//
//go:embed schema/board-v1.schema.json
var boardSchemaV1 []byte

// Schema returns an isolated copy of the current Board JSON Schema.
func Schema() []byte {
	return append([]byte(nil), boardSchemaV1...)
}
