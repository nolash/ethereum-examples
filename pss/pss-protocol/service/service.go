package service

import (
	"context"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/discover"
	"github.com/ethereum/go-ethereum/p2p/protocols"
	"github.com/ethereum/go-ethereum/rpc"

	"../protocol"
)

// DemoService implements the node.Service interface
type DemoService struct {

	// worker mode params
	maxJobs       int           // maximum number of simultaneous hashing jobs the node will accept
	currentJobs   int           // how many jobs currently executing
	maxDifficulty uint8         // the maximum difficulty of jobs this node will handle
	maxTimePerJob time.Duration // maximum time one hashing job will run

	// moocher mode params
	workers map[*protocols.Peer]uint8 // an address book of hasher peers for nodes that send requests

	submits *submitStore
	results *resultStore

	// internal control stuff
	mu     sync.RWMutex
	ctx    context.Context
	cancel func()
}

type DemoServiceParams struct {
	MaxDifficulty uint8
	MaxJobs       int
	MaxTimePerJob time.Duration
	ResultSink    ResultSinkFunc
}

func NewDemoServiceParams(sinkFunc ResultSinkFunc) *DemoServiceParams {
	return &DemoServiceParams{
		ResultSink: sinkFunc,
	}
}

func NewDemoService(params *DemoServiceParams) *DemoService {
	ctx, cancel := context.WithCancel(context.Background())
	return &DemoService{
		maxJobs:       params.MaxJobs,
		maxDifficulty: params.MaxDifficulty,
		maxTimePerJob: params.MaxTimePerJob,
		workers:       make(map[*protocols.Peer]uint8),
		submits:       newSubmitStore(),
		results:       newResultStore(ctx, params.ResultSink),
		ctx:           ctx,
		cancel:        cancel,
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
	proto.SkillsHandler = self.skillsHandlerLocked
	proto.StatusHandler = self.statusHandlerLocked
	proto.RequestHandler = self.requestHandlerLocked
	proto.ResultHandler = self.resultHandlerLocked
	if err := proto.Init(); err != nil {
		log.Crit("can't init demo protocol")
	}
	return []p2p.Protocol{proto.Protocol}
}

func (self *DemoService) Start(srv *p2p.Server) error {
	self.results.Start()
	return nil
}

func (self *DemoService) Stop() error {
	self.cancel()
	return nil
}

// The protocol code provides Hook to run when protocol starts on a peer
func (self *DemoService) Run(p *protocols.Peer) error {
	self.mu.RLock()
	defer self.mu.RUnlock()
	log.Info("run protocol hook", "peer", p, "difficulty", self.maxDifficulty)
	go p.Send(&protocol.Skills{
		Difficulty: self.maxDifficulty,
	})
	return nil
}

func (self *DemoService) getNextWorker(difficulty uint8) *protocols.Peer {
	for p, d := range self.workers {
		if d >= difficulty {
			return p
		}
	}
	return nil
}

func (self *DemoService) SubmitRequest(data []byte, difficulty uint8) (uint64, error) {
	self.mu.Lock()
	p := self.getNextWorker(difficulty)
	if p == nil {
		return 0, fmt.Errorf("Couldn't find any workers for difficulty %d", difficulty)
	}
	id := self.submits.IncId()
	self.mu.Unlock()
	go func(id uint64) {
		req := &protocol.Request{
			Id:         id,
			Data:       data,
			Difficulty: difficulty,
		}
		if err := p.Send(req); err == nil {
			if err := self.submits.Put(req, id); err != nil {
				log.Error("submits put fail", "err", err)
			}
		}
	}(id)
	return id, nil
}

func (self *DemoService) skillsHandlerLocked(msg *protocol.Skills, p *protocols.Peer) error {
	self.mu.Lock()
	defer self.mu.Unlock()
	log.Trace("have skills type", "msg", msg, "peer", p)
	self.workers[p] = msg.Difficulty
	return nil
}

func (self *DemoService) statusHandlerLocked(msg *protocol.Status, p *protocols.Peer) error {
	log.Trace("have status type", "msg", msg, "peer", p)

	self.mu.Lock()
	defer self.mu.Unlock()

	switch msg.Code {
	case protocol.StatusThanksABunch:
		if self.IsWorker() {
			self.results.Del(newCacheId(p.ID(), msg.Id))
		}
	case protocol.StatusBusy:
		if self.IsWorker() {
			return nil
		}
		log.Trace("peer is busy. please implement throttling")
	case protocol.StatusTooHard:
		if self.IsWorker() {
			return nil
		}
		log.Trace("we sent wrong difficulty or it changed. please implement adjusting it")
	case protocol.StatusGaveup:
		if self.IsWorker() {
			return nil
		}
		log.Trace("peer gave up on the job. please implement how to select someone else for the job")
	}

	return nil
}

func (self *DemoService) requestHandlerLocked(msg *protocol.Request, p *protocols.Peer) error {

	self.mu.Lock()
	defer self.mu.Unlock()

	log.Trace("have request type", "msg", msg, "currentjobs", self.currentJobs, "ourdifficulty", self.maxDifficulty, "peer", p)

	if self.currentJobs >= self.maxJobs || self.results.IsFull() {
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

	go func(msg *protocol.Request) {
		ctx, cancel := context.WithTimeout(self.ctx, self.maxTimePerJob)
		defer cancel()
		j, err := doJob(ctx, msg.Data, msg.Difficulty)

		if err != nil {
			go p.Send(&protocol.Status{
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

		cid := newCacheId(p.ID(), res.Id)
		self.results.Put(cid, res)
		self.mu.Lock()
		self.currentJobs--
		self.mu.Unlock()

		p.Send(res)

		log.Debug("finished job", "id", msg.Id, "nonce", j.Nonce, "hash", j.Hash)
	}(msg) // do we need to pass self.mu here?

	return nil
}

func (self *DemoService) resultHandlerLocked(msg *protocol.Result, p *protocols.Peer) error {
	self.mu.RLock()
	defer self.mu.RUnlock()
	if self.maxDifficulty > 0 {
		log.Trace("ignored result type", "msg", msg)
	}
	log.Trace("got result type", "msg", msg, "peer", p)

	if !self.submits.Have(msg.Id) {
		log.Debug("stale or fake request id")
		return nil // in case it's stale not fake don't punish the peer
	}
	if !checkJob(msg.Hash, self.submits.GetData(msg.Id), msg.Nonce) {
		return fmt.Errorf("Got incorrect result", "p", p)
	}
	go p.Send(&protocol.Status{
		Id:   msg.Id,
		Code: protocol.StatusThanksABunch,
	})

	return nil
}

func newCacheId(pid discover.NodeID, id uint64) string {
	c := make([]byte, 8)
	binary.LittleEndian.PutUint64(c, id)
	h := sha1.New()
	h.Write(pid[:])
	h.Write(c)
	return fmt.Sprintf("%x", h.Sum(nil))

}
