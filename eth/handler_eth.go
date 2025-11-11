// Copyright 2020 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package eth

import (
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/clique"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/txpool"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth/protocols/eth"
	"github.com/ethereum/go-ethereum/p2p/enode"
)

// ethHandler implements the eth.Backend interface to handle the various network
// packets that are sent as replies or broadcasts.
type ethHandler handler

func (h *ethHandler) Chain() *core.BlockChain { return h.chain }
func (h *ethHandler) TxPool() eth.TxPool      { return h.txpool }

// RunPeer is invoked when a peer joins on the `eth` protocol.
func (h *ethHandler) RunPeer(peer *eth.Peer, hand eth.Handler) error {
	return (*handler)(h).runEthPeer(peer, hand)
}

// PeerInfo retrieves all known `eth` information about a peer.
func (h *ethHandler) PeerInfo(id enode.ID) interface{} {
	if p := h.peers.peer(id.String()); p != nil {
		return p.info()
	}
	return nil
}

// AcceptTxs retrieves whether transaction processing is enabled on the node
// or if inbound transactions should simply be dropped.
func (h *ethHandler) AcceptTxs() bool {
	return h.synced.Load()
}

// HandleCompactBlock processes a received ProactiveCompactBlock (ExClique PCB protocol)
func (h *ethHandler) HandleCompactBlock(peer *eth.Peer, pcbData []byte, td *big.Int) error {
	// Check if we're using Clique consensus
	if _, ok := h.chain.Engine().(*clique.Clique); !ok {
		// Not using Clique, fallback to standard block handling
		// This shouldn't happen in ExClique network, but handle gracefully
		return fmt.Errorf("compact block received but not using Clique consensus")
	}

	// Deserialize the compact block
	pcb, err := clique.DeserializeCompactBlock(pcbData)
	if err != nil {
		return fmt.Errorf("failed to deserialize compact block: %w", err)
	}

	// Create a simple adapter for the TX-Pool interface
	txPoolAdapter := &txPoolAdapter{pool: h.txpool}

	// Decode PCB using local TX-Pool
	block, _, err := clique.DecodeProactiveCompactBlock(pcb, txPoolAdapter)

	if err != nil {
		// Decode failed - peer will need to send full block
		// This can happen if:
		// 1. CBF false positive (we don't actually have the TX)
		// 2. TX was removed from pool after CBF sync
		// 3. Short ID collision (very rare)
		// Peer should fallback to sending full block on next attempt
		return fmt.Errorf("failed to decode compact block: %w", err)
	}

	// Successfully reconstructed block, process it normally
	return h.handleBlockBroadcast(peer, block, td)
}

// txPoolAdapter adapts eth.txPool to clique.TxPoolInterface
type txPoolAdapter struct {
	pool txPool
}

func (a *txPoolAdapter) Get(hash common.Hash) *types.Transaction {
	return a.pool.Get(hash)
}

func (a *txPoolAdapter) Has(hash common.Hash) bool {
	return a.pool.Has(hash)
}

func (a *txPoolAdapter) GetAllTxs() map[common.Hash]*types.Transaction {
	// Get pending transactions from the pool
	// This is a simplified implementation - in production, you might want
	// to cache this or implement more efficiently
	pending := a.pool.Pending(txpool.PendingFilter{})

	allTxs := make(map[common.Hash]*types.Transaction)
	for _, txList := range pending {
		for _, lazyTx := range txList {
			tx := lazyTx.Resolve()
			if tx != nil {
				allTxs[tx.Hash()] = tx
			}
		}
	}
	return allTxs
}

// Handle is invoked from a peer's message handler when it receives a new remote
// message that the handler couldn't consume and serve itself.
func (h *ethHandler) Handle(peer *eth.Peer, packet eth.Packet) error {
	// Consume any broadcasts and announces, forwarding the rest to the downloader
	switch packet := packet.(type) {
	case *eth.NewBlockHashesPacket:
		hashes, numbers := packet.Unpack()
		return h.handleBlockAnnounces(peer, hashes, numbers)

	case *eth.NewBlockPacket:
		return h.handleBlockBroadcast(peer, packet.Block, packet.TD)

	case *eth.NewPooledTransactionHashesPacket:
		return h.txFetcher.Notify(peer.ID(), packet.Types, packet.Sizes, packet.Hashes)

	case *eth.TransactionsPacket:
		for _, tx := range *packet {
			if tx.Type() == types.BlobTxType {
				return errors.New("disallowed broadcast blob transaction")
			}
		}
		return h.txFetcher.Enqueue(peer.ID(), *packet, false)

	case *eth.PooledTransactionsResponse:
		return h.txFetcher.Enqueue(peer.ID(), *packet, true)

	default:
		return fmt.Errorf("unexpected eth packet type: %T", packet)
	}
}

// handleBlockAnnounces is invoked from a peer's message handler when it transmits a
// batch of block announcements for the local node to process.
func (h *ethHandler) handleBlockAnnounces(peer *eth.Peer, hashes []common.Hash, numbers []uint64) error {
	// Drop all incoming block announces from the p2p network if
	// the chain already entered the pos stage and disconnect the
	// remote peer.
	if h.merger.PoSFinalized() {
		return errors.New("disallowed block announcement")
	}
	// Schedule all the unknown hashes for retrieval
	var (
		unknownHashes  = make([]common.Hash, 0, len(hashes))
		unknownNumbers = make([]uint64, 0, len(numbers))
	)
	for i := 0; i < len(hashes); i++ {
		if !h.chain.HasBlock(hashes[i], numbers[i]) {
			unknownHashes = append(unknownHashes, hashes[i])
			unknownNumbers = append(unknownNumbers, numbers[i])
		}
	}
	for i := 0; i < len(unknownHashes); i++ {
		h.blockFetcher.Notify(peer.ID(), unknownHashes[i], unknownNumbers[i], time.Now(), peer.RequestOneHeader, peer.RequestBodies)
	}
	return nil
}

// handleBlockBroadcast is invoked from a peer's message handler when it transmits a
// block broadcast for the local node to process.
func (h *ethHandler) handleBlockBroadcast(peer *eth.Peer, block *types.Block, td *big.Int) error {
	// Drop all incoming block announces from the p2p network if
	// the chain already entered the pos stage and disconnect the
	// remote peer.
	if h.merger.PoSFinalized() {
		return errors.New("disallowed block broadcast")
	}
	// Schedule the block for import
	h.blockFetcher.Enqueue(peer.ID(), block)

	// Assuming the block is importable by the peer, but possibly not yet done so,
	// calculate the head hash and TD that the peer truly must have.
	var (
		trueHead = block.ParentHash()
		trueTD   = new(big.Int).Sub(td, block.Difficulty())
	)
	// Update the peer's total difficulty if better than the previous
	if _, td := peer.Head(); trueTD.Cmp(td) > 0 {
		peer.SetHead(trueHead, trueTD)
		h.chainSync.handlePeerEvent()
	}
	return nil
}
