package service

import (
	"context"
	"sync"
	"time"

	"../protocol"
)

const (
	defaultResultsCapacity     = 1000
	defaultResultsReleaseDelay = time.Second
)

type ResultSinkFunc func(data interface{})

type resultEntry struct {
	*protocol.Result
	cid     string
	expires time.Time
}

type resultStore struct {
	// handle results
	entries      []*resultEntry // hashing nodes store the results here, while awaiting ack of reception by requester
	idx          map[string]int // index to look up resultentry by
	counter      int            // amount of results stored in resultsCounter
	releaseDelay time.Duration  // time before a result expires and should be passed to sinkFunc
	sinkFunc     ResultSinkFunc // callback to pass data to when result has expired

	mu  sync.RWMutex
	ctx context.Context
}

func newResultStore(ctx context.Context, sinkFunc ResultSinkFunc) *resultStore {
	if sinkFunc == nil {
		panic("yikes, resultsStore.sinkFunc is nil")
	}
	return &resultStore{
		entries:      make([]*resultEntry, defaultResultsCapacity),
		idx:          make(map[string]int),
		releaseDelay: defaultResultsReleaseDelay,
		sinkFunc:     sinkFunc,
		ctx:          ctx,
	}
}

func (self *resultStore) Put(id string, res *protocol.Result) bool {
	self.mu.Lock()
	defer self.mu.Unlock()
	if self.full() {
		return false
	}
	self.entries[self.counter] = &resultEntry{
		Result:  res,
		cid:     id,
		expires: time.Now().Add(self.releaseDelay),
	}
	self.idx[id] = self.counter
	self.counter++
	return true
}

func (self *resultStore) Get(id string) *protocol.Result {
	self.mu.RLock()
	defer self.mu.RUnlock()
	return self.entries[self.idx[id]].Result
}

func (self *resultStore) Del(id string) {
	self.mu.Lock()
	defer self.mu.Unlock()
	self.del(id)
}

func (self *resultStore) del(id string) {
	if i, ok := self.idx[id]; ok {
		self.entries[i] = self.entries[self.counter-1]
		delete(self.idx, id)
		self.counter--
		if self.counter > 0 {
			self.idx[self.entries[i].cid] = i
		}
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
	return self.full()
}

func (self *resultStore) full() bool {
	return self.counter == cap(self.entries)
}

func (self *resultStore) Start() {
	go func() {
		for {
			timer := time.NewTimer(self.releaseDelay)
			select {
			case <-self.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			self.prune()
		}
	}()
}

// BUG: goes through entries that have deleted indicies
// protbably index shuould have expiry, not entry
func (self *resultStore) prune() {
	c := self.Count()
	for i := 0; i < c; i++ {
		self.mu.Lock()
		e := self.entries[i]
		if e.expires.Before(time.Now()) {
			self.del(e.cid)
			self.sinkFunc(e.Result)
		}
		self.mu.Unlock()
	}
}
