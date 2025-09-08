package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/powerloom/snapshot-sequencer-validator/config"
	"github.com/powerloom/snapshot-sequencer-validator/pkgs/consensus"
	"github.com/powerloom/snapshot-sequencer-validator/pkgs/deduplication"
	"github.com/powerloom/snapshot-sequencer-validator/pkgs/eventmonitor"
	"github.com/powerloom/snapshot-sequencer-validator/pkgs/gossipconfig"
	"github.com/powerloom/snapshot-sequencer-validator/pkgs/submissions"
	"github.com/powerloom/snapshot-sequencer-validator/pkgs/workers"
	"github.com/go-redis/redis/v8"
	rpchelper "github.com/powerloom/go-rpc-helper"
	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/routing"
	"github.com/libp2p/go-libp2p/p2p/discovery/util"
	"github.com/libp2p/go-libp2p/p2p/net/connmgr"
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
	dedup       *deduplication.Deduplicator
	
	// Component flags
	enableListener     bool
	enableDequeuer     bool
	enableFinalizer    bool
	enableConsensus    bool
	enableEventMonitor bool
	
	// Component instances
	dequeuer     *submissions.Dequeuer
	batchGen     *consensus.DummyBatchGenerator
	eventMonitor *eventmonitor.EventMonitor
	
	// Configuration
	config      *config.Settings
	sequencerID string
	wg          sync.WaitGroup
}

