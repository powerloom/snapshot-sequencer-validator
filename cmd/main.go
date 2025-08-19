package main

import (
	"context"
	"decentralized-sequencer/config"
	"decentralized-sequencer/pkgs/batcher"
	"decentralized-sequencer/pkgs/clients"
	"decentralized-sequencer/pkgs/collector"
	"decentralized-sequencer/pkgs/ipfs"
	"decentralized-sequencer/pkgs/p2p"
	"decentralized-sequencer/pkgs/prost"
	"decentralized-sequencer/pkgs/redis"
	"decentralized-sequencer/pkgs/service"
	"decentralized-sequencer/pkgs/utils"
	"decentralized-sequencer/pkgs/watcher"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"
)

func main() {
	utils.InitLogger()
	config.LoadConfig()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clients.InitializeReportingClient(config.SettingsObj.SlackReportingUrl, 5*time.Second)
	clients.InitializeTxClient(config.SettingsObj.TxRelayerUrl, time.Duration(config.SettingsObj.HttpTimeout)*time.Second)

	redis.RedisClient = redis.NewRedisClient()

	ipfs.ConnectIPFSNode()

	prost.InitializeTimeouts()
	prost.InitializeSubmissionWindowProcessor()

	if err := prost.ConfigureClient(ctx); err != nil {
		log.Fatal(err)
	}
	defer func() {
		if prost.RPCHelper != nil {
			prost.RPCHelper.Close()
		}
	}()

	if err := prost.ConfigureContractInstance(ctx); err != nil {
		log.Fatal(err)
	}
	prost.ConfigureABI()

	if config.SettingsObj.DataMarketMigration.Enabled {
		for _, mapping := range config.SettingsObj.DataMarketMigration.Mappings {
			if err := prost.MigrateDataMarketState(ctx, mapping.OldMarketAddress, mapping.NewMarketAddress); err != nil {
				log.Fatal(err)
			}
		}
	}

	if err := prost.LoadContractStateVariables(ctx); err != nil {
		log.Fatal(err)
	}

	prost.LoadLuaScript()

	host, kademliaDHT, err := p2p.NewHost(ctx, config.SettingsObj.BootstrapPeers, config.SettingsObj.P2PPort)
	if err != nil {
		log.Fatalf("Failed to create P2P host: %v", err)
	}

	gossipManager, err := p2p.NewGossipsubManager(ctx, config.SettingsObj, redis.RedisClient, host, kademliaDHT)
	if err != nil {
		log.Fatalf("Failed to create gossipsub manager: %v", err)
	}

	watcherService, err := watcher.NewWatcher(*config.SettingsObj)
	if err != nil {
		log.Fatalf("Failed to create watcher: %v", err)
	}

	var wg sync.WaitGroup

	if config.SettingsObj.InitCleanupEnabled {
		for _, dataMarketAddress := range config.SettingsObj.DataMarketAddresses {
			wg.Add(1)
			go func(addr string) {
				defer wg.Done()
				if err := prost.CleanupSubmissionSet(ctx, addr); err != nil {
					log.Printf("Initial cleanup failed for %s: %v", addr, err)
				}
			}(dataMarketAddress)
		}
		for _, dataMarketAddress := range config.SettingsObj.DataMarketAddresses {
			wg.Add(1)
			go func(addr string) {
				defer wg.Done()
				if err := prost.CleanupSubmissionDumpForAllSlots(ctx, addr); err != nil {
					log.Printf("Cleanup failed for all slots: %v", err)
				}
			}(dataMarketAddress)
		}
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		gossipManager.Start(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		watcherService.Start(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		service.StartApiServer()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		collector.StartEventCollector(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		batcher.StartSubmissionProcessor(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		prost.StartPeriodicCleanupRoutine(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		clients.StartMemoryProfiling()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	<-sigCh
	log.Info("Shutting down decentralized sequencer...")

	cancel()
	wg.Wait()
}