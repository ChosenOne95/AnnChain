// Copyright 2017 ZhongAn Information Technology Services Co.,Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mempool

import (
	"container/list"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/spf13/viper"

	auto "github.com/dappledger/AnnChain/gemmill/modules/go-autofile"
	"github.com/dappledger/AnnChain/gemmill/modules/go-clist"
	gcmn "github.com/dappledger/AnnChain/gemmill/modules/go-common"
	"github.com/dappledger/AnnChain/gemmill/types"
)

const cacheSize = 100000

var (
	ErrTxInCache = errors.New("Tx already exists in cache")
)

type Mempool struct {
	config  *viper.Viper
	mtx     sync.Mutex
	txs     *clist.CList // concurrent linked-list of good txs
	counter int64        // simple incrementing counter
	height  int64        // the last block Update()'d to

	// Keep a cache of already-seen txs.
	cache *txCache

	wal *auto.AutoFile // A log of mempool txs

	txLimit int

	txFilters []types.IFilter
}

func NewMempool(conf *viper.Viper) *Mempool {
	mempool := &Mempool{
		config:  conf,
		txs:     clist.New(),
		counter: 0,
		height:  0,
		cache:   newTxCache(cacheSize),
		txLimit: conf.GetInt("block_size") * 2,
	}
	mempool.initWAL()
	return mempool
}

func (mem *Mempool) GetPendingMaxNonce(byt []byte) (uint64, error) {
	return 0, errors.New("raft consense please use GetNonce() interface")
}

func (mem *Mempool) RegisterFilter(filter types.IFilter) {
	mem.txFilters = append(mem.txFilters, filter)
}

// consensus must be able to hold lock to safely update
func (mem *Mempool) Lock() {
	mem.mtx.Lock()
}

func (mem *Mempool) Unlock() {
	mem.mtx.Unlock()
}

// Number of transactions in the mempool clist
func (mem *Mempool) Size() int {
	return mem.txs.Len()
}

// Remove all transactions from mempool and cache
func (mem *Mempool) Flush() {
	mem.Lock()
	mem.cache.Reset()
	for e := mem.txs.Front(); e != nil; e = e.Next() {
		mem.txs.Remove(e)
		e.DetachPrev()
	}
	mem.Unlock()
}

// Return the first element of mem.txs for peer goroutines to call .NextWait() on.
// Blocks until txs has elements.
func (mem *Mempool) TxsFrontWait() *clist.CElement {
	return mem.txs.FrontWait()
}

// Try a new transaction in the mempool.
// Potentially blocking if we're blocking on Update() or Reap().
// cb: A callback from the CheckTx command.
//     It gets called from another goroutine.
// CONTRACT: Either cb will get called, or err returned.
func (mem *Mempool) ReceiveTx(tx types.Tx) (err error) {
	if mem.cache.Exists(tx) {
		return ErrTxInCache
	}
	if mem.config.GetBool("mempool_enable_txs_limits") && mem.txs.Len() > mem.txLimit {
		return errors.New("Too many unsolved TX (rejected)")
	}
	if err := mem.checkTxWithFilters(tx); err != nil {
		return errors.New("plugin checktx failed with error: " + err.Error())
	}
	// TODO: remove this wal, mempool lost may be durable
	if mem.wal != nil {
		mem.wal.Write([]byte(tx))
		mem.wal.Write([]byte("\n"))
	}

	// reach here means the tx can be put into mempool, we just leave the original machanism untouched
	if !mem.cache.Push(tx) {
		return ErrTxInCache
	}

	nc := atomic.AddInt64(&mem.counter, 1)
	memTx := &types.TxInPool{
		Counter: nc,
		Height:  atomic.LoadInt64(&mem.height),
		Tx:      tx,
	}
	mem.txs.PushBack(memTx)

	return nil
}

// Get the valid transactions remaining
// If maxTxs is -1, there is no cap on returned transactions.
func (mem *Mempool) Reap(maxTxs int) []types.Tx {
	mem.Lock()
	txs := mem.collectTxs(maxTxs)
	mem.Unlock()
	return txs
}

// Tell mempool that these txs were committed.
// Mempool will discard these txs.
// NOTE: this should be called *after* block is committed by consensus.
// NOTE: unsafe; Lock/Unlock must be managed by caller
func (mem *Mempool) Update(height int64, txs []types.Tx) {
	// First, create a lookup map of txns in new txs.
	txsMap := make(map[string]struct{})
	for _, tx := range txs {
		txsMap[string(tx)] = struct{}{}
	}

	// Set height
	atomic.StoreInt64(&mem.height, height)

	mem.Lock()
	// Remove transactions that are already in txs, also re-run txs through filters
	mem.refreshMempoolTxs(txsMap)
	mem.Unlock()
}

