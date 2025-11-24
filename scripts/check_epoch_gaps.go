package main

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/redis/go-redis/v9"
)

// EpochReleasedEvent represents an epoch release event
type EpochReleasedEvent struct {
	DataMarket common.Address
	EpochID    *big.Int
	BlockNum   uint64
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run check_epoch_gaps.go <rpc_url> [redis_host:port]")
		fmt.Println("Example: go run check_epoch_gaps.go https://devnet-orbit-rpc.aws2.powerloom.io/rpc?uuid=... localhost:6381")
		os.Exit(1)
	}

	rpcURL := os.Args[1]
	redisAddr := "localhost:6379"
	if len(os.Args) >= 3 {
		redisAddr = os.Args[2]
	}

	// Contract addresses
	legacyProtocolState := common.HexToAddress("0x3B5A0FB70ef68B5dd677C7d614dFB89961f97401")
	newProtocolState := common.HexToAddress("0xC9e7304f719D35919b0371d8B242ab59E0966d63")
	newDataMarket := common.HexToAddress("0xb6c1392944a335b72b9e34f9D4b8c0050cdb511f")

	// Connect to RPC
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		fmt.Printf("Failed to connect to RPC: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	// Connect to Redis
	rdb := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	defer rdb.Close()

	ctx := context.Background()

	// Get current block
	currentBlock, err := client.BlockNumber(ctx)
	if err != nil {
		fmt.Printf("Failed to get current block: %v\n", err)
		os.Exit(1)
	}

	// Scan last 10000 blocks (adjust as needed)
	fromBlock := currentBlock - 10000
	if fromBlock > currentBlock {
		fromBlock = 0
	}

	fmt.Printf("Scanning blocks %d to %d for EpochReleased events...\n", fromBlock, currentBlock)
	fmt.Printf("Legacy ProtocolState: %s\n", legacyProtocolState.Hex())
	fmt.Printf("New ProtocolState: %s\n", newProtocolState.Hex())
	fmt.Printf("New Data Market: %s\n\n", newDataMarket.Hex())

	// Query both contracts
	allEpochs := make(map[string]*EpochReleasedEvent)

	// Query legacy contract
	fmt.Println("Querying legacy ProtocolState contract...")
	legacyEpochs := queryEpochReleasedEvents(client, ctx, legacyProtocolState, fromBlock, currentBlock, newDataMarket)
	fmt.Printf("Found %d epochs from legacy contract\n", len(legacyEpochs))
	for k, v := range legacyEpochs {
		allEpochs[k] = v
	}

	// Query new contract
	fmt.Println("Querying new ProtocolState contract...")
	newEpochs := queryEpochReleasedEvents(client, ctx, newProtocolState, fromBlock, currentBlock, newDataMarket)
	fmt.Printf("Found %d epochs from new contract\n", len(newEpochs))
	for k, v := range newEpochs {
		allEpochs[k] = v
	}

	// Get epochs from Redis VPA timeline
	fmt.Println("\nChecking Redis for processed epochs...")
	redisEpochs := getRedisEpochs(rdb, ctx, newProtocolState.Hex(), newDataMarket.Hex())

	// Compare and find gaps
	fmt.Println("\n=== EPOCH GAP ANALYSIS ===")
	analyzeGaps(allEpochs, redisEpochs)
}

func queryEpochReleasedEvents(client *ethclient.Client, ctx context.Context, contractAddr common.Address, fromBlock, toBlock uint64, dataMarket common.Address) map[string]*EpochReleasedEvent {
	// EpochReleased(address indexed dataMarketAddress, uint256 indexed epochId, uint256 begin, uint256 end, uint256 timestamp)
	// Event signature: keccak256("EpochReleased(address,uint256,uint256,uint256,uint256)")
	eventSig := crypto.Keccak256Hash([]byte("EpochReleased(address,uint256,uint256,uint256,uint256)"))

	// Filter by data market address (first indexed parameter)
	// Address needs to be padded to 32 bytes for topic matching
	dataMarketHash := common.BytesToHash(hexutil.MustDecode("0x" + strings.Repeat("0", 24) + strings.TrimPrefix(dataMarket.Hex(), "0x")))

	query := ethereum.FilterQuery{
		FromBlock: big.NewInt(int64(fromBlock)),
		ToBlock:   big.NewInt(int64(toBlock)),
		Addresses: []common.Address{contractAddr},
		Topics: [][]common.Hash{
			{eventSig},
			{dataMarketHash}, // Filter by data market address (indexed)
		},
	}

	logs, err := client.FilterLogs(ctx, query)
	if err != nil {
		fmt.Printf("Error querying logs for contract %s: %v\n", contractAddr.Hex(), err)
		return make(map[string]*EpochReleasedEvent)
	}

	epochs := make(map[string]*EpochReleasedEvent)
	for _, vLog := range logs {
		// Topics[0] = event signature
		// Topics[1] = dataMarketAddress (indexed)
		// Topics[2] = epochId (indexed)
		if len(vLog.Topics) < 3 {
			continue
		}
		epochID := new(big.Int).SetBytes(vLog.Topics[2].Bytes())
		epochKey := epochID.String()
		epochs[epochKey] = &EpochReleasedEvent{
			DataMarket: dataMarket,
			EpochID:    epochID,
			BlockNum:   vLog.BlockNumber,
		}
	}

	return epochs
}

