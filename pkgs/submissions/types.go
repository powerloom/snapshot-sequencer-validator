package submissions

import (
	"time"
)

// SnapshotSubmission represents a snapshot submission from the network
type SnapshotSubmission struct {
	Request     SnapshotRequest `json:"request"`
	Signature   string          `json:"signature"`
	DataMarket  string          `json:"data_market"`
	NodeVersion *string         `json:"node_version,omitempty"`
}

// SnapshotRequest contains the actual snapshot data
type SnapshotRequest struct {
	SlotId           uint64 `json:"slot_id"`
	Deadline         uint64 `json:"deadline"`
	SnapshotCid      string `json:"snapshot_cid"`
	EpochId          uint64 `json:"epoch_id"`
	ProjectId        string `json:"project_id"`
	AggregateRequest bool   `json:"aggregate_request,omitempty"`
}

// P2PSnapshotSubmission represents batch submissions from collector
type P2PSnapshotSubmission struct {
	EpochID       uint64                `json:"epoch_id"`
	Submissions   []*SnapshotSubmission `json:"submissions"`
	SnapshotterID string                `json:"snapshotter_id"`
	Signature     []byte                `json:"signature"`
}

// ProcessedSubmission represents a submission that has been validated
type ProcessedSubmission struct {
	ID               string
	Submission       *SnapshotSubmission
	SnapshotterAddr  string
	DataMarketAddr   string
	ProcessedAt      time.Time
	ValidatorID      string
}