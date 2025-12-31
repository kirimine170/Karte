package docid

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	nodeIDOnce sync.Once
	nodeID     string
	nodeIDErr  error
	seqMutex   sync.Mutex
)

// getNodeID returns a machine-specific ID based on MAC address or hostname
func getNodeID() (string, error) {
	nodeIDOnce.Do(func() {
		// Try to get MAC address first
		interfaces, err := net.Interfaces()
		if err == nil {
			for _, iface := range interfaces {
				if iface.Flags&net.FlagLoopback == 0 && iface.Flags&net.FlagUp != 0 {
					if addr := iface.HardwareAddr; len(addr) > 0 {
						hash := sha256.Sum256(addr)
						nodeID = hex.EncodeToString(hash[:])[:16] // Use first 16 chars
						return
					}
				}
			}
		}

		// Fallback to hostname hash
		hostname, err := os.Hostname()
		if err == nil {
			hash := sha256.Sum256([]byte(hostname))
			nodeID = hex.EncodeToString(hash[:])[:16]
			return
		}

		// Last resort: use a fixed value (not ideal but ensures functionality)
		nodeID = "0000000000000000"
		nodeIDErr = fmt.Errorf("failed to generate node ID, using default")
	})

	return nodeID, nodeIDErr
}

// getNextSequence returns the next sequence number for doc_id generation
func getNextSequence(seqFile string) (int64, error) {
	seqMutex.Lock()
	defer seqMutex.Unlock()

	var seq int64 = 0

	// Read existing sequence if file exists
	if data, err := os.ReadFile(seqFile); err == nil {
		var seqData struct {
			Sequence int64 `json:"sequence"`
		}
		if err := json.Unmarshal(data, &seqData); err == nil {
			seq = seqData.Sequence
		}
	}

	// Increment sequence
	seq++

	// Save updated sequence
	seqData := struct {
		Sequence int64 `json:"sequence"`
	}{
		Sequence: seq,
	}

	data, err := json.MarshalIndent(seqData, "", "  ")
	if err != nil {
		return 0, fmt.Errorf("failed to marshal sequence: %v", err)
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(seqFile), 0755); err != nil {
		return 0, fmt.Errorf("failed to create sequence directory: %v", err)
	}

	if err := os.WriteFile(seqFile, data, 0644); err != nil {
		return 0, fmt.Errorf("failed to write sequence file: %v", err)
	}

	return seq, nil
}

// GenerateDocID generates a unique document ID using sha256(node_id || timestamp || local_seq)
// seqFile is the path to the sequence file (e.g., ".mdsys/doc_seq.json")
func GenerateDocID(seqFile string) (string, error) {
	nodeID, err := getNodeID()
	if err != nil {
		// Continue even if node ID generation had issues
	}

	timestamp := time.Now().UnixNano()

	seq, err := getNextSequence(seqFile)
	if err != nil {
		return "", fmt.Errorf("failed to get sequence: %v", err)
	}

	// Combine: node_id || timestamp || local_seq
	combined := fmt.Sprintf("%s:%d:%d", nodeID, timestamp, seq)

	// Generate SHA256 hash
	hash := sha256.Sum256([]byte(combined))
	docID := hex.EncodeToString(hash[:])

	return docID, nil
}

