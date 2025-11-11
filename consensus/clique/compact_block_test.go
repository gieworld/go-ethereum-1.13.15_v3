// Copyright 2025 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// ExClique: Proactive Compact Block (PCB) Protocol Tests
// CRITICAL AUDIT: RLP Encoding/Decoding Tests for CompactTransaction

package clique

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
)

// TestCompactTransactionRLPEncoding tests that CompactTransaction can be RLP encoded/decoded via PCB
// CRITICAL: This tests the core PCB wire protocol integrity
// NOTE: CompactTransaction is NOT directly RLP-encodable; it must be part of a ProactiveCompactBlock
func TestCompactTransactionRLPEncoding(t *testing.T) {
	// Test short ID within PCB
	key, _ := crypto.GenerateKey()
	signer := types.NewLondonSigner(big.NewInt(1337))
	tx := types.MustSignNewTx(key, signer, &types.DynamicFeeTx{
		ChainID:   big.NewInt(1337),
		Nonce:     0,
		GasTipCap: big.NewInt(1),
		GasFeeCap: big.NewInt(1),
		Gas:       21000,
		To:        &common.Address{0x01},
		Value:     big.NewInt(1),
		Data:      nil,
	})

	header := &types.Header{
		Number:     big.NewInt(1),
		Difficulty: big.NewInt(1),
		GasLimit:   21000,
		Time:       1000,
		Extra:      make([]byte, 32),
	}

	// Test 1: Short ID PCB
	pcbShort := &ProactiveCompactBlock{
		Header: header,
		Transactions: []CompactTransaction{
			{
				IsShort: true,
				ShortID: []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06},
				Tx:      nil,
			},
		},
		Uncles: nil,
	}

	data, err := rlp.EncodeToBytes(pcbShort)
	if err != nil {
		t.Fatalf("Failed to encode PCB with short ID: %v", err)
	}

	var decodedPCB ProactiveCompactBlock
	if err := rlp.DecodeBytes(data, &decodedPCB); err != nil {
		t.Fatalf("Failed to decode PCB with short ID: %v", err)
	}

	if len(decodedPCB.Transactions) != 1 {
		t.Fatalf("Transaction count mismatch: got %d, want 1", len(decodedPCB.Transactions))
	}
	if !decodedPCB.Transactions[0].IsShort {
		t.Error("IsShort flag not preserved")
	}
	if !bytes.Equal(decodedPCB.Transactions[0].ShortID, pcbShort.Transactions[0].ShortID) {
		t.Errorf("ShortID not preserved: got %x, want %x", decodedPCB.Transactions[0].ShortID, pcbShort.Transactions[0].ShortID)
	}

	// Test 2: Full transaction PCB
	pcbFull := &ProactiveCompactBlock{
		Header: header,
		Transactions: []CompactTransaction{
			{
				IsShort: false,
				ShortID: nil,
				Tx:      tx,
			},
		},
		Uncles: nil,
	}

	data2, err := rlp.EncodeToBytes(pcbFull)
	if err != nil {
		t.Fatalf("Failed to encode PCB with full TX: %v", err)
	}

	var decodedPCBFull ProactiveCompactBlock
	if err := rlp.DecodeBytes(data2, &decodedPCBFull); err != nil {
		t.Fatalf("Failed to decode PCB with full TX: %v", err)
	}

	if len(decodedPCBFull.Transactions) != 1 {
		t.Fatalf("Transaction count mismatch: got %d, want 1", len(decodedPCBFull.Transactions))
	}
	if decodedPCBFull.Transactions[0].IsShort {
		t.Error("Transaction should not be short")
	}
	if decodedPCBFull.Transactions[0].Tx == nil {
		t.Fatal("Transaction was nil after decoding")
	}
	if decodedPCBFull.Transactions[0].Tx.Hash() != tx.Hash() {
		t.Errorf("Transaction hash mismatch: got %x, want %x", decodedPCBFull.Transactions[0].Tx.Hash(), tx.Hash())
	}
}

