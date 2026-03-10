package main

// Types matching the coordinator's proto package (JSON codec).
// These mirror github.com/iotexproject/iotex-core/ioswarm/proto types.

type registerRequest struct {
	AgentID       string `json:"agent_id"`
	Capability    int32  `json:"capability"`
	Region        string `json:"region"`
	Version       string `json:"version"`
	WalletAddress string `json:"wallet_address,omitempty"`
}

type registerResponse struct {
	Accepted             bool   `json:"accepted"`
	Reason               string `json:"reason,omitempty"`
	HeartbeatIntervalSec uint32 `json:"heartbeat_interval_sec"`
}

type getTasksRequest struct {
	AgentID      string `json:"agent_id"`
	MaxLevel     int32  `json:"max_level"`
	MaxBatchSize uint32 `json:"max_batch_size"`
}

type taskBatch struct {
	BatchID   string         `json:"batch_id"`
	Tasks     []*taskPackage `json:"tasks"`
	Timestamp uint64         `json:"timestamp"`
}

type taskPackage struct {
	TaskID      uint32           `json:"task_id"`
	TxRaw       []byte           `json:"tx_raw"`
	Level       int32            `json:"level"`
	Sender      *accountSnapshot `json:"sender"`
	Receiver    *accountSnapshot `json:"receiver"`
	BlockHeight uint64           `json:"block_height"`
}

type accountSnapshot struct {
	Address  string `json:"address"`
	Balance  string `json:"balance"`
	Nonce    uint64 `json:"nonce"`
	CodeHash []byte `json:"code_hash,omitempty"`
}

type taskResult struct {
	TaskID       uint32 `json:"task_id"`
	Valid        bool   `json:"valid"`
	RejectReason string `json:"reject_reason,omitempty"`
	GasEstimate  uint64 `json:"gas_estimate"`
	LatencyUs    uint64 `json:"latency_us"`
}

type batchResult struct {
	AgentID   string        `json:"agent_id"`
	BatchID   string        `json:"batch_id"`
	Results   []*taskResult `json:"results"`
	Timestamp uint64        `json:"timestamp"`
}

type submitResponse struct {
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason,omitempty"`
}

type heartbeatRequest struct {
	AgentID        string  `json:"agent_id"`
	TasksProcessed uint32  `json:"tasks_processed"`
	TasksPending   uint32  `json:"tasks_pending"`
	CPUUsage       float64 `json:"cpu_usage"`
	MemUsage       float64 `json:"mem_usage"`
}

type heartbeatResponse struct {
	Alive     bool        `json:"alive"`
	Directive string      `json:"directive"`
	Payout    *payoutInfo `json:"payout,omitempty"`
}

type payoutInfo struct {
	Epoch       uint64  `json:"epoch"`
	AmountIOTX  float64 `json:"amount_iotx"`
	Rank        int     `json:"rank"`
	TotalAgents int     `json:"total_agents"`
}