func main() {
	// Load configuration
	if err := config.LoadConfig(); err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}
	cfg := config.SettingsObj
	
	// Initialize logger
	log.SetLevel(log.InfoLevel)
	if cfg.DebugMode {
		log.SetLevel(log.DebugLevel)
	}
	
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	// Parse component flags from configuration
	enableListener := cfg.EnableListener
	enableDequeuer := cfg.EnableDequeuer
	enableFinalizer := cfg.EnableFinalizer
	enableConsensus := cfg.EnableConsensus
	enableEventMonitor := cfg.EnableEventMonitor
	
	log.Infof("Starting Unified Sequencer with components:")
	log.Infof("  - Listener: %v", enableListener)
	log.Infof("  - Dequeuer: %v", enableDequeuer)
	log.Infof("  - Finalizer: %v", enableFinalizer)
	log.Infof("  - Consensus: %v", enableConsensus)
	log.Infof("  - Event Monitor: %v", enableEventMonitor)
	
	// Get sequencer ID from configuration
	sequencerID := cfg.SequencerID
	
	// Initialize Redis if any component needs it
	var redisClient *redis.Client
	if enableListener || enableDequeuer || enableFinalizer || enableEventMonitor {
		redisAddr := fmt.Sprintf("%s:%s", cfg.RedisHost, cfg.RedisPort)
		log.Infof("Connecting to Redis at %s (DB: %d)", redisAddr, cfg.RedisDB)
		
		redisClient = redis.NewClient(&redis.Options{
			Addr:     redisAddr,
			Password: cfg.RedisPassword,
			DB:       cfg.RedisDB,
		})
		
		if err := redisClient.Ping(ctx).Err(); err != nil {
			log.Fatalf("Failed to connect to Redis: %v", err)
		}
		log.Infof("Connected to Redis at %s", redisAddr)
	}
	
	// Initialize deduplicator if Redis is available
	var dedup *deduplication.Deduplicator
	if redisClient != nil && enableListener {
		// Configure deduplication
		localCacheSize := cfg.DedupLocalCacheSize
		dedupTTL := cfg.DedupTTL
		
		var err error
		dedup, err = deduplication.NewDeduplicator(redisClient, localCacheSize, dedupTTL)
		if err != nil {
			log.Fatalf("Failed to create deduplicator: %v", err)
		}
		log.Infof("Deduplicator initialized with local cache size %d and TTL %v", localCacheSize, dedupTTL)
	}
	
	// Initialize P2P if listener or consensus is enabled
	var h host.Host
	var ps *pubsub.PubSub
	if enableListener || enableConsensus {
		p2pPort := strconv.Itoa(cfg.P2PPort)
		
		// Create or load private key
		privKey, err := loadOrCreatePrivateKey(cfg.P2PPrivateKey)
		if err != nil {
			log.Fatalf("Failed to get private key: %v", err)
		}
		
		// Configure connection manager for subscriber mode
		connMgr, err := connmgr.NewConnManager(
			cfg.ConnManagerLowWater,
			cfg.ConnManagerHighWater, 
			connmgr.WithGracePeriod(time.Minute),
		)
		if err != nil {
			log.Fatalf("Failed to create connection manager: %v", err)
		}
		log.Infof("Connection manager configured: LowWater=%d, HighWater=%d", cfg.ConnManagerLowWater, cfg.ConnManagerHighWater)
		
		// Build libp2p options
		opts := []libp2p.Option{
			libp2p.Identity(privKey),
			libp2p.ListenAddrStrings(fmt.Sprintf("/ip4/0.0.0.0/tcp/%s", p2pPort)),
			libp2p.EnableNATService(),
			libp2p.ConnectionManager(connMgr),
		}
		
		// Add public IP address if configured
		if cfg.P2PPublicIP != "" {
			publicAddr, err := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/%s/tcp/%s", cfg.P2PPublicIP, p2pPort))
			if err != nil {
				log.Errorf("Failed to create public multiaddr: %v", err)
			} else {
				opts = append(opts, libp2p.AddrsFactory(func(addrs []multiaddr.Multiaddr) []multiaddr.Multiaddr {
					// Add the public address to the list
					return append(addrs, publicAddr)
				}))
				log.Infof("Advertising public IP: %s", cfg.P2PPublicIP)
			}
		}
		
		// Create libp2p host with options
		h, err = libp2p.New(opts...)
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
		if len(cfg.BootstrapPeers) > 0 {
			connectToBootstrap(ctx, h, cfg.BootstrapPeers[0])
		}
		
		// Start discovery on rendezvous point
		rendezvousString := cfg.Rendezvous
		
		routingDiscovery := routing.NewRoutingDiscovery(kademliaDHT)
		
		// Advertise and discover peers on rendezvous
		go func() {
			log.Infof("Starting peer discovery on rendezvous: %s", rendezvousString)
			util.Advertise(ctx, routingDiscovery, rendezvousString)
			
			for {
				select {
				case <-ctx.Done():
					return
				default:
					peerChan, err := routingDiscovery.FindPeers(ctx, rendezvousString)
					if err != nil {
						log.Debugf("Error discovering peers: %v", err)
						time.Sleep(10 * time.Second)
						continue
					}
					
					for p := range peerChan {
						if p.ID == h.ID() {
							continue
						}
						if h.Network().Connectedness(p.ID) != 2 {
							log.Debugf("Found peer through rendezvous: %s", p.ID)
							if err := h.Connect(ctx, p); err != nil {
								log.Debugf("Failed to connect to peer %s: %v", p.ID, err)
							} else {
								log.Infof("Connected to peer via rendezvous: %s", p.ID)
							}
						}
					}
					time.Sleep(30 * time.Second)
				}
			}
		}()
		
		// Also advertise on the submission topics for discovery
		go func() {
			time.Sleep(5 * time.Second) // Wait a bit for DHT to stabilize
			topics := []string{
				"/powerloom/snapshot-submissions/0",
				"/powerloom/snapshot-submissions/all",
			}
			for _, topic := range topics {
				log.Infof("Advertising on topic: %s", topic)
				util.Advertise(ctx, routingDiscovery, topic)
			}
		}()
		
		// Get standardized gossipsub parameters for snapshot submissions mesh
		gossipParams, peerScoreParams, peerScoreThresholds, paramHash := gossipconfig.ConfigureSnapshotSubmissionsMesh(h.ID())
		
		// Create pubsub with standardized parameters
		ps, err = pubsub.NewGossipSub(ctx, h,
			pubsub.WithGossipSubParams(*gossipParams),
			pubsub.WithPeerScore(peerScoreParams, peerScoreThresholds),
			pubsub.WithDiscovery(routingDiscovery),
			pubsub.WithFloodPublish(true),
			pubsub.WithMessageSignaturePolicy(pubsub.StrictSign),
		)
		if err != nil {
			log.Fatalf("Failed to create pubsub: %v", err)
		}
		
		log.Infof("🔑 Gossipsub parameter hash: %s (unified sequencer)", paramHash)
		log.Info("Initialized gossipsub with standardized snapshot submissions mesh parameters")
	}
	
	// Create unified sequencer
	sequencer := &UnifiedSequencer{
		host:               h,
		ctx:                ctx,
		cancel:             cancel,
		ps:                 ps,
		redisClient:        redisClient,
		dedup:              dedup,
		enableListener:     enableListener,
		enableDequeuer:     enableDequeuer,
		enableFinalizer:    enableFinalizer,
		enableConsensus:    enableConsensus,
		enableEventMonitor: enableEventMonitor,
		config:             cfg,
		sequencerID:        sequencerID,
	}
	
	// Initialize components based on flags
	if enableDequeuer && redisClient != nil {
		sequencer.dequeuer = submissions.NewDequeuer(redisClient, sequencerID)
	}
	
	if enableConsensus {
		sequencer.batchGen = consensus.NewDummyBatchGenerator(sequencerID)
	}
	
	if enableEventMonitor && redisClient != nil {
		// Initialize RPC Helper with Powerloom chain config
		rpcConfig := cfg.ToRPCConfig()
		if rpcConfig == nil || len(rpcConfig.Nodes) == 0 {
			log.Fatal("POWERLOOM_RPC_NODES must be configured for event monitoring")
		}
		
		// Set default timeouts if not configured
		if rpcConfig.RequestTimeout == 0 {
			rpcConfig.RequestTimeout = 30 * time.Second
		}
		if rpcConfig.MaxRetries == 0 {
			rpcConfig.MaxRetries = 3
		}
		
		rpcHelper := rpchelper.NewRPCHelper(rpcConfig)
		if err := rpcHelper.Initialize(context.Background()); err != nil {
			log.Fatalf("Failed to initialize RPC helper: %v", err)
		}
		
		// Create event monitor config
		monitorCfg := &eventmonitor.Config{
			RPCHelper:       rpcHelper,
			ContractAddress: cfg.ProtocolStateContract,
			ContractABIPath: cfg.ContractABIPath,
			RedisClient:     redisClient,
			WindowDuration:  cfg.SubmissionWindowDuration, // Use configured duration
			StartBlock:      cfg.EventStartBlock,
			PollInterval:    cfg.EventPollInterval,
			DataMarkets:     cfg.DataMarketAddresses,
			MaxWindows:      cfg.MaxConcurrentWindows,
		}
		
		var err error
		sequencer.eventMonitor, err = eventmonitor.NewEventMonitor(monitorCfg)
		if err != nil {
			log.Errorf("Failed to create event monitor: %v", err)
			// Don't fail completely, just disable event monitor
			sequencer.enableEventMonitor = false
		}
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
		workers := s.config.DequeueWorkers
		if workers == 0 {
			workers = 5 // Default fallback
		}
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
	
	// Start event monitor component
	if s.enableEventMonitor && s.eventMonitor != nil {
		log.Info("Starting Event Monitor component...")
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.runEventMonitor()
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
		var epochID interface{}
		if eid, ok := submissionInfo["epoch_id"]; ok {
			epochID = eid
			log.Infof("📋 Submission Details: Epoch=%v, Topic=%s, Peer=%s",
				epochID, topic, peerID[:16])
			
			// Check if this is a heartbeat message (Epoch 0 with null submissions)
			// Real Epoch 0 submissions will have non-null submissions array
			if epochID == float64(0) || epochID == 0 || epochID == "0" {
				submissions, hasSubmissions := submissionInfo["submissions"]
				
				// Check if this is a heartbeat (null submissions) vs real Epoch 0 data
				if hasSubmissions && submissions == nil {
					// This is a heartbeat message - log and skip
					log.Debugf("💓 Heartbeat received from %s (skipping queue)", peerID[:16])
					if signature, ok := submissionInfo["signature"]; ok {
						// Try to decode the signature to see heartbeat info
						if sigStr, ok := signature.(string); ok {
							log.Debugf("   Heartbeat signature: %s", sigStr)
						}
					}
					return // Don't queue heartbeat messages
				} else if hasSubmissions {
					// This is a real Epoch 0 submission with data
					log.Infof("📥 Real Epoch 0 submission received with data from %s", peerID[:16])
				}
			}
			
			// Enhanced debug logging for ALL submissions
			log.Infof("🔍 DEBUG: Full submission content:")
			log.Infof("   Raw JSON (first 500 chars): %s", truncateString(string(data), 500))
			
			// Log all top-level keys
			keys := make([]string, 0, len(submissionInfo))
			for k := range submissionInfo {
				keys = append(keys, k)
			}
			log.Infof("   Top-level keys: %v", keys)
			
			// Log specific fields if present
			if snapshotterId, ok := submissionInfo["snapshotter_id"]; ok {
				log.Infof("   Snapshotter ID: %v", snapshotterId)
			}
			if signature, ok := submissionInfo["signature"]; ok {
				log.Infof("   Signature present: yes (len=%d)", len(fmt.Sprintf("%v", signature)))
			}
			if submissions, ok := submissionInfo["submissions"]; ok {
				log.Infof("   Submissions field present: %T", submissions)
				// If submissions is an array, check the first item for request field
				if subArray, ok := submissions.([]interface{}); ok && len(subArray) > 0 {
					if firstSub, ok := subArray[0].(map[string]interface{}); ok {
						if request, ok := firstSub["request"]; ok {
							if reqMap, ok := request.(map[string]interface{}); ok {
								log.Infof("   First submission request: ProjectID=%v, SnapshotCID=%v, EpochID=%v",
									reqMap["projectId"], reqMap["snapshotCid"], reqMap["epochId"])
							}
						}
					}
				}
			}
			// Check for top-level request (in case of different message format)
			if request, ok := submissionInfo["request"]; ok {
				log.Infof("   Request field present at top level: %T", request)
			}
		}
		
		// Check if it's a P2P batch submission
		if submissions, ok := submissionInfo["submissions"]; ok {
			if subArray, ok := submissions.([]interface{}); ok {
				log.Infof("   └─ Batch submission with %d items", len(subArray))
			}
		}
		
		// Extract deduplication keys if dedup is enabled
		var dedupKey string
		if s.dedup != nil {
			// Try to extract project_id and snapshot_cid for deduplication
			if request, ok := submissionInfo["request"].(map[string]interface{}); ok {
				projectID := fmt.Sprintf("%v", request["project_id"])
				snapshotCID := fmt.Sprintf("%v", request["snapshot_cid"])
				
				// Convert epoch_id to uint64
				var epoch uint64
				switch v := epochID.(type) {
				case float64:
					epoch = uint64(v)
				case int:
					epoch = uint64(v)
				case string:
					if parsed, err := strconv.ParseUint(v, 10, 64); err == nil {
						epoch = parsed
					}
				}
				
				if projectID != "" && snapshotCID != "" {
					dedupKey = s.dedup.GenerateKey(projectID, epoch, snapshotCID)
					
					// Check if we've seen this submission before
					isNew, err := s.dedup.CheckAndMark(s.ctx, dedupKey)
					if err != nil {
						log.Errorf("Deduplication check failed: %v", err)
						// Continue processing on error to avoid losing submissions
					} else if !isNew {
						log.Debugf("🔁 Duplicate submission detected (key=%s), skipping", dedupKey[:16])
						return // Skip duplicate
					}
				}
				
				log.Infof("   └─ SlotID=%v, ProjectID=%v, CID=%v",
					request["slot_id"], projectID, snapshotCID)
			}
		} else if request, ok := submissionInfo["request"].(map[string]interface{}); ok {
			// No dedup, just log
			log.Infof("   └─ SlotID=%v, ProjectID=%v, CID=%v",
				request["slot_id"], request["project_id"], request["snapshot_cid"])
		}
	} else {
		log.Infof("📋 Received raw submission (%d bytes) from %s", len(data), peerID[:16])
	}
	
	if s.redisClient != nil {
		// Enrich submission with protocol state if not already present
		var enrichedData []byte
		if submissionInfo != nil {
			// Add protocol state if missing
			if _, hasProtocol := submissionInfo["protocol_state"]; !hasProtocol {
				submissionInfo["protocol_state"] = s.config.ProtocolStateContract
			}
			
			// Ensure data market is set (use first configured market as default if missing)
			if _, hasMarket := submissionInfo["data_market"]; !hasMarket && len(s.config.DataMarketAddresses) > 0 {
				submissionInfo["data_market"] = s.config.DataMarketAddresses[0]
			}
			
			enrichedData, _ = json.Marshal(submissionInfo)
		} else {
			enrichedData = data // Use original if parsing failed
		}
		
		err := s.redisClient.LPush(s.ctx, "submissionQueue", enrichedData).Err()
		if err != nil {
			log.Errorf("❌ Failed to queue submission: %v", err)
		} else {
			// Extract key info for logging
			epochID := "unknown"
			projectID := "unknown"
			if submissionInfo != nil {
				if ep, ok := submissionInfo["epochId"]; ok {
					epochID = fmt.Sprintf("%v", ep)
				}
				if proj, ok := submissionInfo["projectId"]; ok {
					projectID = fmt.Sprintf("%v", proj)
				}
			}
			
			log.Infof("✅ Queued submission: Epoch=%s, Project=%s from peer %s", 
				epochID, projectID, peerID[:16])
			
			// Log current queue depth
			if length, err := s.redisClient.LLen(s.ctx, "submissionQueue").Result(); err == nil {
				log.Debugf("   Queue depth now: %d", length)
			}
		}
	} else {
		log.Warnf("⚠️ Redis not available - submission not queued")
	}
}

