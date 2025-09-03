package identity

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/go-redis/redis/v8"
	"github.com/libp2p/go-libp2p/core/peer"
	log "github.com/sirupsen/logrus"
)

// IdentityVerifier validates snapshotter identities against on-chain records
type IdentityVerifier struct {
	redisClient         *redis.Client
	
	// Cache for verified identities
	verifiedCache      map[string]*VerifiedIdentity
	cacheTTL           time.Duration
	
	// Full node addresses
	fullNodeAddresses  map[string]bool
}

// VerifiedIdentity represents a verified snapshotter
type VerifiedIdentity struct {
	SnapshotterAddress common.Address    // Ethereum address from signature
	LibP2PPeerID       peer.ID          // LibP2P peer ID from slot info
	SlotID             uint64
	IsFullNode         bool
	IsFlagged          bool
	VerifiedAt         time.Time
	ExpiresAt          time.Time
}

// SubmissionRequest represents the data that gets signed
type SubmissionRequest struct {
	SlotId       uint64 `json:"slotId"`
	Deadline     uint64 `json:"deadline"`
	SnapshotCid  string `json:"snapshotCid"`
	EpochId      uint64 `json:"epochId"`
	ProjectId    string `json:"projectId"`
}

// SlotInfo represents cached slot information from protocol state
// This now includes BOTH Ethereum address AND LibP2P peer ID
type SlotInfo struct {
	SlotID             uint64
	SnapshotterAddress common.Address  // Ethereum address for signature verification
	LibP2PPeerID       string         // LibP2P peer ID for P2P verification
	DataMarket         string
	EpochID            uint64
}

// Config for IdentityVerifier
type Config struct {
	RedisClient       *redis.Client
	CacheTTL          time.Duration
	FullNodeAddresses []string // List of full node addresses
}

// NewIdentityVerifier creates a new identity verifier
func NewIdentityVerifier(cfg *Config) (*IdentityVerifier, error) {
	// Build full node map
	fullNodes := make(map[string]bool)
	for _, addr := range cfg.FullNodeAddresses {
		fullNodes[strings.ToLower(addr)] = true
	}
	
	return &IdentityVerifier{
		redisClient:       cfg.RedisClient,
		verifiedCache:     make(map[string]*VerifiedIdentity),
		cacheTTL:          cfg.CacheTTL,
		fullNodeAddresses: fullNodes,
	}, nil
}

// RecoverSnapshotterAddress recovers the Ethereum address from a submission signature
func (v *IdentityVerifier) RecoverSnapshotterAddress(request *SubmissionRequest, signature []byte) (common.Address, error) {
	// Hash the request data
	msgHash := hashRequest(request)
	
	// Recover the public key from signature
	pubKey, err := crypto.SigToPub(msgHash, signature)
	if err != nil {
		return common.Address{}, fmt.Errorf("failed to recover public key: %w", err)
	}
	
	// Get address from public key
	address := crypto.PubkeyToAddress(*pubKey)
	return address, nil
}

// hashRequest creates a hash of the submission request for signature verification
func hashRequest(request *SubmissionRequest) []byte {
	// Create a deterministic string representation
	data := fmt.Sprintf("%d:%d:%s:%d:%s",
		request.SlotId,
		request.Deadline,
		request.SnapshotCid,
		request.EpochId,
		request.ProjectId,
	)
	
	// Hash with Ethereum prefix
	return crypto.Keccak256([]byte(fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(data), data)))
}