// maxTxs: -1 means uncapped, 0 means none
func (mem *Mempool) collectTxs(maxTxs int) []types.Tx {
	if maxTxs == 0 {
		return []types.Tx{}
	} else if maxTxs < 0 {
		maxTxs = mem.txs.Len()
	} else {
		maxTxs = gcmn.MinInt(mem.txs.Len(), maxTxs)
	}
	txs := make([]types.Tx, 0, maxTxs)
	for e := mem.txs.Front(); e != nil && len(txs) < maxTxs; e = e.Next() {
		memTx := e.Value.(*types.TxInPool)
		txs = append(txs, memTx.Tx)
	}
	return txs
}

func (mem *Mempool) refreshMempoolTxs(blockTxsMap map[string]struct{}) {
	txsLen := mem.txs.Len()
	index := 0
	for e := mem.txs.Front(); e != nil && index < txsLen; e = e.Next() {
		index++
		memTx := e.Value.(*types.TxInPool)
		// Remove the tx if it's alredy in a block, or rechecking fails
		if _, ok := blockTxsMap[string(memTx.Tx)]; ok {
			mem.txs.Remove(e)
			e.DetachPrev()
			mem.cache.Remove(memTx.Tx)
		} else if err := mem.recheckTx(memTx.Tx); err != nil {
			mem.txs.Remove(e)
			e.DetachPrev()
			mem.cache.Remove(memTx.Tx)
		}
	}
}

func (mem *Mempool) recheckTx(tx types.Tx) error {
	return mem.checkTxWithFilters(tx)
}

func (mem *Mempool) checkTxWithFilters(tx types.Tx) error {
	if mem.txFilters == nil || len(mem.txFilters) == 0 {
		return nil
	}
	for _, p := range mem.txFilters {
		_, err := p.CheckTx(tx)
		if err != nil {
			return err
		}
	}
	return nil
}

func (mem *Mempool) initWAL() {
	walDir := mem.config.GetString("mempool_wal_dir")
	if walDir != "" {
		err := gcmn.EnsureDir(walDir, 0700)
		if err != nil {
			gcmn.PanicSanity(err)
		}
		af, err := auto.OpenAutoFile(walDir + "/wal")
		if err != nil {
			gcmn.PanicSanity(err)
		}
		mem.wal = af
	}
}

func (mem *Mempool) _addToTxs(tx types.Tx) {
	nc := atomic.AddInt64(&mem.counter, 1)
	memTx := &types.TxInPool{
		Counter: nc,
		Height:  atomic.LoadInt64(&mem.height),
		Tx:      tx,
	}
	mem.txs.PushBack(memTx)
}

func (mem *Mempool) _checkTx(tx types.Tx) error {
	if err := mem.checkTxWithFilters(tx); err != nil {
		return errors.New("plugin checktx failed with error: " + err.Error())
	}
	return nil
}

//--------------------------------------------------------------------------------

//--------------------------------------------------------------------------------

type txCache struct {
	mtx      sync.Mutex
	size     int
	checkMap map[string]struct{}
	list     *list.List // to remove oldest tx when cache gets too big
}

func newTxCache(cacheSize int) *txCache {
	return &txCache{
		size:     cacheSize,
		checkMap: make(map[string]struct{}, cacheSize),
		list:     list.New(),
	}
}

func (cache *txCache) Reset() {
	cache.mtx.Lock()
	cache.checkMap = make(map[string]struct{}, cacheSize)
	cache.list.Init()
	cache.mtx.Unlock()
}

func (cache *txCache) Exists(tx types.Tx) bool {
	cache.mtx.Lock()
	_, exists := cache.checkMap[string(tx)]
	cache.mtx.Unlock()
	return exists
}

// Returns false if tx is in cache.
func (cache *txCache) Push(tx types.Tx) bool {
	cache.mtx.Lock()
	defer cache.mtx.Unlock()

	if _, exists := cache.checkMap[string(tx)]; exists {
		return false
	}

	if cache.list.Len() >= cache.size {
		popped := cache.list.Front()
		poppedTx := popped.Value.(types.Tx)
		// NOTE: the tx may have already been removed from the map
		// but deleting a non-existant element is fine
		delete(cache.checkMap, string(poppedTx))
		cache.list.Remove(popped)
	}
	cache.checkMap[string(tx)] = struct{}{}
	cache.list.PushBack(tx)
	return true
}

func (cache *txCache) Remove(tx types.Tx) {
	cache.mtx.Lock()
	delete(cache.checkMap, string(tx))
	cache.mtx.Unlock()
}