// Helper function for min of two ints
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
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
			submissionData := []byte(result[1])
			
			// Parse submission to extract details
			var submission submissions.SnapshotSubmission
			if err := json.Unmarshal(submissionData, &submission); err != nil {
				log.Errorf("Worker %d: Failed to parse submission: %v", workerID, err)
				log.Debugf("Worker %d: Raw submission data: %s", workerID, string(submissionData[:min(200, len(submissionData))]))
				continue
			}
			
			// Generate submission ID
			submissionID := fmt.Sprintf("%d-%s-%d-%s", 
				submission.Request.EpochId, 
				submission.Request.ProjectId,
				submission.Request.SlotId,
				submission.Request.SnapshotCid)
			
			// Log processing
			log.Infof("Worker %d processing: Epoch=%d, Project=%s, Slot=%d, Market=%s, CID=%s",
				workerID, submission.Request.EpochId, submission.Request.ProjectId, 
				submission.Request.SlotId, submission.DataMarket, submission.Request.SnapshotCid)
			
			// Process and store the submission
			if s.dequeuer != nil {
				if err := s.dequeuer.ProcessSubmission(&submission, submissionID); err != nil {
					log.Errorf("Worker %d: Failed to process submission %s: %v", workerID, submissionID, err)
				} else {
					log.Debugf("Worker %d: Successfully processed and stored submission %s", workerID, submissionID)
				}
			} else {
				log.Warnf("Worker %d: Dequeuer not initialized, skipping storage", workerID)
			}
		}
	}
}

