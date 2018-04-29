package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/protocols"
	"github.com/ethereum/go-ethereum/rpc"

	"../protocol"
)

var (
	submitsDelay = time.Second
)

type DemoService struct {
	tooLazyToWork bool
	maxJobs       int
	currentJobs   int
	maxDifficulty uint8
	maxTimePerJob time.Duration
	mu            sync.RWMutex
	ctx           context.Context
	cancel        func()
	workers       map[*protocols.Peer]uint8
	submitLastId  uint64
	submits       []*protocol.Request
	submitsPos    uint8
	submitsDelay  time.Duration
	results       []*protocol.Result
}

func NewDemoService(maxDifficulty uint8, maxJobs int, maxTimePerJob time.Duration) *DemoService {
	ctx, cancel := context.WithCancel(context.Background())
	return &DemoService{
		maxJobs:       maxJobs,
		maxDifficulty: maxDifficulty,
		maxTimePerJob: maxTimePerJob,
		ctx:           ctx,
		cancel:        cancel,
		workers:       make(map[*protocols.Peer]uint8),
		submits:       make([]*protocol.Request, 10),
		submitsDelay:  time.Second,
	}
}

func (self *DemoService) SetLazy(lazy bool) {
	self.tooLazyToWork = lazy
}

func (self *DemoService) APIs() []rpc.API {
	return []rpc.API{}
}

func (self *DemoService) Protocols() (protos []p2p.Protocol) {
	proto, err := protocol.NewDemoProtocol(self.Run)
	if err != nil {
		log.Crit("can't start demo protocol")
	}
	proto.SkillsHandler = self.skillsHandler
	proto.StatusHandler = self.statusHandler
	proto.RequestHandler = self.requestHandler
	proto.ResultHandler = self.resultHandler
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
	log.Info("run protocol hook", "peer", p)
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

func (self *DemoService) submitJob(data []byte, difficulty uint8) error {
	p := self.getNextWorker(difficulty)
	if p == nil {
		return errors.New("Couldn't find any workers")
	}
	self.mu.Lock()
	defer self.mu.Unlock()
	go p.Send(&protocol.Request{
		Id:         self.submitLastId,
		Data:       data,
		Difficulty: difficulty,
	})
	self.submitLastId++
	return nil
}

// message handling area
func (self *DemoService) skillsHandler(msg *protocol.Skills, p *protocols.Peer) error {
	log.Info("have skills type", "msg", msg)
	self.mu.Lock()
	self.workers[p] = msg.Difficulty
	self.mu.Unlock()
	return nil
}

func (self *DemoService) statusHandler(msg *protocol.Status, p *protocols.Peer) error {
	log.Info("have status type", "msg", msg)
	return nil
}

func (self *DemoService) requestHandler(msg *protocol.Request, p *protocols.Peer) error {
	log.Debug("have request type", "msg", msg)

	self.mu.Lock()
	if self.maxDifficulty < msg.Difficulty {
		self.mu.Unlock()
		go p.Send(&protocol.Status{Code: protocol.StatusTooHard})
		return nil
	}
	if self.currentJobs == self.maxJobs {
		self.mu.Unlock()
		go p.Send(&protocol.Status{Code: protocol.StatusBusy})
		return nil
	}
	self.currentJobs++
	self.mu.Unlock()

	ctx, cancel := context.WithTimeout(self.ctx, self.maxTimePerJob)
	defer cancel()
	j, err := doJob(ctx, msg.Data, msg.Difficulty)

	self.mu.Lock()
	self.currentJobs--
	self.mu.Unlock()
	if err != nil {
		go p.Send(&protocol.Status{Code: protocol.StatusGaveup})
		return nil
	}

	go p.Send(&protocol.Result{
		Id:    msg.Id,
		Nonce: j.Nonce,
		Hash:  j.Hash,
	})

	return nil
}

func (self *DemoService) resultHandler(msg *protocol.Result, p *protocols.Peer) error {
	if !self.tooLazyToWork {
		log.Warn("ignored result type", "msg", msg)
	}
	log.Debug("got result type", "msg", msg)

	return nil
}
