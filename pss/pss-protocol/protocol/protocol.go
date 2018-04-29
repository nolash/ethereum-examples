package protocol

import (
	"errors"

	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/protocols"
)

const (
	StatusOK = iota
	StatusBusy
	StatusTooHard
	StatusGaveup
)

const (
	HashSHA1 = iota
)

const (
	protoName    = "demo"
	protoVersion = 1
	protoMax     = 2048
)

// protocol message types
type Result struct {
	Id    uint64
	Nonce []byte
	Hash  []byte
}

type Status struct {
	Code uint8
}

type Request struct {
	Id         uint64
	Data       []byte
	Difficulty uint8
}

type Skills struct {
	Difficulty uint8
}

var (
	Messages = []interface{}{
		&Skills{},
		&Status{},
		&Request{},
		&Result{},
	}

	Spec = &protocols.Spec{
		Name:       protoName,
		Version:    protoVersion,
		MaxMsgSize: protoMax,
		Messages:   Messages,
	}
)

// the protocol itself
type DemoProtocol struct {
	Protocol       p2p.Protocol
	SkillsHandler  func(*Skills, *protocols.Peer) error
	StatusHandler  func(*Status, *protocols.Peer) error
	RequestHandler func(*Request, *protocols.Peer) error
	ResultHandler  func(*Result, *protocols.Peer) error
	handler        func(interface{}) error
	runHook        func(*protocols.Peer) error
}

func NewDemoProtocol(runHook func(*protocols.Peer) error) (*DemoProtocol, error) {

	proto := &DemoProtocol{
		Protocol: p2p.Protocol{
			Name:    protoName,
			Version: protoVersion,
			Length:  4,
		},
		runHook: runHook,
	}

	return proto, nil
}

func (self *DemoProtocol) Init() error {
	if self.SkillsHandler == nil {
		return errors.New("missing skills handler")
	}
	if self.StatusHandler == nil {
		return errors.New("missing status handler")
	}
	if self.RequestHandler == nil {
		return errors.New("missing request handler")
	}
	if self.ResultHandler == nil {
		return errors.New("missing response handler")
	}
	self.Protocol.Run = self.run
	return nil
}

func (self *DemoProtocol) run(p *p2p.Peer, rw p2p.MsgReadWriter) error {
	pp := protocols.NewPeer(p, rw, Spec)
	log.Error("peer", "peer", pp, "self", self)
	go self.runHook(pp)
	dp := &DemoPeer{
		Peer:           pp,
		skillsHandler:  self.SkillsHandler,
		statusHandler:  self.StatusHandler,
		requestHandler: self.RequestHandler,
		resultHandler:  self.ResultHandler,
	}
	return pp.Run(dp.Handle)
}