func (s *UnifiedSequencer) runFinalizer() {
	log.Info("Starting finalizer component with parallel workers")
	
	// Start multiple parallel workers
	numWorkers := s.config.FinalizerWorkers
	if numWorkers == 0 {
		numWorkers = 5 // Default to 5 workers
	}
	
	log.Infof("Starting %d parallel finalization workers", numWorkers)
	
	for i := 0; i < numWorkers; i++ {
		go s.runFinalizationWorker(i)
	}
	
	// Also start aggregation worker
	go s.runAggregationWorker()
	
	// Wait for shutdown
	<-s.ctx.Done()
	log.Info("Finalizer component shutting down")
}

func (s *UnifiedSequencer) runFinalizationWorker(workerID int) {
	log.Infof("Finalization worker %d started", workerID)
	
	// Create worker monitor
	monitor := workers.NewWorkerMonitor(
		s.redisClient, 
		fmt.Sprintf("finalizer-%d", workerID),
		workers.WorkerTypeFinalizer,
		s.config.ProtocolStateContract,
	)
	monitor.StartWorker(s.ctx)
	defer monitor.CleanupWorker()
	
	// Get protocol state and data market from config
	protocolState := s.config.ProtocolStateContract
	dataMarket := ""
	if len(s.config.DataMarketAddresses) > 0 {
		dataMarket = s.config.DataMarketAddresses[0]
	}
	
	// Batch parts queue key
	queueKey := fmt.Sprintf("%s:%s:batchPartsQueue", protocolState, dataMarket)
	
	for {
		select {
		case <-s.ctx.Done():
			log.Infof("Finalization worker %d shutting down", workerID)
			return
		default:
			// Pop from batch parts queue with blocking timeout
			result, err := s.redisClient.BRPop(s.ctx, 5*time.Second, queueKey).Result()
			if err != nil {
				if err == redis.Nil {
					continue // Queue empty, wait
				}
				log.Errorf("Worker %d: Error popping from queue: %v", workerID, err)
				time.Sleep(1 * time.Second)
				continue
			}
			
			if len(result) < 2 {
				continue
			}
			
			// Parse batch part data
			var batchPart map[string]interface{}
			if err := json.Unmarshal([]byte(result[1]), &batchPart); err != nil {
				log.Errorf("Worker %d: Failed to parse batch part: %v", workerID, err)
				continue
			}
			
			epochID := uint64(batchPart["epoch_id"].(float64))
			batchID := int(batchPart["batch_id"].(float64))
			totalBatches := int(batchPart["total_batches"].(float64))
			projectIDs := batchPart["project_ids"].([]interface{})
			
			// Update monitoring
			batchInfo := fmt.Sprintf("epoch:%d:batch:%d/%d", epochID, batchID, totalBatches)
			monitor.ProcessingStarted(batchInfo)
			
			// Process this batch part
			if err := s.processBatchPart(epochID, batchID, totalBatches, projectIDs, monitor); err != nil {
				log.Errorf("Worker %d: Failed to process batch part %d for epoch %d: %v", 
					workerID, batchID, epochID, err)
				monitor.ProcessingFailed(err)
			} else {
				monitor.ProcessingCompleted()
				log.Infof("Worker %d: Completed batch part %d/%d for epoch %d", 
					workerID, batchID, totalBatches, epochID)
			}
		}
	}
}

