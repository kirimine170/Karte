package ephyoutbox

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ExperimentProducer only prepares evidence, queues proposals, and reads status.
// Acceptance remains a separate Karte human-review operation.
type ExperimentProducer struct{ publisher *ExperimentPublisher }

type ExperimentProducerStatus struct {
	CandidateID   string   `json:"candidate_id"`
	Phase         string   `json:"phase"`
	PayloadSHA256 string   `json:"payload_sha256"`
	State         string   `json:"state"`
	Verification  string   `json:"verification"`
	Adopted       bool     `json:"adopted"`
	Receipt       *Receipt `json:"receipt,omitempty"`
}

func NewExperimentProducer(dataDir string) (*ExperimentProducer, error) {
	p, err := NewExperimentPublisher(dataDir)
	if err != nil {
		return nil, err
	}
	return &ExperimentProducer{publisher: p}, nil
}

func (p *ExperimentProducer) Close() error { return p.publisher.Close() }

func (p *ExperimentProducer) operationRoot() (*os.Root, error) {
	return p.publisher.store.dataHandle.OpenRoot(".")
}

const producerBindingDir = ".mdsys/ephy/experiment-producer"
const producerOutboxDir = ".mdsys/ephy/outbox"

func producerDirectory(root *os.Root, name string, create bool) (*os.Root, error) {
	current, err := root.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(name, "/") {
		if create {
			if err := current.Mkdir(part, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				current.Close()
				return nil, err
			}
		}
		info, err := current.Lstat(part)
		var next *os.Root
		if err == nil {
			next, err = openEvidenceDirectory(current, part)
		}
		if err == nil {
			opened, statErr := next.Stat(".")
			if statErr != nil || !os.SameFile(info, opened) {
				err = fmt.Errorf("producer directory changed while opening: %s", part)
			}
		}
		if err == nil && create {
			err = syncEvidenceDirectory(current)
		}
		current.Close()
		if err != nil {
			if next != nil {
				next.Close()
			}
			return nil, err
		}
		current = next
	}
	return current, nil
}

func readProducerInputFile(name string, limit int64) ([]byte, error) {
	abs, err := filepath.Abs(name)
	if err != nil {
		return nil, err
	}
	root, err := openProducerInputRoot(filepath.Dir(abs))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return readProducerRootFile(root, filepath.Base(abs), limit)
}

// Every segment is inspected through an anchored root; neither source files nor
// outbox reads accept links, case aliases, devices, or oversized allocation.
func readProducerRootFile(root *os.Root, name string, limit int64) ([]byte, error) {
	var parent *os.Root
	var err error
	if path.Dir(name) == "." {
		parent, err = root.OpenRoot(".")
	} else {
		parent, err = producerDirectory(root, path.Dir(name), false)
	}
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	base := path.Base(name)
	info, err := parent.Lstat(base)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("input must be a regular file within size limit: %s", name)
	}
	if err := exactEvidenceEntry(parent, base); err != nil {
		return nil, err
	}
	file, err := parent.Open(base)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("input file changed while opening: %s", name)
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("input exceeds size limit: %s", name)
	}
	return raw, nil
}

// Linking a completely flushed temporary file creates a destination in one
// exclusive filesystem operation. Link never replaces an existing destination.
// Unsupported filesystems fail closed; there is no check-then-rename fallback.
func installProducerJSON(root *os.Root, name string, raw []byte) error {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	temp := ".producer-" + hex.EncodeToString(nonce[:]) + ".tmp"
	if err := writeEvidenceFile(root, temp, raw); err != nil {
		root.Remove(temp)
		return err
	}
	defer root.Remove(temp)
	if err := root.Link(temp, name); err != nil {
		existing, readErr := readProducerRootFile(root, name, producerMaxJSON)
		if readErr != nil || !bytes.Equal(raw, existing) {
			return fmt.Errorf("publish without replacement: %w; existing payload differs or is unreadable (%v)", err, readErr)
		}
	}
	return syncEvidenceDirectory(root)
}

