package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	rpchelper "github.com/powerloom/go-rpc-helper"
	"github.com/powerloom/go-rpc-helper/reporting"
	log "github.com/sirupsen/logrus"
)

// Settings holds all configuration for the decentralized sequencer
type Settings struct {
	// Core Identity
	SequencerID string
	
	// Ethereum RPC Configuration
	RPCNodes                 []string         // Primary RPC nodes for load balancing
	ArchiveRPCNodes          []string         // Archive nodes for historical queries
	ProtocolStateContract    string           // Protocol state contract address
	IdentityRegistryContract string           // Future: LibP2P identity registry contract
	ChainID                  int64
	
	// Data Market Configuration
	DataMarketAddresses      []string         // String addresses
	DataMarketContracts      []common.Address // Parsed common.Address types
	// DATA_SOURCES removed - project IDs generated directly from contract addresses
	
	// Redis Configuration
	RedisHost     string
	RedisPort     string
	RedisDB       int
	RedisPassword string
	
	// P2P Network Configuration
	P2PPort           int
	P2PPrivateKey     string   // Hex-encoded private key
	P2PPublicIP       string   // Public IP for NAT traversal
	BootstrapPeers    []string // Bootstrap peer multiaddrs
	Rendezvous        string   // Rendezvous point for discovery
	
	// Connection Manager
	ConnManagerLowWater  int
	ConnManagerHighWater int
	
	// Gossipsub Configuration
	GossipsubHeartbeat   time.Duration
	GossipsubParams      map[string]interface{} // Additional gossipsub parameters
	
	// Submission Window Configuration
	SubmissionWindowDuration time.Duration
	MaxConcurrentWindows     int
	WindowCleanupInterval    time.Duration
	
	// Event Monitoring
	EventPollInterval   time.Duration
	EventStartBlock     uint64
	EventBlockBatchSize uint64
	BlockFetchTimeout   time.Duration
	
	// Identity & Verification  
	// Snapshotter identities managed by protocol state contract only
	SkipIdentityVerification bool     // Skip verification for testing
	FullNodeAddresses        []string // Addresses that are full nodes
	FlaggedSnapshottersCheck bool
	VerificationCacheTTL     time.Duration
	
	// Dequeuer Configuration
	DequeueWorkers         int
	DequeueBatchSize       int
	DequeueTimeout         time.Duration
	MaxSubmissionsPerEpoch int
	
	// Deduplication Configuration
	DedupEnabled        bool
	DedupLocalCacheSize int
	DedupTTL            time.Duration
	
	// Component Toggles
	EnableListener     bool
	EnableDequeuer     bool
	EnableFinalizer    bool
	EnableConsensus    bool
	EnableEventMonitor bool
	
	// API Configuration
	APIHost      string
	APIPort      int
	APIAuthToken string
	
	// Monitoring & Debugging
	SlackWebhookURL string
	MetricsEnabled  bool
	MetricsPort     int
	LogLevel        string
	DebugMode       bool
	
	// Performance Tuning
	BatchProcessingTimeout time.Duration
	ContractQueryTimeout   time.Duration
}

var (
	// SettingsObj is the global settings instance
	SettingsObj *Settings
)

