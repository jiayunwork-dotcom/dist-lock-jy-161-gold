package raft

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/distributed-lock/backend/internal/lock"
	"github.com/distributed-lock/backend/internal/metrics"
	"github.com/distributed-lock/backend/internal/ratelimit"
	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
	"go.uber.org/zap"
)

type RaftCommandType string

const (
	CmdAcquire         RaftCommandType = "acquire"
	CmdRelease         RaftCommandType = "release"
	CmdHeartbeat       RaftCommandType = "heartbeat"
	CmdAdjustLease     RaftCommandType = "adjust_lease"
	CmdAdjustCapacity  RaftCommandType = "adjust_capacity"
	CmdClearQueue      RaftCommandType = "clear_queue"
	CmdRegisterDep     RaftCommandType = "register_dependency"
	CmdRemoveDep       RaftCommandType = "remove_dependency"
	CmdCreateGroup     RaftCommandType = "create_group"
	CmdDeleteGroup     RaftCommandType = "delete_group"
	CmdAddToGroup      RaftCommandType = "add_to_group"
	CmdRemoveFromGroup RaftCommandType = "remove_from_group"
	CmdBatchAcquire    RaftCommandType = "batch_acquire"
	CmdBatchRelease    RaftCommandType = "batch_release"
	CmdCascadeRelease  RaftCommandType = "cascade_release"

	CmdRateLimitCreateRule   RaftCommandType = "ratelimit_create_rule"
	CmdRateLimitUpdateRule   RaftCommandType = "ratelimit_update_rule"
	CmdRateLimitDeleteRule   RaftCommandType = "ratelimit_delete_rule"
	CmdRateLimitCheck        RaftCommandType = "ratelimit_check"
	CmdRateLimitAdjustQuota  RaftCommandType = "ratelimit_adjust_quota"
	CmdRateLimitBorrowTokens RaftCommandType = "ratelimit_borrow_tokens"
	CmdRateLimitRepayTokens  RaftCommandType = "ratelimit_repay_tokens"
	CmdRateLimitReportLatency RaftCommandType = "ratelimit_report_latency"
)

