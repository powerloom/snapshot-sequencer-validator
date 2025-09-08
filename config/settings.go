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
	ProtocolStateContract    string           // Protocol state contract address (manages identities)
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
	ContractABIPath     string
	
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
	
	// Finalizer Configuration
	FinalizerWorkers      int
	FinalizationBatchSize int
	
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
		// Core Identity
		SequencerID: getEnv("SEQUENCER_ID", "unified-sequencer-1"),
		
		// Ethereum RPC Configuration
		ChainID: int64(getEnvAsInt("CHAIN_ID", 1)),
		
		// Redis Configuration - Read directly from env
		RedisHost:     getEnv("REDIS_HOST", "localhost"),
		RedisPort:     getEnv("REDIS_PORT", "6379"),
		RedisDB:       getEnvAsInt("REDIS_DB", 0),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),
		
		// P2P Network Configuration
		P2PPort:       getEnvAsInt("P2P_PORT", 9001),
		P2PPrivateKey: getEnv("PRIVATE_KEY", ""),
		P2PPublicIP:   getEnv("PUBLIC_IP", ""),
		Rendezvous:    getEnv("RENDEZVOUS_POINT", "powerloom-snapshot-sequencer-network"),
		
		// Connection Manager
		ConnManagerLowWater:  getEnvAsInt("CONN_MANAGER_LOW_WATER", 100),
		ConnManagerHighWater: getEnvAsInt("CONN_MANAGER_HIGH_WATER", 400),
		
		// Gossipsub Configuration
		GossipsubHeartbeat: time.Duration(getEnvAsInt("GOSSIPSUB_HEARTBEAT_MS", 700)) * time.Millisecond,
		GossipsubParams:    make(map[string]interface{}),
		
		// Submission Window Configuration
		SubmissionWindowDuration: time.Duration(getEnvAsInt("SUBMISSION_WINDOW_DURATION", 60)) * time.Second,
		MaxConcurrentWindows:     getEnvAsInt("MAX_CONCURRENT_WINDOWS", 100),
		WindowCleanupInterval:    5 * time.Minute,
		
		// Event Monitoring
		EventPollInterval:   time.Duration(getEnvAsInt("EVENT_POLL_INTERVAL", 12)) * time.Second,
		EventStartBlock:     uint64(getEnvAsInt("EVENT_START_BLOCK", 0)),
		EventBlockBatchSize: uint64(getEnvAsInt("EVENT_BLOCK_BATCH_SIZE", 1000)),
		BlockFetchTimeout:   time.Duration(getEnvAsInt("BLOCK_FETCH_TIMEOUT", 30)) * time.Second,
		ContractABIPath:     getEnv("CONTRACT_ABI_PATH", "./abi/ProtocolContract.json"),
		
		// Identity & Verification
		SkipIdentityVerification: getBoolEnv("SKIP_IDENTITY_VERIFICATION", false),
		FlaggedSnapshottersCheck: getBoolEnv("CHECK_FLAGGED_SNAPSHOTTERS", true),
		VerificationCacheTTL:     time.Duration(getEnvAsInt("VERIFICATION_CACHE_TTL", 600)) * time.Second,
		
		// Dequeuer Configuration
		DequeueWorkers:         getEnvAsInt("DEQUEUER_WORKERS", 5),
		DequeueBatchSize:       getEnvAsInt("DEQUEUE_BATCH_SIZE", 10),
		DequeueTimeout:         5 * time.Second,
		MaxSubmissionsPerEpoch: getEnvAsInt("MAX_SUBMISSIONS_PER_EPOCH", 100),
		
		// Deduplication Configuration
		DedupEnabled:        getBoolEnv("DEDUP_ENABLED", true),
		DedupLocalCacheSize: getEnvAsInt("DEDUP_LOCAL_CACHE_SIZE", 10000),
		DedupTTL:            time.Duration(getEnvAsInt("DEDUP_TTL_SECONDS", 7200)) * time.Second,
		
		// Component Toggles
		EnableListener:     getBoolEnv("ENABLE_LISTENER", true),
		EnableDequeuer:     getBoolEnv("ENABLE_DEQUEUER", true),
		EnableFinalizer:    getBoolEnv("ENABLE_FINALIZER", true),
		EnableConsensus:    getBoolEnv("ENABLE_CONSENSUS", true),
		EnableEventMonitor: getBoolEnv("ENABLE_EVENT_MONITOR", false),
		
		// Finalizer Configuration
		FinalizerWorkers:      getEnvAsInt("FINALIZER_WORKERS", 5),
		FinalizationBatchSize: getEnvAsInt("FINALIZATION_BATCH_SIZE", 20),
		
		// API Configuration
		APIHost:      getEnv("API_HOST", "0.0.0.0"),
		APIPort:      getEnvAsInt("API_PORT", 8080),
		APIAuthToken: getEnv("API_AUTH_TOKEN", ""),
		
		// Monitoring & Debugging
		SlackWebhookURL:        getEnv("SLACK_WEBHOOK_URL", ""),
		MetricsEnabled:         getBoolEnv("METRICS_ENABLED", false),
		MetricsPort:            getEnvAsInt("METRICS_PORT", 9090),
		LogLevel:               getEnv("LOG_LEVEL", "info"),
		DebugMode:              getBoolEnv("DEBUG_MODE", false),
		
		// Performance Tuning
		BatchProcessingTimeout: 5 * time.Minute,
		ContractQueryTimeout:   30 * time.Second,
		
		// Contract Addresses
		ProtocolStateContract:    getEnv("PROTOCOL_STATE_CONTRACT", ""),
	}
	
	// Load complex configurations that require additional parsing
	if err := loadRPCConfig(); err != nil {
		return fmt.Errorf("failed to load RPC config: %w", err)
	}
	
	if err := loadDataMarkets(); err != nil {
		return fmt.Errorf("failed to load data markets: %w", err)
	}
	
	loadBootstrapPeers()
	loadFullNodeAddresses()
	
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
		// Support comma-separated format (simplest)
		if !strings.HasPrefix(powerloomNodesStr, "[") {
			SettingsObj.RPCNodes = strings.Split(powerloomNodesStr, ",")
		} else {
			// JSON array format
			if err := json.Unmarshal([]byte(powerloomNodesStr), &SettingsObj.RPCNodes); err != nil {
				return fmt.Errorf("failed to parse POWERLOOM_RPC_NODES as JSON array: %w", err)
			}
		}
	}
	
	// Load Powerloom Archive nodes (optional)
	powerloomArchiveStr := getEnv("POWERLOOM_ARCHIVE_RPC_NODES", "")
	if powerloomArchiveStr != "" && powerloomArchiveStr != "[]" {
		if !strings.HasPrefix(powerloomArchiveStr, "[") {
			SettingsObj.ArchiveRPCNodes = strings.Split(powerloomArchiveStr, ",")
		} else {
			json.Unmarshal([]byte(powerloomArchiveStr), &SettingsObj.ArchiveRPCNodes)
		}
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
	
	if dataMarketsStr == "" {
		return nil
	}
	
	// Support comma-separated format (simplest and most reliable)
	// Example: 0x123...,0x456...,0x789...
	if !strings.HasPrefix(dataMarketsStr, "[") {
		SettingsObj.DataMarketAddresses = strings.Split(dataMarketsStr, ",")
	} else {
		// If it starts with [, require proper JSON with quoted strings
		// Example: ["0x123...","0x456..."]
		if err := json.Unmarshal([]byte(dataMarketsStr), &SettingsObj.DataMarketAddresses); err != nil {
			return fmt.Errorf("failed to parse DATA_MARKET_ADDRESSES as JSON array (addresses must be quoted): %w", err)
		}
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

