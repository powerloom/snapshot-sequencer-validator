//go:build ignore

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// EpochLifecycle tracks the complete lifecycle of an epoch
type EpochLifecycle struct {
	EpochID         string
	Released        *LifecycleEvent
	Finalized       *LifecycleEvent
	Aggregated      *LifecycleEvent
	VPAPriority     *LifecycleEvent
	VPASubmission   *LifecycleEvent
	Status          string // "complete", "stuck", "missing_steps"
	MissingSteps    []string
	TimeToFinalize  *time.Duration
	TimeToAggregate *time.Duration
	TimeToSubmit    *time.Duration
}

type LifecycleEvent struct {
	Timestamp int64
	Status    string
	Details   map[string]interface{}
}

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: go run epoch_lifecycle_tracker.go <redis_host:port> <protocol> <market> [epoch_id]")
		fmt.Println("Example: go run epoch_lifecycle_tracker.go localhost:6381 0xC9e7304f719D35919b0371d8B242ab59E0966d63 0xb6c1392944a335b72b9e34f9D4b8c0050cdb511f")
		fmt.Println("Or: go run epoch_lifecycle_tracker.go localhost:6381 0xC9e7304f719D35919b0371d8B242ab59E0966d63 0xb6c1392944a335b72b9e34f9D4b8c0050cdb511f 23867390")
		os.Exit(1)
	}

	redisAddr := os.Args[1]
	protocol := os.Args[2]
	market := os.Args[3]
	var targetEpoch string
	if len(os.Args) >= 5 {
		targetEpoch = os.Args[4]
	}

	rdb := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	defer rdb.Close()

	ctx := context.Background()

	if targetEpoch != "" {
		// Track single epoch
		lifecycle := trackEpochLifecycle(rdb, ctx, protocol, market, targetEpoch)
		printLifecycle(lifecycle)
	} else {
		// Track recent epochs
		epochs := getRecentEpochs(rdb, ctx, protocol, market, 50)
		fmt.Printf("Tracking lifecycle for %d recent epochs...\n\n", len(epochs))

		lifecycles := make([]*EpochLifecycle, 0, len(epochs))
		for _, epochID := range epochs {
			lifecycle := trackEpochLifecycle(rdb, ctx, protocol, market, epochID)
			lifecycles = append(lifecycles, lifecycle)
		}

		// Group by status
		complete := make([]*EpochLifecycle, 0)
		stuck := make([]*EpochLifecycle, 0)
		incomplete := make([]*EpochLifecycle, 0)

		for _, lc := range lifecycles {
			switch lc.Status {
			case "complete":
				complete = append(complete, lc)
			case "stuck":
				stuck = append(stuck, lc)
			default:
				incomplete = append(incomplete, lc)
			}
		}

		fmt.Printf("=== SUMMARY ===\n")
		fmt.Printf("Complete: %d\n", len(complete))
		fmt.Printf("Stuck: %d\n", len(stuck))
		fmt.Printf("Incomplete: %d\n", len(incomplete))

		if len(stuck) > 0 {
			fmt.Printf("\n⚠️  STUCK EPOCHS:\n")
			for _, lc := range stuck {
				fmt.Printf("  Epoch %s: Missing %s\n", lc.EpochID, strings.Join(lc.MissingSteps, ", "))
			}
		}

		if len(incomplete) > 0 {
			fmt.Printf("\n⏳ INCOMPLETE EPOCHS:\n")
			for _, lc := range incomplete {
				fmt.Printf("  Epoch %s: Missing %s\n", lc.EpochID, strings.Join(lc.MissingSteps, ", "))
			}
		}
	}
}