func (s *UnifiedSequencer) processBatchPart(epochID uint64, batchID int, totalBatches int, projectIDs []interface{}, monitor *workers.WorkerMonitor) error {
	ctx := context.Background()
	
	// Get protocol state and data market from config
	protocolState := s.config.ProtocolStateContract
	dataMarket := ""
	if len(s.config.DataMarketAddresses) > 0 {
		dataMarket = s.config.DataMarketAddresses[0]
	}
	
	// Track batch part as processing
	epochStr := fmt.Sprintf("%d", epochID)
	workers.TrackBatchPart(s.redisClient, epochStr, batchID, "processing")
	
	// Process each project in this batch part
	partResults := make(map[string]interface{})
	
	for _, projectIDRaw := range projectIDs {
		projectID := projectIDRaw.(string)
		
		// Get all submissions for this project in this epoch
		submissionPattern := fmt.Sprintf("%s:%s:epoch:%d:project:%s:*", protocolState, dataMarket, epochID, projectID)
		
		// Use SCAN to find all submission keys for this project
		iter := s.redisClient.Scan(ctx, 0, submissionPattern, 100).Iterator()
		
		// Track CID votes
		cidVotes := make(map[string]int)
		
		for iter.Next(ctx) {
			key := iter.Val()
			
			// Get the submission data
			submissionData, err := s.redisClient.Get(ctx, key).Result()
			if err != nil {
				log.Errorf("Failed to get submission from %s: %v", key, err)
				continue
			}
			
			// Parse to get CID
			var submission map[string]interface{}
			if err := json.Unmarshal([]byte(submissionData), &submission); err != nil {
				log.Errorf("Failed to parse submission: %v", err)
				continue
			}
			
			// Extract CID and count vote
			if request, ok := submission["request"].(map[string]interface{}); ok {
				if cid, ok := request["snapshot_cid"].(string); ok {
					cidVotes[cid]++
				}
			}
		}
		
		if err := iter.Err(); err != nil {
			log.Errorf("Error scanning submissions: %v", err)
		}
		
		// Select winning CID (majority vote)
		var winningCID string
		maxVotes := 0
		for cid, votes := range cidVotes {
			if votes > maxVotes {
				winningCID = cid
				maxVotes = votes
			}
		}
		
		if winningCID != "" {
			partResults[projectID] = map[string]interface{}{
				"cid":   winningCID,
				"votes": maxVotes,
			}
		}
	}
	
	// Store batch part results
	partKey := fmt.Sprintf("%s:%s:batch:part:%d:%d", protocolState, dataMarket, epochID, batchID)
	partData, err := json.Marshal(partResults)
	if err != nil {
		return fmt.Errorf("failed to marshal batch part: %w", err)
	}
	
	if err := s.redisClient.Set(ctx, partKey, partData, 2*time.Hour).Err(); err != nil {
		return fmt.Errorf("failed to store batch part: %w", err)
	}
	
	// Mark batch part as completed
	workers.TrackBatchPart(s.redisClient, epochStr, batchID, "completed")
	
	// Update progress tracking
	completedKey := fmt.Sprintf("epoch:%d:parts:completed", epochID)
	completed, _ := s.redisClient.Incr(ctx, completedKey).Result()
	
	// Check if all parts are complete
	workers.UpdateBatchPartsProgress(s.redisClient, epochStr, int(completed), totalBatches)
	
	log.Infof("✅ Processed batch part %d/%d for epoch %d: %d projects finalized", 
		batchID, totalBatches, epochID, len(partResults))
	
	return nil
}

