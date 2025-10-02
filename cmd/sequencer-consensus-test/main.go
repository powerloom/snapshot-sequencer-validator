package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/powerloom/snapshot-sequencer-validator/pkgs/consensus"
	"github.com/powerloom/snapshot-sequencer-validator/pkgs/submissions"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/libp2p/go-libp2p/p2p/discovery/routing"
	"github.com/libp2p/go-libp2p/p2p/discovery/util"
	"github.com/multiformats/go-multiaddr"
	log "github.com/sirupsen/logrus"
)

const (
	RendezvousString = "powerloom-snapshot-sequencer-network"
	DiscoveryTopic   = "/powerloom/snapshot-submissions/0"
	SubmissionsTopic = "/powerloom/snapshot-submissions/all"
	ConsensusTopic   = "/powerloom/consensus/votes"
	BatchTopic       = "/powerloom/consensus/batches"
)

type Validator struct {
	host         host.Host
	ctx          context.Context
	ps           *pubsub.PubSub
	topics       map[string]*pubsub.Topic
	subs         map[string]*pubsub.Subscription
	batchGen     *consensus.DummyBatchGenerator
	votes        map[uint64]map[string]*consensus.FinalizedBatch // epoch -> sequencerID -> batch
	votesMutex   sync.Mutex
	redisClient  *redis.Client
	dequeuer     *submissions.Dequeuer
	sequencerID  string
	isProcessing bool
	processMutex sync.Mutex
}