func getRedisEpochs(rdb *redis.Client, ctx context.Context, protocol, market string) map[string]bool {
	processed := make(map[string]bool)

	// Check VPA priority timeline
	priorityKey := fmt.Sprintf("%s:%s:vpa:priority:timeline", protocol, market)
	entries, err := rdb.ZRange(ctx, priorityKey, 0, -1).Result()
	if err == nil {
		for _, entry := range entries {
			// Format: "epochID:priority:status"
			parts := strings.Split(entry, ":")
			if len(parts) > 0 {
				processed[parts[0]] = true
			}
		}
	}

	// Check finalized batches
	finalizedPattern := fmt.Sprintf("%s:%s:finalized:*", protocol, market)
	keys, err := rdb.Keys(ctx, finalizedPattern).Result()
	if err == nil {
		for _, key := range keys {
			parts := strings.Split(key, ":")
			if len(parts) >= 3 {
				processed[parts[2]] = true
			}
		}
	}

	return processed
}

func analyzeGaps(released map[string]*EpochReleasedEvent, processed map[string]bool) {
	// Convert epoch IDs to integers and sort
	epochIDs := make([]int, 0, len(released))
	for k := range released {
		id, err := strconv.Atoi(k)
		if err == nil {
			epochIDs = append(epochIDs, id)
		}
	}
	sort.Ints(epochIDs)

	if len(epochIDs) == 0 {
		fmt.Println("No epochs found in contracts")
		return
	}

	fmt.Printf("Total epochs released: %d\n", len(epochIDs))
	fmt.Printf("Epochs processed (in Redis): %d\n", len(processed))
	fmt.Printf("Epoch range: %d to %d\n\n", epochIDs[0], epochIDs[len(epochIDs)-1])

	// Find gaps
	missing := make([]int, 0)
	for i := 1; i < len(epochIDs); i++ {
		gap := epochIDs[i] - epochIDs[i-1]
		if gap > 1 {
			for j := epochIDs[i-1] + 1; j < epochIDs[i]; j++ {
				missing = append(missing, j)
			}
		}
	}

	if len(missing) > 0 {
		fmt.Printf("⚠️  Found %d missing epochs in release sequence:\n", len(missing))
		// Group consecutive missing epochs
		groups := groupConsecutive(missing)
		for _, group := range groups {
			if len(group) == 1 {
				fmt.Printf("  - Missing: %d\n", group[0])
			} else {
				fmt.Printf("  - Missing: %d-%d (%d epochs)\n", group[0], group[len(group)-1], len(group))
			}
		}
	} else {
		fmt.Println("✅ No gaps in epoch release sequence")
	}

	// Find unprocessed epochs
	unprocessed := make([]int, 0)
	for _, id := range epochIDs {
		idStr := strconv.Itoa(id)
		if !processed[idStr] {
			unprocessed = append(unprocessed, id)
		}
	}

	if len(unprocessed) > 0 {
		fmt.Printf("\n⚠️  Found %d epochs released but not processed:\n", len(unprocessed))
		// Show first 20
		showCount := 20
		if len(unprocessed) < showCount {
			showCount = len(unprocessed)
		}
		for i := 0; i < showCount; i++ {
			fmt.Printf("  - Epoch %d (released at block %d)\n", unprocessed[i], released[strconv.Itoa(unprocessed[i])].BlockNum)
		}
		if len(unprocessed) > showCount {
			fmt.Printf("  ... and %d more\n", len(unprocessed)-showCount)
		}
	} else {
		fmt.Println("\n✅ All released epochs have been processed")
	}
}

func groupConsecutive(nums []int) [][]int {
	if len(nums) == 0 {
		return nil
	}
	groups := [][]int{{nums[0]}}
	for i := 1; i < len(nums); i++ {
		if nums[i] == nums[i-1]+1 {
			groups[len(groups)-1] = append(groups[len(groups)-1], nums[i])
		} else {
			groups = append(groups, []int{nums[i]})
		}
	}
	return groups
}
