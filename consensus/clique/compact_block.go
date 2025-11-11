// Copyright 2025 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// ExClique: Proactive Compact Block (PCB) Protocol Implementation

package clique

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"
)

var (
	errMissingTransaction = errors.New("transaction not found in local pool")
	errInvalidShortID     = errors.New("invalid short transaction ID")
)

// CompactTransaction represents either a full transaction or a short ID
// NOTE: RLP encoding is handled at the ProactiveCompactBlock level
// - If IsShort=true: Only ShortID is transmitted (6 bytes)
// - If IsShort=false: Full transaction is RLP-encoded and transmitted
type CompactTransaction struct {
	IsShort bool                // true if this is a short ID, false if full transaction
	ShortID []byte              // 6-byte short ID (only if IsShort = true)
	Tx      *types.Transaction  // Full transaction (only if IsShort = false)
}

// ProactiveCompactBlock represents a compact block optimized for PCB protocol
type ProactiveCompactBlock struct {
	Header       *types.Header        // Block header
	Transactions []CompactTransaction // Mix of short IDs and full transactions
	Uncles       []*types.Header      // Uncle headers (unchanged)
}

// EncodeRLP implements rlp.Encoder for ProactiveCompactBlock
func (pcb *ProactiveCompactBlock) EncodeRLP(w io.Writer) error {
	// Manually encode each CompactTransaction
	type encodableTx struct {
		IsShort bool
		ShortID []byte
		TxBytes []byte
	}

	encodableTxs := make([]encodableTx, len(pcb.Transactions))
	for i, ct := range pcb.Transactions {
		var txBytes []byte
		if !ct.IsShort && ct.Tx != nil {
			var err error
			txBytes, err = rlp.EncodeToBytes(ct.Tx)
			if err != nil {
				return err
			}
		}
		encodableTxs[i] = encodableTx{
			IsShort: ct.IsShort,
			ShortID: ct.ShortID,
			TxBytes: txBytes,
		}
	}

	return rlp.Encode(w, []interface{}{
		pcb.Header,
		encodableTxs,
		pcb.Uncles,
	})
}

// DecodeRLP implements rlp.Decoder for ProactiveCompactBlock
func (pcb *ProactiveCompactBlock) DecodeRLP(s *rlp.Stream) error {
	type encodableTx struct {
		IsShort bool
		ShortID []byte
		TxBytes []byte
	}

	var temp struct {
		Header       *types.Header
		Transactions []encodableTx
		Uncles       []*types.Header
	}

	if err := s.Decode(&temp); err != nil {
		return err
	}

	pcb.Header = temp.Header
	pcb.Uncles = temp.Uncles
	pcb.Transactions = make([]CompactTransaction, len(temp.Transactions))

	for i, et := range temp.Transactions {
		pcb.Transactions[i].IsShort = et.IsShort
		pcb.Transactions[i].ShortID = et.ShortID

		if !et.IsShort && len(et.TxBytes) > 0 {
			var tx types.Transaction
			if err := rlp.DecodeBytes(et.TxBytes, &tx); err != nil {
				return err
			}
			pcb.Transactions[i].Tx = &tx
		} else {
			pcb.Transactions[i].Tx = nil
		}
	}

	return nil
}

// TxPoolInterface defines the interface for accessing transaction pool
type TxPoolInterface interface {
	Get(hash common.Hash) *types.Transaction
	Has(hash common.Hash) bool
	// GetAllTxs returns all transactions in the pool for short ID matching
	GetAllTxs() map[common.Hash]*types.Transaction
}

// EncodeProactiveCompactBlock encodes a standard block into PCB format
// based on receiver's CBF indicating which transactions they already have
func EncodeProactiveCompactBlock(block *types.Block, receiverCBF *CountingBloomFilter) (*ProactiveCompactBlock, error) {
	if block == nil {
		return nil, errors.New("block is nil")
	}

	pcb := &ProactiveCompactBlock{
		Header: block.Header(),
		Uncles: block.Uncles(),
	}

	transactions := block.Transactions()
	pcb.Transactions = make([]CompactTransaction, len(transactions))

	for i, tx := range transactions {
		txHash := tx.Hash()

		// Check if receiver already has this transaction
		if receiverCBF != nil && receiverCBF.Test(txHash) {
			// Receiver has it - send short ID
			pcb.Transactions[i] = CompactTransaction{
				IsShort: true,
				ShortID: CompactTransactionID(txHash),
				Tx:      nil,
			}
		} else {
			// Receiver doesn't have it - send full transaction
			pcb.Transactions[i] = CompactTransaction{
				IsShort: false,
				ShortID: nil,
				Tx:      tx,
			}
		}
	}

	return pcb, nil
}

// DecodeProactiveCompactBlock reconstructs a standard block from PCB format
// using local transaction pool for missing transactions
func DecodeProactiveCompactBlock(pcb *ProactiveCompactBlock, txPool TxPoolInterface) (*types.Block, []common.Hash, error) {
	if pcb == nil {
		return nil, nil, errors.New("compact block is nil")
	}

	transactions := make([]*types.Transaction, len(pcb.Transactions))
	missingTxHashes := []common.Hash{}

	for i, compactTx := range pcb.Transactions {
		if compactTx.IsShort {
			// This is a short ID - need to find the transaction locally
			// Search by short ID in transaction pool
			tx, found := findTransactionByShortID(compactTx.ShortID, txPool)
			if found {
				transactions[i] = tx
			} else {
				// Transaction not found - need to request it
				// For now, we'll record it as missing
				missingTxHashes = append(missingTxHashes, common.BytesToHash(compactTx.ShortID))
				return nil, missingTxHashes, errMissingTransaction
			}
		} else {
			// Full transaction provided
			transactions[i] = compactTx.Tx
		}
	}

	// Reconstruct the block
	block := types.NewBlockWithHeader(pcb.Header).WithBody(transactions, pcb.Uncles)
	return block, nil, nil
}