func main() {
	// Initialize logger
	log.SetLevel(log.InfoLevel)
	if os.Getenv("DEBUG_MODE") == "true" {
		log.SetLevel(log.DebugLevel)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Get configuration from environment
	sequencerID := os.Getenv("SEQUENCER_ID")
	if sequencerID == "" {
		sequencerID = "validator-default"
	}
	
	p2pPort := os.Getenv("P2P_PORT")
	if p2pPort == "" {
		p2pPort = "9001"
	}

	// Create or load private key
	var privKey crypto.PrivKey
	privKeyHex := os.Getenv("PRIVATE_KEY")
	if privKeyHex != "" {
		keyBytes, err := hex.DecodeString(privKeyHex)
		if err != nil {
			log.Fatalf("Failed to decode private key: %v", err)
		}
		privKey, err = crypto.UnmarshalEd25519PrivateKey(keyBytes)
		if err != nil {
			log.Fatalf("Failed to unmarshal private key: %v", err)
		}
		log.Info("Loaded private key from environment")
	} else {
		var err error
		privKey, _, err = crypto.GenerateEd25519Key(rand.Reader)
		if err != nil {
			log.Fatalf("Failed to generate private key: %v", err)
		}
		log.Info("Generated new private key")
	}

	// Initialize Redis client with proper configuration
	redisHost := os.Getenv("REDIS_HOST")
	if redisHost == "" {
		redisHost = "localhost"
	}
	
	redisPort := os.Getenv("REDIS_PORT")
	if redisPort == "" {
		redisPort = "6379"
	}
	
	redisDB := 0
	if dbStr := os.Getenv("REDIS_DB"); dbStr != "" {
		if db, err := fmt.Sscanf(dbStr, "%d", &redisDB); err == nil && db == 1 {
			// Successfully parsed DB number
		} else {
			log.Warnf("Invalid REDIS_DB value: %s, using default 0", dbStr)
		}
	}
	
	redisPassword := os.Getenv("REDIS_PASSWORD")
	
	redisAddr := fmt.Sprintf("%s:%s", redisHost, redisPort)
	log.Infof("Connecting to Redis at %s (DB: %d)", redisAddr, redisDB)
	
	redisClient := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: redisPassword,
		DB:       redisDB,
	})
	
	// Test Redis connection
	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	log.Infof("Connected to Redis at %s", redisAddr)

	// Create libp2p host
	host, err := libp2p.New(
		libp2p.Identity(privKey),
		libp2p.ListenAddrStrings(fmt.Sprintf("/ip4/0.0.0.0/tcp/%s", p2pPort)),
		libp2p.EnableNATService(),
	)
	if err != nil {
		log.Fatalf("Failed to create host: %v", err)
	}

	log.Infof("Validator %s started with peer ID: %s", sequencerID, host.ID())
	for _, addr := range host.Addrs() {
		log.Infof("Listening on: %s/p2p/%s", addr, host.ID())
	}

	// Setup DHT
	kademliaDHT, err := dht.New(ctx, host, dht.Mode(dht.ModeClient))
	if err != nil {
		log.Fatalf("Failed to create DHT: %v", err)
	}

	if err = kademliaDHT.Bootstrap(ctx); err != nil {
		log.Fatalf("Failed to bootstrap DHT: %v", err)
	}

	// Connect to bootstrap nodes
	bootstrapAddr := os.Getenv("BOOTSTRAP_MULTIADDR")
	if bootstrapAddr != "" {
		maddr, err := multiaddr.NewMultiaddr(bootstrapAddr)
		if err != nil {
			log.Errorf("Invalid bootstrap address: %v", err)
		} else {
			peerInfo, err := peer.AddrInfoFromP2pAddr(maddr)
			if err != nil {
				log.Errorf("Failed to parse bootstrap peer info: %v", err)
			} else {
				if err := host.Connect(ctx, *peerInfo); err != nil {
					log.Errorf("Failed to connect to bootstrap: %v", err)
				} else {
					log.Infof("Connected to bootstrap node: %s", peerInfo.ID)
				}
			}
		}
	}

	// Setup mDNS for local discovery
	mdnsService := mdns.NewMdnsService(host, RendezvousString, &discoveryNotifee{h: host})
	if err := mdnsService.Start(); err != nil {
		log.Errorf("Failed to start mDNS: %v", err)
	}

	// Setup routing discovery
	routingDiscovery := routing.NewRoutingDiscovery(kademliaDHT)
	util.Advertise(ctx, routingDiscovery, RendezvousString)

	// Create pubsub
	ps, err := pubsub.NewGossipSub(ctx, host,
		pubsub.WithFloodPublish(true),
		pubsub.WithMessageSignaturePolicy(pubsub.StrictSign),
	)
	if err != nil {
		log.Fatalf("Failed to create pubsub: %v", err)
	}

	// Initialize dequeuer
	// Using placeholder values for testing - in production these come from config
	chainID := int64(11155111) // Sepolia testnet
	protocolStateContract := "0xE88E5f64AEB483d7057645326AdDFA24A3B312DF"
	enableSlotValidation := false // Disabled for testing
	dequeuer, err := submissions.NewDequeuer(redisClient, sequencerID, chainID, protocolStateContract, enableSlotValidation)
	if err != nil {
		log.Fatalf("Failed to create dequeuer: %v", err)
	}

	validator := &Validator{
		host:        host,
		ctx:         ctx,
		ps:          ps,
		topics:      make(map[string]*pubsub.Topic),
		subs:        make(map[string]*pubsub.Subscription),
		batchGen:    consensus.NewDummyBatchGenerator(sequencerID),
		votes:       make(map[uint64]map[string]*consensus.FinalizedBatch),
		redisClient: redisClient,
		dequeuer:    dequeuer,
		sequencerID: sequencerID,
	}

	// Join topics
	if err := validator.joinTopic(DiscoveryTopic); err != nil {
		log.Fatalf("Failed to join discovery topic: %v", err)
	}
	if err := validator.joinTopic(SubmissionsTopic); err != nil {
		log.Fatalf("Failed to join submissions topic: %v", err)
	}
	if err := validator.joinTopic(ConsensusTopic); err != nil {
		log.Fatalf("Failed to join consensus topic: %v", err)
	}
	if err := validator.joinTopic(BatchTopic); err != nil {
		log.Fatalf("Failed to join batch topic: %v", err)
	}

	// Start message handlers
	go validator.handleDiscoveryMessages()
	go validator.handleSubmissionMessages() // Enhanced to queue actual submissions
	go validator.handleConsensusMessages()
	
	// Start the dequeuer to process submissions
	go validator.startDequeuer()
	
	// Start batch generation every 30 seconds
	go validator.startBatchGeneration()

	// Periodically discover peers
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				validator.discoverPeers(routingDiscovery)
			case <-ctx.Done():
				return
			}
		}
	}()

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Info("Shutting down validator...")
	cancel()
}

func (v *Validator) joinTopic(topicName string) error {
	topic, err := v.ps.Join(topicName)
	if err != nil {
		return fmt.Errorf("failed to join topic %s: %w", topicName, err)
	}
	v.topics[topicName] = topic

	sub, err := topic.Subscribe()
	if err != nil {
		return fmt.Errorf("failed to subscribe to topic %s: %w", topicName, err)
	}
	v.subs[topicName] = sub

	log.Infof("Joined topic: %s", topicName)
	return nil
}

