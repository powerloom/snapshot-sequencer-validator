package ipfs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	
	ipfsApi "github.com/ipfs/kubo/client/rpc"
	"github.com/ipfs/go-cid"
	files "github.com/ipfs/boxo/files"
	"github.com/ipfs/boxo/path"
	log "github.com/sirupsen/logrus"
)

// Client wraps the IPFS kubo client
type Client struct {
	api *ipfsApi.HttpApi
}

// NewClient creates a new IPFS client
func NewClient(apiURL string) (*Client, error) {
	if apiURL == "" {
		apiURL = "/ip4/127.0.0.1/tcp/5001" // Default IPFS API endpoint
	}
	
	api, err := ipfsApi.NewURLApiWithClient(apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create IPFS client: %w", err)
	}
	
	return &Client{
		api: api,
	}, nil
}

// StoreFinalizedBatch stores a finalized batch in IPFS and returns the CID
func (c *Client) StoreFinalizedBatch(ctx context.Context, batch interface{}) (string, error) {
	// Marshal batch to JSON
	jsonData, err := json.Marshal(batch)
	if err != nil {
		return "", fmt.Errorf("failed to marshal batch: %w", err)
	}
	
	// Create a reader from the JSON data
	reader := bytes.NewReader(jsonData)
	
	// Add to IPFS
	path, err := c.api.Unixfs().Add(ctx, files.NewReaderFile(reader))
	if err != nil {
		return "", fmt.Errorf("failed to add to IPFS: %w", err)
	}
	
	cidStr := path.String()
	log.Infof("Stored finalized batch in IPFS: %s", cidStr)
	
	// Pin the content to prevent garbage collection
	if err := c.api.Pin().Add(ctx, path); err != nil {
		log.Warnf("Failed to pin batch CID %s: %v", cidStr, err)
	}
	
	return cidStr, nil
}

// RetrieveFinalizedBatch retrieves a finalized batch from IPFS by CID
func (c *Client) RetrieveFinalizedBatch(ctx context.Context, cidStr string) ([]byte, error) {
	// Parse CID
	parsedCID, err := cid.Parse(cidStr)
	if err != nil {
		return nil, fmt.Errorf("invalid CID %s: %w", cidStr, err)
	}
	
	// Get the content from IPFS using boxo path
	ipfsPath := path.FromCid(parsedCID)
	reader, err := c.api.Unixfs().Get(ctx, ipfsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get from IPFS: %w", err)
	}
	
	// Read as file
	file := files.ToFile(reader)
	if file == nil {
		return nil, fmt.Errorf("expected file from IPFS")
	}
	defer file.Close()
	
	// Read all content
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(file); err != nil {
		return nil, fmt.Errorf("failed to read content: %w", err)
	}
	
	return buf.Bytes(), nil
}

// IsAvailable checks if IPFS node is accessible
func (c *Client) IsAvailable(ctx context.Context) bool {
	// Try to get the node ID as a connectivity check
	_, err := c.api.Key().Self(ctx)
	return err == nil
}