package vpa

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	abiloader "github.com/powerloom/snapshot-sequencer-validator/pkgs/abi"
)

// ContractABI holds the parsed ABI and provides helper methods
type ContractABI struct {
	abi         abi.ABI
	rawJSON     json.RawMessage
	eventHashes map[string]common.Hash // event name -> keccak256 hash
}

// LoadContractABI loads and parses a contract ABI from file
func LoadContractABI(filepath string) (*ContractABI, error) {
	// Read the ABI file
	data, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read ABI file: %w", err)
	}

	// Parse the ABI
	parsedABI, err := abi.JSON(strings.NewReader(string(data)))
	if err != nil {
		return nil, fmt.Errorf("failed to parse ABI: %w", err)
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
func (c *ContractABI) ParseLog(log any) (string, any, error) {
	// This is a placeholder - actual implementation would depend on log format
	// For now, we'll focus on the event hash extraction functionality
	return "", nil, fmt.Errorf("ParseLog not implemented")
}

// FetchVPAAddress fetches the VPA contract address from the NEW ProtocolState contract
func FetchVPAAddress(rpcURL, newProtocolStateContract string) (common.Address, error) {
	fmt.Printf("🔍 DEBUG: FetchVPAAddress called with RPC: %s, Contract: %s\n", rpcURL, newProtocolStateContract)

	if rpcURL == "" {
		return common.Address{}, fmt.Errorf("RPC URL is required")
	}
	if newProtocolStateContract == "" {
		return common.Address{}, fmt.Errorf("NEW ProtocolState contract address is required")
	}

	// Load NEW ProtocolState ABI using standardized path resolution
	protocolStateABIParsed, err := abiloader.LoadABI("PowerloomProtocolState.abi.json")
	if err != nil {
		return common.Address{}, fmt.Errorf("failed to load NEW ProtocolState ABI: %w", err)
	}

	// Convert to ContractABI wrapper for compatibility with existing code
	protocolStateABI := &ContractABI{
		abi:         protocolStateABIParsed,
		eventHashes: make(map[string]common.Hash),
	}

	// Pre-compute event hashes
	for eventName, event := range protocolStateABIParsed.Events {
		hash := crypto.Keccak256Hash([]byte(event.Sig))
		protocolStateABI.eventHashes[eventName] = hash
	}

	// Create ethclient
	fmt.Printf("🔍 DEBUG: Creating ethclient...\n")
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		return common.Address{}, fmt.Errorf("failed to create ethclient: %w", err)
	}
	defer client.Close()
	fmt.Printf("🔍 DEBUG: Successfully created ethclient\n")

	// Create contract binding
	parsedABI := protocolStateABI.GetABI()
	protocolStateAddress := common.HexToAddress(newProtocolStateContract)
	contract := bind.NewBoundContract(protocolStateAddress, parsedABI, client, client, client)
	fmt.Printf("🔍 DEBUG: Created contract binding for address: %s\n", protocolStateAddress.Hex())

	// Call validatorPriorityAssigner() method
	var results []any
	opts := &bind.CallOpts{Context: context.Background()}
	fmt.Printf("🔍 DEBUG: Calling validatorPriorityAssigner()...\n")
	err = contract.Call(opts, &results, "validatorPriorityAssigner")
	if err != nil {
		return common.Address{}, fmt.Errorf("failed to call validatorPriorityAssigner(): %w", err)
	}
	fmt.Printf("🔍 DEBUG: Successfully called validatorPriorityAssigner()\n")

	if len(results) == 0 {
		return common.Address{}, fmt.Errorf("validatorPriorityAssigner() returned no results")
	}

	vpaAddress, ok := results[0].(common.Address)
	if !ok {
		return common.Address{}, fmt.Errorf("validatorPriorityAssigner() returned invalid type: %T", results[0])
	}

	fmt.Printf("🔍 DEBUG: VPA Address: %s\n", vpaAddress.Hex())

	if vpaAddress == (common.Address{}) {
		return common.Address{}, fmt.Errorf("NEW ProtocolState returned zero address for validatorPriorityAssigner")
	}

	return vpaAddress, nil
}
