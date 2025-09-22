package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/powerloom/snapshot-sequencer-validator/config"
	"github.com/powerloom/snapshot-sequencer-validator/pkgs/p2p"
	"github.com/go-redis/redis/v8"
	"github.com/sirupsen/logrus"
)

var log = logrus.New()

type P2PGateway struct {
	ctx        context.Context
	cancel     context.CancelFunc
	p2pHost    *p2p.P2PHost
	redisClient *redis.Client
	config     *config.Settings

	// Topic subscriptions
	submissionSub *pubsub.Subscription
	batchSub      *pubsub.Subscription
	presenceSub   *pubsub.Subscription

	// Topic handlers
	submissionTopic *pubsub.Topic
	batchTopic      *pubsub.Topic
	presenceTopic   *pubsub.Topic
}

func NewP2PGateway(cfg *config.Settings) (*P2PGateway, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Initialize Redis
	redisOpts := &redis.Options{
		Addr: fmt.Sprintf("%s:%s", cfg.RedisHost, cfg.RedisPort),
		DB:   cfg.RedisDB,
	}
	// Only set password if it's not empty
	if cfg.RedisPassword != "" {
		redisOpts.Password = cfg.RedisPassword
	}
	redisClient := redis.NewClient(redisOpts)

	if err := redisClient.Ping(ctx).Err(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	// Initialize P2P host
	p2pHost, err := p2p.NewP2PHost(ctx, cfg)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create P2P host: %w", err)
	}

	gateway := &P2PGateway{
		ctx:         ctx,
		cancel:      cancel,
		p2pHost:     p2pHost,
		redisClient: redisClient,
		config:      cfg,
	}

	// Setup topic subscriptions
	if err := gateway.setupTopics(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to setup topics: %w", err)
	}

	return gateway, nil
}

func (g *P2PGateway) setupTopics() error {
	// Snapshot submissions topic
	submissionTopic, err := g.p2pHost.Pubsub.Join("/powerloom/snapshot-submissions/all")
	if err != nil {
		return fmt.Errorf("failed to join submission topic: %w", err)
	}
	g.submissionTopic = submissionTopic

	g.submissionSub, err = submissionTopic.Subscribe()
	if err != nil {
		return fmt.Errorf("failed to subscribe to submission topic: %w", err)
	}

	// Finalized batches topic
	batchTopic, err := g.p2pHost.Pubsub.Join("/powerloom/finalized-batches/all")
	if err != nil {
		return fmt.Errorf("failed to join batch topic: %w", err)
	}
	g.batchTopic = batchTopic

	g.batchSub, err = batchTopic.Subscribe()
	if err != nil {
		return fmt.Errorf("failed to subscribe to batch topic: %w", err)
	}

	// Validator presence topic
	presenceTopic, err := g.p2pHost.Pubsub.Join("/powerloom/validator/presence")
	if err != nil {
		return fmt.Errorf("failed to join presence topic: %w", err)
	}
	g.presenceTopic = presenceTopic

	g.presenceSub, err = presenceTopic.Subscribe()
	if err != nil {
		return fmt.Errorf("failed to subscribe to presence topic: %w", err)
	}

	log.Info("P2P Gateway: Subscribed to all topics")
	return nil
}

func (g *P2PGateway) handleIncomingSubmissions() {
	for {
		msg, err := g.submissionSub.Next(g.ctx)
		if err != nil {
			if g.ctx.Err() != nil {
				return
			}
			log.WithError(err).Error("Failed to get next submission message")
			continue
		}

		// Ignore our own messages
		if msg.ReceivedFrom == g.p2pHost.Host.ID() {
			continue
		}

		// Route to Redis for dequeuer processing
		if err := g.redisClient.LPush(g.ctx, "submissionQueue", msg.Data).Err(); err != nil {
			log.WithError(err).Error("Failed to push submission to Redis")
		} else {
			log.Debug("P2P Gateway: Routed submission to Redis queue")
		}
	}
}

func (g *P2PGateway) handleIncomingBatches() {
	for {
		msg, err := g.batchSub.Next(g.ctx)
		if err != nil {
			if g.ctx.Err() != nil {
				return
			}
			log.WithError(err).Error("Failed to get next batch message")
			continue
		}

		// Ignore our own messages
		if msg.ReceivedFrom == g.p2pHost.Host.ID() {
			continue
		}

		// Parse to get epoch ID
		var batchData map[string]interface{}
		if err := json.Unmarshal(msg.Data, &batchData); err != nil {
			log.WithError(err).Error("Failed to parse batch data")
			continue
		}

		epochID, ok := batchData["epochId"]
		if !ok {
			log.Error("Batch missing epochId")
			continue
		}

		// Route to Redis for aggregator processing
		key := fmt.Sprintf("incoming:batch:%v", epochID)
		if err := g.redisClient.Set(g.ctx, key, msg.Data, 30*time.Minute).Err(); err != nil {
			log.WithError(err).Error("Failed to store incoming batch")
		} else {
			// Also add to aggregation queue
			if err := g.redisClient.LPush(g.ctx, "aggregation:queue", epochID).Err(); err != nil {
				log.WithError(err).Error("Failed to add to aggregation queue")
			}
			log.WithField("epoch", epochID).Info("P2P Gateway: Received batch from network")
		}
	}
}

