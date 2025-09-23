package p2p

import (
	"context"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/net/connmgr"
	"github.com/multiformats/go-multiaddr"
	"github.com/powerloom/snapshot-sequencer-validator/config"
	log "github.com/sirupsen/logrus"
)

type P2PHost struct {
	Host    host.Host
	Pubsub  *pubsub.PubSub
	DHT     *dht.IpfsDHT
	ctx     context.Context
}

func NewP2PHost(ctx context.Context, cfg *config.Settings) (*P2PHost, error) {
	// Create or load private key
	privKey, err := loadOrCreatePrivateKey(cfg.P2PPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get private key: %w", err)
	}

	p2pPort := strconv.Itoa(cfg.P2PPort)

	// Configure connection manager
	connMgr, err := connmgr.NewConnManager(
		cfg.ConnManagerLowWater,
		cfg.ConnManagerHighWater,
		connmgr.WithGracePeriod(time.Minute),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection manager: %w", err)
	}
	log.Infof("Connection manager configured: LowWater=%d, HighWater=%d", cfg.ConnManagerLowWater, cfg.ConnManagerHighWater)

	// Build libp2p options (simplified, matching working implementation)
	opts := []libp2p.Option{
		libp2p.Identity(privKey),
		libp2p.ListenAddrStrings(fmt.Sprintf("/ip4/0.0.0.0/tcp/%s", p2pPort)),
		libp2p.EnableNATService(),
		libp2p.ConnectionManager(connMgr),
	}

	// Add public IP if configured
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
	h, err := libp2p.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create host: %w", err)
	}

	log.Infof("P2P Host started with peer ID: %s", h.ID())

	// Setup DHT
	kademliaDHT, err := dht.New(ctx, h, dht.Mode(dht.ModeClient))
	if err != nil {
		return nil, fmt.Errorf("failed to create DHT: %w", err)
	}

	if err = kademliaDHT.Bootstrap(ctx); err != nil {
		return nil, fmt.Errorf("failed to bootstrap DHT: %w", err)
	}

	// Create pubsub
	ps, err := pubsub.NewGossipSub(
		ctx,
		h,
		pubsub.WithFloodPublish(true),
		pubsub.WithPeerExchange(true),
		pubsub.WithDirectPeers([]peer.AddrInfo{}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create pubsub: %w", err)
	}

	p2pHost := &P2PHost{
		Host:   h,
		Pubsub: ps,
		DHT:    kademliaDHT,
		ctx:    ctx,
	}

	// Connect to bootstrap if configured
	if len(cfg.BootstrapPeers) > 0 {
		if err := p2pHost.ConnectToBootstrap(cfg.BootstrapPeers[0]); err != nil {
			log.WithError(err).Warn("Failed to connect to bootstrap peer")
		}
	}

	return p2pHost, nil
}

func (p *P2PHost) ConnectToBootstrap(bootstrapAddr string) error {
	maddr, err := multiaddr.NewMultiaddr(bootstrapAddr)
	if err != nil {
		return fmt.Errorf("invalid bootstrap address: %w", err)
	}

	peerinfo, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		return fmt.Errorf("failed to parse bootstrap peer info: %w", err)
	}

	if err := p.Host.Connect(p.ctx, *peerinfo); err != nil {
		return fmt.Errorf("failed to connect to bootstrap: %w", err)
	}

	log.Infof("Connected to bootstrap peer: %s", peerinfo.ID)
	return nil
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