// LoadConfig loads configuration from environment variables
func LoadConfig() error {
	SettingsObj = &Settings{
		// Set defaults
		ChainID:                  1, // Mainnet
		P2PPort:                  9001,
		RedisHost:                "localhost",
		RedisPort:                "6379",
		RedisDB:                  0,
		Rendezvous:               "powerloom-snapshot-sequencer-network",
		ConnManagerLowWater:      100,
		ConnManagerHighWater:     400,
		GossipsubHeartbeat:       700 * time.Millisecond,
		SubmissionWindowDuration: 60 * time.Second,
		MaxConcurrentWindows:     100,
		WindowCleanupInterval:    5 * time.Minute,
		EventPollInterval:        12 * time.Second,
		EventBlockBatchSize:      1000,
		BlockFetchTimeout:        30 * time.Second,
		VerificationCacheTTL:     10 * time.Minute,
		DequeueWorkers:           5,
		DequeueBatchSize:         10,
		DequeueTimeout:           5 * time.Second,
		MaxSubmissionsPerEpoch:   100,
		DedupLocalCacheSize:      10000,
		DedupTTL:                 2 * time.Hour,
		DedupEnabled:             true,
		EnableListener:           true,
		EnableDequeuer:           true,
		EnableFinalizer:          true,
		EnableConsensus:          true,
		EnableEventMonitor:       false, // Off by default until contracts configured
		APIPort:                  8080,
		MetricsPort:              9090,
		LogLevel:                 "info",
		BatchProcessingTimeout:   5 * time.Minute,
		ContractQueryTimeout:     30 * time.Second,
		SkipIdentityVerification: false,
		FlaggedSnapshottersCheck: true,
		GossipsubParams:          make(map[string]interface{}),
	}
	
	// Core Identity
	SettingsObj.SequencerID = getEnv("SEQUENCER_ID", "unified-sequencer-1")
	
	// Load RPC Configuration
	if err := loadRPCConfig(); err != nil {
		return fmt.Errorf("failed to load RPC config: %w", err)
	}
	
	// Load Contract Addresses
	SettingsObj.ProtocolStateContract = getEnv("PROTOCOL_STATE_CONTRACT", "")
	SettingsObj.IdentityRegistryContract = getEnv("IDENTITY_REGISTRY_CONTRACT", "")
	
	// Load Data Markets
	if err := loadDataMarkets(); err != nil {
		return fmt.Errorf("failed to load data markets: %w", err)
	}
	
	// Load Redis Configuration
	SettingsObj.RedisHost = getEnv("REDIS_HOST", SettingsObj.RedisHost)
	SettingsObj.RedisPort = getEnv("REDIS_PORT", SettingsObj.RedisPort)
	SettingsObj.RedisDB = getEnvAsInt("REDIS_DB", SettingsObj.RedisDB)
	SettingsObj.RedisPassword = getEnv("REDIS_PASSWORD", "")
	
	// Load P2P Configuration
	SettingsObj.P2PPort = getEnvAsInt("P2P_PORT", SettingsObj.P2PPort)
	SettingsObj.P2PPrivateKey = getEnv("PRIVATE_KEY", "")
	SettingsObj.P2PPublicIP = getEnv("PUBLIC_IP", "")
	SettingsObj.Rendezvous = getEnv("RENDEZVOUS_POINT", SettingsObj.Rendezvous)
	loadBootstrapPeers()
	
	// Connection Manager
	SettingsObj.ConnManagerLowWater = getEnvAsInt("CONN_MANAGER_LOW_WATER", SettingsObj.ConnManagerLowWater)
	SettingsObj.ConnManagerHighWater = getEnvAsInt("CONN_MANAGER_HIGH_WATER", SettingsObj.ConnManagerHighWater)
	
	// Gossipsub
	heartbeatMs := getEnvAsInt("GOSSIPSUB_HEARTBEAT_MS", 700)
	SettingsObj.GossipsubHeartbeat = time.Duration(heartbeatMs) * time.Millisecond
	
	// Submission Window
	windowSeconds := getEnvAsInt("SUBMISSION_WINDOW_DURATION", 60)
	SettingsObj.SubmissionWindowDuration = time.Duration(windowSeconds) * time.Second
	SettingsObj.MaxConcurrentWindows = getEnvAsInt("MAX_CONCURRENT_WINDOWS", SettingsObj.MaxConcurrentWindows)
	
	// Event Monitoring
	SettingsObj.EventPollInterval = time.Duration(getEnvAsInt("EVENT_POLL_INTERVAL", 12)) * time.Second
	SettingsObj.EventStartBlock = uint64(getEnvAsInt("EVENT_START_BLOCK", 0))
	SettingsObj.EventBlockBatchSize = uint64(getEnvAsInt("EVENT_BLOCK_BATCH_SIZE", 1000))
	SettingsObj.BlockFetchTimeout = time.Duration(getEnvAsInt("BLOCK_FETCH_TIMEOUT", 30)) * time.Second
	
	// Identity & Verification
	loadFullNodeAddresses()
	SettingsObj.SkipIdentityVerification = getBoolEnv("SKIP_IDENTITY_VERIFICATION", SettingsObj.SkipIdentityVerification)
	SettingsObj.FlaggedSnapshottersCheck = getBoolEnv("CHECK_FLAGGED_SNAPSHOTTERS", SettingsObj.FlaggedSnapshottersCheck)
	cacheTTLSeconds := getEnvAsInt("VERIFICATION_CACHE_TTL", 600)
	SettingsObj.VerificationCacheTTL = time.Duration(cacheTTLSeconds) * time.Second
	
	// Dequeuer
	SettingsObj.DequeueWorkers = getEnvAsInt("DEQUEUER_WORKERS", SettingsObj.DequeueWorkers)
	SettingsObj.DequeueBatchSize = getEnvAsInt("DEQUEUE_BATCH_SIZE", SettingsObj.DequeueBatchSize)
	SettingsObj.MaxSubmissionsPerEpoch = getEnvAsInt("MAX_SUBMISSIONS_PER_EPOCH", SettingsObj.MaxSubmissionsPerEpoch)
	
	// Deduplication
	SettingsObj.DedupEnabled = getBoolEnv("DEDUP_ENABLED", SettingsObj.DedupEnabled)
	SettingsObj.DedupLocalCacheSize = getEnvAsInt("DEDUP_LOCAL_CACHE_SIZE", SettingsObj.DedupLocalCacheSize)
	dedupTTLSeconds := getEnvAsInt("DEDUP_TTL_SECONDS", 7200)
	SettingsObj.DedupTTL = time.Duration(dedupTTLSeconds) * time.Second
	
	// Component Toggles
	SettingsObj.EnableListener = getBoolEnv("ENABLE_LISTENER", SettingsObj.EnableListener)
	SettingsObj.EnableDequeuer = getBoolEnv("ENABLE_DEQUEUER", SettingsObj.EnableDequeuer)
	SettingsObj.EnableFinalizer = getBoolEnv("ENABLE_FINALIZER", SettingsObj.EnableFinalizer)
	SettingsObj.EnableConsensus = getBoolEnv("ENABLE_CONSENSUS", SettingsObj.EnableConsensus)
	SettingsObj.EnableEventMonitor = getBoolEnv("ENABLE_EVENT_MONITOR", SettingsObj.EnableEventMonitor)
	
	// API Configuration
	SettingsObj.APIHost = getEnv("API_HOST", "0.0.0.0")
	SettingsObj.APIPort = getEnvAsInt("API_PORT", SettingsObj.APIPort)
	SettingsObj.APIAuthToken = getEnv("API_AUTH_TOKEN", "")
	
	// Monitoring
	SettingsObj.SlackWebhookURL = getEnv("SLACK_WEBHOOK_URL", "")
	SettingsObj.MetricsEnabled = getBoolEnv("METRICS_ENABLED", false)
	SettingsObj.MetricsPort = getEnvAsInt("METRICS_PORT", SettingsObj.MetricsPort)
	SettingsObj.LogLevel = getEnv("LOG_LEVEL", SettingsObj.LogLevel)
	SettingsObj.DebugMode = getBoolEnv("DEBUG_MODE", false)
	
	// DATA_SOURCES removed - project IDs generated directly from contract addresses
	
	// Configure logging
	configureLogging()
	
	// Validate configuration
	if err := validateConfig(); err != nil {
		return fmt.Errorf("configuration validation failed: %w", err)
	}
	
	// Log configuration summary
	logConfigSummary()
	
	return nil
}

