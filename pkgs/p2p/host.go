package p2p

import (
	"context"
	"encoding/hex"
	"fmt"
	"strconv"

	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
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

	// Build libp2p options
	opts := []libp2p.Option{
		libp2p.Identity(privKey),
		libp2p.ListenAddrStrings(
			fmt.Sprintf("/ip4/0.0.0.0/tcp/%s", p2pPort),
			fmt.Sprintf("/ip6/::/tcp/%s", p2pPort),
		),
		libp2p.DefaultMuxers,
		libp2p.DefaultTransports,
		libp2p.DefaultSecurity,
		libp2p.Routing(func(h host.Host) (routing.PeerRouting, error) {
			return dht.New(ctx, h, dht.Mode(dht.ModeClient))
		}),
		libp2p.NATPortMap(),
		libp2p.EnableAutoRelay(),
		libp2p.EnableNATService(),
	}

	// Add public IP if configured
	if cfg.P2PPublicIP != "" {
		publicAddr, err := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/%s/tcp/%s", cfg.P2PPublicIP, p2pPort))
		if err == nil {
			opts = append(opts, libp2p.AddrsFactory(func(addrs []multiaddr.Multiaddr) []multiaddr.Multiaddr {
				return append(addrs, publicAddr)
			}))
			log.Infof("Advertising public IP: %s", cfg.P2PPublicIP)
		}
	}

	// Create libp2p host
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