func (g *P2PGateway) handleValidatorPresence() {
	for {
		msg, err := g.presenceSub.Next(g.ctx)
		if err != nil {
			if g.ctx.Err() != nil {
				return
			}
			log.WithError(err).Error("Failed to get next presence message")
			continue
		}

		// Track active validators
		validatorID := peer.ID(msg.ReceivedFrom).String()
		key := fmt.Sprintf("validator:active:%s", validatorID)
		if err := g.redisClient.Set(g.ctx, key, time.Now().Unix(), 5*time.Minute).Err(); err != nil {
			log.WithError(err).Error("Failed to track validator presence")
		}
	}
}

func (g *P2PGateway) handleOutgoingMessages() {
	// Watch for messages to broadcast from other components
	for {
		select {
		case <-g.ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
			// Check for outgoing batch broadcasts
			result, err := g.redisClient.BRPop(g.ctx, time.Second, "outgoing:broadcast:batch").Result()
			if err != nil {
				if err != redis.Nil {
					log.WithError(err).Debug("No outgoing messages")
				}
				continue
			}

			if len(result) < 2 {
				continue
			}

			// Parse the message
			var msg map[string]interface{}
			if err := json.Unmarshal([]byte(result[1]), &msg); err != nil {
				log.WithError(err).Error("Failed to parse outgoing message")
				continue
			}

			// Determine topic based on message type
			msgType, _ := msg["type"].(string)
			var topic *pubsub.Topic

			switch msgType {
			case "batch":
				topic = g.batchTopic
			case "presence":
				topic = g.presenceTopic
			default:
				log.WithField("type", msgType).Error("Unknown message type")
				continue
			}

			// Broadcast the message
			data, _ := json.Marshal(msg["data"])
			if err := topic.Publish(g.ctx, data); err != nil {
				log.WithError(err).Error("Failed to broadcast message")
			} else {
				epochID := msg["epochId"]
				log.WithField("epoch", epochID).Info("P2P Gateway: Broadcast batch to network")
			}
		}
	}
}

func (g *P2PGateway) sendPresenceHeartbeat() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-g.ctx.Done():
			return
		case <-ticker.C:
			presence := map[string]interface{}{
				"peer_id":    g.p2pHost.Host.ID().String(),
				"timestamp":  time.Now().Unix(),
				"version":    "1.0.0",
			}

			data, _ := json.Marshal(presence)
			if err := g.presenceTopic.Publish(g.ctx, data); err != nil {
				log.WithError(err).Error("Failed to send presence heartbeat")
			}
		}
	}
}

func (g *P2PGateway) Start() error {
	log.Info("Starting P2P Gateway")

	// Start all handlers
	go g.handleIncomingSubmissions()
	go g.handleIncomingBatches()
	go g.handleValidatorPresence()
	go g.handleOutgoingMessages()
	go g.sendPresenceHeartbeat()

	// Log connection info
	addrs := g.p2pHost.Host.Addrs()
	for _, addr := range addrs {
		if !strings.Contains(addr.String(), "127.0.0.1") && !strings.Contains(addr.String(), "::1") {
			multiaddr := fmt.Sprintf("%s/p2p/%s", addr, g.p2pHost.Host.ID())
			log.WithField("multiaddr", multiaddr).Info("P2P Gateway listening")
		}
	}

	// Monitor connected peers
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-g.ctx.Done():
				return
			case <-ticker.C:
				peers := g.p2pHost.Host.Network().Peers()
				log.WithFields(logrus.Fields{
					"connected_peers": len(peers),
					"submission_peers": len(g.submissionTopic.ListPeers()),
					"batch_peers": len(g.batchTopic.ListPeers()),
				}).Info("P2P Gateway status")
			}
		}
	}()

	return nil
}

func (g *P2PGateway) Stop() {
	log.Info("Stopping P2P Gateway")
	g.cancel()
	if g.p2pHost != nil {
		g.p2pHost.Host.Close()
	}
	if g.redisClient != nil {
		g.redisClient.Close()
	}
}

func main() {
	// Setup logging
	log.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})

	if os.Getenv("DEBUG_MODE") == "true" {
		log.SetLevel(logrus.DebugLevel)
	}

	// Load configuration
	if err := config.LoadConfig(); err != nil {
		log.WithError(err).Fatal("Failed to load configuration")
	}
	cfg := config.SettingsObj

	// Create and start gateway
	gateway, err := NewP2PGateway(cfg)
	if err != nil {
		log.WithError(err).Fatal("Failed to create P2P Gateway")
	}

	if err := gateway.Start(); err != nil {
		log.WithError(err).Fatal("Failed to start P2P Gateway")
	}

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	gateway.Stop()
}