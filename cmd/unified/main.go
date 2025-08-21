package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/powerloom/snapshot-sequencer-validator/pkgs/consensus"
	"github.com/powerloom/snapshot-sequencer-validator/pkgs/submissions"
	"github.com/go-redis/redis/v8"
	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	log "github.com/sirupsen/logrus"
)

type UnifiedSequencer struct {
	// Core components
	host        host.Host
	ctx         context.Context
	cancel      context.CancelFunc
	ps          *pubsub.PubSub
	redisClient *redis.Client
	
	// Component flags
	enableListener  bool
	enableDequeuer  bool
	enableFinalizer bool
	enableConsensus bool
	
	// Component instances
	dequeuer *submissions.Dequeuer
	batchGen *consensus.DummyBatchGenerator
	
	// Configuration
	sequencerID string
	wg          sync.WaitGroup
}

func main() {
	// Initialize logger
	log.SetLevel(log.InfoLevel)
	if os.Getenv("DEBUG_MODE") == "true" {
		log.SetLevel(log.DebugLevel)
	}
	
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	// Parse component flags from environment
	enableListener := getBoolEnv("ENABLE_LISTENER", true)
	enableDequeuer := getBoolEnv("ENABLE_DEQUEUER", true)
	enableFinalizer := getBoolEnv("ENABLE_FINALIZER", true)
	enableConsensus := getBoolEnv("ENABLE_CONSENSUS", true)
	
	log.Infof("Starting Unified Sequencer with components:")
	log.Infof("  - Listener: %v", enableListener)
	log.Infof("  - Dequeuer: %v", enableDequeuer)
	log.Infof("  - Finalizer: %v", enableFinalizer)
	log.Infof("  - Consensus: %v", enableConsensus)
	
	// Get configuration
	sequencerID := os.Getenv("SEQUENCER_ID")
	if sequencerID == "" {
		sequencerID = "unified-sequencer"
	}
	
	// Initialize Redis if any component needs it
	var redisClient *redis.Client
	if enableListener || enableDequeuer || enableFinalizer {
		// Get Redis configuration from environment
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
			if db, err := strconv.Atoi(dbStr); err == nil {
				redisDB = db
			} else {
				log.Warnf("Invalid REDIS_DB value: %s, using default 0", dbStr)
			}
		}
		
		redisPassword := os.Getenv("REDIS_PASSWORD")
		
		redisAddr := fmt.Sprintf("%s:%s", redisHost, redisPort)
		log.Infof("Connecting to Redis at %s (DB: %d)", redisAddr, redisDB)
		
		redisClient = redis.NewClient(&redis.Options{
			Addr:     redisAddr,
			Password: redisPassword,
			DB:       redisDB,
		})
		
		if err := redisClient.Ping(ctx).Err(); err != nil {
			log.Fatalf("Failed to connect to Redis: %v", err)
		}
		log.Infof("Connected to Redis at %s", redisAddr)
	}
	
	// Initialize P2P if listener or consensus is enabled
	var h host.Host
	var ps *pubsub.PubSub
	if enableListener || enableConsensus {
		p2pPort := os.Getenv("P2P_PORT")
		if p2pPort == "" {
			p2pPort = "9001"
		}
		
		// Create or load private key
		privKey, err := loadOrCreatePrivateKey()
		if err != nil {
			log.Fatalf("Failed to get private key: %v", err)
		}
		
		// Create libp2p host
		h, err = libp2p.New(
			libp2p.Identity(privKey),
			libp2p.ListenAddrStrings(fmt.Sprintf("/ip4/0.0.0.0/tcp/%s", p2pPort)),
			libp2p.EnableNATService(),
		)
		if err != nil {
			log.Fatalf("Failed to create host: %v", err)
		}
		
		log.Infof("P2P Host started with peer ID: %s", h.ID())
		
		// Setup DHT
		kademliaDHT, err := dht.New(ctx, h, dht.Mode(dht.ModeClient))
		if err != nil {
			log.Fatalf("Failed to create DHT: %v", err)
		}
		
		if err = kademliaDHT.Bootstrap(ctx); err != nil {
			log.Fatalf("Failed to bootstrap DHT: %v", err)
		}
		
		// Connect to bootstrap if configured
		connectToBootstrap(ctx, h)
		
		// Create pubsub
		ps, err = pubsub.NewGossipSub(ctx, h,
			pubsub.WithFloodPublish(true),
			pubsub.WithMessageSignaturePolicy(pubsub.StrictSign),
		)
		if err != nil {
			log.Fatalf("Failed to create pubsub: %v", err)
		}
	}
	
	// Create unified sequencer
	sequencer := &UnifiedSequencer{
		host:            h,
		ctx:             ctx,
		cancel:          cancel,
		ps:              ps,
		redisClient:     redisClient,
		enableListener:  enableListener,
		enableDequeuer:  enableDequeuer,
		enableFinalizer: enableFinalizer,
		enableConsensus: enableConsensus,
		sequencerID:     sequencerID,
	}
	
	// Initialize components based on flags
	if enableDequeuer && redisClient != nil {
		sequencer.dequeuer = submissions.NewDequeuer(redisClient, sequencerID)
	}
	
	if enableConsensus {
		sequencer.batchGen = consensus.NewDummyBatchGenerator(sequencerID)
	}
	
	// Start components
	sequencer.Start()
	
	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	
	log.Info("Shutting down unified sequencer...")
	cancel()
	sequencer.wg.Wait()
}