// loadRPCConfig loads RPC node configuration
func loadRPCConfig() error {
	// Load Powerloom Protocol Chain RPC nodes (all interactions in this component)
	powerloomNodesStr := getEnv("POWERLOOM_RPC_NODES", "")
	if powerloomNodesStr != "" {
		if err := json.Unmarshal([]byte(powerloomNodesStr), &SettingsObj.RPCNodes); err != nil {
			return fmt.Errorf("failed to parse POWERLOOM_RPC_NODES JSON: %w", err)
		}
	}
	
	// Load Powerloom Archive nodes (optional)
	powerloomArchiveStr := getEnv("POWERLOOM_ARCHIVE_RPC_NODES", "")
	if powerloomArchiveStr != "" {
		json.Unmarshal([]byte(powerloomArchiveStr), &SettingsObj.ArchiveRPCNodes)
	}
	
	// Clean quotes from URLs
	for i := range SettingsObj.RPCNodes {
		SettingsObj.RPCNodes[i] = strings.Trim(SettingsObj.RPCNodes[i], "\" ")
	}
	for i := range SettingsObj.ArchiveRPCNodes {
		SettingsObj.ArchiveRPCNodes[i] = strings.Trim(SettingsObj.ArchiveRPCNodes[i], "\" ")
	}
	
	// Set chain ID
	SettingsObj.ChainID = int64(getEnvAsInt("CHAIN_ID", 1))
	
	return nil
}

