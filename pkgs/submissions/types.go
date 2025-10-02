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

// SubmissionMetadata tracks WHO submitted WHAT for challenges/proofs
// This provides accountability and enables challenge workflows in the protocol
type SubmissionMetadata struct {
	SubmitterID          string   `json:"submitter_id"`            // Snapshotter's EVM address extracted from EIP-712 signature
	SnapshotCID          string   `json:"snapshot_cid"`            // IPFS CID of the snapshot data they submitted
	Timestamp            uint64   `json:"timestamp"`               // Unix timestamp when submission was finalized
	Signature            []byte   `json:"signature"`               // Snapshotter's EIP-712 signature proving they submitted this CID (base64 encoded)
	SlotID               uint64   `json:"slot_id"`                 // Numeric slot ID of the snapshotter
	VoteCount            int      `json:"vote_count"`              // How many validators confirmed seeing this submission
	ValidatorsConfirming []string `json:"validators_confirming"`   // List of validator IDs that saw and confirmed this submission
}