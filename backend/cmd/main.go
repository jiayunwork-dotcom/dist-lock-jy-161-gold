package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/distributed-lock/backend/internal/api"
	"github.com/distributed-lock/backend/internal/config"
	"github.com/distributed-lock/backend/internal/deadlock"
	"github.com/distributed-lock/backend/internal/lock"
	raftpkg "github.com/distributed-lock/backend/internal/raft"
	"github.com/distributed-lock/backend/internal/ratelimit"
	"github.com/distributed-lock/backend/internal/metrics"
	"go.uber.org/zap"
)

func main() {
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	cfg, err := config.Load()
	if err != nil {
		logger.Fatal("Failed to load configuration", zap.Error(err))
	}

	metricsCollector := metrics.NewPrometheusCollector()
	tokenGen := lock.NewSimpleTokenGenerator()

	lockMgr := lock.NewLockManager(cfg.LockConfig, logger, tokenGen, metricsCollector)
	rateLimitMgr := ratelimit.NewRateLimitManager(metricsCollector)

	nodeConfig := raftpkg.NodeConfig{
		NodeID:    cfg.NodeID,
		BindAddr:  cfg.RaftAddr,
		DataDir:   cfg.DataDir,
		Peers:     cfg.Peers,
		Bootstrap: cfg.Bootstrap,
	}

	raftMgr, err := raftpkg.NewRaftManager(nodeConfig, cfg.LockConfig, logger, lockMgr, rateLimitMgr, metricsCollector)
	if err != nil {
		logger.Fatal("Failed to create Raft manager", zap.Error(err))
	}
	defer raftMgr.Shutdown()

	deadlockDetector := deadlock.NewDetector(lockMgr, cfg.LockConfig, logger, metricsCollector)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go deadlockDetector.Start(ctx)

	server := api.NewServer(lockMgr, raftMgr, rateLimitMgr, metricsCollector, cfg, logger)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		logger.Info("Shutdown signal received")
		cancel()
	}()

	if err := server.Start(ctx, cfg.HTTPAddr); err != nil && err != context.Canceled {
		logger.Fatal("Server failed", zap.Error(err))
	}

	logger.Info("Server shutdown complete")
}
