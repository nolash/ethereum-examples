package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/protocols"
	"github.com/ethereum/go-ethereum/rpc"

	"../protocol"
)

const (
	defaultSubmitsDelay    = time.Second
	defaultSubmitsCapacity = 1000
	defaultResultsCapacity = 1000
)

// DemoService implements the node.Service interface
type DemoService struct {
	maxJobs         int                          // maximum number of simultaneous hashing jobs the node will accept
	currentJobs     int                          // how many jobs currently executing
	maxDifficulty   uint8                        // the maximum difficulty of jobs this node will handle
	maxTimePerJob   time.Duration                // maximum time one hashing job will run
	workers         map[*protocols.Peer]uint8    // an address book of hasher peers for nodes that send requests
	submitLastId    uint64                       // last request id sent from this node
	submits         []*protocol.Request          // a wrapping array cache of requests used to retrieve the request data on a result response
	submitsCursor   int                          // the current write position on the wrapping array cache
	submitsIdx      map[uint64]*protocol.Request // index to look up the request cache though a request id
	submitsCapacity int                          // size of request cache (wrap threshold)
	results         map[uint64]*protocol.Result  // hashing nodes store the results here, while awaiting ack of reception by requester
	resultsCounter  int                          // amount of results stored in resultsCounter
	resultsCapacity int

	mu     sync.RWMutex
	ctx    context.Context
	cancel func()
}

func NewDemoService(maxDifficulty uint8, maxJobs int, maxTimePerJob time.Duration) *DemoService {
	ctx, cancel := context.WithCancel(context.Background())
	return &DemoService{
		maxJobs:         maxJobs,
		maxDifficulty:   maxDifficulty,
		maxTimePerJob:   maxTimePerJob,
		ctx:             ctx,
		cancel:          cancel,
		workers:         make(map[*protocols.Peer]uint8),
		submits:         make([]*protocol.Request, defaultSubmitsCapacity),
		submitsIdx:      make(map[uint64]*protocol.Request),
		submitsCapacity: defaultSubmitsCapacity,
		results:         make(map[uint64]*protocol.Result),
		resultsCapacity: defaultResultsCapacity,
	}
}

func (self *DemoService) IsWorker() bool {
	return self.maxDifficulty > 0
}

func (self *DemoService) APIs() []rpc.API {
	return []rpc.API{
		{
			Namespace: "demo",
			Version:   "1.0",
			Service:   newDemoServiceAPI(self),
			Public:    true,
		},
	}
}

func (self *DemoService) Protocols() (protos []p2p.Protocol) {
	proto, err := protocol.NewDemoProtocol(self.Run)
	if err != nil {
		log.Crit("can't create demo protocol")
	}
	proto.SkillsHandler = self.skillsHandler
	proto.StatusHandler = self.statusHandler
	proto.RequestHandler = self.requestHandler
	proto.ResultHandler = self.resultHandler
	if err := proto.Init(); err != nil {
		log.Crit("can't init demo protocol")
	}
	return []p2p.Protocol{proto.Protocol}
}

func (self *DemoService) Start(srv *p2p.Server) error {
	return nil
}

func (self *DemoService) Stop() error {
	self.cancel()
	return nil
}

// The protocol code provides Hook to run when protocol starts on a peer
func (self *DemoService) Run(p *protocols.Peer) error {
	self.mu.RLock()
	log.Info("run protocol hook", "peer", p, "difficulty", self.maxDifficulty)
	defer self.mu.RUnlock()
	go p.Send(&protocol.Skills{
		Difficulty: self.maxDifficulty,
	})
	return nil
}

func (self *DemoService) getNextWorker(difficulty uint8) *protocols.Peer {
	self.mu.RLock()
	defer self.mu.RUnlock()
	for p, d := range self.workers {
		if d >= difficulty {
			return p
		}
	}
	return nil
}

func (self *DemoService) submitRequest(data []byte, difficulty uint8) (uint64, error) {
	p := self.getNextWorker(difficulty)
	if p == nil {
		return 0, fmt.Errorf("Couldn't find any workers for difficulty %d", difficulty)
	}
	self.mu.Lock()
	defer func() {
		self.submitLastId++
		self.mu.Unlock()
	}()
	go func(id uint64) {
		req := &protocol.Request{
			Id:         id,
			Data:       data,
			Difficulty: difficulty,
		}
		if err := p.Send(req); err == nil {
			self.addSubmitsEntry(req)
		}
	}(self.submitLastId)
	return self.submitLastId, nil
}