func (s *UnifiedSequencer) Start() {
	// Start P2P listener component
	if s.enableListener && s.ps != nil {
		log.Info("Starting P2P Listener component...")
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.runListener()
		}()
	}
	
	// Start dequeuer component
	if s.enableDequeuer && s.redisClient != nil {
		log.Info("Starting Dequeuer component...")
		workers := getIntEnv("DEQUEUER_WORKERS", 5)
		for i := 0; i < workers; i++ {
			s.wg.Add(1)
			go func(workerID int) {
				defer s.wg.Done()
				s.runDequeuerWorker(workerID)
			}(i)
		}
		
		// Monitor queue depth
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.monitorQueueDepth()
		}()
	}
	
	// Start finalizer component
	if s.enableFinalizer && s.redisClient != nil {
		log.Info("Starting Finalizer component...")
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.runFinalizer()
		}()
	}
	
	// Start consensus component
	if s.enableConsensus && s.ps != nil {
		log.Info("Starting Consensus component...")
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.runConsensus()
		}()
	}
	
	log.Info("All enabled components started successfully")
}

func (s *UnifiedSequencer) runListener() {
	// Join required topics
	topics := []string{
		"/powerloom/snapshot-submissions/0",
		"/powerloom/snapshot-submissions/all",
	}
	
	subs := make(map[string]*pubsub.Subscription)
	
	for _, topicName := range topics {
		topic, err := s.ps.Join(topicName)
		if err != nil {
			log.Errorf("Failed to join topic %s: %v", topicName, err)
			continue
		}
		
		sub, err := topic.Subscribe()
		if err != nil {
			log.Errorf("Failed to subscribe to topic %s: %v", topicName, err)
			continue
		}
		
		subs[topicName] = sub
		log.Infof("📡 Subscribed to topic: %s", topicName)
		
		// Handle messages for each topic
		go s.handleSubmissionMessages(sub)
	}
	
	// Periodic stats logging
	go s.logListenerStats(topics)
	
	// Keep listener running
	<-s.ctx.Done()
}

func (s *UnifiedSequencer) logListenerStats(topics []string) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	log.Info("🔵 Listener component active and monitoring P2P network")
	
	for {
		select {
		case <-ticker.C:
			log.Info("====== P2P LISTENER STATUS ======")
			log.Infof("Host ID: %s", s.host.ID())
			log.Infof("Connected Peers: %d", len(s.host.Network().Peers()))
			
			for _, topic := range topics {
				peers := s.ps.ListPeers(topic)
				log.Infof("Topic %s: %d peers", topic, len(peers))
			}
			log.Info("=================================")
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *UnifiedSequencer) handleSubmissionMessages(sub *pubsub.Subscription) {
	topicName := sub.Topic()
	isDiscoveryTopic := topicName == "/powerloom/snapshot-submissions/0"
	topicLabel := "SUBMISSIONS"
	if isDiscoveryTopic {
		topicLabel = "DISCOVERY/TEST"
	}
	log.Infof("🎧 Started listening on %s topic: %s", topicLabel, topicName)
	
	for {
		msg, err := sub.Next(s.ctx)
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
			log.Errorf("Error reading message from %s: %v", topicName, err)
			continue
		}
		
		// Skip own messages
		if s.host != nil && msg.ReceivedFrom == s.host.ID() {
			continue
		}
		
		topicLabel := "SUBMISSION"
		if topicName == "/powerloom/snapshot-submissions/0" {
			topicLabel = "TEST/DISCOVERY"
		}
		log.Infof("📨 RECEIVED %s on %s from peer %s (size: %d bytes)",
			topicLabel, topicName, msg.ReceivedFrom.ShortString(), len(msg.Data))
		
		// Queue the submission for processing
		s.queueSubmissionFromP2P(msg.Data, topicName, msg.ReceivedFrom.String())
	}
}

