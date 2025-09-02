package deduplication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	lru "github.com/hashicorp/golang-lru/v2"
	log "github.com/sirupsen/logrus"
)

// Deduplicator provides two-layer deduplication for snapshot submissions
type Deduplicator struct {
	redis      *redis.Client
	localCache *lru.Cache[string, bool]
	ttl        time.Duration
	keyPrefix  string
}

// NewDeduplicator creates a new deduplicator with local LRU cache and Redis backend
func NewDeduplicator(redisClient *redis.Client, localCacheSize int, ttl time.Duration) (*Deduplicator, error) {
	cache, err := lru.New[string, bool](localCacheSize)
	if err != nil {
		return nil, fmt.Errorf("failed to create LRU cache: %w", err)
	}

	return &Deduplicator{
		redis:      redisClient,
		localCache: cache,
		ttl:        ttl,
		keyPrefix:  "dedup:",
	}, nil
}

// GenerateKey creates a deduplication key from submission data
func (d *Deduplicator) GenerateKey(projectID string, epochID uint64, snapshotCID string) string {
	data := fmt.Sprintf("%s:%d:%s", projectID, epochID, snapshotCID)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:16]) // Use first 16 bytes for shorter keys
}

// CheckAndMark checks if a submission was seen and marks it if not
// Returns true if this is a NEW submission (should be processed)
func (d *Deduplicator) CheckAndMark(ctx context.Context, key string) (bool, error) {
	fullKey := d.keyPrefix + key

	// Fast path: Check local LRU cache
	if d.localCache.Contains(key) {
		log.Debugf("Dedup hit (local cache): %s", key)
		return false, nil
	}

	// Slow path: Check Redis with atomic SetNX
	// SetNX only sets if key doesn't exist, returns false if it exists
	ok, err := d.redis.SetNX(ctx, fullKey, time.Now().Unix(), d.ttl).Result()
	if err != nil {
		return false, fmt.Errorf("redis SetNX failed: %w", err)
	}

	if ok {
		// New submission - add to local cache
		d.localCache.Add(key, true)
		log.Debugf("Dedup miss (new submission): %s", key)
		return true, nil
	}

	// Already seen - add to local cache to speed up future checks
	d.localCache.Add(key, true)
	log.Debugf("Dedup hit (redis): %s", key)
	return false, nil
}

// CleanupExpired removes expired entries (handled automatically by Redis TTL)
// This is mainly for stats/monitoring
func (d *Deduplicator) GetStats(ctx context.Context) (map[string]interface{}, error) {
	pattern := d.keyPrefix + "*"
	
	// Use SCAN to count keys without blocking
	var cursor uint64
	var totalKeys int64
	
	for {
		keys, nextCursor, err := d.redis.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, err
		}
		
		totalKeys += int64(len(keys))
		cursor = nextCursor
		
		if cursor == 0 {
			break
		}
	}

	// Get memory usage estimate
	memoryUsage := totalKeys * 100 // Rough estimate: 100 bytes per key

	return map[string]interface{}{
		"total_dedup_keys":   totalKeys,
		"local_cache_size":   d.localCache.Len(),
		"ttl_seconds":        d.ttl.Seconds(),
		"estimated_memory":   memoryUsage,
	}, nil
}

// ClearLocal clears the local LRU cache (useful for testing)
func (d *Deduplicator) ClearLocal() {
	d.localCache.Purge()
	log.Info("Local deduplication cache cleared")
}