func (p *ExperimentProducer) Prepare(bundleDir, metadataPath string) (ExperimentProducerStatus, error) {
	// Never put derived managed writes inside the original Worker bundle.
	inputPath, err := filepath.EvalSymlinks(bundleDir)
	if err != nil {
		return ExperimentProducerStatus{}, err
	}
	inputPath, err = filepath.Abs(inputPath)
	if err != nil {
		return ExperimentProducerStatus{}, err
	}
	dataPath, err := filepath.EvalSymlinks(p.publisher.store.dataRoot)
	if err != nil {
		return ExperimentProducerStatus{}, err
	}
	inside, err := filepath.Rel(inputPath, dataPath)
	if err != nil {
		return ExperimentProducerStatus{}, err
	}
	if inside == "." || (inside != ".." && !strings.HasPrefix(inside, ".."+string(filepath.Separator))) {
		return ExperimentProducerStatus{}, fmt.Errorf("Karte data directory must be outside the read-only Worker bundle")
	}
	binding, contents, err := loadWorkerBundle(bundleDir, metadataPath)
	if err != nil {
		return ExperimentProducerStatus{}, err
	}
	root, err := p.operationRoot()
	if err != nil {
		return ExperimentProducerStatus{}, err
	}
	defer root.Close()
	id := binding.Record.CandidateID
	// Bind all metadata, source bytes, record, and proposal before any evidence or
	// pending write. The binding remains after receipts, preventing ID reuse.
	existing, readErr := readProducerRootFile(root, producerBindingDir+"/"+id+".json", producerMaxJSON)
	if readErr == nil {
		if !bytes.Equal(existing, producerJSON(binding)) {
			return ExperimentProducerStatus{}, fmt.Errorf("candidate_id is already bound to a different complete payload")
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return ExperimentProducerStatus{}, readErr
	} else {
		oldEvidence, evidenceErr := producerDirectory(root, ".mdsys/ephy/experiments/"+id, false)
		if oldEvidence != nil {
			oldEvidence.Close()
		}
		if !errors.Is(evidenceErr, os.ErrNotExist) {
			return ExperimentProducerStatus{}, fmt.Errorf("candidate evidence exists without this producer binding (%v)", evidenceErr)
		}
		for _, dir := range []string{"pending", "accepted", "rejected", "receipts", "transactions"} {
			_, err := readProducerRootFile(root, producerOutboxDir+"/"+dir+"/"+id+".json", producerMaxJSON)
			if !errors.Is(err, os.ErrNotExist) {
				return ExperimentProducerStatus{}, fmt.Errorf("candidate exists in outbox without this producer binding: %s (%v)", dir, err)
			}
		}
	}
	dir, err := producerDirectory(root, producerBindingDir, true)
	if err != nil {
		return ExperimentProducerStatus{}, err
	}
	err = installProducerJSON(dir, id+".json", producerJSON(binding))
	dir.Close()
	if err != nil {
		return ExperimentProducerStatus{}, err
	}
	if _, err := p.publisher.Publish(binding.Record, contents); err != nil {
		return ExperimentProducerStatus{}, err
	}
	return p.Status(id)
}

func (p *ExperimentProducer) readBinding(root *os.Root, id string) (producerBinding, error) {
	if !validEvidenceCandidate(id) {
		return producerBinding{}, fmt.Errorf("invalid candidate_id")
	}
	raw, err := readProducerRootFile(root, producerBindingDir+"/"+id+".json", producerMaxJSON)
	if err != nil {
		return producerBinding{}, err
	}
	var binding producerBinding
	if err := decodeProducerJSON(raw, &binding); err != nil {
		return producerBinding{}, err
	}
	if binding.SchemaVersion != producerBindingVersion || binding.Record.CandidateID != id {
		return producerBinding{}, fmt.Errorf("producer binding identity mismatch")
	}
	if len(binding.Entries) != len(workerArtifactIDs)+2 {
		return producerBinding{}, fmt.Errorf("producer binding evidence count mismatch")
	}
	contents := map[string][]byte{}
	var total int64
	for _, entry := range binding.Entries {
		if !isValidLogicalRef(entry.LogicalRef) || !isSHA256(entry.SHA256) || entry.SizeBytes < 0 || entry.SizeBytes > producerMaxArtifact {
			return producerBinding{}, fmt.Errorf("invalid producer binding entry")
		}
		if _, ok := contents[entry.LogicalRef]; ok {
			return producerBinding{}, fmt.Errorf("duplicate producer binding entry")
		}
		total += entry.SizeBytes
		if total > producerMaxEvidence+2*producerMaxJSON {
			return producerBinding{}, fmt.Errorf("producer binding exceeds total size limit")
		}
		content, err := readProducerRootFile(root, ".mdsys/ephy/experiments/"+id+"/"+entry.LogicalRef, entry.SizeBytes)
		if err != nil || int64(len(content)) != entry.SizeBytes || SHA256Bytes(content) != entry.SHA256 {
			return producerBinding{}, fmt.Errorf("prepared evidence is missing or changed: %s (%v)", entry.LogicalRef, err)
		}
		contents[entry.LogicalRef] = content
	}
	expected, err := buildProducerBinding(contents)
	if err != nil {
		return producerBinding{}, err
	}
	if !bytes.Equal(producerJSON(expected), producerJSON(binding)) {
		return producerBinding{}, fmt.Errorf("complete producer payload binding mismatch")
	}
	if err := p.publisher.store.Verify(id, binding.Record.Evidence); err != nil {
		return producerBinding{}, err
	}
	return binding, nil
}

func producerStatus(root *os.Root, binding producerBinding) (ExperimentProducerStatus, error) {
	id := binding.Record.CandidateID
	status := ExperimentProducerStatus{CandidateID: id, Phase: "prepared", PayloadSHA256: SHA256Bytes(producerJSON(binding)), State: "experiment", Verification: "unverified", Adopted: false}
	found := map[string]bool{}
	for _, dir := range []string{"pending", "accepted", "rejected"} {
		raw, err := readProducerRootFile(root, producerOutboxDir+"/"+dir+"/"+id+".json", producerMaxJSON)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return status, err
		}
		proposal, err := DecodeProposal(raw)
		if err != nil {
			return status, err
		}
		if !bytes.Equal(producerJSON(proposal), producerJSON(binding.Proposal)) {
			return status, fmt.Errorf("outbox proposal does not match bound payload: %s", dir)
		}
		found[dir] = true
	}
	if found["accepted"] && found["rejected"] {
		return status, fmt.Errorf("contradictory processed proposals")
	}
	raw, err := readProducerRootFile(root, producerOutboxDir+"/receipts/"+id+".json", producerMaxJSON)
	if errors.Is(err, os.ErrNotExist) {
		if found["accepted"] || found["rejected"] {
			return status, fmt.Errorf("processed proposal has no receipt")
		}
		if found["pending"] {
			status.Phase = "pending"
		}
		return status, nil
	}
	if err != nil {
		return status, err
	}
	var receipt Receipt
	if err := decodeProducerJSON(raw, &receipt); err != nil {
		return status, err
	}
	if err := receipt.Validate(); err != nil {
		return status, err
	}
	if receipt.CandidateID != id {
		return status, fmt.Errorf("receipt candidate_id does not match requested payload")
	}
	docID, _ := DeriveCreateDocID(id)
	if receipt.DocID != nil && *receipt.DocID != docID {
		return status, fmt.Errorf("receipt doc_id does not match bound create proposal")
	}
	if receipt.RelativePath != nil {
		if !producerReceiptPlacement(binding.Proposal, docID, *receipt.RelativePath) {
			return status, fmt.Errorf("receipt placement does not match bound report proposal")
		}
	}
	switch receipt.Result {
	case "accepted":
		if !found["accepted"] || found["pending"] {
			return status, fmt.Errorf("accepted receipt requires the matching archived proposal; acceptance may still be finishing")
		}
		status.Phase = "report_accepted"
	case "rejected":
		if !found["rejected"] || found["pending"] {
			return status, fmt.Errorf("rejected receipt requires the matching archived proposal; rejection may still be finishing")
		}
		status.Phase = "rejected"
	default:
		if !found["pending"] && !found["rejected"] {
			return status, fmt.Errorf("receipt has no matching proposal")
		}
		status.Phase = receipt.Result
	}
	status.Receipt = &receipt
	return status, nil
}