// loadDataMarkets loads data market addresses
func loadDataMarkets() error {
	dataMarketsStr := getEnv("DATA_MARKET_ADDRESSES", "")
	
	// Try JSON array format
	if strings.HasPrefix(dataMarketsStr, "[") {
		if err := json.Unmarshal([]byte(dataMarketsStr), &SettingsObj.DataMarketAddresses); err != nil {
			return fmt.Errorf("failed to parse DATA_MARKET_ADDRESSES: %w", err)
		}
	} else if dataMarketsStr != "" {
		// Try comma-separated
		SettingsObj.DataMarketAddresses = strings.Split(dataMarketsStr, ",")
	}
	
	// Clean and convert to common.Address
	SettingsObj.DataMarketContracts = make([]common.Address, 0, len(SettingsObj.DataMarketAddresses))
	for _, addr := range SettingsObj.DataMarketAddresses {
		addr = strings.TrimSpace(strings.Trim(addr, "\""))
		if addr != "" && addr != "0x0" {
			SettingsObj.DataMarketContracts = append(SettingsObj.DataMarketContracts, common.HexToAddress(addr))
		}
	}
	
	return nil
}

// loadBootstrapPeers loads bootstrap peer addresses
func loadBootstrapPeers() {
	// Try BOOTSTRAP_PEERS first (can be JSON array or comma-separated)
	peersStr := getEnv("BOOTSTRAP_PEERS", "")
	if peersStr != "" {
		if strings.HasPrefix(peersStr, "[") {
			json.Unmarshal([]byte(peersStr), &SettingsObj.BootstrapPeers)
		} else {
			SettingsObj.BootstrapPeers = strings.Split(peersStr, ",")
		}
	} else {
		// Fallback to single BOOTSTRAP_MULTIADDR (backward compatibility)
		singlePeer := getEnv("BOOTSTRAP_MULTIADDR", "")
		if singlePeer != "" {
			SettingsObj.BootstrapPeers = []string{singlePeer}
		}
	}
	
	// Clean the peer addresses
	for i := range SettingsObj.BootstrapPeers {
		SettingsObj.BootstrapPeers[i] = strings.TrimSpace(strings.Trim(SettingsObj.BootstrapPeers[i], "\""))
	}
}

// loadFullNodeAddresses loads full node addresses
func loadFullNodeAddresses() {
	fullNodesStr := getEnv("FULL_NODE_ADDRESSES", "")
	
	if strings.HasPrefix(fullNodesStr, "[") {
		json.Unmarshal([]byte(fullNodesStr), &SettingsObj.FullNodeAddresses)
	} else if fullNodesStr != "" {
		SettingsObj.FullNodeAddresses = strings.Split(fullNodesStr, ",")
	}
	
	// Clean and lowercase
	for i := range SettingsObj.FullNodeAddresses {
		SettingsObj.FullNodeAddresses[i] = strings.ToLower(strings.TrimSpace(SettingsObj.FullNodeAddresses[i]))
	}
}

// loadDataSources loads data sources per market
func loadDataSources() error {
	// DATA_SOURCES removed - project IDs generated directly from contract addresses
	
	return nil
}

// configureLogging sets up the logger based on configuration
func configureLogging() {
	// Set log level
	switch strings.ToLower(SettingsObj.LogLevel) {
	case "debug":
		log.SetLevel(log.DebugLevel)
	case "info":
		log.SetLevel(log.InfoLevel)
	case "warn", "warning":
		log.SetLevel(log.WarnLevel)
	case "error":
		log.SetLevel(log.ErrorLevel)
	default:
		log.SetLevel(log.InfoLevel)
	}
	
	// Override with debug mode
	if SettingsObj.DebugMode {
		log.SetLevel(log.DebugLevel)
	}
	
	// Set formatter
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
		ForceColors:   true,
	})
}