func (v *Validator) handleDiscoveryMessages() {
	sub := v.subs[DiscoveryTopic]
	log.Infof("🎧 Validator listening on discovery topic: %s", DiscoveryTopic)
	
	messageCount := 0
	for {
		msg, err := sub.Next(v.ctx)
		if err != nil {
			if v.ctx.Err() != nil {
				return
			}
			log.Errorf("Error reading discovery message: %v", err)
			continue
		}

		// Skip own messages
		if msg.ReceivedFrom == v.host.ID() {
			continue
		}

		messageCount++
		log.Infof("📨 [DISCOVERY MSG #%d] Received from %s (size: %d bytes)",
			messageCount, msg.ReceivedFrom.ShortString(), len(msg.Data))
		
		// Process as submission - epoch 0 can contain actual submissions for testing
		v.processSubmission(msg)
	}
}

func (v *Validator) handleSubmissionMessages() {
	sub := v.subs[SubmissionsTopic]
	log.Infof("🎧 Validator listening for submissions on: %s", SubmissionsTopic)
	
	messageCount := 0
	for {
		msg, err := sub.Next(v.ctx)
		if err != nil {
			if v.ctx.Err() != nil {
				return
			}
			log.Errorf("Error reading submission message: %v", err)
			continue
		}

		// Skip own messages
		if msg.ReceivedFrom == v.host.ID() {
			continue
		}

		messageCount++
		log.Infof("📨 [MSG #%d] RECEIVED SUBMISSION from %s (size: %d bytes)",
			messageCount, msg.ReceivedFrom.ShortString(), len(msg.Data))

		// Process the submission
		v.processSubmission(msg)
	}
}