func producerReceiptPlacement(proposal Proposal, docID, relativePath string) bool {
	primary := buildPlacementPath(proposal.Placement.Project, "report", proposal.Placement.YearMonth, proposal.Placement.PreferredFilename)
	if relativePath == primary {
		return true
	}
	if path.Dir(relativePath) != path.Dir(primary) {
		return false
	}
	ext := path.Ext(primary)
	stem := strings.TrimSuffix(path.Base(primary), ext)
	for length := collisionDocIDPrefixLength; length <= len(docID); length += 4 {
		if path.Base(relativePath) == stem+"--"+docID[:length]+ext {
			return true
		}
	}
	return false
}

func (p *ExperimentProducer) Status(id string) (ExperimentProducerStatus, error) {
	root, err := p.operationRoot()
	if err != nil {
		return ExperimentProducerStatus{}, err
	}
	defer root.Close()
	binding, err := p.readBinding(root, id)
	if err != nil {
		return ExperimentProducerStatus{}, err
	}
	return producerStatus(root, binding)
}

func (p *ExperimentProducer) Publish(id string) (ExperimentProducerStatus, error) {
	root, err := p.operationRoot()
	if err != nil {
		return ExperimentProducerStatus{}, err
	}
	defer root.Close()
	binding, err := p.readBinding(root, id)
	if err != nil {
		return ExperimentProducerStatus{}, err
	}
	status, err := producerStatus(root, binding)
	if err != nil {
		return status, err
	}
	if status.Receipt != nil {
		return status, nil
	}
	dir, err := producerDirectory(root, producerOutboxDir+"/pending", true)
	if err != nil {
		return status, err
	}
	defer dir.Close()
	if err := installProducerJSON(dir, id+".json", producerJSON(binding.Proposal)); err != nil {
		return status, err
	}
	// Re-read after publication; a concurrent human acceptance remains visible.
	return producerStatus(root, binding)
}
