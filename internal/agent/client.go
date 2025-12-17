package agent

import (
	"encoding/json"
	"fmt"
	"net"
	"os/user"
	"path/filepath"
	"runtime"
	"time"
)

// JSONRPCRequest represents a JSON-RPC 2.0 request
type JSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
	ID      interface{} `json:"id"`
}

// JSONRPCResponse represents a JSON-RPC 2.0 response
type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
	ID      interface{} `json:"id"`
}

// RPCError represents a JSON-RPC error
type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// Client is the IPC client for karte-agent
type Client struct {
	socketPath string
	conn       net.Conn
}

// NewClient creates a new agent client
func NewClient() (*Client, error) {
	socketPath, err := getSocketPath()
	if err != nil {
		return nil, fmt.Errorf("failed to get socket path: %w", err)
	}

	return &Client{
		socketPath: socketPath,
	}, nil
}

// getSocketPath returns the socket path based on OS
func getSocketPath() (string, error) {
	currentUser, err := user.Current()
	if err != nil {
		return "", err
	}

	username := currentUser.Username
	username = filepath.Base(username)

	if runtime.GOOS == "windows" {
		return fmt.Sprintf("\\\\.\\pipe\\karte-agent-%s", username), nil
	}

	homeDir := currentUser.HomeDir
	return filepath.Join(homeDir, ".karte", "agent.sock"), nil
}

// Connect connects to the agent
func (c *Client) Connect() error {
	if c.conn != nil {
		return nil // Already connected
	}

	var conn net.Conn
	var err error

	if runtime.GOOS == "windows" {
		// Named Pipe for Windows
		conn, err = net.Dial("tcp", c.socketPath)
	} else {
		// Unix Domain Socket for macOS/Linux
		conn, err = net.Dial("unix", c.socketPath)
	}

	if err != nil {
		return fmt.Errorf("failed to connect to agent: %w", err)
	}

	c.conn = conn
	return nil
}

// Close closes the connection
func (c *Client) Close() error {
	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

// Call calls a JSON-RPC method
func (c *Client) Call(method string, params interface{}) (*JSONRPCResponse, error) {
	// Ensure connected
	if c.conn == nil {
		if err := c.Connect(); err != nil {
			return nil, fmt.Errorf("failed to connect: %w", err)
		}
	}

	// Create request
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      time.Now().UnixNano(),
	}

	// Encode request
	encoder := json.NewEncoder(c.conn)
	if err := encoder.Encode(req); err != nil {
		c.conn = nil // Reset connection on error
		return nil, fmt.Errorf("failed to encode request: %w", err)
	}

	// Decode response
	var resp JSONRPCResponse
	decoder := json.NewDecoder(c.conn)
	if err := decoder.Decode(&resp); err != nil {
		c.conn = nil // Reset connection on error
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &resp, nil
}

// IsConnected checks if the client is connected
func (c *Client) IsConnected() bool {
	return c.conn != nil
}

// CheckConnection checks if agent is available
func (c *Client) CheckConnection() bool {
	if c.conn == nil {
		if err := c.Connect(); err != nil {
			return false
		}
	}
	return true
}


