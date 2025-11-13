package protocol

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/sirupsen/logrus"
)

// ProtocolState binds to the ProtocolState contract
type ProtocolState struct {
	client         *ethclient.Client
	contractAddr   common.Address
	abi            abi.ABI
	privateKey     *ecdsa.PrivateKey
	validatorAddr  common.Address
	chainID        *big.Int
}

// BatchSubmission represents a batch submission to the protocol
type BatchSubmission struct {
	EpochID           uint64
	BatchCID          string
	ProjectIDs        []string
	SnapshotCIDs      []string
	FinalizedCIDsRoot []byte
	GasLimit          uint64
}

// SubmissionResult holds the result of a batch submission
type SubmissionResult struct {
	TxHash     string
	BlockNumber uint64
	GasUsed    uint64
	Success    bool
	Error      error
}

// NewProtocolState creates a new ProtocolState contract client
func NewProtocolState(rpcURL string, contractAddr string, validatorPrivateKey string, chainID int64) (*ProtocolState, error) {
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Ethereum client: %w", err)
	}

	if !common.IsHexAddress(contractAddr) {
		return nil, fmt.Errorf("invalid contract address: %s", contractAddr)
	}

	// Parse private key
	privateKey, err := crypto.HexToECDSA(validatorPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	// Get validator address from private key
	validatorAddr := crypto.PubkeyToAddress(privateKey.PublicKey)

	// Load the ProtocolState ABI
	protocolABI, err := abi.JSON(strings.NewReader(ProtocolStateABI))
	if err != nil {
		return nil, fmt.Errorf("failed to load ProtocolState ABI: %w", err)
	}

	return &ProtocolState{
		client:        client,
		contractAddr:  common.HexToAddress(contractAddr),
		abi:           protocolABI,
		privateKey:    privateKey,
		validatorAddr: validatorAddr,
		chainID:       big.NewInt(chainID),
	}, nil
}

// SubmitBatch submits a batch to the ProtocolState contract
func (ps *ProtocolState) SubmitBatch(ctx context.Context, dataMarketAddr string, submission *BatchSubmission) (*SubmissionResult, error) {
	if !common.IsHexAddress(dataMarketAddr) {
		return nil, fmt.Errorf("invalid data market address: %s", dataMarketAddr)
	}

	// Prepare batch data
	projectIDs := make([][32]byte, len(submission.ProjectIDs))
	snapshotCIDs := make([]string, len(submission.SnapshotCIDs))

	for i, projectID := range submission.ProjectIDs {
		// Convert project ID to bytes32 (pad/truncate as needed)
		projectIDBytes := []byte(projectID)
		if len(projectIDBytes) > 32 {
			projectIDBytes = projectIDBytes[:32]
		}
		copy(projectIDs[i][:], projectIDBytes)
		snapshotCIDs[i] = submission.SnapshotCIDs[i]
	}

	// Create call data for submitSubmissionBatch
	data, err := ps.abi.Pack("submitSubmissionBatch",
		common.HexToAddress(dataMarketAddr),
		big.NewInt(int64(submission.EpochID)),
		submission.BatchCID,
		projectIDs,
		snapshotCIDs,
		submission.FinalizedCIDsRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to pack submitSubmissionBatch call: %w", err)
	}

	// Estimate gas
	gasLimit := submission.GasLimit
	if gasLimit == 0 {
		msg := ethereum.CallMsg{
			From:  ps.validatorAddr,
			To:    &ps.contractAddr,
			Data:  data,
		}
		gasLimit, err = ps.client.EstimateGas(ctx, msg)
		if err != nil {
			return nil, fmt.Errorf("failed to estimate gas: %w", err)
		}
		// Add 20% buffer
		gasLimit = uint64(float64(gasLimit) * 1.2)
	}

	// Get nonce
	nonce, err := ps.client.PendingNonceAt(ctx, ps.validatorAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %w", err)
	}

	// Get suggested gas price
	gasPrice, err := ps.client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get gas price: %w", err)
	}

	// Create transaction
	tx := types.NewTransaction(
		nonce,
		ps.contractAddr,
		big.NewInt(0), // 0 value
		gasLimit,
		gasPrice,
		data,
	)

	// Sign transaction
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(ps.chainID), ps.privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %w", err)
	}

	// Send transaction
	logrus.WithFields(logrus.Fields{
		"tx_hash":     signedTx.Hash().Hex(),
		"gas_limit":   gasLimit,
		"gas_price":   gasPrice.String(),
		"nonce":       nonce,
		"epoch_id":    submission.EpochID,
		"batch_cid":   submission.BatchCID,
	}).Info("📤 Submitting batch to ProtocolState")

	err = ps.client.SendTransaction(ctx, signedTx)
	if err != nil {
		return &SubmissionResult{
			Success: false,
			Error:   fmt.Errorf("failed to send transaction: %w", err),
		}, err
	}

	// Wait for receipt
	receipt, err := bind.WaitMined(ctx, ps.client, signedTx)
	if err != nil {
		return &SubmissionResult{
			TxHash:  signedTx.Hash().Hex(),
			Success: false,
			Error:   fmt.Errorf("transaction failed: %w", err),
		}, err
	}

	success := receipt.Status == 1
	result := &SubmissionResult{
		TxHash:      receipt.TxHash.Hex(),
		BlockNumber: receipt.BlockNumber.Uint64(),
		GasUsed:     receipt.GasUsed,
		Success:     success,
	}

	if success {
		logrus.WithFields(logrus.Fields{
			"tx_hash":      result.TxHash,
			"block_number": result.BlockNumber,
			"gas_used":     result.GasUsed,
			"epoch_id":     submission.EpochID,
		}).Info("✅ Batch submission successful")
	} else {
		result.Error = fmt.Errorf("transaction reverted")
		logrus.WithFields(logrus.Fields{
			"tx_hash":  result.TxHash,
			"epoch_id": submission.EpochID,
		}).Error("❌ Batch submission failed")
	}

	return result, nil
}