func getRecentEpochs(rdb *redis.Client, ctx context.Context, protocol, market string, limit int) []string {
	// Get from VPA priority timeline
	priorityKey := fmt.Sprintf("%s:%s:vpa:priority:timeline", protocol, market)
	entries, err := rdb.ZRevRange(ctx, priorityKey, 0, int64(limit-1)).Result()
	if err != nil {
		return []string{}
	}

	epochSet := make(map[string]bool)
	for _, entry := range entries {
		parts := strings.Split(entry, ":")
		if len(parts) > 0 {
			epochSet[parts[0]] = true
		}
	}

	// Get from finalized batches (use SCAN instead of KEYS for production safety)
	finalizedPattern := fmt.Sprintf("%s:%s:finalized:*", protocol, market)
	var keys []string
	var cursor uint64
	for {
		scanKeys, nextCursor, err := rdb.Scan(ctx, cursor, finalizedPattern, 100).Result()
		if err != nil {
			break
		}
		keys = append(keys, scanKeys...)
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	for _, key := range keys {
		parts := strings.Split(key, ":")
		if len(parts) >= 3 {
			epochSet[parts[2]] = true
		}
	}

	// Convert to sorted list
	epochs := make([]string, 0, len(epochSet))
	for epoch := range epochSet {
		epochs = append(epochs, epoch)
	}

	// Sort by epoch ID (descending)
	sort.Slice(epochs, func(i, j int) bool {
		epochI, _ := strconv.Atoi(epochs[i])
		epochJ, _ := strconv.Atoi(epochs[j])
		return epochI > epochJ
	})

	return epochs
}

func trackEpochLifecycle(rdb *redis.Client, ctx context.Context, protocol, market, epochID string) *EpochLifecycle {
	lc := &EpochLifecycle{
		EpochID:      epochID,
		MissingSteps: make([]string, 0),
	}

	// Check epoch release (from epoch info)
	epochInfoKey := fmt.Sprintf("epoch:%s:%s:info", market, epochID)
	info, err := rdb.HGetAll(ctx, epochInfoKey).Result()
	if err == nil && len(info) > 0 {
		if releasedAt, ok := info["released_at"]; ok {
			ts, _ := strconv.ParseInt(releasedAt, 10, 64)
			// Convert map[string]string to map[string]interface{}
			details := make(map[string]interface{})
			for k, v := range info {
				details[k] = v
			}
			lc.Released = &LifecycleEvent{
				Timestamp: ts,
				Status:    "released",
				Details:   details,
			}
		}
	} else {
		lc.MissingSteps = append(lc.MissingSteps, "released")
	}

	// Check finalized batch
	finalizedKey := fmt.Sprintf("%s:%s:finalized:%s", protocol, market, epochID)
	finalizedData, err := rdb.Get(ctx, finalizedKey).Result()
	if err == nil {
		var batch map[string]interface{}
		if json.Unmarshal([]byte(finalizedData), &batch) == nil {
			if timestamp, ok := batch["timestamp"].(float64); ok {
				lc.Finalized = &LifecycleEvent{
					Timestamp: int64(timestamp),
					Status:    "finalized",
					Details:   batch,
				}
			}
		}
	} else {
		lc.MissingSteps = append(lc.MissingSteps, "finalized")
	}

	// Check aggregated batch
	aggregatedKey := fmt.Sprintf("%s:%s:batch:aggregated:%s", protocol, market, epochID)
	aggregatedData, err := rdb.Get(ctx, aggregatedKey).Result()
	if err == nil {
		var batch map[string]interface{}
		if json.Unmarshal([]byte(aggregatedData), &batch) == nil {
			if timestamp, ok := batch["timestamp"].(float64); ok {
				lc.Aggregated = &LifecycleEvent{
					Timestamp: int64(timestamp),
					Status:    "aggregated",
					Details:   batch,
				}
			}
		}
	} else {
		lc.MissingSteps = append(lc.MissingSteps, "aggregated")
	}

	// Check VPA priority
	priorityKey := fmt.Sprintf("%s:%s:vpa:priority:%s", protocol, market, epochID)
	priorityData, err := rdb.Get(ctx, priorityKey).Result()
	if err == nil {
		var priority map[string]interface{}
		if json.Unmarshal([]byte(priorityData), &priority) == nil {
			if timestamp, ok := priority["timestamp"].(float64); ok {
				lc.VPAPriority = &LifecycleEvent{
					Timestamp: int64(timestamp),
					Status:    priority["status"].(string),
					Details:   priority,
				}
			}
		}
	} else {
		lc.MissingSteps = append(lc.MissingSteps, "vpa_priority")
	}

	// Check VPA submission
	submissionKey := fmt.Sprintf("%s:%s:vpa:submission:%s", protocol, market, epochID)
	submissionData, err := rdb.Get(ctx, submissionKey).Result()
	if err == nil {
		var submission map[string]interface{}
		if json.Unmarshal([]byte(submissionData), &submission) == nil {
			if timestamp, ok := submission["timestamp"].(float64); ok {
				lc.VPASubmission = &LifecycleEvent{
					Timestamp: int64(timestamp),
					Status:    fmt.Sprintf("%v", submission["success"]),
					Details:   submission,
				}
			}
		}
	} else {
		lc.MissingSteps = append(lc.MissingSteps, "vpa_submission")
	}

	// Calculate timing
	if lc.Released != nil {
		if lc.Finalized != nil {
			duration := time.Duration(lc.Finalized.Timestamp-lc.Released.Timestamp) * time.Second
			lc.TimeToFinalize = &duration
		}
		if lc.Aggregated != nil {
			duration := time.Duration(lc.Aggregated.Timestamp-lc.Released.Timestamp) * time.Second
			lc.TimeToAggregate = &duration
		}
		if lc.VPASubmission != nil {
			duration := time.Duration(lc.VPASubmission.Timestamp-lc.Released.Timestamp) * time.Second
			lc.TimeToSubmit = &duration
		}
	}

	// Determine status
	if len(lc.MissingSteps) == 0 {
		lc.Status = "complete"
	} else if lc.Released != nil && len(lc.MissingSteps) > 3 {
		lc.Status = "stuck"
	} else {
		lc.Status = "incomplete"
	}

	return lc
}

func printLifecycle(lc *EpochLifecycle) {
	fmt.Printf("=== EPOCH %s LIFECYCLE ===\n", lc.EpochID)
	fmt.Printf("Status: %s\n", lc.Status)

	if lc.Released != nil {
		fmt.Printf("✅ Released: %s\n", time.Unix(lc.Released.Timestamp, 0).Format(time.RFC3339))
	} else {
		fmt.Printf("❌ Released: MISSING\n")
	}

	if lc.Finalized != nil {
		fmt.Printf("✅ Finalized: %s", time.Unix(lc.Finalized.Timestamp, 0).Format(time.RFC3339))
		if lc.TimeToFinalize != nil {
			fmt.Printf(" (took %v)", *lc.TimeToFinalize)
		}
		fmt.Println()
	} else {
		fmt.Printf("❌ Finalized: MISSING\n")
	}

	if lc.Aggregated != nil {
		fmt.Printf("✅ Aggregated: %s", time.Unix(lc.Aggregated.Timestamp, 0).Format(time.RFC3339))
		if lc.TimeToAggregate != nil {
			fmt.Printf(" (took %v)", *lc.TimeToAggregate)
		}
		fmt.Println()
	} else {
		fmt.Printf("❌ Aggregated: MISSING\n")
	}

	if lc.VPAPriority != nil {
		fmt.Printf("✅ VPA Priority: %s at %s\n", lc.VPAPriority.Status, time.Unix(lc.VPAPriority.Timestamp, 0).Format(time.RFC3339))
	} else {
		fmt.Printf("❌ VPA Priority: MISSING\n")
	}

	if lc.VPASubmission != nil {
		fmt.Printf("✅ VPA Submission: %s at %s", lc.VPASubmission.Status, time.Unix(lc.VPASubmission.Timestamp, 0).Format(time.RFC3339))
		if lc.TimeToSubmit != nil {
			fmt.Printf(" (took %v)", *lc.TimeToSubmit)
		}
		fmt.Println()
	} else {
		fmt.Printf("❌ VPA Submission: MISSING\n")
	}

	if len(lc.MissingSteps) > 0 {
		fmt.Printf("\nMissing steps: %s\n", strings.Join(lc.MissingSteps, ", "))
	}
}