func (v *Validator) processSubmission(msg *pubsub.Message) {
	log.Infof("🔍 Processing submission from %s", msg.ReceivedFrom.ShortString())
	
	// Log first 100 chars of raw data for debugging
	if len(msg.Data) > 0 {
		preview := string(msg.Data)
		if len(preview) > 100 {
			preview = preview[:100] + "..."
		}
		log.Debugf("   Raw data preview: %s", preview)
	}
	
	// Try to parse as P2PSnapshotSubmission first
	var p2pSubmission submissions.P2PSnapshotSubmission
	err := json.Unmarshal(msg.Data, &p2pSubmission)
	if err != nil {
		// Try direct SnapshotSubmission for backward compatibility
		var submission submissions.SnapshotSubmission
		err = json.Unmarshal(msg.Data, &submission)
		if err != nil {
			log.Errorf("❌ Failed to unmarshal submission: %v", err)
			log.Debugf("   Invalid JSON data: %s", string(msg.Data)[:min(200, len(msg.Data))])
			return
		}
		log.Infof("📦 Single submission for Epoch=%d, Slot=%d, Project=%s",
			submission.Request.EpochId, submission.Request.SlotId, submission.Request.ProjectId)
		// Queue single submission
		v.queueSubmission(&submission, msg.ID)
		return
	}
	
	// Process each submission in the P2P message
	log.Infof("📦 BATCH: %d submissions from snapshotter %s, Epoch=%d",
		len(p2pSubmission.Submissions), p2pSubmission.SnapshotterID, p2pSubmission.EpochID)
	
	for i, submission := range p2pSubmission.Submissions {
		log.Debugf("   [%d/%d] Project=%s, Slot=%d, CID=%s",
			i+1, len(p2pSubmission.Submissions),
			submission.Request.ProjectId,
			submission.Request.SlotId,
			submission.Request.SnapshotCid[:min(16, len(submission.Request.SnapshotCid))])
		v.queueSubmission(submission, msg.ID)
	}
	
	log.Infof("✅ Successfully processed batch from %s", msg.ReceivedFrom.ShortString())
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (v *Validator) queueSubmission(submission *submissions.SnapshotSubmission, msgID string) {
	submissionID := uuid.New().String()
	
	// Marshal submission data
	submissionData, err := json.Marshal(submission)
	if err != nil {
		log.Errorf("Error marshalling submission data: %v", err)
		return
	}
	
	// Create queue entry
	queueData := map[string]interface{}{
		"submission_id":       submissionID,
		"data_market_address": submission.DataMarket,
		"data":                string(submissionData),
		"timestamp":           time.Now().Unix(),
		"validator_id":        v.sequencerID,
	}
	
	queueDataJSON, err := json.Marshal(queueData)
	if err != nil {
		log.Errorf("Error marshalling queue data: %v", err)
		return
	}
	
	// Add to Redis queue
	err = v.redisClient.LPush(v.ctx, "submissionQueue", queueDataJSON).Err()
	if err != nil {
		log.Errorf("Error adding to Redis queue: %v", err)
		return
	}
	
	log.Infof("✅ Queued submission %s | Epoch=%d, Slot=%d, Project=%s",
		submissionID[:8], submission.Request.EpochId, submission.Request.SlotId, submission.Request.ProjectId)
	
	// Track submission count
	key := fmt.Sprintf("validator:%s:epoch:%d:slot:%d:count",
		v.sequencerID, submission.Request.EpochId, submission.Request.SlotId)
	v.redisClient.Incr(v.ctx, key)
	v.redisClient.Expire(v.ctx, key, 5*time.Minute)
}

func (v *Validator) discoverPeers(routingDiscovery *routing.RoutingDiscovery) {
	peerChan, err := routingDiscovery.FindPeers(v.ctx, RendezvousString)
	if err != nil {
		log.Errorf("Peer discovery failed: %v", err)
		return
	}

	for peer := range peerChan {
		if peer.ID == v.host.ID() {
			continue
		}
		if v.host.Network().Connectedness(peer.ID) != 1 {
			if err := v.host.Connect(v.ctx, peer); err != nil {
				log.Debugf("Failed to connect to discovered peer %s: %v", peer.ID, err)
			} else {
				log.Infof("Connected to discovered peer: %s", peer.ID)
			}
		}
	}
}

type discoveryNotifee struct {
	h host.Host
}

func (n *discoveryNotifee) HandlePeerFound(pi peer.AddrInfo) {
	if err := n.h.Connect(context.Background(), pi); err != nil {
		log.Debugf("Failed to connect to mDNS peer %s: %v", pi.ID, err)
	} else {
		log.Infof("Connected to mDNS discovered peer: %s", pi.ID)
	}
}

func (v *Validator) startBatchGeneration() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	// Wait 10 seconds before first batch to allow network formation
	time.Sleep(10 * time.Second)
	
	for {
		select {
		case <-ticker.C:
			// Calculate epoch based on Unix time (30-second epochs)
			// This ensures all validators use the same epoch ID
			epoch := uint64(time.Now().Unix() / 30)
			
			log.Infof("⏰ Starting epoch %d at %s", epoch, time.Now().Format("15:04:05"))
			
			// Generate batch for this epoch
			batch := v.batchGen.GenerateDummyBatch(epoch)
			
			// Store our own vote
			v.votesMutex.Lock()
			if v.votes[epoch] == nil {
				v.votes[epoch] = make(map[string]*consensus.FinalizedBatch)
			}
			v.votes[epoch][batch.SequencerId] = batch
			v.votesMutex.Unlock()
			
			// Broadcast batch as vote
			v.broadcastBatch(batch)
			
			// After 20 seconds, check consensus
			go func(epochToCheck uint64) {
				time.Sleep(20 * time.Second)
				v.checkConsensus(epochToCheck)
			}(epoch)
			
		case <-v.ctx.Done():
			return
		}
	}
}

func (v *Validator) broadcastBatch(batch *consensus.FinalizedBatch) {
	data, err := json.Marshal(batch)
	if err != nil {
		log.Errorf("Failed to marshal batch: %v", err)
		return
	}
	
	topic := v.topics[BatchTopic]
	if topic == nil {
		log.Error("Batch topic not initialized")
		return
	}
	
	if err := topic.Publish(v.ctx, data); err != nil {
		log.Errorf("Failed to publish batch: %v", err)
	} else {
		log.Infof("📤 Broadcast batch for epoch %d with %d projects", 
			batch.EpochId, len(batch.ProjectIds))
	}
}

func (v *Validator) handleConsensusMessages() {
	sub := v.subs[BatchTopic]
	for {
		msg, err := sub.Next(v.ctx)
		if err != nil {
			if v.ctx.Err() != nil {
				return
			}
			log.Errorf("Error reading consensus message: %v", err)
			continue
		}
		
		// Skip own messages
		if msg.ReceivedFrom == v.host.ID() {
			continue
		}
		
		// Parse batch
		var batch consensus.FinalizedBatch
		if err := json.Unmarshal(msg.Data, &batch); err != nil {
			log.Errorf("Failed to unmarshal batch: %v", err)
			continue
		}
		
		// Store vote
		v.votesMutex.Lock()
		if v.votes[batch.EpochId] == nil {
			v.votes[batch.EpochId] = make(map[string]*consensus.FinalizedBatch)
		}
		v.votes[batch.EpochId][batch.SequencerId] = &batch
		v.votesMutex.Unlock()
		
		log.Infof("📥 Received batch from %s for epoch %d", 
			batch.SequencerId, batch.EpochId)
	}
}

