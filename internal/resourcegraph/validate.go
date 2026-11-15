package resourcegraph

import "errors"

func (g Graph) Validate() error {
	if g.Type != "resource_graph" || g.Version != Version || g.WorkspaceID == "" {
		return errors.New("invalid_graph_header")
	}

	resources := make(map[string]Resource, len(g.Resources))
	aliases := make(map[string]bool)
	for _, resource := range g.Resources {
		if _, exists := resources[resource.ID]; exists {
			return errors.New("duplicate_resource")
		}
		resources[resource.ID] = resource
		if resource.LegacyDocID == "" {
			continue
		}
		if resource.Kind != "markdown" {
			return errors.New("legacy_id_kind")
		}
		if aliases[resource.LegacyDocID] {
			return errors.New("duplicate_legacy_id")
		}
		aliases[resource.LegacyDocID] = true
	}

	revisions := make(map[string]Revision, len(g.Revisions))
	blobSizes := make(map[string]int64)
	for _, revision := range g.Revisions {
		if _, exists := revisions[revision.ID]; exists {
			return errors.New("duplicate_revision")
		}
		if _, exists := resources[revision.ResourceID]; !exists {
			return errors.New("unknown_revision_resource")
		}
		if !consistentBlobSize(blobSizes, revision.Blob) {
			return errors.New("inconsistent_blob_size")
		}
		revisions[revision.ID] = revision
	}
	if err := validateParents(g.Revisions, revisions); err != nil {
		return err
	}

	relations := make(map[string]bool, len(g.Relations))
	for _, relation := range g.Relations {
		if relations[relation.ID] {
			return errors.New("duplicate_relation")
		}
		relations[relation.ID] = true
		if err := validateRelation(relation, resources, revisions); err != nil {
			return err
		}
	}

	operations := make(map[string]bool, len(g.ChangeEvents))
	for _, event := range g.ChangeEvents {
		if operations[event.OperationID] {
			return errors.New("duplicate_operation")
		}
		operations[event.OperationID] = true
		if _, exists := revisions[event.RevisionID]; !exists {
			return errors.New("unknown_event_revision")
		}
	}
	return nil
}

// A topological walk checks only revision parents. Relation cycles are valid.
func validateParents(ordered []Revision, revisions map[string]Revision) error {
	children := make(map[string][]string)
	remaining := make(map[string]int)
	var ready []string
	for _, revision := range ordered {
		if len(revision.Parents) == 0 {
			ready = append(ready, revision.ID)
		}
		remaining[revision.ID] = len(revision.Parents)
		seen := make(map[string]bool, len(revision.Parents))
		for _, id := range revision.Parents {
			parent, exists := revisions[id]
			if !exists || parent.ResourceID != revision.ResourceID {
				return errors.New("invalid_parent_resource")
			}
			if seen[id] {
				return errors.New("duplicate_parent")
			}
			seen[id] = true
			children[id] = append(children[id], revision.ID)
		}
	}
	for i := 0; i < len(ready); i++ {
		for _, id := range children[ready[i]] {
			remaining[id]--
			if remaining[id] == 0 {
				ready = append(ready, id)
			}
		}
	}
	if len(ready) != len(ordered) {
		return errors.New("revision_cycle")
	}
	return nil
}

func validateRelation(relation Relation, resources map[string]Resource, revisions map[string]Revision) error {
	if _, exists := revisions[relation.SourceRevisionID]; !exists {
		return errors.New("unknown_relation_source")
	}
	target := relation.Target
	resource, exists := resources[target.ResourceID]
	if !exists {
		return errors.New("unknown_relation_target")
	}
	if target.Mode == "follow_head" {
		return nil // The transport must surface multiple heads for live links.
	}
	revision, exists := revisions[target.RevisionID]
	if !exists || revision.ResourceID != target.ResourceID {
		return errors.New("unknown_target_revision")
	}
	if target.Anchor != nil && !validAnchor(*target.Anchor, resource.Kind, revision.Blob.Size) {
		return errors.New("invalid_anchor")
	}
	return nil
}

func validAnchor(anchor Anchor, kind string, bytes int64) bool {
	switch anchor.Kind {
	case "markdown_bytes":
		return kind == "markdown" && anchor.Start != nil && anchor.End != nil && *anchor.End > *anchor.Start && *anchor.End <= bytes
	case "pdf_page":
		if kind != "pdf" {
			return false
		}
		const scale = 1000000
		rect := anchor.Rect
		return rect == nil || (rect.X+rect.Width <= scale && rect.Y+rect.Height <= scale)
	case "csv_cells":
		return kind == "csv" && anchor.EndColumn >= anchor.StartColumn && anchor.EndRow >= anchor.StartRow
	case "audio_ms":
		return kind == "audio" && anchor.Start != nil && anchor.End != nil && *anchor.End > *anchor.Start
	default:
		return false
	}
}

func consistentBlobSize(sizes map[string]int64, blob Blob) bool {
	if size, exists := sizes[blob.SHA256]; exists && size != blob.Size {
		return false
	}
	sizes[blob.SHA256] = blob.Size
	return true
}

// Validate checks referential integrity after schema validation. The caller
// must obtain authorization and project body bytes before constructing this
// record. Nothing in this method can grant access or verify blob contents.
func (s ShareSnapshot) Validate() error {
	if s.Type != "share_snapshot" || s.Version != Version {
		return errors.New("invalid_snapshot_header")
	}
	items := make(map[string]bool, len(s.Items))
	blobSizes := make(map[string]int64)
	for _, item := range s.Items {
		if items[item.ID] {
			return errors.New("duplicate_snapshot_item")
		}
		if !consistentBlobSize(blobSizes, item.Blob) {
			return errors.New("inconsistent_blob_size")
		}
		items[item.ID] = true
	}
	if !items[s.RootItemID] {
		return errors.New("unknown_snapshot_root")
	}
	adjacent := make(map[string][]string)
	seenEdges := make(map[ShareEdge]bool, len(s.Relations))
	for _, relation := range s.Relations {
		if !items[relation.SourceItemID] || !items[relation.TargetItemID] {
			return errors.New("unknown_snapshot_relation_endpoint")
		}
		if seenEdges[relation] {
			return errors.New("duplicate_snapshot_relation")
		}
		seenEdges[relation] = true
		adjacent[relation.SourceItemID] = append(adjacent[relation.SourceItemID], relation.TargetItemID)
	}
	for _, placeholder := range s.Placeholders {
		if !items[placeholder.SourceItemID] {
			return errors.New("unknown_placeholder_source")
		}
	}
	visited := map[string]bool{s.RootItemID: true}
	queue := []string{s.RootItemID}
	for i := 0; i < len(queue); i++ {
		for _, id := range adjacent[queue[i]] {
			if !visited[id] {
				visited[id] = true
				queue = append(queue, id)
			}
		}
	}
	if len(visited) != len(items) {
		return errors.New("unreachable_snapshot_item")
	}
	return nil
}

func (r ShareRequest) Validate(g Graph) error {
	if r.Type != "share_request" || r.Version != Version || r.WorkspaceID != g.WorkspaceID {
		return errors.New("invalid_request_scope")
	}
	for _, revision := range g.Revisions {
		if revision.ID == r.RootRevisionID {
			return nil
		}
	}
	return errors.New("unknown_request_revision")
}