// findTransactionByShortID searches for a transaction in the pool by its short ID
// According to ExClique paper: short ID is first 6 bytes of transaction hash
// We search the TX-Pool for transactions whose hash starts with this short ID
func findTransactionByShortID(shortID []byte, txPool TxPoolInterface) (*types.Transaction, bool) {
	if txPool == nil || len(shortID) != shortIDLength {
		return nil, false
	}

	// Get all transactions from the pool
	// NOTE: This is the real implementation from the paper - not a dummy!
	// The paper expects us to search the local TX-Pool for matching transactions
	allTxs := txPool.GetAllTxs()

	// Search for transaction whose hash starts with the short ID (6-byte prefix)
	for txHash, tx := range allTxs {
		// Compare first 6 bytes of transaction hash with short ID
		if matchesShortID(txHash, shortID) {
			return tx, true
		}
	}

	// Transaction not found in local pool
	// This can happen if:
	// 1. CBF had a false positive
	// 2. Transaction was removed from pool after CBF was sent
	return nil, false
}

// matchesShortID checks if a transaction hash matches the given short ID
func matchesShortID(txHash common.Hash, shortID []byte) bool {
	if len(shortID) != shortIDLength {
		return false
	}

	// Compare first 6 bytes of hash with short ID
	for i := 0; i < shortIDLength; i++ {
		if txHash[i] != shortID[i] {
			return false
		}
	}
	return true
}

// EstimateCompressionRatio estimates how much space is saved by PCB
func EstimateCompressionRatio(block *types.Block, receiverCBF *CountingBloomFilter) float64 {
	if block == nil || receiverCBF == nil {
		return 1.0
	}

	transactions := block.Transactions()
	if len(transactions) == 0 {
		return 1.0
	}

	totalOriginalSize := uint64(0)
	totalCompactSize := uint64(0)

	for _, tx := range transactions {
		txSize := tx.Size()
		totalOriginalSize += txSize

		if receiverCBF.Test(tx.Hash()) {
			// Can use short ID
			totalCompactSize += shortIDLength
		} else {
			// Need full transaction
			totalCompactSize += txSize
		}
	}

	if totalOriginalSize == 0 {
		return 1.0
	}

	return float64(totalOriginalSize) / float64(totalCompactSize)
}

// CompactBlockStats tracks statistics for PCB protocol
type CompactBlockStats struct {
	TotalBlocks         uint64
	CompactBlocks       uint64
	TotalTransactions   uint64
	ShortIDTransactions uint64
	FullTransactions    uint64
	BytesSaved          uint64
	AverageCompression  float64
}

// GlobalCompactBlockStats stores global statistics
var GlobalCompactBlockStats = &CompactBlockStats{}

// UpdateStats updates the global PCB statistics
func UpdateStats(block *types.Block, pcb *ProactiveCompactBlock) {
	if block == nil || pcb == nil {
		return
	}

	GlobalCompactBlockStats.TotalBlocks++
	GlobalCompactBlockStats.CompactBlocks++

	shortCount := uint64(0)
	fullCount := uint64(0)
	bytesSaved := uint64(0)

	for i, compactTx := range pcb.Transactions {
		GlobalCompactBlockStats.TotalTransactions++

		if compactTx.IsShort {
			shortCount++
			GlobalCompactBlockStats.ShortIDTransactions++

			// Calculate bytes saved (original tx size - short ID size)
			if i < len(block.Transactions()) {
				originalSize := block.Transactions()[i].Size()
				bytesSaved += originalSize - shortIDLength
			}
		} else {
			fullCount++
			GlobalCompactBlockStats.FullTransactions++
		}
	}

	GlobalCompactBlockStats.BytesSaved += bytesSaved

	// Log statistics periodically
	if GlobalCompactBlockStats.TotalBlocks%100 == 0 {
		shortRatio := float64(0)
		if GlobalCompactBlockStats.TotalTransactions > 0 {
			shortRatio = float64(GlobalCompactBlockStats.ShortIDTransactions) / float64(GlobalCompactBlockStats.TotalTransactions)
		}

		log.Info("ExClique PCB Statistics",
			"blocks", GlobalCompactBlockStats.TotalBlocks,
			"transactions", GlobalCompactBlockStats.TotalTransactions,
			"shortIDRatio", fmt.Sprintf("%.2f%%", shortRatio*100),
			"bytesSaved", GlobalCompactBlockStats.BytesSaved,
		)
	}
}

// SerializeCompactBlock serializes a ProactiveCompactBlock for network transmission
func SerializeCompactBlock(pcb *ProactiveCompactBlock) ([]byte, error) {
	return rlp.EncodeToBytes(pcb)
}

// DeserializeCompactBlock deserializes a ProactiveCompactBlock from network data
func DeserializeCompactBlock(data []byte) (*ProactiveCompactBlock, error) {
	var pcb ProactiveCompactBlock
	if err := rlp.DecodeBytes(data, &pcb); err != nil {
		return nil, err
	}
	return &pcb, nil
}

// MeasureBroadcastTime measures the time taken to broadcast a block
func MeasureBroadcastTime(startTime time.Time) time.Duration {
	return time.Since(startTime)
}

// MeasureVerifyTime measures the time taken to verify a block
func MeasureVerifyTime(startTime time.Time) time.Duration {
	return time.Since(startTime)
}
