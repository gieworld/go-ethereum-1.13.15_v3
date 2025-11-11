// Copyright 2025 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// ExClique: Counting Bloom Filter implementation for Proactive Compact Block

package clique

import (
	"hash/fnv"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rlp"
)

const (
	// CBF parameters optimized for transaction tracking
	cbfSize      = 65536 // Number of counters (64KB for 4-bit counters)
	cbfHashCount = 4     // Number of hash functions
	maxCounter   = 15    // Maximum counter value (4-bit counter)
)

// CountingBloomFilter is a space-efficient probabilistic data structure
// that supports both add and remove operations for tracking transactions
type CountingBloomFilter struct {
	counters [cbfSize]uint8 // 4-bit counters (2 per byte)
	size     uint
	hashNum  uint
	mu       sync.RWMutex
}

// NewCountingBloomFilter creates a new counting bloom filter
func NewCountingBloomFilter() *CountingBloomFilter {
	return &CountingBloomFilter{
		size:    cbfSize,
		hashNum: cbfHashCount,
	}
}

// hashFunctions generates multiple hash values for a given key
func (cbf *CountingBloomFilter) hashFunctions(data []byte) []uint {
	hashes := make([]uint, cbf.hashNum)

	// Use FNV hash as base
	h1 := fnv.New64a()
	h1.Write(data)
	hash1 := h1.Sum64()

	// Generate multiple hashes using double hashing technique
	for i := uint(0); i < cbf.hashNum; i++ {
		// h(i) = (hash1 + i * hash2) mod size
		hash2 := hash1 >> 32
		combinedHash := (hash1 + uint64(i)*hash2)
		hashes[i] = uint(combinedHash % uint64(cbf.size))
	}

	return hashes
}

// getCounter retrieves the counter value at the given position
func (cbf *CountingBloomFilter) getCounter(pos uint) uint8 {
	byteIndex := pos / 2
	isHighNibble := pos%2 == 0

	if isHighNibble {
		return (cbf.counters[byteIndex] >> 4) & 0x0F
	}
	return cbf.counters[byteIndex] & 0x0F
}

// setCounter sets the counter value at the given position
func (cbf *CountingBloomFilter) setCounter(pos uint, value uint8) {
	if value > maxCounter {
		value = maxCounter
	}

	byteIndex := pos / 2
	isHighNibble := pos%2 == 0

	if isHighNibble {
		cbf.counters[byteIndex] = (cbf.counters[byteIndex] & 0x0F) | (value << 4)
	} else {
		cbf.counters[byteIndex] = (cbf.counters[byteIndex] & 0xF0) | value
	}
}

// Add adds an element to the counting bloom filter
func (cbf *CountingBloomFilter) Add(txHash common.Hash) {
	cbf.mu.Lock()
	defer cbf.mu.Unlock()

	hashes := cbf.hashFunctions(txHash.Bytes())
	for _, pos := range hashes {
		counter := cbf.getCounter(pos)
		if counter < maxCounter {
			cbf.setCounter(pos, counter+1)
		}
	}
}

// Remove removes an element from the counting bloom filter
func (cbf *CountingBloomFilter) Remove(txHash common.Hash) {
	cbf.mu.Lock()
	defer cbf.mu.Unlock()

	hashes := cbf.hashFunctions(txHash.Bytes())
	for _, pos := range hashes {
		counter := cbf.getCounter(pos)
		if counter > 0 {
			cbf.setCounter(pos, counter-1)
		}
	}
}

// Test checks if an element might be in the set
// Returns true if element is probably in the set, false if definitely not
func (cbf *CountingBloomFilter) Test(txHash common.Hash) bool {
	cbf.mu.RLock()
	defer cbf.mu.RUnlock()

	hashes := cbf.hashFunctions(txHash.Bytes())
	for _, pos := range hashes {
		if cbf.getCounter(pos) == 0 {
			return false
		}
	}
	return true
}

// Clear resets all counters to zero
func (cbf *CountingBloomFilter) Clear() {
	cbf.mu.Lock()
	defer cbf.mu.Unlock()

	for i := range cbf.counters {
		cbf.counters[i] = 0
	}
}

// Encode serializes the CBF for network transmission
func (cbf *CountingBloomFilter) Encode() ([]byte, error) {
	cbf.mu.RLock()
	defer cbf.mu.RUnlock()

	return rlp.EncodeToBytes(cbf.counters[:])
}

// Decode deserializes the CBF from network data
func (cbf *CountingBloomFilter) Decode(data []byte) error {
	cbf.mu.Lock()
	defer cbf.mu.Unlock()

	var counters [cbfSize]uint8
	if err := rlp.DecodeBytes(data, &counters); err != nil {
		return err
	}

	cbf.counters = counters
	return nil
}

// CompactTransactionID generates a 6-byte short ID for a transaction
func CompactTransactionID(txHash common.Hash) []byte {
	// Use first 6 bytes of the hash as short ID
	return txHash[:shortIDLength]
}

// EstimateFalsePositiveRate estimates the false positive rate
func (cbf *CountingBloomFilter) EstimateFalsePositiveRate(numItems uint) float64 {
	// Formula: (1 - e^(-k*n/m))^k
	// where k = number of hash functions, n = number of items, m = filter size
	k := float64(cbf.hashNum)
	n := float64(numItems)
	m := float64(cbf.size)

	// Simplified approximation
	exp := -k * n / m
	prob := 1.0

	// Approximate e^exp
	for i := 0; i < 10; i++ {
		prob += exp
		exp *= exp / float64(i+2)
	}

	// (1 - e^(-k*n/m))^k
	result := 1.0 - prob
	for i := uint(1); i < cbf.hashNum; i++ {
		result *= (1.0 - prob)
	}

	return result
}