// validateConfig validates the loaded configuration
func validateConfig() error {
	// Check required fields for enabled components
	if SettingsObj.EnableListener || SettingsObj.EnableConsensus {
		if len(SettingsObj.BootstrapPeers) == 0 {
			log.Warn("No bootstrap peers configured - P2P networking may not work")
		}
	}
	
	if SettingsObj.EnableEventMonitor {
		if SettingsObj.ProtocolStateContract == "" {
			return fmt.Errorf("PROTOCOL_STATE_CONTRACT required when event monitor is enabled")
		}
		if len(SettingsObj.RPCNodes) == 0 {
			return fmt.Errorf("POWERLOOM_RPC_NODES required when event monitor is enabled")
		}
		if len(SettingsObj.DataMarketAddresses) == 0 {
			log.Warn("No data markets configured - event monitor will not track any markets")
		}
	}
	
	if SettingsObj.EnableDequeuer {
		if SettingsObj.RedisHost == "" {
			return fmt.Errorf("Redis configuration required when dequeuer is enabled")
		}
	}
	
	return nil
}

// logConfigSummary logs a summary of the configuration
func logConfigSummary() {
	log.Info("=== Configuration Loaded ===")
	log.Infof("Sequencer ID: %s", SettingsObj.SequencerID)
	log.Infof("Components: Listener=%v, Dequeuer=%v, Finalizer=%v, Consensus=%v, EventMonitor=%v",
		SettingsObj.EnableListener, SettingsObj.EnableDequeuer, SettingsObj.EnableFinalizer,
		SettingsObj.EnableConsensus, SettingsObj.EnableEventMonitor)
	
	if len(SettingsObj.RPCNodes) > 0 {
		log.Infof("RPC Nodes: %d configured", len(SettingsObj.RPCNodes))
	}
	
	if SettingsObj.ProtocolStateContract != "" {
		log.Infof("Protocol State Contract: %s", SettingsObj.ProtocolStateContract)
	}
	
	if len(SettingsObj.DataMarketContracts) > 0 {
		log.Infof("Data Markets: %d configured", len(SettingsObj.DataMarketContracts))
	}
	
	log.Infof("Redis: %s:%s (DB %d)", SettingsObj.RedisHost, SettingsObj.RedisPort, SettingsObj.RedisDB)
	log.Infof("P2P: Port %d, Bootstrap peers: %d", SettingsObj.P2PPort, len(SettingsObj.BootstrapPeers))
	
	if SettingsObj.DedupEnabled {
		log.Infof("Deduplication: Enabled (TTL: %v, Cache: %d)", SettingsObj.DedupTTL, SettingsObj.DedupLocalCacheSize)
	}
	
	log.Info("============================")
}

// Helper functions

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvAsInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

func getBoolEnv(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		value = strings.ToLower(value)
		return value == "true" || value == "1" || value == "yes"
	}
	return defaultValue
}

// IsFullNode checks if an address is configured as a full node
func IsFullNode(address string) bool {
	address = strings.ToLower(address)
	for _, fullNode := range SettingsObj.FullNodeAddresses {
		if strings.ToLower(fullNode) == address {
			return true
		}
	}
	return false
}

// IsValidDataMarket checks if an address is a configured data market
func IsValidDataMarket(address string) bool {
	address = strings.ToLower(address)
	for _, market := range SettingsObj.DataMarketAddresses {
		if strings.ToLower(market) == address {
			return true
		}
	}
	return false
}

// ToRPCConfig converts Settings to go-rpc-helper RPCConfig
func (s *Settings) ToRPCConfig() *rpchelper.RPCConfig {
	config := &rpchelper.RPCConfig{
		Nodes: func() []rpchelper.NodeConfig {
			var nodes []rpchelper.NodeConfig
			for _, url := range s.RPCNodes {
				nodes = append(nodes, rpchelper.NodeConfig{URL: url})
			}
			return nodes
		}(),
		ArchiveNodes: func() []rpchelper.NodeConfig {
			var nodes []rpchelper.NodeConfig
			for _, url := range s.ArchiveRPCNodes {
				nodes = append(nodes, rpchelper.NodeConfig{URL: url})
			}
			return nodes
		}(),
		MaxRetries:     3,
		RetryDelay:     500 * time.Millisecond,
		MaxRetryDelay:  30 * time.Second,
		RequestTimeout: 30 * time.Second,
	}
	
	// Configure webhook if SlackWebhookURL is provided
	if s.SlackWebhookURL != "" {
		config.WebhookConfig = &reporting.WebhookConfig{
			URL:     s.SlackWebhookURL,
			Timeout: 30 * time.Second,
			Retries: 3,
		}
	}
	
	return config
}

