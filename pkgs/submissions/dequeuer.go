package submissions

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	log "github.com/sirupsen/logrus"
)

// Dequeuer handles processing of queued submissions
type Dequeuer struct {
	redisClient         *redis.Client
	sequencerID         string
	processedSubmissions map[string]*ProcessedSubmission
	submissionsMutex     sync.RWMutex
	stats               DequeuerStats
	statsMutex          sync.RWMutex
}

// DequeuerStats tracks processing metrics
type DequeuerStats struct {
	TotalProcessed   uint64
	SuccessfulCount  uint64
	FailedCount      uint64
	LastProcessedAt  time.Time
	ProcessingRate   float64 // submissions per second
}

// NewDequeuer creates a new submission dequeuer
func NewDequeuer(redisClient *redis.Client, sequencerID string) *Dequeuer {
	return &Dequeuer{
		redisClient:          redisClient,
		sequencerID:          sequencerID,
		processedSubmissions: make(map[string]*ProcessedSubmission),
	}
}

// ProcessSubmission validates and stores a submission
func (d *Dequeuer) ProcessSubmission(submission *SnapshotSubmission, submissionID string) error {
	startTime := time.Now()
	
	// Check for duplicate
	d.submissionsMutex.RLock()
	if _, exists := d.processedSubmissions[submissionID]; exists {
		d.submissionsMutex.RUnlock()
		log.Debugf("Submission %s already processed", submissionID)
		return nil
	}
	d.submissionsMutex.RUnlock()
	
	// Validate submission
	if err := d.validateSubmission(submission); err != nil {
		d.updateStats(false, time.Since(startTime))
		return fmt.Errorf("validation failed: %w", err)
	}
	
	// Store in local state
	processed := &ProcessedSubmission{
		ID:             submissionID,
		Submission:     submission,
		DataMarketAddr: submission.DataMarket,
		ProcessedAt:    time.Now(),
		ValidatorID:    d.sequencerID,
	}
	
	d.submissionsMutex.Lock()
	d.processedSubmissions[submissionID] = processed
	
	// Clean up old submissions (keep last 1000)
	if len(d.processedSubmissions) > 1000 {
		d.cleanupOldSubmissions()
	}
	d.submissionsMutex.Unlock()
	
	// Store processing result in Redis for coordination
	d.storeProcessingResult(submissionID, processed)
	
	// Update stats
	d.updateStats(true, time.Since(startTime))
	
	log.Debugf("Successfully processed submission %s for epoch %d, slot %d",
		submissionID, submission.Request.EpochId, submission.Request.SlotId)
	
	return nil
}

func (d *Dequeuer) validateSubmission(submission *SnapshotSubmission) error {
	// Epoch 0 heartbeat handling: Skip validation for epoch 0 with empty CID
	// These are P2P mesh maintenance messages from Go local collector, not actual submissions
	if submission.Request.EpochId == 0 && submission.Request.SnapshotCid == "" {
		return fmt.Errorf("epoch 0 heartbeat: skipping")
	}

	// Basic validation for real submissions
	if submission.Request.SnapshotCid == "" {
		return fmt.Errorf("empty snapshot CID")
	}

	if submission.Request.ProjectId == "" {
		return fmt.Errorf("empty project ID")
	}

	if submission.DataMarket == "" {
		return fmt.Errorf("empty data market address")
	}

	return nil
}

func (d *Dequeuer) storeProcessingResult(submissionID string, processed *ProcessedSubmission) {
	ctx := context.Background()
	
	// Extract protocol and market from submission
	dataMarket := processed.Submission.DataMarket
	protocolState := processed.Submission.ProtocolState
	
	// Namespace keys by protocol:market:epoch
	// Format: {protocol}:{market}:processed:{sequencer_id}:{submission_id}
	key := fmt.Sprintf("%s:%s:processed:%s:%s", 
		protocolState, dataMarket, d.sequencerID, submissionID)
	
	data, err := json.Marshal(processed)
	if err != nil {
		log.Errorf("Failed to marshal processing result: %v", err)
		return
	}
	
	// Store with TTL
	err = d.redisClient.Set(ctx, key, data, 10*time.Minute).Err()
	if err != nil {
		log.Errorf("Failed to store processing result: %v", err)
	}
	
	// Update epoch processing set (namespaced by protocol:market:epoch)
	epochKey := fmt.Sprintf("%s:%s:epoch:%d:processed", 
		protocolState, dataMarket, processed.Submission.Request.EpochId)
	d.redisClient.SAdd(ctx, epochKey, submissionID)
	d.redisClient.Expire(ctx, epochKey, 1*time.Hour)
	
	log.Debugf("Stored submission %s with epochKey: %s (protocolState=%s, market=%s, epoch=%d)", 
		submissionID, epochKey, protocolState, dataMarket, processed.Submission.Request.EpochId)
}

func (d *Dequeuer) cleanupOldSubmissions() {
	// Keep only the most recent 500 submissions
	if len(d.processedSubmissions) <= 500 {
		return
	}
	
	// Create slice of submissions sorted by time
	type submissionEntry struct {
		id   string
		time time.Time
	}
	
	entries := make([]submissionEntry, 0, len(d.processedSubmissions))
	for id, sub := range d.processedSubmissions {
		entries = append(entries, submissionEntry{id: id, time: sub.ProcessedAt})
	}
	
	// Sort by time (oldest first)
	for i := 0; i < len(entries)-1; i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[i].time.After(entries[j].time) {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}
	
	// Remove oldest entries
	toRemove := len(entries) - 500
	for i := 0; i < toRemove; i++ {
		delete(d.processedSubmissions, entries[i].id)
	}
}

func (d *Dequeuer) updateStats(success bool, processingTime time.Duration) {
	d.statsMutex.Lock()
	defer d.statsMutex.Unlock()
	
	d.stats.TotalProcessed++
	if success {
		d.stats.SuccessfulCount++
	} else {
		d.stats.FailedCount++
	}
	d.stats.LastProcessedAt = time.Now()
	
	// Calculate rolling average processing rate
	if processingTime > 0 {
		rate := 1.0 / processingTime.Seconds()
		if d.stats.ProcessingRate == 0 {
			d.stats.ProcessingRate = rate
		} else {
			// Exponential moving average
			d.stats.ProcessingRate = 0.9*d.stats.ProcessingRate + 0.1*rate
		}
	}
}

// GetStats returns current dequeuer statistics
func (d *Dequeuer) GetStats() DequeuerStats {
	d.statsMutex.RLock()
	defer d.statsMutex.RUnlock()
	return d.stats
}

// GetProcessedCount returns count of processed submissions for an epoch
func (d *Dequeuer) GetProcessedCount(epochID uint64) int {
	d.submissionsMutex.RLock()
	defer d.submissionsMutex.RUnlock()
	
	count := 0
	for _, sub := range d.processedSubmissions {
		if sub.Submission.Request.EpochId == epochID {
			count++
		}
	}
	return count
}