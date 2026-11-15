// Package resourcegraph validates the semantic parts of the proposed G0
// contract after JSON Schema validation. It does not read or write workspaces.
package resourcegraph

const Version = "0.1"

type Graph struct {
	Type         string        `json:"type"`
	Version      string        `json:"schema_version"`
	WorkspaceID  string        `json:"workspace_id"`
	Resources    []Resource    `json:"resources"`
	Revisions    []Revision    `json:"revisions"`
	Relations    []Relation    `json:"relations"`
	ChangeEvents []ChangeEvent `json:"change_events"`
}

type Resource struct {
	ID          string `json:"resource_id"`
	Kind        string `json:"kind"`
	LegacyDocID string `json:"legacy_doc_id,omitempty"`
}

type Blob struct {
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	MediaType string `json:"media_type"`
}

type Revision struct {
	ID             string   `json:"revision_id"`
	ResourceID     string   `json:"resource_id"`
	Parents        []string `json:"parent_revision_ids"`
	Blob           Blob     `json:"blob"`
	MetadataSHA256 string   `json:"metadata_sha256,omitempty"`
}

type Relation struct {
	ID               string `json:"relation_id"`
	Kind             string `json:"kind"`
	SourceRevisionID string `json:"source_revision_id"`
	Target           Target `json:"target"`
}

type Target struct {
	Mode       string  `json:"mode"`
	ResourceID string  `json:"resource_id"`
	RevisionID string  `json:"revision_id,omitempty"`
	Anchor     *Anchor `json:"anchor,omitempty"`
}

type Anchor struct {
	Kind        string `json:"kind"`
	Start       *int64 `json:"start,omitempty"`
	End         *int64 `json:"end,omitempty"`
	Page        int64  `json:"page,omitempty"`
	Rect        *Rect  `json:"rect,omitempty"`
	StartRow    int64  `json:"start_row,omitempty"`
	EndRow      int64  `json:"end_row,omitempty"`
	StartColumn int64  `json:"start_column,omitempty"`
	EndColumn   int64  `json:"end_column,omitempty"`
}

type Rect struct {
	X      int64 `json:"x"`
	Y      int64 `json:"y"`
	Width  int64 `json:"width"`
	Height int64 `json:"height"`
}

type ChangeEvent struct {
	OperationID string `json:"operation_id"`
	Kind        string `json:"kind"`
	RevisionID  string `json:"revision_id"`
}

type ShareRequest struct {
	Type           string    `json:"type"`
	Version        string    `json:"schema_version"`
	WorkspaceID    string    `json:"workspace_id"`
	RootRevisionID string    `json:"root_revision_id"`
	GrantRef       *GrantRef `json:"grant_ref,omitempty"`
}

// GrantRef points to an existing authority. A decoded request grants no access.
type GrantRef struct {
	Authority string `json:"authority"`
	PolicyID  string `json:"policy_id"`
	ScopeID   string `json:"scope_id"`
}

// ShareSnapshot contains only snapshot-local references and projected blobs.
type ShareSnapshot struct {
	Type         string        `json:"type"`
	Version      string        `json:"schema_version"`
	RootItemID   string        `json:"root_item_id"`
	Items        []ShareItem   `json:"items"`
	Relations    []ShareEdge   `json:"relations"`
	Placeholders []Placeholder `json:"placeholders"`
}

type ShareItem struct {
	ID   string `json:"item_id"`
	Blob Blob   `json:"blob"`
}

type ShareEdge struct {
	Kind         string `json:"kind"`
	SourceItemID string `json:"source_item_id"`
	TargetItemID string `json:"target_item_id"`
}

type Placeholder struct {
	Kind         string `json:"kind"`
	SourceItemID string `json:"source_item_id"`
	Status       string `json:"status"`
}
