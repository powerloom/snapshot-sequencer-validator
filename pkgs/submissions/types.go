package submissions

import (
	"time"
)

// SnapshotSubmission represents a snapshot submission from the network
type SnapshotSubmission struct {
	Request       SnapshotRequest `json:"request"`
	Signature     string          `json:"signature"`
	Header        string          `json:"header,omitempty"`       // Block header
	ProtocolState string          `json:"protocolState,omitempty"` // Protocol state contract address
	DataMarket    string          `json:"dataMarket"`              // Data market contract address
	NodeVersion   *string         `json:"nodeVersion,omitempty"`
}

// SnapshotRequest contains the actual snapshot data
type SnapshotRequest struct {
	SlotId           uint64 `json:"slotId,omitempty"`
	Deadline         uint64 `json:"deadline"`
	SnapshotCid      string `json:"snapshotCid,omitempty"`
	EpochId          uint64 `json:"epochId,omitempty"`
	ProjectId        string `json:"projectId,omitempty"`
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