// add submits to entry cache
func (self *DemoService) addSubmitsEntry(req *protocol.Request) {
	self.mu.Lock()
	defer self.mu.Unlock()
	self.submitsCursor++
	self.submitsCursor %= self.submitsCapacity
	if self.submits[self.submitsCursor] != nil {
		delete(self.submitsIdx, self.submits[self.submitsCursor].Id)
	}
	self.submits[self.submitsCursor] = req
	self.submitsIdx[req.Id] = req
}

func (self *DemoService) addResultsEntry(res *protocol.Result) {
	self.mu.Lock()
	defer self.mu.Unlock()
	self.results[res.Id] = res
	self.resultsCounter++
}

func (self *DemoService) getResultsEntry(id uint64) *protocol.Result {
	self.mu.RLock()
	defer self.mu.RUnlock()
	return self.results[id]
}

func (self *DemoService) deleteResultsEntry(id uint64) {
	self.mu.Lock()
	defer self.mu.Unlock()
	delete(self.results, id)
}

func (self *DemoService) skillsHandler(msg *protocol.Skills, p *protocols.Peer) error {
	log.Trace("have skills type", "msg", msg, "peer", p)
	self.mu.Lock()
	self.workers[p] = msg.Difficulty
	self.mu.Unlock()
	return nil
}

func (self *DemoService) statusHandler(msg *protocol.Status, p *protocols.Peer) error {
	log.Trace("have status type", "msg", msg, "peer", p)
	return nil
}

func (self *DemoService) requestHandler(msg *protocol.Request, p *protocols.Peer) error {

	self.mu.Lock()
	defer self.mu.Unlock()

	log.Trace("have request type", "msg", msg, "currentjobs", self.currentJobs, "ourdifficulty", self.maxDifficulty, "peer", p)

	if self.currentJobs >= self.maxJobs || self.resultsCounter == self.resultsCapacity {
		go p.Send(&protocol.Status{
			Id:   msg.Id,
			Code: protocol.StatusBusy,
		})
		log.Error("Too busy!")
		return nil
	}

	if self.maxDifficulty < msg.Difficulty {
		go p.Send(&protocol.Status{
			Id:   msg.Id,
			Code: protocol.StatusTooHard,
		})
		return fmt.Errorf("too hard!")
	}
	self.currentJobs++

	go func(msg *protocol.Request, lmu sync.RWMutex) {
		ctx, cancel := context.WithTimeout(self.ctx, self.maxTimePerJob)
		defer cancel()
		j, err := doJob(ctx, msg.Data, msg.Difficulty)

		if err != nil {
			p.Send(&protocol.Status{
				Id:   msg.Id,
				Code: protocol.StatusGaveup,
			})
			log.Debug("too long!")
			return
		}
		res := &protocol.Result{
			Id:    msg.Id,
			Nonce: j.Nonce,
			Hash:  j.Hash,
		}
		p.Send(res)

		lmu.Lock()
		self.results[res.Id] = res
		self.resultsCounter++
		self.currentJobs--
		lmu.Unlock()

		log.Debug("finished job", "id", msg.Id, "nonce", j.Nonce, "hash", j.Hash)
	}(msg, self.mu) // do we need to pass self.mu here?

	return nil
}

func (self *DemoService) resultHandler(msg *protocol.Result, p *protocols.Peer) error {
	self.mu.RLock()
	defer self.mu.RUnlock()
	if self.maxDifficulty > 0 {
		log.Trace("ignored result type", "msg", msg)
	}
	log.Trace("got result type", "msg", msg, "peer", p)

	if self.submitsIdx[msg.Id] == nil {
		log.Debug("stale or fake request id")
		return nil // in case it's stale not fake don't punish the peer
	}
	if !checkJob(msg.Hash, self.submitsIdx[msg.Id].Data, msg.Nonce) {
		return fmt.Errorf("Got incorrect result", "p", p)
	}
	go p.Send(&protocol.Status{
		Id:   msg.Id,
		Code: protocol.StatusThanksABunch,
	})

	return nil
}
