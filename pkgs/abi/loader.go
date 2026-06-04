package abi

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/sirupsen/logrus"
)

// GetABIDir returns the base directory for ABI files
// Checks environment variable ABI_DIR first, then uses defaults
func GetABIDir() string {
	// Check environment variable first
	if abiDir := os.Getenv("ABI_DIR"); abiDir != "" {
		return abiDir
	}

	// Default paths (try in order)
	defaultPaths := []string{
		"/root/abi", // Docker container (standard location)
		"./abi",     // Local development
		"/app/abi",  // Alternative Docker path
	}

	for _, path := range defaultPaths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	// Fallback to Docker path (most common)
	return "/root/abi"
}

// ResolveABIPath resolves the full path to an ABI file
// Uses ABI_DIR environment variable or defaults
func ResolveABIPath(filename string) string {
	abiDir := GetABIDir()
	return filepath.Join(abiDir, filename)
}

// HardhatArtifact represents a Hardhat compilation artifact
type HardhatArtifact struct {
	Format       string          `json:"_format"`
	ContractName string          `json:"contractName"`
	SourceName   string          `json:"sourceName"`
	ABI          json.RawMessage `json:"abi"`
	Bytecode     string          `json:"bytecode,omitempty"`
}

// LoadABI loads an ABI from file using standardized path resolution
// Supports both raw ABI JSON files and Hardhat artifact files
func LoadABI(filename string) (abi.ABI, error) {
	abiPath := ResolveABIPath(filename)

	logrus.WithField("path", abiPath).Debug("Loading ABI file")

	data, err := os.ReadFile(abiPath)
	if err != nil {
		return abi.ABI{}, fmt.Errorf("failed to read ABI file %s: %w", abiPath, err)
	}

	// Try to parse as Hardhat artifact first
	var artifact HardhatArtifact
	if err := json.Unmarshal(data, &artifact); err == nil && artifact.Format != "" {
		// This is a Hardhat artifact, extract the ABI
		logrus.WithFields(logrus.Fields{
			"path":         abiPath,
			"contractName": artifact.ContractName,
			"format":       artifact.Format,
		}).Debug("Detected Hardhat artifact, extracting ABI")

		parsedABI, err := abi.JSON(strings.NewReader(string(artifact.ABI)))
		if err != nil {
			return abi.ABI{}, fmt.Errorf("failed to parse ABI from Hardhat artifact %s: %w", abiPath, err)
		}

		logrus.WithFields(logrus.Fields{
			"path":    abiPath,
			"methods": len(parsedABI.Methods),
			"events":  len(parsedABI.Events),
		}).Debug("Successfully loaded ABI from Hardhat artifact")

		return parsedABI, nil
	}

	// Not a Hardhat artifact, try parsing as raw ABI
	parsedABI, err := abi.JSON(strings.NewReader(string(data)))
	if err != nil {
		return abi.ABI{}, fmt.Errorf("failed to parse ABI from %s (not a Hardhat artifact or valid ABI): %w", abiPath, err)
	}

	logrus.WithFields(logrus.Fields{
		"path":    abiPath,
		"methods": len(parsedABI.Methods),
		"events":  len(parsedABI.Events),
	}).Debug("Successfully loaded raw ABI")

	return parsedABI, nil
}

// MustLoadABI loads an ABI and panics on error (for initialization)
func MustLoadABI(filename string) abi.ABI {
	parsedABI, err := LoadABI(filename)
	if err != nil {
		logrus.WithError(err).Fatalf("Failed to load required ABI: %s", filename)
	}
	return parsedABI
}