func (s *UnifiedSequencer) runAggregationWorker() {
	log.Info("Aggregation worker started")
	
	// Create worker monitor
	monitor := workers.NewWorkerMonitor(
		s.redisClient,
		"aggregator-main",
		workers.WorkerTypeAggregator,
		s.config.ProtocolStateContract,
	)
	monitor.StartWorker(s.ctx)
	defer monitor.CleanupWorker()
	
	for {
		select {
		case <-s.ctx.Done():
			log.Info("Aggregation worker shutting down")
			return
		default:
			// Pop from aggregation queue
			result, err := s.redisClient.BRPop(s.ctx, 5*time.Second, "aggregationQueue").Result()
			if err != nil {
				if err == redis.Nil {
					continue // Queue empty
				}
				log.Errorf("Aggregation worker: Error popping from queue: %v", err)
				time.Sleep(1 * time.Second)
				continue
			}
			
			if len(result) < 2 {
				continue
			}
			
			// Parse aggregation data
			var aggData map[string]interface{}
			if err := json.Unmarshal([]byte(result[1]), &aggData); err != nil {
				log.Errorf("Aggregation worker: Failed to parse data: %v", err)
				continue
			}
			
			epochIDStr := aggData["epoch_id"].(string)
			epochID, _ := strconv.ParseUint(epochIDStr, 10, 64)
			partsCompleted := int(aggData["parts_completed"].(float64))
			
			// Update monitoring
			monitor.ProcessingStarted(fmt.Sprintf("epoch:%d", epochID))
			
			// Aggregate all batch parts
			if err := s.aggregateBatchParts(epochID, partsCompleted); err != nil {
				log.Errorf("Aggregation worker: Failed to aggregate epoch %d: %v", epochID, err)
				monitor.ProcessingFailed(err)
			} else {
				monitor.ProcessingCompleted()
				log.Infof("✅ Aggregation worker: Successfully aggregated epoch %d", epochID)
			}
		}
	}
}

