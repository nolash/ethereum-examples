package service

import (
	"context"
	"fmt"
	"os"
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
	prid    protocol.ID // was result.ID?
	expires time.Time
}

type resultStore struct {
	// handle results
	entries      []*resultEntry // hashing nodes store the results here, while awaiting ack of reception by requester
	idx          sync.Map       // index to look up resultentry by
	counter      int            // amount of results stored in resultsCounter
	capacity     int            // amount of results possible to store
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
		entries: make([]*resultEntry, defaultResultsCapacity),
		//idx:          make(map[protocol.ID]int),
		releaseDelay: defaultResultsReleaseDelay,
		capacity:     defaultResultsCapacity,
		sinkFunc:     sinkFunc,
		ctx:          ctx,
	}
}

func (self *resultStore) Put(id protocol.ID, res *protocol.Result) bool {
	self.mu.Lock()
	defer self.mu.Unlock()
	if self.full() {
		return false
	}
	self.entries[self.counter] = &resultEntry{
		Result:  res,
		prid:    id,
		expires: time.Now().Add(self.releaseDelay),
	}
	self.idx.Store(id, self.counter)
	//self.idx[id] = self.counter
	fmt.Fprintf(os.Stderr, ">>>>>>>>>>>>>> adding %x idx %d\n", id, self.counter)
	self.counter++
	return true
}

func (self *resultStore) Get(id protocol.ID) *protocol.Result {
	self.mu.RLock()
	defer self.mu.RUnlock()
	n, ok := self.idx.Load(id)
	if !ok {
		return nil
	}
	return self.entries[n.(int)].Result
}

func (self *resultStore) Del(id protocol.ID) {
	self.mu.Lock()
	defer self.mu.Unlock()
	self.del(id)
}

func (self *resultStore) del(id protocol.ID) {
	if n, ok := self.idx.Load(id); ok {
		fmt.Fprintf(os.Stderr, ">>>>>>>>>>>>>>> removing %x idx %d\n", id, n.(int))
		self.entries[n.(int)] = self.entries[self.counter-1]
		self.idx.Delete(id)
		self.counter--
		if self.counter >= 0 {
			self.idx.Store(self.entries[n.(int)].prid, n.(int))
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
	return self.counter == self.capacity
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

// TODO: this procedure needs priority control, so it doesn't block for too long
func (self *resultStore) prune() {
	self.mu.Lock()
	var prids []protocol.ID
	//for k, n := range self.idx {
	self.idx.Range(func(k interface{}, n interface{}) bool {
		prids = append(prids, k.(protocol.ID))
		fmt.Fprintf(os.Stderr, ">>>>>>>>>>>>>>>>>>>>>>>> have key %08x idx %d\n", k.(protocol.ID), n.(int))
		return true
	})
	//}
	self.mu.Unlock()
	for _, prid := range prids {
		self.mu.Lock()
		n, _ := self.idx.Load(prid)
		e := self.entries[n.(int)]
		if e.expires.Before(time.Now()) {
			self.del(prid)
			self.sinkFunc(e.Result)
		}
		self.mu.Unlock()
	}
	fmt.Fprintf(os.Stderr, ">>>>>>> prune done\n")
}
