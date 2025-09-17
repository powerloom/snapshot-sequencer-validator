package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-redis/redis/v8"
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
	"github.com/powerloom/snapshot-sequencer-validator/config"
	"github.com/powerloom/snapshot-sequencer-validator/pkgs/consensus"
	"github.com/powerloom/snapshot-sequencer-validator/pkgs/gossipconfig"
	"github.com/powerloom/snapshot-sequencer-validator/pkgs/ipfs"
	log "github.com/sirupsen/logrus"
)

func main() {
	// Parse command-line flags
	var (
		validatorID    = flag.String("id", "", "Validator ID (required)")
		listenPort     = flag.Int("port", 0, "P2P listen port (0 for random)")
		bootstrapAddrs = flag.String("bootstrap", "", "Bootstrap node addresses (comma-separated)")
		redisAddr      = flag.String("redis", "localhost:6379", "Redis address")
		ipfsAPI        = flag.String("ipfs", "", "IPFS API address (e.g., localhost:5001)")
		testMode       = flag.Bool("test", false, "Enable test mode with dummy batch generation")
		verbose        = flag.Bool("v", false, "Enable verbose logging")
	)
	flag.Parse()

	if *validatorID == "" {
		log.Fatal("Validator ID is required (use -id flag)")
	}

	// Set log level
	if *verbose {
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetLevel(log.InfoLevel)
	}

	log.Infof("Starting consensus test node: %s", *validatorID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize Redis
	redisClient := redis.NewClient(&redis.Options{
		Addr: *redisAddr,
	})
	defer redisClient.Close()

	// Test Redis connection
	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Warnf("Redis not available: %v (continuing without persistence)", err)
		redisClient = nil
	}

	// Initialize IPFS client if configured
	var ipfsClient *ipfs.Client
	if *ipfsAPI != "" {
		ipfsClient = ipfs.NewClient(*ipfsAPI)
		log.Infof("IPFS client initialized: %s", *ipfsAPI)
	} else {
		log.Warn("No IPFS API configured, batch storage will be simulated")
	}

	// Create P2P host
	h, ps, err := createP2PHost(ctx, *listenPort, *bootstrapAddrs)
	if err != nil {
		log.Fatalf("Failed to create P2P host: %v", err)
	}
	defer h.Close()

	log.Infof("P2P Host started with ID: %s", h.ID())
	for _, addr := range h.Addrs() {
		log.Infof("Listening on: %s/p2p/%s", addr, h.ID())
	}

	// Initialize P2P consensus
	p2pConsensus, err := consensus.NewP2PConsensus(ctx, h, ps, redisClient, ipfsClient, *validatorID)
	if err != nil {
		log.Fatalf("Failed to initialize P2P consensus: %v", err)
	}
	defer p2pConsensus.Close()

	// If test mode, start generating dummy batches
	if *testMode {
		go generateTestBatches(ctx, p2pConsensus, *validatorID, ipfsClient)
	}

	// Monitor consensus status
	go monitorConsensusStatus(ctx, p2pConsensus)

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	log.Info("Consensus test node running. Press Ctrl+C to stop.")
	<-sigCh

	log.Info("Shutting down consensus test node...")
}

