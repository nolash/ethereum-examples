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
)

type DemoService struct {
	maxJobs         int
	currentJobs     int
	maxDifficulty   uint8
	maxTimePerJob   time.Duration
	mu              sync.RWMutex
	ctx             context.Context
	cancel          func()
	workers         map[*protocols.Peer]uint8
	submitLastId    uint64
	submits         []*protocol.Request
	submitsCounter  int
	submitsIdx      map[uint64]*protocol.Request
	submitsCapacity int
	submitsDelay    time.Duration
	results         []*protocol.Result
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
		submitsDelay:    time.Second,
		submitsIdx:      make(map[uint64]*protocol.Request),
		submitsCapacity: defaultSubmitsCapacity,
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

// hook to run when protocol starts on a peer
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

// add submits to entry
func (self *DemoService) addSubmitsEntry(req *protocol.Request) {
	self.mu.Lock()
	defer self.mu.Unlock()
	self.submitsCounter++
	self.submitsCounter %= self.submitsCapacity
	if self.submits[self.submitsCounter] != nil {
		delete(self.submitsIdx, self.submits[self.submitsCounter].Id)
	}
	self.submits[self.submitsCounter] = req
	self.submitsIdx[req.Id] = req
}

// message handling area
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

	log.Trace("have request type", "msg", msg, "currentjobs", self.currentJobs, "ourdifficulty", self.maxDifficulty, "peer", p)
	if self.maxDifficulty < msg.Difficulty {
		self.mu.Unlock()
		log.Debug("too hard!")
		go p.Send(&protocol.Status{
			Id:   msg.Id,
			Code: protocol.StatusTooHard,
		})
		return nil
	}
	if self.currentJobs >= self.maxJobs {
		self.mu.Unlock()
		log.Debug("too busy!")
		go p.Send(&protocol.Status{
			Id:   msg.Id,
			Code: protocol.StatusBusy,
		})
		return nil
	}
	self.currentJobs++
	self.mu.Unlock()

	go func(msg *protocol.Request) {
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

		p.Send(&protocol.Result{
			Id:    msg.Id,
			Nonce: j.Nonce,
			Hash:  j.Hash,
		})
		self.mu.Lock()
		self.currentJobs--
		self.mu.Unlock()

		log.Debug("finished job", "id", msg.Id, "nonce", j.Nonce, "hash", j.Hash)
	}(msg)

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