type RaftCommand struct {
	Type    RaftCommandType     `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type RaftManager struct {
	raft       *raft.Raft
	raftStore  *raftboltdb.BoltStore
	config     lock.Config
	logger     *zap.Logger
	lockMgr    *lock.LockManager
	rateLimitMgr *ratelimit.RateLimitManager
	metrics    *metrics.PrometheusCollector
	nodeID     string
	raftDir    string
	leaderAddr string
	mu         sync.RWMutex
	notifyCh   chan bool
}

type NodeConfig struct {
	NodeID   string
	BindAddr string
	DataDir  string
	Peers    map[string]string
	Bootstrap bool
}

type LockStateSnapshot struct {
	Locks          map[string]*lock.Lock                     `json:"locks"`
	Dependencies  map[string]map[string]bool                `json:"dependencies"`
	Groups        map[string]*lock.LockGroup                `json:"groups"`
	RateLimitSnapshot *ratelimit.RuleSnapshot               `json:"rate_limit_snapshot,omitempty"`
}

type fsm struct {
	lockMgr     *lock.LockManager
	rateLimitMgr *ratelimit.RateLimitManager
	logger      *zap.Logger
}

func (f *fsm) Apply(log *raft.Log) interface{} {
	var cmd RaftCommand
	if err := json.Unmarshal(log.Data, &cmd); err != nil {
		f.logger.Error("Failed to unmarshal raft command", zap.Error(err))
		return err
	}

	ctx := context.Background()
	switch cmd.Type {
	case CmdAcquire:
		var req lock.AcquireRequest
		if err := json.Unmarshal(cmd.Payload, &req); err != nil {
			return err
		}
		resp, err := f.lockMgr.Acquire(ctx, &req)
		return map[string]interface{}{"resp": resp, "err": err}

	case CmdRelease:
		var req lock.ReleaseRequest
		if err := json.Unmarshal(cmd.Payload, &req); err != nil {
			return err
		}
		resp, err := f.lockMgr.Release(ctx, &req)
		return map[string]interface{}{"resp": resp, "err": err}

	case CmdHeartbeat:
		var req lock.HeartbeatRequest
		if err := json.Unmarshal(cmd.Payload, &req); err != nil {
			return err
		}
		resp, err := f.lockMgr.Heartbeat(ctx, &req)
		return map[string]interface{}{"resp": resp, "err": err}

	case CmdAdjustLease:
		var payload struct {
			LockID   lock.LockIdentifier `json:"lock_id"`
			LeaseTime time.Duration    `json:"lease_time"`
		}
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			return err
		}
		err := f.lockMgr.AdjustLeaseTime(payload.LockID, payload.LeaseTime)
		return map[string]interface{}{"err": err}

	case CmdAdjustCapacity:
		var payload struct {
			LockID      lock.LockIdentifier `json:"lock_id"`
			NewCapacity int              `json:"new_capacity"`
		}
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			return err
		}
		err := f.lockMgr.AdjustSemaphoreCapacity(payload.LockID, payload.NewCapacity)
		return map[string]interface{}{"err": err}

	case CmdClearQueue:
		var payload struct {
			LockID lock.LockIdentifier `json:"lock_id"`
		}
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			return err
		}
		err := f.lockMgr.ClearWaitQueue(payload.LockID)
		return map[string]interface{}{"err": err}

	case CmdRegisterDep:
		var req lock.RegisterDependencyRequest
		if err := json.Unmarshal(cmd.Payload, &req); err != nil {
			return err
		}
		err := f.lockMgr.RegisterDependency(req.ParentLock, req.ChildLock)
		return map[string]interface{}{"err": err}

	case CmdRemoveDep:
		var req lock.RemoveDependencyRequest
		if err := json.Unmarshal(cmd.Payload, &req); err != nil {
			return err
		}
		err := f.lockMgr.RemoveDependency(req.ParentLock, req.ChildLock)
		return map[string]interface{}{"err": err}

	case CmdCreateGroup:
		var req lock.CreateGroupRequest
		if err := json.Unmarshal(cmd.Payload, &req); err != nil {
			return err
		}
		err := f.lockMgr.CreateGroup(&req)
		return map[string]interface{}{"err": err}

	case CmdDeleteGroup:
		var payload struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			return err
		}
		err := f.lockMgr.DeleteGroup(payload.Name)
		return map[string]interface{}{"err": err}

	case CmdAddToGroup:
		var req lock.AddLockToGroupRequest
		if err := json.Unmarshal(cmd.Payload, &req); err != nil {
			return err
		}
		err := f.lockMgr.AddLockToGroup(req.GroupName, req.LockID)
		return map[string]interface{}{"err": err}

	case CmdRemoveFromGroup:
		var req lock.RemoveLockFromGroupRequest
		if err := json.Unmarshal(cmd.Payload, &req); err != nil {
			return err
		}
		err := f.lockMgr.RemoveLockFromGroup(req.GroupName, req.LockID)
		return map[string]interface{}{"err": err}

	case CmdBatchAcquire:
		var req lock.BatchAcquireRequest
		if err := json.Unmarshal(cmd.Payload, &req); err != nil {
			return err
		}
		resp, err := f.lockMgr.BatchAcquire(ctx, &req)
		return map[string]interface{}{"resp": resp, "err": err}

	case CmdBatchRelease:
		var req lock.BatchReleaseRequest
		if err := json.Unmarshal(cmd.Payload, &req); err != nil {
			return err
		}
		resp, err := f.lockMgr.BatchRelease(ctx, &req)
		return map[string]interface{}{"resp": resp, "err": err}

	case CmdCascadeRelease:
		var req lock.CascadeReleaseInfo
		if err := json.Unmarshal(cmd.Payload, &req); err != nil {
			return err
		}
		f.lockMgr.CascadeRelease(req.RootLock, req.ClientID)
		return map[string]interface{}{"err": nil}

	case CmdRateLimitCreateRule:
		var req ratelimit.CreateRuleRequest
		if err := json.Unmarshal(cmd.Payload, &req); err != nil {
			return err
		}
		err := f.rateLimitMgr.CreateRule(&req)
		return map[string]interface{}{"err": err}

	case CmdRateLimitUpdateRule:
		var payload struct {
			Key string                    `json:"key"`
			Req ratelimit.UpdateRuleRequest `json:"req"`
		}
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			return err
		}
		err := f.rateLimitMgr.UpdateRule(payload.Key, &payload.Req)
		return map[string]interface{}{"err": err}

	case CmdRateLimitDeleteRule:
		var payload struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			return err
		}
		err := f.rateLimitMgr.DeleteRule(payload.Key)
		return map[string]interface{}{"err": err}

	case CmdRateLimitCheck:
		var req ratelimit.CheckRequest
		if err := json.Unmarshal(cmd.Payload, &req); err != nil {
			return err
		}
		result := f.rateLimitMgr.Check(&req)
		return map[string]interface{}{"result": result}

	case CmdRateLimitAdjustQuota:
		var req ratelimit.AdjustQuotaRequest
		if err := json.Unmarshal(cmd.Payload, &req); err != nil {
			return err
		}
		err := f.rateLimitMgr.AdjustQuota(&req)
		return map[string]interface{}{"err": err}

	case CmdRateLimitReportLatency:
		var req ratelimit.ReportLatencyRequest
		if err := json.Unmarshal(cmd.Payload, &req); err != nil {
			return err
		}
		err := f.rateLimitMgr.ReportLatency(&req)
		return map[string]interface{}{"err": err}

	case CmdRateLimitBorrowTokens:
		var req ratelimit.BorrowTokensRequest
		if err := json.Unmarshal(cmd.Payload, &req); err != nil {
			return err
		}
		amount, err := f.rateLimitMgr.BorrowTokens(&req)
		return map[string]interface{}{"amount": amount, "err": err}

	case CmdRateLimitRepayTokens:
		var req ratelimit.RepayTokensRequest
		if err := json.Unmarshal(cmd.Payload, &req); err != nil {
			return err
		}
		amount, err := f.rateLimitMgr.RepayTokens(&req)
		return map[string]interface{}{"amount": amount, "err": err}
	}

	return nil
}

func (f *fsm) Snapshot() (raft.FSMSnapshot, error) {
	allLocks := f.lockMgr.GetAllLocks()
	deps := f.lockMgr.GetAllDependencies()
	groups := f.lockMgr.GetAllGroups()
	snapshot := &LockStateSnapshot{
		Locks:          allLocks,
		Dependencies:  deps.Edges,
		Groups:        groups,
	}
	if f.rateLimitMgr != nil {
		snapshot.RateLimitSnapshot = f.rateLimitMgr.GetSnapshot()
	}
	return &fsmSnapshot{data: snapshot}, nil
}

func (f *fsm) Restore(rc io.ReadCloser) error {
	defer rc.Close()
	var snapshot LockStateSnapshot
	if err := json.NewDecoder(rc).Decode(&snapshot); err != nil {
		f.logger.Error("Failed to decode snapshot", zap.Error(err))
		return err
	}
	f.lockMgr.RestoreFromSnapshot(snapshot.Locks)
	if snapshot.Dependencies != nil {
		f.lockMgr.RestoreDependencies(snapshot.Dependencies)
	}
	if snapshot.Groups != nil {
		f.lockMgr.RestoreGroups(snapshot.Groups)
	}
	if snapshot.RateLimitSnapshot != nil && f.rateLimitMgr != nil {
		f.rateLimitMgr.RestoreFromSnapshot(snapshot.RateLimitSnapshot)
	}
	f.logger.Info("Snapshot restored successfully",
		zap.Int("lock_count", len(snapshot.Locks)),
		zap.Int("dep_count", len(snapshot.Dependencies)),
		zap.Int("group_count", len(snapshot.Groups)))
	return nil
}

type fsmSnapshot struct {
	data *LockStateSnapshot
}

func (s *fsmSnapshot) Persist(sink raft.SnapshotSink) error {
	err := func() error {
		enc := json.NewEncoder(sink)
		if err := enc.Encode(s.data); err != nil {
			return err
		}
		return nil
	}()
	if err != nil {
		sink.Cancel()
		return err
	}
	return sink.Close()
}

func (s *fsmSnapshot) Release() {}

func NewRaftManager(nodeConfig NodeConfig, config lock.Config, logger *zap.Logger,
	lockMgr *lock.LockManager, rateLimitMgr *ratelimit.RateLimitManager, metrics *metrics.PrometheusCollector) (*RaftManager, error) {

	rm := &RaftManager{
		config:       config,
		logger:       logger,
		lockMgr:      lockMgr,
		rateLimitMgr: rateLimitMgr,
		metrics:      metrics,
		nodeID:       nodeConfig.NodeID,
		raftDir:      nodeConfig.DataDir,
		notifyCh:     make(chan bool, 1),
	}

	if err := os.MkdirAll(nodeConfig.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data dir: %w", err)
	}

	store, err := raftboltdb.NewBoltStore(filepath.Join(nodeConfig.DataDir, "raft.db"))
	if err != nil {
		return nil, fmt.Errorf("failed to create bolt store: %w", err)
	}
	rm.raftStore = store

	snapshotStore, err := raft.NewFileSnapshotStore(nodeConfig.DataDir, 3, os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("failed to create snapshot store: %w", err)
	}

	addr, err := net.ResolveTCPAddr("tcp", nodeConfig.BindAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve bind addr: %w", err)
	}

	transport, err := raft.NewTCPTransport(nodeConfig.BindAddr, addr, 3, 10*time.Second, os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("failed to create transport: %w", err)
	}

	raftConfig := raft.DefaultConfig()
	raftConfig.LocalID = raft.ServerID(nodeConfig.NodeID)
	raftConfig.ElectionTimeout = config.RaftElectionTimeoutMax
	raftConfig.HeartbeatTimeout = config.RaftElectionTimeoutMin
	raftConfig.SnapshotInterval = config.RaftSnapshotInterval
	raftConfig.SnapshotThreshold = 10000
	raftConfig.NotifyCh = rm.notifyCh

	fsmImpl := &fsm{
		lockMgr:     lockMgr,
		rateLimitMgr: rateLimitMgr,
		logger:      logger,
	}

	ra, err := raft.NewRaft(raftConfig, fsmImpl, store, store, snapshotStore, transport)
	if err != nil {
		return nil, fmt.Errorf("failed to create raft: %w", err)
	}
	rm.raft = ra

	if nodeConfig.Bootstrap {
		configuration := raft.Configuration{
			Servers: make([]raft.Server, 0, len(nodeConfig.Peers)+1),
		}
		for id, addr := range nodeConfig.Peers {
			configuration.Servers = append(configuration.Servers, raft.Server{
				ID:      raft.ServerID(id),
				Address: raft.ServerAddress(addr),
			})
		}
		configuration.Servers = append(configuration.Servers, raft.Server{
			ID:      raft.ServerID(nodeConfig.NodeID),
			Address: raft.ServerAddress(nodeConfig.BindAddr),
		})

		future := ra.BootstrapCluster(configuration)
		if err := future.Error(); err != nil && err != raft.ErrCantBootstrap {
			return nil, fmt.Errorf("failed to bootstrap cluster: %w", err)
		}
	}

	go rm.monitorLeadership()

	return rm, nil
}

func (rm *RaftManager) monitorLeadership() {
	for range rm.notifyCh {
		leader := rm.raft.Leader()
		rm.mu.Lock()
		rm.leaderAddr = string(leader)
		rm.mu.Unlock()

		if rm.raft.State() == raft.Leader {
			rm.metrics.RecordRaftElection()
			rm.logger.Info("Became Raft leader", zap.String("leader_addr", string(leader)))
		} else {
			rm.logger.Info("Raft leadership changed", zap.String("leader_addr", string(leader)))
		}
	}
}

func (rm *RaftManager) IsLeader() bool {
	return rm.raft.State() == raft.Leader
}

func (rm *RaftManager) GetLeader() string {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.leaderAddr
}

func (rm *RaftManager) GetState() string {
	return rm.raft.State().String()
}

func (rm *RaftManager) ApplyCommand(cmd RaftCommand, timeout time.Duration) (interface{}, error) {
	if !rm.IsLeader() {
		return nil, errors.New("not leader")
	}

	data, err := json.Marshal(cmd)
	if err != nil {
		return nil, err
	}

	future := rm.raft.Apply(data, timeout)
	if err := future.Error(); err != nil {
		return nil, err
	}

	return future.Response(), nil
}

func (rm *RaftManager) AddPeer(nodeID string, addr string) error {
	if !rm.IsLeader() {
		return errors.New("not leader")
	}

	future := rm.raft.AddVoter(raft.ServerID(nodeID), raft.ServerAddress(addr), 0, 0)
	return future.Error()
}

func (rm *RaftManager) RemovePeer(nodeID string) error {
	if !rm.IsLeader() {
		return errors.New("not leader")
	}

	future := rm.raft.RemoveServer(raft.ServerID(nodeID), 0, 0)
	return future.Error()
}

func (rm *RaftManager) GetConfiguration() (raft.Configuration, error) {
	future := rm.raft.GetConfiguration()
	if err := future.Error(); err != nil {
		return raft.Configuration{}, err
	}
	return future.Configuration(), nil
}

func (rm *RaftManager) GetLastIndex() uint64 {
	return rm.raft.LastIndex()
}

func (rm *RaftManager) GetAppliedIndex() uint64 {
	return rm.raft.AppliedIndex()
}

func (rm *RaftManager) Shutdown() error {
	if rm.raftStore != nil {
		rm.raftStore.Close()
	}
	future := rm.raft.Shutdown()
	return future.Error()
}

func (rm *RaftManager) GetRateLimitMgr() *ratelimit.RateLimitManager {
	return rm.rateLimitMgr
}
