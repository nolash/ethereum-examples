package service

import (
	"sync"
	"time"

	"../protocol"
)

const (
	defaultResultsCapacity     = 1000
	defaultResultsReleaseDelay = time.Second
)

type ResultSinkFunc func(data []byte)

type resultEntry struct {
	expires time.Time
	result  *protocol.Result
}

type resultStore struct {
	// handle results
	entries      map[uint64]*protocol.Result // hashing nodes store the results here, while awaiting ack of reception by requester
	counter      int                         // amount of results stored in resultsCounter
	capacity     int
	releaseDelay time.Duration
	sinkFunc     ResultSinkFunc // callback to pass data to when result has expired

	mu sync.RWMutex
}

func newResultStore(sinkFunc ResultSinkFunc) *resultStore {
	if sinkFunc == nil {
		panic("yikes, resultsStore.sinkFunc is nil")
	}
	return &resultStore{
		entries:      make(map[uint64]*protocol.Result),
		capacity:     defaultResultsCapacity,
		releaseDelay: defaultResultsReleaseDelay,
		sinkFunc:     sinkFunc,
	}
}

func (self *resultStore) Put(res *protocol.Result) {
	self.mu.Lock()
	defer self.mu.Unlock()
	self.entries[res.Id] = res
	self.counter++
}

func (self *resultStore) Get(id uint64) *protocol.Result {
	self.mu.RLock()
	defer self.mu.RUnlock()
	return self.entries[id]
}

func (self *resultStore) Del(id uint64) {
	self.mu.Lock()
	defer self.mu.Unlock()
	if _, ok := self.entries[id]; ok {
		delete(self.entries, id)
		self.counter--
	}
}

func (self *resultStore) Count() int {
	self.mu.RLock()
	defer self.mu.RUnlock()
	return self.counter
}

func (self *resultStore) IsFull() bool {
	self.mu.RLock()
	defer self.mu.RUnlock()
	return self.counter == self.capacity
}

func (self *resultStore) start() {

}

func (self *resultStore) stop() {

}

func (self *resultStore) prune() {

}
