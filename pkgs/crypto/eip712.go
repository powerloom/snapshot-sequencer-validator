package crypto

import (
	"encoding/base64"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	log "github.com/sirupsen/logrus"
)

// SnapshotRequest represents the EIP-712 request structure
type SnapshotRequest struct {
	SlotId      uint64
	Deadline    uint64
	SnapshotCid string
	EpochId     uint64
	ProjectId   string
}

// EIP712Verifier handles EIP-712 signature verification
type EIP712Verifier struct {
	chainID           *big.Int
	verifyingContract common.Address
}

// NewEIP712Verifier creates a new verifier with domain parameters
func NewEIP712Verifier(chainID int64, verifyingContract string) (*EIP712Verifier, error) {
	if !common.IsHexAddress(verifyingContract) {
		return nil, fmt.Errorf("invalid verifying contract address: %s", verifyingContract)
	}

	return &EIP712Verifier{
		chainID:           big.NewInt(chainID),
		verifyingContract: common.HexToAddress(verifyingContract),
	}, nil
}

// HashRequest creates the EIP-712 hash for a snapshot request
func (v *EIP712Verifier) HashRequest(request *SnapshotRequest) ([]byte, error) {
	typedData := apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain": []apitypes.Type{
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
			},
			"EIPRequest": []apitypes.Type{
				{Name: "slotId", Type: "uint256"},
				{Name: "deadline", Type: "uint256"},
				{Name: "snapshotCid", Type: "string"},
				{Name: "epochId", Type: "uint256"},
				{Name: "projectId", Type: "string"},
			},
		},
		PrimaryType: "EIPRequest",
		Domain: apitypes.TypedDataDomain{
			Name:              "PowerloomProtocolContract",
			Version:           "0.1",
			ChainId:           (*math.HexOrDecimal256)(v.chainID),
			VerifyingContract: v.verifyingContract.Hex(),
		},
		Message: apitypes.TypedDataMessage{
			"slotId":      (*math.HexOrDecimal256)(big.NewInt(int64(request.SlotId))),
			"deadline":    (*math.HexOrDecimal256)(big.NewInt(int64(request.Deadline))),
			"snapshotCid": request.SnapshotCid,
			"epochId":     (*math.HexOrDecimal256)(big.NewInt(int64(request.EpochId))),
			"projectId":   request.ProjectId,
		},
	}

	domainSeparator, err := typedData.HashStruct("EIP712Domain", typedData.Domain.Map())
	if err != nil {
		return nil, fmt.Errorf("failed to hash domain: %w", err)
	}

	typedDataHash, err := typedData.HashStruct(typedData.PrimaryType, typedData.Message)
	if err != nil {
		return nil, fmt.Errorf("failed to hash message: %w", err)
	}

	// EIP-712 hash: keccak256("\x19\x01" ‖ domainSeparator ‖ hashStruct(message))
	rawData := append([]byte{0x19, 0x01}, domainSeparator...)
	rawData = append(rawData, typedDataHash...)
	hash := crypto.Keccak256Hash(rawData)

	return hash.Bytes(), nil
}

// RecoverAddress recovers the signer's address from message hash and signature
func RecoverAddress(msgHash, signature []byte) (common.Address, error) {
	if len(signature) != 65 {
		return common.Address{}, fmt.Errorf("invalid signature length: %d, expected 65", len(signature))
	}

	// Signature format: [R || S || V] where V is 27 or 28
	// Ethereum's Ecrecover expects V to be 0 or 1
	v := signature[64]
	if v != 27 && v != 28 {
		return common.Address{}, fmt.Errorf("invalid recovery id: got %d, expected 27 or 28 (r=%x, s=%x)",
			v, signature[0:4], signature[32:36])
	}

	// Adjust V for Ecrecover (subtract 27)
	signature[64] -= 27

	// Recover public key
	pubKeyRaw, err := crypto.Ecrecover(msgHash, signature)
	if err != nil {
		return common.Address{}, fmt.Errorf("ecrecover failed (adjusted_v=%d): %w", signature[64], err)
	}

	// Unmarshal public key
	pubKey, err := crypto.UnmarshalPubkey(pubKeyRaw)
	if err != nil {
		return common.Address{}, fmt.Errorf("pubkey unmarshal failed (pubKeyRaw_len=%d): %w", len(pubKeyRaw), err)
	}

	// Derive address from public key
	recoveredAddr := crypto.PubkeyToAddress(*pubKey)
	return recoveredAddr, nil
}

// VerifySignature verifies an EIP-712 signature and returns the signer's address
func (v *EIP712Verifier) VerifySignature(request *SnapshotRequest, signatureStr string) (common.Address, error) {
	// Snapshotter sends hex-encoded signatures (with or without 0x prefix)
	// Expected: 130 chars (65 bytes * 2) or 132 chars (with 0x prefix)
	hexStr := signatureStr
	if len(hexStr) >= 2 && hexStr[:2] == "0x" {
		hexStr = hexStr[2:]
	}

	if len(hexStr) != 130 {
		return common.Address{}, fmt.Errorf("invalid signature length: expected 130 hex chars (65 bytes), got %d chars", len(hexStr))
	}

	signature := common.FromHex(hexStr)
	if len(signature) != 65 {
		return common.Address{}, fmt.Errorf("hex decode produced wrong length: got %d bytes, expected 65", len(signature))
	}

	log.Debugf("EIP-712 signature decode: hex_input=%s, r=%x, s=%x, v=%d",
		hexStr[:20]+"...", signature[0:4], signature[32:36], signature[64])

	// Hash the request
	msgHash, err := v.HashRequest(request)
	if err != nil {
		return common.Address{}, fmt.Errorf("EIP-712 hash generation failed: %w", err)
	}

	// Recover signer address
	signerAddr, err := RecoverAddress(msgHash, signature)
	if err != nil {
		return common.Address{}, fmt.Errorf("address recovery failed (msgHash=0x%x, sig_v=%d): %w",
			msgHash, signature[64], err)
	}

	// Debug logging to trace signature verification
	log.Debugf("EIP-712 verification details: slotId=%d, deadline=%d, epochId=%d, projectId=%s, CID=%s, msgHash=0x%x, signer=%s",
		request.SlotId, request.Deadline, request.EpochId, request.ProjectId, request.SnapshotCid, msgHash, signerAddr.Hex())

	return signerAddr, nil
}

// DecodeSignature decodes a base64-encoded signature string
func DecodeSignature(signatureStr string) ([]byte, error) {
	signature, err := base64.StdEncoding.DecodeString(signatureStr)
	if err != nil {
		return nil, fmt.Errorf("failed to decode signature: %w", err)
	}

	if len(signature) != 65 {
		return nil, fmt.Errorf("invalid signature length: %d, expected 65", len(signature))
	}

	return signature, nil
}