func (v *Validator) checkConsensus(epoch uint64) {
	v.votesMutex.Lock()
	defer v.votesMutex.Unlock()
	
	epochVotes := v.votes[epoch]
	if epochVotes == nil {
		log.Warnf("No votes for epoch %d", epoch)
		return
	}
	
	totalVotes := len(epochVotes)
	log.Infof("🗳️  Epoch %d: Received %d votes", epoch, totalVotes)
	
	// Simple majority check (in production, use stake-weighted voting)
	if totalVotes >= 2 {
		log.Infof("✅ CONSENSUS ACHIEVED for epoch %d with %d validators", 
			epoch, totalVotes)
		
		// Log all participating validators
		for sequencerID := range epochVotes {
			log.Infof("  - Validator: %s", sequencerID)
		}
	} else {
		log.Warnf("❌ CONSENSUS FAILED for epoch %d (only %d votes, need at least 2)", 
			epoch, totalVotes)
	}
	
	// Clean up old epochs (keep last 10)
	for oldEpoch := range v.votes {
		if oldEpoch < epoch-10 {
			delete(v.votes, oldEpoch)
		}
	}
}

func (v *Validator) startDequeuer() {
	log.Info("Starting dequeuer for processing submissions...")
	
	// Configure dequeuer with worker pool
	workers := 5
	if w := os.Getenv("DEQUEUER_WORKERS"); w != "" {
		if wInt, err := fmt.Sscanf(w, "%d", &workers); err == nil && wInt > 0 {
			workers = wInt
		}
	}
	
	// Start worker pool
	for i := 0; i < workers; i++ {
		go v.dequeueWorker(i)
	}
	
	// Monitor queue depth periodically
	go v.monitorQueueDepth()
}

func (v *Validator) dequeueWorker(workerID int) {
	log.Infof("Dequeue worker %d started", workerID)
	
	for {
		select {
		case <-v.ctx.Done():
			log.Infof("Dequeue worker %d shutting down", workerID)
			return
		default:
			// Pop from queue with timeout
			result, err := v.redisClient.BRPop(v.ctx, 2*time.Second, "submissionQueue").Result()
			if err != nil {
				if err == redis.Nil {
					// Queue is empty, continue
					continue
				}
				log.Errorf("Worker %d: Error popping from queue: %v", workerID, err)
				continue
			}
			
			if len(result) < 2 {
				continue
			}
			
			// Process the submission
			v.processQueuedSubmission(workerID, result[1])
		}
	}
}

func (v *Validator) processQueuedSubmission(workerID int, data string) {
	var queueData map[string]interface{}
	if err := json.Unmarshal([]byte(data), &queueData); err != nil {
		log.Errorf("Worker %d: Failed to unmarshal queue data: %v", workerID, err)
		return
	}
	
	submissionID := queueData["submission_id"].(string)
	submissionDataStr := queueData["data"].(string)
	
	var submission submissions.SnapshotSubmission
	if err := json.Unmarshal([]byte(submissionDataStr), &submission); err != nil {
		log.Errorf("Worker %d: Failed to unmarshal submission: %v", workerID, err)
		return
	}
	
	// Process the submission (validation, storage, etc.)
	if err := v.dequeuer.ProcessSubmission(&submission, submissionID); err != nil {
		log.Errorf("Worker %d: Failed to process submission %s: %v", 
			workerID, submissionID, err)
		return
	}
	
	log.Infof("Worker %d: Successfully processed submission %s for epoch %d",
		workerID, submissionID, submission.Request.EpochId)
}

func (v *Validator) monitorQueueDepth() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			length, err := v.redisClient.LLen(v.ctx, "submissionQueue").Result()
			if err != nil {
				log.Errorf("Failed to get queue length: %v", err)
				continue
			}
			
			if length > 100 {
				log.Warnf("⚠️ Queue depth high: %d submissions pending", length)
			} else if length > 0 {
				log.Infof("📊 Queue depth: %d submissions pending", length)
			}
		case <-v.ctx.Done():
			return
		}
	}
}