// createP2PHost creates a libp2p host with DHT and pubsub
func createP2PHost(ctx context.Context, port int, bootstrapAddrs string) (host.Host, *pubsub.PubSub, error) {
	// Generate or load private key
	privKey, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate key: %w", err)
	}

	// Create connection manager
	connMgr, err := connmgr.NewConnManager(10, 100, connmgr.WithGracePeriod(time.Minute))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create conn manager: %w", err)
	}

	// Build host options
	opts := []libp2p.Option{
		libp2p.Identity(privKey),
		libp2p.ConnectionManager(connMgr),
		libp2p.EnableNATService(),
		libp2p.NATPortMap(),
		libp2p.EnableAutoRelay(),
	}

	if port > 0 {
		opts = append(opts, libp2p.ListenAddrStrings(
			fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", port),
		))
	} else {
		opts = append(opts, libp2p.ListenAddrStrings(
			"/ip4/0.0.0.0/tcp/0",
		))
	}

	// Create host
	h, err := libp2p.New(opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create host: %w", err)
	}

	// Setup DHT
	kademliaDHT, err := dht.New(ctx, h, dht.Mode(dht.ModeClient))
	if err != nil {
		h.Close()
		return nil, nil, fmt.Errorf("failed to create DHT: %w", err)
	}

	if err := kademliaDHT.Bootstrap(ctx); err != nil {
		h.Close()
		return nil, nil, fmt.Errorf("failed to bootstrap DHT: %w", err)
	}

	// Setup pubsub with gossipsub
	gossipCfg := gossipconfig.DefaultGossipSubParams()
	ps, err := pubsub.NewGossipSub(ctx, h, pubsub.WithGossipSubParams(gossipCfg))
	if err != nil {
		h.Close()
		return nil, nil, fmt.Errorf("failed to create pubsub: %w", err)
	}

	// Connect to bootstrap nodes
	if bootstrapAddrs != "" {
		for _, addr := range strings.Split(bootstrapAddrs, ",") {
			maddr, err := multiaddr.NewMultiaddr(strings.TrimSpace(addr))
			if err != nil {
				log.Errorf("Invalid bootstrap address %s: %v", addr, err)
				continue
			}

			peerInfo, err := peer.AddrInfoFromP2pAddr(maddr)
			if err != nil {
				log.Errorf("Failed to parse bootstrap peer info: %v", err)
				continue
			}

			if err := h.Connect(ctx, *peerInfo); err != nil {
				log.Errorf("Failed to connect to bootstrap %s: %v", peerInfo.ID, err)
			} else {
				log.Infof("Connected to bootstrap node: %s", peerInfo.ID)
			}
		}
	}

	// Setup discovery
	routingDiscovery := routing.NewRoutingDiscovery(kademliaDHT)
	util.Advertise(ctx, routingDiscovery, "powerloom-consensus")

	// Discover peers
	go func() {
		for {
			peers, err := util.FindPeers(ctx, routingDiscovery, "powerloom-consensus")
			if err != nil {
				log.Debugf("Discovery error: %v", err)
			} else {
				for p := range peers {
					if p.ID != h.ID() && len(p.Addrs) > 0 {
						h.Connect(ctx, p)
					}
				}
			}
			time.Sleep(30 * time.Second)
		}
	}()

	return h, ps, nil
}

// generateTestBatches generates dummy batches for testing
func generateTestBatches(ctx context.Context, p2pConsensus *consensus.P2PConsensus, validatorID string, ipfsClient *ipfs.Client) {
	batchGen := consensus.NewDummyBatchGenerator(validatorID)
	ticker := time.NewTicker(35 * time.Second) // Generate batch every 35 seconds (after epoch closes)
	defer ticker.Stop()

	// Wait a bit before starting
	time.Sleep(10 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			epochID := uint64(time.Now().Unix() / 30) - 1 // Previous epoch

			// Generate dummy batch
			batch := batchGen.GenerateDummyBatch(epochID)

			// Simulate IPFS storage if no real IPFS
			if ipfsClient == nil {
				// Create fake IPFS CID
				batch.BatchIPFSCID = fmt.Sprintf("Qm%s", hex.EncodeToString(batch.MerkleRoot)[:44])
			} else {
				// Store in real IPFS
				cid, err := ipfsClient.StoreFinalizedBatch(ctx, batch)
				if err != nil {
					log.Errorf("Failed to store batch in IPFS: %v", err)
					batch.BatchIPFSCID = fmt.Sprintf("Qm%s", hex.EncodeToString(batch.MerkleRoot)[:44])
				} else {
					batch.BatchIPFSCID = cid
				}
			}

			log.Infof("Generated test batch for epoch %d with %d projects", epochID, len(batch.ProjectIds))

			// Broadcast to consensus network
			if err := p2pConsensus.BroadcastFinalizedBatch(batch); err != nil {
				log.Errorf("Failed to broadcast batch: %v", err)
			}
		}
	}
}

// monitorConsensusStatus periodically logs consensus status
func monitorConsensusStatus(ctx context.Context, p2pConsensus *consensus.P2PConsensus) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			currentEpoch := uint64(time.Now().Unix() / 30)

			// Check last 3 epochs
			for i := uint64(0); i < 3; i++ {
				epochID := currentEpoch - i
				status := p2pConsensus.GetConsensusStatus(epochID)

				if status != nil {
					if status.ConsensusReached {
						log.Infof("✅ Epoch %d: CONSENSUS REACHED - %d/%d validators agree",
							epochID, status.ReceivedBatches, status.TotalValidators)
					} else if status.ReceivedBatches > 0 {
						log.Infof("⏳ Epoch %d: %d validators reported (no consensus yet)",
							epochID, status.ReceivedBatches)
					}
				}

				// Also show validator batches
				batches := p2pConsensus.GetValidatorBatches(epochID)
				if len(batches) > 0 {
					log.Debugf("  Validators for epoch %d:", epochID)
					for validatorID, batch := range batches {
						log.Debugf("    • %s: CID=%s... Merkle=%s...",
							validatorID, batch.BatchIPFSCID[:12], batch.MerkleRoot[:8])
					}
				}
			}
		}
	}
}