func (s *UnifiedSequencer) aggregateBatchParts(epochID uint64, totalParts int) error {
	ctx := context.Background()
	
	// Get protocol state and data market
	protocolState := s.config.ProtocolStateContract
	dataMarket := ""
	if len(s.config.DataMarketAddresses) > 0 {
		dataMarket = s.config.DataMarketAddresses[0]
	}
	
	// Collect all batch parts
	aggregatedResults := make(map[string]interface{})
	
	for i := 0; i < totalParts; i++ {
		partKey := fmt.Sprintf("%s:%s:batch:part:%d:%d", protocolState, dataMarket, epochID, i)
		partData, err := s.redisClient.Get(ctx, partKey).Result()
		if err != nil {
			log.Errorf("Failed to get batch part %d for epoch %d: %v", i, epochID, err)
			continue
		}
		
		var partResults map[string]interface{}
		if err := json.Unmarshal([]byte(partData), &partResults); err != nil {
			log.Errorf("Failed to parse batch part %d: %v", i, err)
			continue
		}
		
		// Merge results
		for projectID, data := range partResults {
			aggregatedResults[projectID] = data
		}
		
		// Clean up part data
		s.redisClient.Del(ctx, partKey)
	}
	
	// Create finalized batch
	if err := s.createFinalizedBatch(epochID, aggregatedResults); err != nil {
		return fmt.Errorf("failed to create finalized batch: %w", err)
	}
	
	// Clean up tracking data
	s.redisClient.Del(ctx, 
		fmt.Sprintf("epoch:%d:parts:completed", epochID),
		fmt.Sprintf("epoch:%d:parts:total", epochID),
		fmt.Sprintf("epoch:%d:parts:ready", epochID),
	)
	
	log.Infof("📦 Aggregated %d projects from %d batch parts for epoch %d", 
		len(aggregatedResults), totalParts, epochID)
	
	return nil
}

func (s *UnifiedSequencer) processBatchFinalization(epochID uint64) {
	ctx := context.Background()
	
	// Get protocol state and data market from config
	protocolState := s.config.ProtocolStateContract
	dataMarket := ""
	if len(s.config.DataMarketAddresses) > 0 {
		dataMarket = s.config.DataMarketAddresses[0]
	}
	
	// Get ready batch for this epoch
	batchKey := fmt.Sprintf("%s:%s:batch:ready:%d", protocolState, dataMarket, epochID)
	batchData, err := s.redisClient.Get(ctx, batchKey).Result()
	if err != nil {
		if err == redis.Nil {
			log.Warnf("Finalizer: No ready batch found for epoch %d", epochID)
		} else {
			log.Errorf("Finalizer: Failed to get batch for epoch %d: %v", epochID, err)
		}
		return
	}
	
	// Parse batch data
	var projectSubmissions map[string]interface{}
	if err := json.Unmarshal([]byte(batchData), &projectSubmissions); err != nil {
		log.Errorf("Finalizer: Failed to parse batch data for epoch %d: %v", epochID, err)
		return
	}
	
	// Create finalized batch (actual merkle tree and signing)
	if err := s.createFinalizedBatch(epochID, projectSubmissions); err != nil {
		log.Errorf("Finalizer: Failed to create finalized batch for epoch %d: %v", epochID, err)
		return
	}
	
	// Clean up ready batch
	s.redisClient.Del(ctx, batchKey)
	
	log.Infof("✅ Finalizer: Successfully finalized batch for epoch %d with %d projects", 
		epochID, len(projectSubmissions))
}