// VerifySubmissionWithP2P verifies a submission's authenticity including P2P identity
func (v *IdentityVerifier) VerifySubmissionWithP2P(
	ctx context.Context,
	request *SubmissionRequest,
	signature []byte,
	dataMarketAddress string,
	senderPeerID peer.ID, // The peer ID that sent this submission over P2P
) (*VerifiedIdentity, error) {
	// Recover snapshotter address from signature
	snapshotterAddr, err := v.RecoverSnapshotterAddress(request, signature)
	if err != nil {
		return nil, fmt.Errorf("failed to recover snapshotter address: %w", err)
	}
	
	// Check cache first
	cacheKey := fmt.Sprintf("%s:%d:%s", snapshotterAddr.Hex(), request.SlotId, senderPeerID.String())
	if cached, ok := v.verifiedCache[cacheKey]; ok {
		if time.Now().Before(cached.ExpiresAt) {
			log.Debugf("✅ Cache hit for snapshotter %s, slot %d, peer %s", 
				snapshotterAddr.Hex(), request.SlotId, senderPeerID)
			return cached, nil
		}
		delete(v.verifiedCache, cacheKey)
	}
	
	// Check if snapshotter is flagged
	isFlagged, err := v.checkIfFlagged(ctx, dataMarketAddress, snapshotterAddr)
	if err != nil {
		return nil, err
	}
	if isFlagged {
		return nil, fmt.Errorf("snapshotter %s is flagged", snapshotterAddr.Hex())
	}
	
	// Get slot info (now includes LibP2P peer ID)
	slotInfo, err := v.getSlotInfo(ctx, dataMarketAddress, request.SlotId)
	if err != nil {
		return nil, fmt.Errorf("failed to get slot info: %w", err)
	}
	
	// Verify Ethereum address matches
	if snapshotterAddr.Hex() != slotInfo.SnapshotterAddress.Hex() {
		return nil, fmt.Errorf("snapshotter address mismatch: got %s, expected %s",
			snapshotterAddr.Hex(), slotInfo.SnapshotterAddress.Hex())
	}
	
	// NEW: Verify LibP2P peer ID matches
	if slotInfo.LibP2PPeerID != "" {
		expectedPeerID, err := peer.Decode(slotInfo.LibP2PPeerID)
		if err != nil {
			log.Warnf("Failed to decode expected peer ID %s: %v", slotInfo.LibP2PPeerID, err)
		} else if senderPeerID != expectedPeerID {
			return nil, fmt.Errorf("libp2p peer ID mismatch: got %s, expected %s",
				senderPeerID.String(), expectedPeerID.String())
		}
		log.Debugf("✅ LibP2P peer ID verified: %s", senderPeerID)
	} else {
		log.Debugf("⚠️ No LibP2P peer ID in slot info for slot %d (legacy mode)", request.SlotId)
	}
	
	// Check if it's a full node
	isFullNode := v.isFullNode(snapshotterAddr.Hex())
	
	// Create verified identity
	verified := &VerifiedIdentity{
		SnapshotterAddress: snapshotterAddr,
		LibP2PPeerID:       senderPeerID,
		SlotID:             request.SlotId,
		IsFullNode:         isFullNode,
		IsFlagged:          false,
		VerifiedAt:         time.Now(),
		ExpiresAt:          time.Now().Add(v.cacheTTL),
	}
	
	// Cache the result
	v.verifiedCache[cacheKey] = verified
	
	log.Infof("✅ Fully verified submission: snapshotter=%s, peer=%s, slot=%d", 
		snapshotterAddr.Hex(), senderPeerID, request.SlotId)
	
	return verified, nil
}

// VerifySubmission verifies a submission without P2P identity (backward compatibility)
func (v *IdentityVerifier) VerifySubmission(
	ctx context.Context,
	request *SubmissionRequest,
	signature []byte,
	dataMarketAddress string,
) (*VerifiedIdentity, error) {
	// Call the P2P version with empty peer ID for backward compatibility
	return v.VerifySubmissionWithP2P(ctx, request, signature, dataMarketAddress, "")
}

// checkIfFlagged checks if a snapshotter is flagged in Redis
func (v *IdentityVerifier) checkIfFlagged(ctx context.Context, dataMarket string, snapshotter common.Address) (bool, error) {
	flaggedKey := fmt.Sprintf("flagged_snapshotters:%s", strings.ToLower(dataMarket))
	return v.redisClient.SIsMember(ctx, flaggedKey, snapshotter.Hex()).Result()
}

// getSlotInfo retrieves slot information from Redis cache
// Now includes LibP2P peer ID field
func (v *IdentityVerifier) getSlotInfo(ctx context.Context, dataMarket string, slotID uint64) (*SlotInfo, error) {
	slotKey := fmt.Sprintf("slotInfo:%s:%d", strings.ToLower(dataMarket), slotID)
	
	// Get slot info from Redis
	data, err := v.redisClient.HGetAll(ctx, slotKey).Result()
	if err != nil {
		return nil, err
	}
	
	if len(data) == 0 {
		return nil, fmt.Errorf("slot info not found for slot %d", slotID)
	}
	
	// Parse the data
	slotInfo := &SlotInfo{
		SlotID:     slotID,
		DataMarket: dataMarket,
	}
	
	// Parse Ethereum address
	if addr, ok := data["snapshotter_address"]; ok {
		slotInfo.SnapshotterAddress = common.HexToAddress(addr)
	}
	
	// NEW: Parse LibP2P peer ID
	if peerID, ok := data["libp2p_peer_id"]; ok {
		slotInfo.LibP2PPeerID = peerID
	}
	
	// Parse epoch ID if present
	if epochStr, ok := data["epoch_id"]; ok {
		// Parse epoch ID from string
		fmt.Sscanf(epochStr, "%d", &slotInfo.EpochID)
	}
	
	return slotInfo, nil
}