// TestProactiveCompactBlockRLPEncoding tests PCB serialization/deserialization
func TestProactiveCompactBlockRLPEncoding(t *testing.T) {
	// Create a test transaction
	key, _ := crypto.GenerateKey()
	signer := types.NewLondonSigner(big.NewInt(1337))
	tx := types.MustSignNewTx(key, signer, &types.DynamicFeeTx{
		ChainID:   big.NewInt(1337),
		Nonce:     0,
		GasTipCap: big.NewInt(1),
		GasFeeCap: big.NewInt(1),
		Gas:       21000,
		To:        &common.Address{0x01},
		Value:     big.NewInt(1),
	})

	header := &types.Header{
		Number:     big.NewInt(1),
		Difficulty: big.NewInt(1),
		GasLimit:   21000,
		Time:       1000,
		Extra:      make([]byte, 32),
	}

	block := types.NewBlockWithHeader(header).WithBody([]*types.Transaction{tx}, nil)

	// Encode as PCB (without CBF, so full transaction)
	pcb, err := EncodeProactiveCompactBlock(block, nil)
	if err != nil {
		t.Fatalf("Failed to encode block as PCB: %v", err)
	}

	// Serialize
	data, err := SerializeCompactBlock(pcb)
	if err != nil {
		t.Fatalf("Failed to serialize PCB: %v", err)
	}

	// Deserialize
	decodedPCB, err := DeserializeCompactBlock(data)
	if err != nil {
		t.Fatalf("Failed to deserialize PCB: %v", err)
	}

	// Verify structure
	if decodedPCB.Header.Number.Cmp(header.Number) != 0 {
		t.Errorf("Header number mismatch: got %v, want %v", decodedPCB.Header.Number, header.Number)
	}
	if len(decodedPCB.Transactions) != 1 {
		t.Fatalf("Transaction count mismatch: got %d, want 1", len(decodedPCB.Transactions))
	}
	if decodedPCB.Transactions[0].IsShort {
		t.Error("Transaction should be full, not short (no CBF provided)")
	}
	if decodedPCB.Transactions[0].Tx.Hash() != tx.Hash() {
		t.Errorf("Transaction hash mismatch: got %x, want %x", decodedPCB.Transactions[0].Tx.Hash(), tx.Hash())
	}
}

