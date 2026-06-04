//go:build ignore
// +build ignore

package main

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	abiloader "github.com/powerloom/snapshot-sequencer-validator/pkgs/abi"
	"github.com/sirupsen/logrus"
)

func main() {
	// Setup logging
	log := logrus.New()
	log.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})
	log.SetLevel(logrus.DebugLevel)

	// Configuration from env.validator2.devnet
	rpcURL := "https://devnet-orbit-rpc.aws2.powerloom.io/rpc?uuid=9dff8954-24f0-11f0-b88f-devnet"
	protocolStateAddr := "0xC9e7304f719D35919b0371d8B242ab59E0966d63"
	dataMarketAddr := "0xb6c1392944a335b72b9e34f9D4b8c0050cdb511f"
	epochID := uint64(23847425)

	log.WithFields(logrus.Fields{
		"rpc_url":        rpcURL,
		"protocol_state": protocolStateAddr,
		"data_market":    dataMarketAddr,
		"epoch_id":       epochID,
	}).Info("Starting event log check test")

	// Connect to RPC
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		log.WithError(err).Fatal("Failed to connect to RPC")
	}
	defer client.Close()

	log.Info("✅ Connected to RPC")

	// Load ProtocolState ABI
	protocolStateABI, err := abiloader.LoadABI("PowerloomProtocolState.abi.json")
	if err != nil {
		log.WithError(err).Fatal("Failed to load ProtocolState ABI")
	}

	log.Info("✅ Loaded ProtocolState ABI")

	// Get the BatchSubmissionsCompleted event signature
	event, found := protocolStateABI.Events["BatchSubmissionsCompleted"]
	if !found {
		log.Fatal("BatchSubmissionsCompleted event not found in ABI")
	}

	eventSig := event.ID
	log.WithField("event_signature", eventSig.Hex()).Info("✅ Found BatchSubmissionsCompleted event")

	// Prepare filter query
	protocolStateCommonAddr := common.HexToAddress(protocolStateAddr)
	dataMarket := common.HexToAddress(dataMarketAddr)
	epochIDBig := big.NewInt(int64(epochID))

	// Topics:
	// [0] = event signature (BatchSubmissionsCompleted)
	// [1] = dataMarketAddress (indexed, left-padded to 32 bytes)
	// [2] = epochId (indexed, as uint256)
	dataMarketHash := common.BytesToHash(dataMarket.Bytes())
	epochIDHash := common.BigToHash(epochIDBig)

	log.WithFields(logrus.Fields{
		"data_market_hash": dataMarketHash.Hex(),
		"epoch_id_hash":    epochIDHash.Hex(),
	}).Debug("Prepared topic hashes")

	query := ethereum.FilterQuery{
		Addresses: []common.Address{protocolStateCommonAddr},
		Topics: [][]common.Hash{
			{eventSig},       // Event signature
			{dataMarketHash}, // dataMarketAddress (left-padded to 32 bytes)
			{epochIDHash},    // epochId (uint256)
		},
	}

	log.WithFields(logrus.Fields{
		"contract_address": protocolStateCommonAddr.Hex(),
		"topics_count":     len(query.Topics),
	}).Info("Querying event logs...")

	// Query event logs
	logs, err := client.FilterLogs(ctx, query)
	if err != nil {
		log.WithError(err).Fatal("Failed to filter BatchSubmissionsCompleted logs")
	}

	log.WithField("logs_found", len(logs)).Info("✅ Query completed")

	// Display results
	if len(logs) > 0 {
		fmt.Println("\n✅ BATCH SUBMISSIONS COMPLETED EVENT FOUND!")
		fmt.Println("=" + string(make([]byte, 60)) + "=")
		for i, logEntry := range logs {
			fmt.Printf("\nLog #%d:\n", i+1)
			fmt.Printf("  Block Number: %d\n", logEntry.BlockNumber)
			fmt.Printf("  Block Hash: %s\n", logEntry.BlockHash.Hex())
			fmt.Printf("  Transaction Hash: %s\n", logEntry.TxHash.Hex())
			fmt.Printf("  Log Index: %d\n", logEntry.Index)
			fmt.Printf("  Topics Count: %d\n", len(logEntry.Topics))
			for j, topic := range logEntry.Topics {
				fmt.Printf("    Topic[%d]: %s\n", j, topic.Hex())
			}
			fmt.Printf("  Data Length: %d bytes\n", len(logEntry.Data))
			if len(logEntry.Data) > 0 {
				fmt.Printf("  Data: %s\n", common.Bytes2Hex(logEntry.Data))
			}
		}
		fmt.Println("\n" + string(make([]byte, 62)) + "\n")
		os.Exit(0) // Success - event found
	} else {
		fmt.Println("\n❌ NO BATCH SUBMISSIONS COMPLETED EVENT FOUND")
		fmt.Println("=" + string(make([]byte, 60)) + "=")
		fmt.Printf("\nNo BatchSubmissionsCompleted events found for:\n")
		fmt.Printf("  Epoch ID: %d\n", epochID)
		fmt.Printf("  Data Market: %s\n", dataMarketAddr)
		fmt.Printf("  Protocol State: %s\n", protocolStateAddr)
		fmt.Println("\nThis means submissions have NOT been completed for this epoch yet.")
		fmt.Println("\n" + string(make([]byte, 62)) + "\n")
		os.Exit(1) // No event found
	}
}
