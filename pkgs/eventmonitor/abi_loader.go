package eventmonitor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	abiloader "github.com/powerloom/snapshot-sequencer-validator/pkgs/abi"
)

// ContractABI holds the parsed ABI and provides helper methods
type ContractABI struct {
	abi         abi.ABI
	rawJSON     json.RawMessage
	eventHashes map[string]common.Hash // event name -> keccak256 hash
}

// LoadContractABI loads and parses a contract ABI from file
// Supports both absolute paths and filenames (resolved via standardized ABI loader)
func LoadContractABI(filepathOrFilename string) (*ContractABI, error) {
	var parsedABI abi.ABI
	var data []byte
	var err error

	// Check if it's an absolute path or just a filename
	if filepath.IsAbs(filepathOrFilename) || strings.Contains(filepathOrFilename, string(os.PathSeparator)) {
		// Absolute path or relative path - read directly
		data, err = os.ReadFile(filepathOrFilename)
		if err != nil {
			return nil, fmt.Errorf("failed to read ABI file: %w", err)
		}

		// Try to parse as Hardhat artifact first
		var artifact struct {
			Format       string          `json:"_format"`
			ContractName string          `json:"contractName"`
			ABI          json.RawMessage `json:"abi"`
		}
		if err := json.Unmarshal(data, &artifact); err == nil && artifact.Format != "" {
			// This is a Hardhat artifact, extract the ABI
			parsedABI, err = abi.JSON(strings.NewReader(string(artifact.ABI)))
			if err != nil {
				return nil, fmt.Errorf("failed to parse ABI from Hardhat artifact: %w", err)
			}
		} else {
			// Not a Hardhat artifact, parse as raw ABI
			parsedABI, err = abi.JSON(strings.NewReader(string(data)))
			if err != nil {
				return nil, fmt.Errorf("failed to parse ABI: %w", err)
			}
		}
	} else {
		// Just a filename - use standardized loader
		parsedABI, err = abiloader.LoadABI(filepathOrFilename)
		if err != nil {
			return nil, fmt.Errorf("failed to load ABI: %w", err)
		}
		// Read raw data for rawJSON field
		abiPath := abiloader.ResolveABIPath(filepathOrFilename)
		data, err = os.ReadFile(abiPath)
		if err != nil {
			// If we can't read the file again, just use empty rawJSON
			data = []byte{}
		}
	}

	// Create the ContractABI instance
	contractABI := &ContractABI{
		abi:         parsedABI,
		rawJSON:     data,
		eventHashes: make(map[string]common.Hash),
	}

	// Pre-compute event hashes
	for eventName, event := range parsedABI.Events {
		// The event.Sig already contains the canonical signature string
		hash := crypto.Keccak256Hash([]byte(event.Sig))
		contractABI.eventHashes[eventName] = hash
	}

	return contractABI, nil
}

// GetEventHash returns the keccak256 hash for a given event name
func (c *ContractABI) GetEventHash(eventName string) (common.Hash, error) {
	hash, exists := c.eventHashes[eventName]
	if !exists {
		return common.Hash{}, fmt.Errorf("event %s not found in ABI", eventName)
	}
	return hash, nil
}

// GetEvent returns the ABI event definition for a given event name
func (c *ContractABI) GetEvent(eventName string) (abi.Event, error) {
	event, exists := c.abi.Events[eventName]
	if !exists {
		return abi.Event{}, fmt.Errorf("event %s not found in ABI", eventName)
	}
	return event, nil
}

// HasEvent checks if an event exists in the ABI
func (c *ContractABI) HasEvent(eventName string) bool {
	_, exists := c.abi.Events[eventName]
	return exists
}

// GetABI returns the underlying parsed ABI
func (c *ContractABI) GetABI() abi.ABI {
	return c.abi
}

// ParseLog parses a log entry using the ABI
func (c *ContractABI) ParseLog(log interface{}) (string, interface{}, error) {
	// This is a placeholder - actual implementation would depend on log format
	// For now, we'll focus on the event hash extraction functionality
	return "", nil, fmt.Errorf("ParseLog not implemented")
}