// SubmitBatchAsync submits a batch asynchronously and returns the result via channel
func (ps *ProtocolState) SubmitBatchAsync(ctx context.Context, dataMarketAddr string, submission *BatchSubmission) <-chan *SubmissionResult {
	resultChan := make(chan *SubmissionResult, 1)

	go func() {
		defer close(resultChan)
		result, err := ps.SubmitBatch(ctx, dataMarketAddr, submission)
		if err != nil {
			resultChan <- &SubmissionResult{
				Success: false,
				Error:   err,
			}
			return
		}
		resultChan <- result
	}()

	return resultChan
}

// WaitForConfirmation waits for transaction confirmation
func (ps *ProtocolState) WaitForConfirmation(ctx context.Context, txHash string) (*types.Receipt, error) {
	hash := common.HexToHash(txHash)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			receipt, err := ps.client.TransactionReceipt(ctx, hash)
			if err == nil {
				return receipt, nil
			}
			if err.Error() != "not found" {
				return nil, fmt.Errorf("failed to get receipt: %w", err)
			}
			time.Sleep(2 * time.Second)
		}
	}
}

// Close closes the client connection
func (ps *ProtocolState) Close() {
	if ps.client != nil {
		ps.client.Close()
	}
}

// ProtocolStateABI contains the simplified ABI for the ProtocolState contract
const ProtocolStateABI = `[
	{
		"inputs": [
			{"internalType": "address", "name": "dataMarket", "type": "address"},
			{"internalType": "uint256", "name": "epochId", "type": "uint256"},
			{"internalType": "string", "name": "batchCid", "type": "string"},
			{"internalType": "bytes32[]", "name": "projectIds", "type": "bytes32[]"},
			{"internalType": "string[]", "name": "snapshotCids", "type": "string[]"},
			{"internalType": "bytes32", "name": "finalizedCidsRootHash", "type": "bytes32"}
		],
		"name": "submitSubmissionBatch",
		"outputs": [],
		"stateMutability": "nonpayable",
		"type": "function"
	}
]`