// TestShortIDMatching tests the short ID matching logic
func TestShortIDMatching(t *testing.T) {
	txHash := common.HexToHash("0x0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	shortID := CompactTransactionID(txHash)

	if len(shortID) != shortIDLength {
		t.Fatalf("Short ID length mismatch: got %d, want %d", len(shortID), shortIDLength)
	}

	// Test matching
	if !matchesShortID(txHash, shortID) {
		t.Error("Short ID should match the original transaction hash")
	}

	// Test non-matching
	differentHash := common.HexToHash("0xffffffffffffffff090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	if matchesShortID(differentHash, shortID) {
		t.Error("Short ID should not match different transaction hash")
	}
}

// TestPCBWithCBF tests PCB encoding with CBF (short IDs)
func TestPCBWithCBF(t *testing.T) {
	// Create a test transaction
	key, _ := crypto.GenerateKey()
	signer := types.NewLondonSigner(big.NewInt(1337))
	tx := types.MustSignNewTx(key, signer, &types.DynamicFeeTx{
		ChainID:   big.NewInt(1337),
		Nonce:     0,
		GasTipCap: big.NewInt(1),
		GasFeeCap: big.NewInt(1),
		Gas:       21000,
		To:        &common.Address{0x01},
		Value:     big.NewInt(1),
	})

	header := &types.Header{
		Number:     big.NewInt(1),
		Difficulty: big.NewInt(1),
		GasLimit:   21000,
		Time:       1000,
		Extra:      make([]byte, 32),
	}

	block := types.NewBlockWithHeader(header).WithBody([]*types.Transaction{tx}, nil)

	// Create CBF and add the transaction
	cbf := NewCountingBloomFilter()
	cbf.Add(tx.Hash())

	// Encode as PCB with CBF (should use short ID)
	pcb, err := EncodeProactiveCompactBlock(block, cbf)
	if err != nil {
		t.Fatalf("Failed to encode block as PCB with CBF: %v", err)
	}

	// Verify short ID was used
	if len(pcb.Transactions) != 1 {
		t.Fatalf("Transaction count mismatch: got %d, want 1", len(pcb.Transactions))
	}
	if !pcb.Transactions[0].IsShort {
		t.Error("Transaction should be short (CBF contains it)")
	}
	if len(pcb.Transactions[0].ShortID) != shortIDLength {
		t.Errorf("Short ID length mismatch: got %d, want %d", len(pcb.Transactions[0].ShortID), shortIDLength)
	}

	// Serialize and deserialize
	data, err := SerializeCompactBlock(pcb)
	if err != nil {
		t.Fatalf("Failed to serialize PCB: %v", err)
	}

	decodedPCB, err := DeserializeCompactBlock(data)
	if err != nil {
		t.Fatalf("Failed to deserialize PCB: %v", err)
	}

	// Verify short ID preserved
	if !decodedPCB.Transactions[0].IsShort {
		t.Error("IsShort flag not preserved after serialization")
	}
	if !bytes.Equal(decodedPCB.Transactions[0].ShortID, pcb.Transactions[0].ShortID) {
		t.Error("Short ID not preserved after serialization")
	}
}

// MockTxPool for testing PCB decoding
type mockTxPool struct {
	txs map[common.Hash]*types.Transaction
}

func (m *mockTxPool) Get(hash common.Hash) *types.Transaction {
	return m.txs[hash]
}

func (m *mockTxPool) Has(hash common.Hash) bool {
	_, ok := m.txs[hash]
	return ok
}

func (m *mockTxPool) GetAllTxs() map[common.Hash]*types.Transaction {
	return m.txs
}

// TestPCBDecoding tests PCB decoding with mock TX pool
func TestPCBDecoding(t *testing.T) {
	// Create a test transaction
	key, _ := crypto.GenerateKey()
	signer := types.NewLondonSigner(big.NewInt(1337))
	tx := types.MustSignNewTx(key, signer, &types.DynamicFeeTx{
		ChainID:   big.NewInt(1337),
		Nonce:     0,
		GasTipCap: big.NewInt(1),
		GasFeeCap: big.NewInt(1),
		Gas:       21000,
		To:        &common.Address{0x01},
		Value:     big.NewInt(1),
	})

	header := &types.Header{
		Number:     big.NewInt(1),
		Difficulty: big.NewInt(1),
		GasLimit:   21000,
		Time:       1000,
		Extra:      make([]byte, 32),
	}

	originalBlock := types.NewBlockWithHeader(header).WithBody([]*types.Transaction{tx}, nil)

	// Create CBF and encode as PCB
	cbf := NewCountingBloomFilter()
	cbf.Add(tx.Hash())
	pcb, err := EncodeProactiveCompactBlock(originalBlock, cbf)
	if err != nil {
		t.Fatalf("Failed to encode PCB: %v", err)
	}

	// Create mock TX pool with the transaction
	pool := &mockTxPool{
		txs: map[common.Hash]*types.Transaction{
			tx.Hash(): tx,
		},
	}

	// Decode PCB
	decodedBlock, missingTxs, err := DecodeProactiveCompactBlock(pcb, pool)
	if err != nil {
		t.Fatalf("Failed to decode PCB: %v", err)
	}
	if len(missingTxs) > 0 {
		t.Errorf("Unexpected missing transactions: %d", len(missingTxs))
	}

	// Verify decoded block matches original
	if decodedBlock.Hash() != originalBlock.Hash() {
		t.Errorf("Block hash mismatch: got %x, want %x", decodedBlock.Hash(), originalBlock.Hash())
	}
	if len(decodedBlock.Transactions()) != 1 {
		t.Fatalf("Transaction count mismatch: got %d, want 1", len(decodedBlock.Transactions()))
	}
	if decodedBlock.Transactions()[0].Hash() != tx.Hash() {
		t.Errorf("Transaction hash mismatch: got %x, want %x", decodedBlock.Transactions()[0].Hash(), tx.Hash())
	}
}

// TestPCBDecodingMissingTransaction tests error handling when TX is missing
func TestPCBDecodingMissingTransaction(t *testing.T) {
	// Create a test transaction
	key, _ := crypto.GenerateKey()
	signer := types.NewLondonSigner(big.NewInt(1337))
	tx := types.MustSignNewTx(key, signer, &types.DynamicFeeTx{
		ChainID:   big.NewInt(1337),
		Nonce:     0,
		GasTipCap: big.NewInt(1),
		GasFeeCap: big.NewInt(1),
		Gas:       21000,
		To:        &common.Address{0x01},
		Value:     big.NewInt(1),
	})

	header := &types.Header{
		Number:     big.NewInt(1),
		Difficulty: big.NewInt(1),
		GasLimit:   21000,
		Time:       1000,
		Extra:      make([]byte, 32),
	}

	originalBlock := types.NewBlockWithHeader(header).WithBody([]*types.Transaction{tx}, nil)

	// Create CBF and encode as PCB
	cbf := NewCountingBloomFilter()
	cbf.Add(tx.Hash())
	pcb, err := EncodeProactiveCompactBlock(originalBlock, cbf)
	if err != nil {
		t.Fatalf("Failed to encode PCB: %v", err)
	}

	// Create EMPTY TX pool (transaction not present)
	pool := &mockTxPool{
		txs: map[common.Hash]*types.Transaction{},
	}

	// Decode PCB - should fail with missing transaction
	_, missingTxs, err := DecodeProactiveCompactBlock(pcb, pool)
	if err == nil {
		t.Fatal("Expected error when transaction missing, got nil")
	}
	if err != errMissingTransaction {
		t.Errorf("Expected errMissingTransaction, got: %v", err)
	}
	if len(missingTxs) == 0 {
		t.Error("Expected missing transaction list to be non-empty")
	}
}