func (s *UnifiedSequencer) createFinalizedBatch(epochID uint64, projectSubmissions map[string]interface{}) error {
	ctx := context.Background()
	
	// Extract project IDs and CIDs
	projectIDs := make([]string, 0)
	snapshotCIDs := make([]string, 0) 
	projectVotes := make(map[string]uint32)
	
	for projectID, submissionData := range projectSubmissions {
		// Handle the new structure with vote metadata
		if dataMap, ok := submissionData.(map[string]interface{}); ok {
			if cid, ok := dataMap["cid"].(string); ok {
				projectIDs = append(projectIDs, projectID)
				snapshotCIDs = append(snapshotCIDs, cid)
				
				// Extract vote count
				if votes, ok := dataMap["votes"].(float64); ok {
					projectVotes[projectID] = uint32(votes)
				}
			}
		} else if cid, ok := submissionData.(string); ok {
			// Fallback for old format (direct CID string)
			projectIDs = append(projectIDs, projectID)
			snapshotCIDs = append(snapshotCIDs, cid)
			projectVotes[projectID] = 1 // Default to 1 vote
		}
	}
	
	if len(projectIDs) == 0 {
		return fmt.Errorf("no valid projects in batch")
	}
	
	// TODO: Implement actual merkle tree calculation
	// For now, simple hash concatenation
	combined := ""
	for i := range projectIDs {
		combined += projectIDs[i] + ":" + snapshotCIDs[i] + ","
	}
	hash := sha256.Sum256([]byte(combined))
	merkleRoot := hash[:]
	
	// TODO: Implement actual BLS signing
	// For now, placeholder signature
	sigData := fmt.Sprintf("%d:%s:%s", epochID, merkleRoot, s.sequencerID)
	sigHash := sha256.Sum256([]byte(sigData))
	blsSignature := sigHash[:]
	
	finalizedBatch := &consensus.FinalizedBatch{
		EpochId:      epochID,
		ProjectIds:   projectIDs,
		SnapshotCids: snapshotCIDs,
		MerkleRoot:   merkleRoot,
		BlsSignature: blsSignature,
		SequencerId:  s.sequencerID,
		Timestamp:    uint64(time.Now().Unix()),
		ProjectVotes: projectVotes,
	}
	
	// Store finalized batch in Redis
	protocolState := s.config.ProtocolStateContract
	dataMarket := ""
	if len(s.config.DataMarketAddresses) > 0 {
		dataMarket = s.config.DataMarketAddresses[0]
	}
	finalizedKey := fmt.Sprintf("%s:%s:finalized:%d", protocolState, dataMarket, epochID)
	
	finalizedData, err := json.Marshal(finalizedBatch)
	if err != nil {
		return fmt.Errorf("failed to marshal finalized batch: %w", err)
	}
	
	// Store with TTL
	if err := s.redisClient.Set(ctx, finalizedKey, finalizedData, 24*time.Hour).Err(); err != nil {
		return fmt.Errorf("failed to store finalized batch: %w", err)
	}
	
	log.Infof("📦 Created finalized batch for epoch %d: %d projects, merkle=%s", 
		epochID, len(projectIDs), hex.EncodeToString(merkleRoot[:8]))
	
	return nil
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

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func loadOrCreatePrivateKey(privKeyHex string) (crypto.PrivKey, error) {
	if privKeyHex != "" {
		// Decode hex string to bytes
		privKeyBytes, err := hex.DecodeString(privKeyHex)
		if err != nil {
			return nil, fmt.Errorf("failed to decode private key hex: %v", err)
		}
		// Load existing key
		return crypto.UnmarshalEd25519PrivateKey(privKeyBytes)
	}
	// Generate new key
	privKey, _, err := crypto.GenerateEd25519Key(nil)
	return privKey, err
}

func connectToBootstrap(ctx context.Context, h host.Host, bootstrapAddr string) {
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

func (s *UnifiedSequencer) runEventMonitor() {
	log.Info("🔍 Starting event monitor for EpochReleased events")
	
	// Start monitoring - this will handle submission windows
	if err := s.eventMonitor.Start(); err != nil {
		log.Errorf("Event monitor failed: %v", err)
		return
	}
	
	// Wait for context cancellation
	<-s.ctx.Done()
	s.eventMonitor.Stop()
	log.Info("Event monitor stopped")
}