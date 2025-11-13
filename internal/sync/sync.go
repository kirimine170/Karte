package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
	// "github.com/wailsapp/wails/v2/pkg/runtime" // Removed: v2 dependency, sync is currently disabled
)

// SyncManager manages file synchronization between peers
type SyncManager struct {
	ctx        context.Context
	root       string
	Peers      map[string]*Peer
	PeersMutex sync.RWMutex
	listener   net.Listener
	port       int
}

// Peer represents a connected peer
type Peer struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Address  string    `json:"address"`
	Port     int       `json:"port"`
	LastSeen time.Time `json:"last_seen"`
	Conn     net.Conn
}

// FileChange represents a file change event
type FileChange struct {
	Path    string    `json:"path"`
	Content string    `json:"content"`
	Time    time.Time `json:"time"`
	PeerID  string    `json:"peer_id"`
}

// NewSyncManager creates a new sync manager
func NewSyncManager(ctx context.Context, root string) *SyncManager {
	return &SyncManager{
		ctx:   ctx,
		root:  root,
		Peers: make(map[string]*Peer),
		port:  8080, // Default port for sync
	}
}

// Start starts the sync manager
func (sm *SyncManager) Start() error {
	// Start listening for peer connections
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", sm.port))
	if err != nil {
		return fmt.Errorf("failed to start listener: %v", err)
	}
	sm.listener = listener

	// Start peer discovery
	go sm.startPeerDiscovery()

	// Start accepting connections
	go sm.acceptConnections()

	log.Printf("[Sync] Sync manager started on port %d", sm.port)
	return nil
}

// Stop stops the sync manager
func (sm *SyncManager) Stop() error {
	if sm.listener != nil {
		sm.listener.Close()
	}

	// Close all peer connections
	sm.PeersMutex.Lock()
	for _, peer := range sm.Peers {
		if peer.Conn != nil {
			peer.Conn.Close()
		}
	}
	sm.PeersMutex.Unlock()

	return nil
}

// GetPeers returns the list of connected peers
func (sm *SyncManager) GetPeers() []Peer {
	sm.PeersMutex.RLock()
	defer sm.PeersMutex.RUnlock()

	var peers []Peer
	for _, peer := range sm.Peers {
		peers = append(peers, *peer)
	}
	return peers
}

// ConnectToPeer connects to a peer
func (sm *SyncManager) ConnectToPeer(address string, port int) error {
	conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", address, port))
	if err != nil {
		return fmt.Errorf("failed to connect to peer: %v", err)
	}

	peer := &Peer{
		ID:       fmt.Sprintf("peer_%d", time.Now().Unix()),
		Name:     fmt.Sprintf("Peer at %s:%d", address, port),
		Address:  address,
		Port:     port,
		LastSeen: time.Now(),
		Conn:     conn,
	}

	sm.PeersMutex.Lock()
	sm.Peers[peer.ID] = peer
	sm.PeersMutex.Unlock()

	// Start handling peer messages
	go sm.handlePeerMessages(peer)

	log.Printf("[Sync] Connected to peer %s", peer.Name)
	return nil
}

// BroadcastFileChange broadcasts a file change to all peers
func (sm *SyncManager) BroadcastFileChange(path, content string) error {
	change := FileChange{
		Path:    path,
		Content: content,
		Time:    time.Now(),
		PeerID:  "local",
	}

	data, err := json.Marshal(change)
	if err != nil {
		return fmt.Errorf("failed to marshal file change: %v", err)
	}

	sm.PeersMutex.RLock()
	defer sm.PeersMutex.RUnlock()

	for _, peer := range sm.Peers {
		if peer.Conn != nil {
			_, err := peer.Conn.Write(append(data, '\n'))
			if err != nil {
				log.Printf("[Sync] Error: Failed to send to peer %s: %v", peer.Name, err)
			}
		}
	}

	return nil
}

// startPeerDiscovery starts peer discovery using mDNS/Bonjour
func (sm *SyncManager) startPeerDiscovery() {
	// Simple peer discovery implementation
	// In a production app, you'd use mDNS/Bonjour
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-sm.ctx.Done():
			return
		case <-ticker.C:
			sm.discoverPeers()
		}
	}
}

// discoverPeers discovers peers on the local network
func (sm *SyncManager) discoverPeers() {
	// Simple implementation - scan local network for peers
	// In production, use proper mDNS/Bonjour discovery
	interfaces, err := net.Interfaces()
	if err != nil {
		return
	}

	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ipnet.IP.To4() != nil {
					sm.scanNetwork(ipnet.IP.String())
				}
			}
		}
	}
}

// scanNetwork scans a network for peers
func (sm *SyncManager) scanNetwork(network string) {
	// Simple network scanning implementation
	// In production, use proper discovery protocols
	for i := 1; i < 255; i++ {
		ip := fmt.Sprintf("%s.%d", network[:len(network)-1], i)
		go func(addr string) {
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", addr, sm.port), time.Second)
			if err == nil {
				conn.Close()
				// Found a peer, try to connect
				sm.ConnectToPeer(addr, sm.port)
			}
		}(ip)
	}
}

// acceptConnections accepts incoming peer connections
func (sm *SyncManager) acceptConnections() {
	for {
		conn, err := sm.listener.Accept()
		if err != nil {
			if sm.ctx.Err() != nil {
				return
			}
			log.Printf("[Sync] Error: Failed to accept connection: %v", err)
			continue
		}

		peer := &Peer{
			ID:       fmt.Sprintf("peer_%d", time.Now().Unix()),
			Name:     fmt.Sprintf("Peer from %s", conn.RemoteAddr()),
			Address:  conn.RemoteAddr().String(),
			LastSeen: time.Now(),
			Conn:     conn,
		}

		sm.PeersMutex.Lock()
		sm.Peers[peer.ID] = peer
		sm.PeersMutex.Unlock()

		go sm.handlePeerMessages(peer)
	}
}

// handlePeerMessages handles messages from a peer
func (sm *SyncManager) handlePeerMessages(peer *Peer) {
	defer func() {
		if peer.Conn != nil {
			peer.Conn.Close()
		}
		sm.PeersMutex.Lock()
		delete(sm.Peers, peer.ID)
		sm.PeersMutex.Unlock()
	}()

	decoder := json.NewDecoder(peer.Conn)
	for {
		var change FileChange
		err := decoder.Decode(&change)
		if err != nil {
			log.Printf("[Sync] Error: Failed to decode message from peer %s: %v", peer.Name, err)
			return
		}

		// Apply file change
		err = sm.applyFileChange(change)
		if err != nil {
			log.Printf("[Sync] Error: Failed to apply file change: %v", err)
		}

		// Emit event to frontend (disabled: v2 dependency removed)
		// runtime.EventsEmit(sm.ctx, "file-synced", change)
	}
}

// applyFileChange applies a file change from a peer
func (sm *SyncManager) applyFileChange(change FileChange) error {
	// Ensure the file path is safe
	if !filepath.IsLocal(change.Path) {
		return fmt.Errorf("unsafe file path: %s", change.Path)
	}

	fullPath := filepath.Join(sm.root, change.Path)

	// Create directory if it doesn't exist
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %v", err)
	}

	// Write file
	err := os.WriteFile(fullPath, []byte(change.Content), 0644)
	if err != nil {
		return fmt.Errorf("failed to write file: %v", err)
	}

	log.Printf("[Sync] Applied file change from peer: %s", change.Path)
	return nil
}
