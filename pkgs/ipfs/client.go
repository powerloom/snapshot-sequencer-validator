package ipfs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

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
		apiURL = "127.0.0.1:5001" // Default IPFS API endpoint
	}

	// Handle different input formats
	if strings.HasPrefix(apiURL, "/ip4/") || strings.HasPrefix(apiURL, "/dns/") {
		// Convert multiaddr: /ip4/172.29.0.2/tcp/5001 -> http://172.29.0.2:5001
		parts := strings.Split(apiURL, "/")
		if len(parts) >= 5 {
			host := parts[2]
			port := parts[4]
			apiURL = fmt.Sprintf("http://%s:%s", host, port)
		}
	} else if !strings.HasPrefix(apiURL, "http://") && !strings.HasPrefix(apiURL, "https://") {
		// Simple host:port format -> add http://
		apiURL = "http://" + apiURL
	}

	// Create HTTP client with reasonable timeouts
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        10,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression:  true,
		},
	}

	api, err := ipfsApi.NewURLApiWithClient(apiURL, httpClient)
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
	
	// Add to IPFS with CIDv1 format
	path, err := c.api.Unixfs().Add(ctx, files.NewReaderFile(reader), func(settings *ipfsApi.UnixfsAddSettings) error {
		settings.CidVersion = 1  // Force CIDv1
		settings.Chunker = "size-262144"  // 256KB chunks
		settings.Pin = true  // Auto-pin
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to add to IPFS: %w", err)
	}
	
	cidStr := path.String()
	log.Infof("Stored finalized batch in IPFS: %s", cidStr)
	
	// Content is already pinned via settings.Pin = true
	
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