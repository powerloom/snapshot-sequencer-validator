//go:build ignore
// +build ignore

package main

import (
	"context"
	"fmt"
	"math/big"
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

	log.WithFields(logrus.Fields{
		"rpc_url":        rpcURL,
		"protocol_state": protocolStateAddr,
		"data_market":    dataMarketAddr,
	}).Info("Starting broad event log check test (all epochs)")

	// Connect to RPC
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		log.WithError(err).Fatal("Failed to connect to RPC")
	}
	defer client.Close()

	log.Info("✅ Connected to RPC")

	// Get current block to limit search range
	currentBlock, err := client.BlockNumber(ctx)
	if err != nil {
		log.WithError(err).Fatal("Failed to get current block")
	}
	log.WithField("current_block", currentBlock).Info("Got current block")

	// Search last 10000 blocks (reasonable range)
	fromBlock := big.NewInt(0)
	if currentBlock > 10000 {
		fromBlock = big.NewInt(int64(currentBlock - 10000))
	}
	toBlock := big.NewInt(int64(currentBlock))

	log.WithFields(logrus.Fields{
		"from_block": fromBlock.Uint64(),
		"to_block":   toBlock.Uint64(),
	}).Info("Searching block range")

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

	// Prepare filter query - only filter by event signature and data market
	protocolStateCommonAddr := common.HexToAddress(protocolStateAddr)
	dataMarket := common.HexToAddress(dataMarketAddr)
	dataMarketHash := common.BytesToHash(dataMarket.Bytes())

	log.WithFields(logrus.Fields{
		"data_market_hash": dataMarketHash.Hex(),
	}).Debug("Prepared topic hashes")

	query := ethereum.FilterQuery{
		FromBlock: fromBlock,
		ToBlock:   toBlock,
		Addresses: []common.Address{protocolStateCommonAddr},
		Topics: [][]common.Hash{
			{eventSig},       // Event signature
			{dataMarketHash}, // dataMarketAddress (left-padded to 32 bytes)
			nil,              // epochId - don't filter, get all epochs
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
		fmt.Println("\n✅ BATCH SUBMISSIONS COMPLETED EVENTS FOUND!")
		fmt.Println("=" + string(make([]byte, 60)) + "=")
		fmt.Printf("\nFound %d event(s) in the last %d blocks:\n\n", len(logs), toBlock.Uint64()-fromBlock.Uint64())

		for i, logEntry := range logs {
			// Parse epoch ID from topic[2]
			var epochID *big.Int
			if len(logEntry.Topics) >= 3 {
				epochID = new(big.Int).SetBytes(logEntry.Topics[2].Bytes())
			}

			fmt.Printf("Event #%d:\n", i+1)
			fmt.Printf("  Epoch ID: %s\n", epochID.String())
			fmt.Printf("  Block Number: %d\n", logEntry.BlockNumber)
			fmt.Printf("  Block Hash: %s\n", logEntry.BlockHash.Hex())
			fmt.Printf("  Transaction Hash: %s\n", logEntry.TxHash.Hex())
			fmt.Printf("  Log Index: %d\n", logEntry.Index)

			// Parse timestamp from data (last 32 bytes)
			if len(logEntry.Data) >= 32 {
				timestamp := new(big.Int).SetBytes(logEntry.Data[len(logEntry.Data)-32:])
				fmt.Printf("  Timestamp: %s (%s)\n", timestamp.String(), time.Unix(timestamp.Int64(), 0).Format(time.RFC3339))
			}
			fmt.Println()
		}
		fmt.Println(string(make([]byte, 62)) + "\n")
	} else {
		fmt.Println("\n❌ NO BATCH SUBMISSIONS COMPLETED EVENTS FOUND")
		fmt.Println("=" + string(make([]byte, 60)) + "=")
		fmt.Printf("\nNo BatchSubmissionsCompleted events found in the last %d blocks\n", toBlock.Uint64()-fromBlock.Uint64())
		fmt.Printf("  Data Market: %s\n", dataMarketAddr)
		fmt.Printf("  Protocol State: %s\n", protocolStateAddr)
		fmt.Println("\nThis could mean:")
		fmt.Println("  1. No submissions have been completed recently")
		fmt.Println("  2. Events are in older blocks (beyond search range)")
		fmt.Println("  3. Event signature or contract address might be incorrect")
		fmt.Println("\n" + string(make([]byte, 62)) + "\n")
	}
}
