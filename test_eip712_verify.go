package main

import (
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

func main() {
	// Test data from centralized sequencer log
	slotID := uint64(4036)
	deadline := uint64(1563438)
	epochID := uint64(190110)
	projectID := "pairContract_trade_volume:0xeedff72a683058f8ff531e8c98575f920430fdc5:mainnet-UNISWAPV2-ETH"
	snapshotCID := "bafkreiei5wglv5xlzpmhh2crkksftqt5snlwjwjrtudoo4l7orizyorype"
	signatureHex := "1fa5648e41b611e0b4cfeba8f691410f70a866c588604f3dd26f1ffe1b832bf3530d838b56f6dfb2a4883e9d7e79aa075d2e48f0112218924d266bd46b83639d1b"
	expectedSigner := "0xa865187E8E86ae8c649a7bD8DE1C6E0a3Bd4b2Be"

	chainID := big.NewInt(7869)
	verifyingContract := common.HexToAddress("0x000AA7d3a6a2556496f363B59e56D9aA1881548F")

	// Build EIP-712 typed data
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
			ChainId:           (*math.HexOrDecimal256)(chainID),
			VerifyingContract: verifyingContract.Hex(),
		},
		Message: apitypes.TypedDataMessage{
			"slotId":      (*math.HexOrDecimal256)(big.NewInt(int64(slotID))),
			"deadline":    (*math.HexOrDecimal256)(big.NewInt(int64(deadline))),
			"snapshotCid": snapshotCID,
			"epochId":     (*math.HexOrDecimal256)(big.NewInt(int64(epochID))),
			"projectId":   projectID,
		},
	}

	// Hash domain
	domainSeparator, err := typedData.HashStruct("EIP712Domain", typedData.Domain.Map())
	if err != nil {
		fmt.Printf("Failed to hash domain: %v\n", err)
		return
	}

	// Hash message
	typedDataHash, err := typedData.HashStruct(typedData.PrimaryType, typedData.Message)
	if err != nil {
		fmt.Printf("Failed to hash message: %v\n", err)
		return
	}

	// EIP-712 final hash
	rawData := append([]byte{0x19, 0x01}, domainSeparator...)
	rawData = append(rawData, typedDataHash...)
	msgHash := crypto.Keccak256Hash(rawData)

	fmt.Printf("EIP-712 Verification Test\n")
	fmt.Printf("========================\n")
	fmt.Printf("Chain ID: %d\n", chainID.Int64())
	fmt.Printf("Verifying Contract: %s\n", verifyingContract.Hex())
	fmt.Printf("Slot ID: %d\n", slotID)
	fmt.Printf("Deadline: %d\n", deadline)
	fmt.Printf("Epoch ID: %d\n", epochID)
	fmt.Printf("Project ID: %s\n", projectID)
	fmt.Printf("Snapshot CID: %s\n", snapshotCID)
	fmt.Printf("\n")
	fmt.Printf("Domain Separator: 0x%x\n", domainSeparator)
	fmt.Printf("Typed Data Hash: 0x%x\n", typedDataHash)
	fmt.Printf("Message Hash: 0x%x\n", msgHash.Bytes())
	fmt.Printf("\n")

	// Decode signature
	signature := common.Hex2Bytes(signatureHex)
	if len(signature) != 65 {
		fmt.Printf("Invalid signature length: %d\n", len(signature))
		return
	}

	fmt.Printf("Signature: %s\n", signatureHex)
	fmt.Printf("  r: 0x%x\n", signature[0:32])
	fmt.Printf("  s: 0x%x\n", signature[32:64])
	fmt.Printf("  v: %d\n", signature[64])
	fmt.Printf("\n")

	// Verify v is 27 or 28
	if signature[64] != 27 && signature[64] != 28 {
		fmt.Printf("Invalid recovery id: %d (expected 27 or 28)\n", signature[64])
		return
	}

	// Adjust v for Ecrecover
	signature[64] -= 27

	// Recover public key
	pubKeyRaw, err := crypto.Ecrecover(msgHash.Bytes(), signature)
	if err != nil {
		fmt.Printf("Ecrecover failed: %v\n", err)
		return
	}

	pubKey, err := crypto.UnmarshalPubkey(pubKeyRaw)
	if err != nil {
		fmt.Printf("Pubkey unmarshal failed: %v\n", err)
		return
	}

	// Derive address
	recoveredAddr := crypto.PubkeyToAddress(*pubKey)

	fmt.Printf("Recovered Address: %s\n", recoveredAddr.Hex())
	fmt.Printf("Expected Address:  %s\n", expectedSigner)
	fmt.Printf("\n")

	if recoveredAddr.Hex() == expectedSigner {
		fmt.Printf("✅ VERIFICATION PASSED\n")
	} else {
		fmt.Printf("❌ VERIFICATION FAILED\n")
		fmt.Printf("Addresses do not match!\n")
	}
}