// isFullNode checks if an address is a full node
func (v *IdentityVerifier) isFullNode(address string) bool {
	return v.fullNodeAddresses[strings.ToLower(address)]
}

// CacheSlotInfo caches slot information from protocol state
// Now includes LibP2P peer ID
func (v *IdentityVerifier) CacheSlotInfo(ctx context.Context, dataMarket string, slotInfo *SlotInfo) error {
	slotKey := fmt.Sprintf("slotInfo:%s:%d", strings.ToLower(dataMarket), slotInfo.SlotID)
	
	data := map[string]interface{}{
		"slot_id":             slotInfo.SlotID,
		"snapshotter_address": slotInfo.SnapshotterAddress.Hex(),
		"data_market":         slotInfo.DataMarket,
		"epoch_id":            slotInfo.EpochID,
		"cached_at":           time.Now().Unix(),
	}
	
	// Add LibP2P peer ID if present
	if slotInfo.LibP2PPeerID != "" {
		data["libp2p_peer_id"] = slotInfo.LibP2PPeerID
	}
	
	pipe := v.redisClient.Pipeline()
	pipe.HMSet(ctx, slotKey, data)
	pipe.Expire(ctx, slotKey, 24*time.Hour)
	
	_, err := pipe.Exec(ctx)
	if err == nil {
		log.Debugf("📝 Cached slot info: slot=%d, eth=%s, p2p=%s", 
			slotInfo.SlotID, slotInfo.SnapshotterAddress.Hex(), slotInfo.LibP2PPeerID)
	}
	return err
}

// ValidateSubmissionEligibility performs comprehensive eligibility checks
func (v *IdentityVerifier) ValidateSubmissionEligibility(
	ctx context.Context,
	request *SubmissionRequest,
	signature []byte,
	dataMarket string,
	senderPeerID peer.ID,
	windowOpen bool,
) error {
	// Check if submission window is open
	if !windowOpen {
		return fmt.Errorf("submission window closed for epoch %d", request.EpochId)
	}
	
	// Verify the submission with P2P identity
	identity, err := v.VerifySubmissionWithP2P(ctx, request, signature, dataMarket, senderPeerID)
	if err != nil {
		return fmt.Errorf("submission verification failed: %w", err)
	}
	
	// Additional checks can be added here:
	// - Check if snapshotter meets minimum stake requirements
	// - Check if they're in the eligible set for this epoch
	// - Check rate limits
	
	log.Debugf("✅ Submission eligible: eth=%s, p2p=%s, epoch=%d, slot=%d",
		identity.SnapshotterAddress.Hex(), identity.LibP2PPeerID, 
		request.EpochId, request.SlotId)
	
	return nil
}

// UpdateSlotLibP2PIdentity updates just the LibP2P peer ID for a slot
// This can be called when a snapshotter registers their P2P identity
func (v *IdentityVerifier) UpdateSlotLibP2PIdentity(
	ctx context.Context,
	dataMarket string,
	slotID uint64,
	peerID peer.ID,
) error {
	slotKey := fmt.Sprintf("slotInfo:%s:%d", strings.ToLower(dataMarket), slotID)
	
	// Update only the libp2p_peer_id field
	err := v.redisClient.HSet(ctx, slotKey, "libp2p_peer_id", peerID.String()).Err()
	if err == nil {
		log.Infof("🔗 Updated LibP2P identity for slot %d: %s", slotID, peerID)
	}
	return err
}

// GetSlotsByPeerID finds all slots assigned to a specific LibP2P peer ID
// This is useful for debugging and monitoring
func (v *IdentityVerifier) GetSlotsByPeerID(ctx context.Context, peerID peer.ID) ([]uint64, error) {
	// This would require maintaining a reverse index
	// For now, return empty - this is a placeholder for future enhancement
	log.Debugf("GetSlotsByPeerID not yet implemented for peer: %s", peerID)
	return []uint64{}, nil
}

// Utility function to parse signature from hex string
func ParseSignature(hexSig string) ([]byte, error) {
	// Remove 0x prefix if present
	hexSig = strings.TrimPrefix(hexSig, "0x")
	return hex.DecodeString(hexSig)
}