func (s *UnifiedSequencer) queueSubmissionFromP2P(data []byte, topic string, peerID string) {
	// Try to parse the submission to log details
	var submissionInfo map[string]interface{}
	if err := json.Unmarshal(data, &submissionInfo); err == nil {
		// Successfully parsed - log key details
		if epochID, ok := submissionInfo["epoch_id"]; ok {
			log.Infof("📋 Submission Details: Epoch=%v, Topic=%s, Peer=%s",
				epochID, topic, peerID[:16])
		}
		
		// Check if it's a P2P batch submission
		if submissions, ok := submissionInfo["submissions"]; ok {
			if subArray, ok := submissions.([]interface{}); ok {
				log.Infof("   └─ Batch submission with %d items", len(subArray))
			}
		}
		
		// Log request details if available
		if request, ok := submissionInfo["request"].(map[string]interface{}); ok {
			log.Infof("   └─ SlotID=%v, ProjectID=%v, CID=%v",
				request["slot_id"], request["project_id"], request["snapshot_cid"])
		}
	} else {
		log.Infof("📋 Received raw submission (%d bytes) from %s", len(data), peerID[:16])
	}
	
	if s.redisClient != nil {
		err := s.redisClient.LPush(s.ctx, "submissionQueue", data).Err()
		if err != nil {
			log.Errorf("❌ Failed to queue submission: %v", err)
		} else {
			log.Infof("✅ Successfully queued submission to Redis")
			
			// Log current queue depth
			if length, err := s.redisClient.LLen(s.ctx, "submissionQueue").Result(); err == nil {
				log.Debugf("   Queue depth now: %d", length)
			}
		}
	} else {
		log.Warnf("⚠️ Redis not available - submission not queued")
	}
}

func (s *UnifiedSequencer) runDequeuerWorker(workerID int) {
	log.Infof("Dequeuer worker %d started", workerID)
	
	for {
		select {
		case <-s.ctx.Done():
			log.Infof("Dequeuer worker %d shutting down", workerID)
			return
		default:
			// Pop from queue with timeout
			result, err := s.redisClient.BRPop(s.ctx, 2*time.Second, "submissionQueue").Result()
			if err != nil {
				if err == redis.Nil {
					continue // Queue empty
				}
				log.Errorf("Worker %d: Error popping from queue: %v", workerID, err)
				continue
			}
			
			if len(result) < 2 {
				continue
			}
			
			// Process the submission
			log.Debugf("Worker %d processing submission", workerID)
			// Processing logic here
		}
	}
}

func (s *UnifiedSequencer) runFinalizer() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			log.Info("Finalizer: Checking for submissions to finalize...")
			// Finalization logic here
		case <-s.ctx.Done():
			log.Info("Finalizer shutting down")
			return
		}
	}
}

func (s *UnifiedSequencer) runConsensus() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			epoch := uint64(time.Now().Unix() / 30)
			log.Infof("Consensus: Processing epoch %d", epoch)
			// Consensus logic here
		case <-s.ctx.Done():
			log.Info("Consensus component shutting down")
			return
		}
	}
}

func (s *UnifiedSequencer) monitorQueueDepth() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			length, err := s.redisClient.LLen(s.ctx, "submissionQueue").Result()
			if err != nil {
				log.Errorf("Failed to get queue length: %v", err)
				continue
			}
			
			if length > 100 {
				log.Warnf("⚠️ Queue depth high: %d submissions pending", length)
			} else if length > 0 {
				log.Infof("📊 Queue depth: %d submissions", length)
			}
		case <-s.ctx.Done():
			return
		}
	}
}

func getBoolEnv(key string, defaultValue bool) bool {
	val := os.Getenv(key)
	if val == "" {
		return defaultValue
	}
	return val == "true" || val == "1" || val == "yes"
}

func getIntEnv(key string, defaultValue int) int {
	val := os.Getenv(key)
	if val == "" {
		return defaultValue
	}
	intVal, err := strconv.Atoi(val)
	if err != nil {
		return defaultValue
	}
	return intVal
}

func loadOrCreatePrivateKey() (crypto.PrivKey, error) {
	privKeyHex := os.Getenv("PRIVATE_KEY")
	if privKeyHex != "" {
		// Load existing key
		return crypto.UnmarshalEd25519PrivateKey([]byte(privKeyHex))
	}
	// Generate new key
	privKey, _, err := crypto.GenerateEd25519Key(nil)
	return privKey, err
}

func connectToBootstrap(ctx context.Context, h host.Host) {
	bootstrapAddr := os.Getenv("BOOTSTRAP_MULTIADDR")
	if bootstrapAddr == "" {
		log.Warn("No BOOTSTRAP_MULTIADDR configured, skipping bootstrap connection")
		return
	}
	
	// Parse bootstrap multiaddr
	maddr, err := multiaddr.NewMultiaddr(bootstrapAddr)
	if err != nil {
		log.Errorf("Invalid bootstrap address %s: %v", bootstrapAddr, err)
		return
	}
	
	// Extract peer info from multiaddr
	peerInfo, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		log.Errorf("Failed to parse bootstrap peer info: %v", err)
		return
	}
	
	// Connect to bootstrap node
	if err := h.Connect(ctx, *peerInfo); err != nil {
		log.Errorf("Failed to connect to bootstrap node: %v", err)
		return
	}
	
	log.Infof("✅ Connected to bootstrap node: %s", peerInfo.ID)
}