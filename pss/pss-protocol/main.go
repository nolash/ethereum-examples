package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/node"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/protocols"
	"github.com/ethereum/go-ethereum/rpc"
	swarmapi "github.com/ethereum/go-ethereum/swarm/api"
	"github.com/ethereum/go-ethereum/swarm/network"
	"github.com/ethereum/go-ethereum/swarm/network/stream"
	"github.com/ethereum/go-ethereum/swarm/pss"
	"github.com/ethereum/go-ethereum/swarm/state"
	"github.com/ethereum/go-ethereum/swarm/storage"

	"./service"
)

const (
	ipcName      = "pssmailboxdemo.ipc"
	protoName    = "mb"
	protoVersion = 1
	protoMax     = 2048
)

var (
	verbose = flag.Bool("v", false, "log verbosity")
	port    = flag.Int("p", 30499, "p2p port")
	bzzport = flag.String("b", "8555", "bzz port")
	enode   = flag.String("e", "", "enode to connect to")
)

type pssService interface {
	node.Service
	Spec() *protocols.Spec
	Topic() *pss.Topic
	Init(*pss.Pss) error
}

type protocol struct {
	*p2p.Protocol
	spec        *protocols.Spec
	params      *pss.ProtocolParams
	topic       *pss.Topic
	pssProtocol *pss.Protocol
}

func init() {
	flag.Parse()
	if *verbose {
		log.Root().SetHandler(log.CallerFileHandler(log.LvlFilterHandler(log.LvlTrace, (log.StreamHandler(os.Stderr, log.TerminalFormat(true))))))
	}
}

type pssSampleService struct {
	ps        *pss.Pss
	bzz       *network.Bzz
	lstore    *storage.LocalStore
	streamer  *stream.Registry
	rh        *storage.ResourceHandler
	services  []pssService
	protocols map[string]*protocol
}

func newPssSampleService() *pssSampleService {
	return &pssSampleService{
		protocols: make(map[string]*protocol),
	}
}

func (self *pssSampleService) init(cfg *swarmapi.Config) error {
	var err error

	// master parameters
	privkey := cfg.ShiftPrivateKey()
	kp := network.NewKadParams()
	to := network.NewKademlia(
		common.FromHex(cfg.BzzKey),
		kp,
	)
	addr := &network.BzzAddr{
		OAddr: common.FromHex(cfg.BzzKey),
		UAddr: common.FromHex(cfg.BzzKey),
	}

	// storage
	lstoreparams := storage.NewDefaultLocalStoreParams()
	lstoreparams.Init(filepath.Join(cfg.Path, "chunk"))
	self.lstore, err = storage.NewLocalStore(lstoreparams, nil)
	if err != nil {
		return fmt.Errorf("lstore fail: %v", err)
	}

	// resource handler
	rhparams := &storage.ResourceHandlerParams{
		QueryMaxPeriods: &storage.ResourceLookupParams{
			Limit: false,
		},
		Signer: &storage.GenericResourceSigner{
			PrivKey: privkey,
		},
		EthClient: storage.NewBlockEstimator(),
	}
	self.rh, err = storage.NewResourceHandler(rhparams)
	if err != nil {
		return fmt.Errorf("resource fail: %v", err)
	}
	self.lstore.Validators = []storage.ChunkValidator{self.rh}

	// sync/stream
	stateStore, err := state.NewDBStore(filepath.Join(cfg.Path, "state-store.db"))
	if err != nil {
		return fmt.Errorf("statestore fail: %v", err)
	}
	db := storage.NewDBAPI(self.lstore)
	delivery := stream.NewDelivery(to, db)

	self.streamer = stream.NewRegistry(addr, delivery, db, stateStore, &stream.RegistryOptions{
		DoSync:     false,
		DoRetrieve: true,
	})

	// pss
	pssparams := pss.NewPssParams(privkey)
	self.ps = pss.NewPss(to, pssparams)
	for _, s := range self.services {
		err := s.Init(self.ps)
		if err != nil {
			return fmt.Errorf("register pss protocol '%s' fail", s.Spec().Name)
		}
	}

	// bzz protocol
	bzzconfig := &network.BzzConfig{
		OverlayAddr:  addr.OAddr,
		UnderlayAddr: addr.UAddr,
		HiveParams:   cfg.HiveParams,
	}
	self.bzz = network.NewBzz(bzzconfig, to, stateStore, stream.Spec, self.streamer.Run)

	return nil
}

func (self *pssSampleService) APIs() []rpc.API {
	var apis []rpc.API
	for _, s := range self.services {
		apis = append(apis, s.APIs()...)
	}
	return apis
}

func (self *pssSampleService) Protocols() (protos []p2p.Protocol) {
	protos = append(protos, self.bzz.Protocols()...)
	protos = append(protos, self.ps.Protocols()...)
	return
}

func (self *pssSampleService) Start(srv *p2p.Server) error {
	err := self.bzz.Start(srv)
	if err != nil {
		return err
	}
	self.streamer.Start(srv)
	self.ps.Start(srv)
	for _, s := range self.services {
		s.Start(srv)
	}
	return nil
}

func (self *pssSampleService) Stop() error {
	for _, s := range self.services {
		s.Stop()
	}
	self.ps.Stop()
	self.streamer.Stop()
	self.lstore.Close()
	return nil
}

func main() {
	exitCode := 0
	datadir, err := ioutil.TempDir("", "pssmailboxdemo-")
	if err != nil {
		log.Error("dir create fail", "err", err)
		os.Exit(exitCode)
	}
	exitCode++
	defer os.RemoveAll(datadir)

	cfg := &node.DefaultConfig
	cfg.P2P.ListenAddr = fmt.Sprintf(":%d", *port)
	cfg.P2P.EnableMsgEvents = true
	cfg.IPCPath = ipcName
	cfg.DataDir = datadir

	stack, err := node.New(cfg)
	if err != nil {
		log.Error("node create fail", "err", err)
		os.Exit(exitCode)
	}
	exitCode++

	privkey, err := crypto.GenerateKey()
	if err != nil {
		log.Error(err.Error())
		os.Exit(exitCode)
	}
	exitCode++
	bzzCfg := swarmapi.NewConfig()
	bzzCfg.SyncEnabled = false
	bzzCfg.Port = *bzzport
	bzzCfg.Path = datadir
	bzzCfg.Init(privkey)

	// create our service
	bzzSvc := newPssSampleService()
	bzzSvc.services = append(bzzSvc.services, &service.DemoService{})
	if err := bzzSvc.init(bzzCfg); err != nil {
		log.Error(err.Error())
		os.Exit(exitCode)
	}
	exitCode++
	if err := stack.Register(func(ctx *node.ServiceContext) (node.Service, error) {
		return bzzSvc, nil
	}); err != nil {
		log.Error(err.Error())
		os.Exit(exitCode)
	}
	exitCode++
	if err := stack.Start(); err != nil {
		log.Error(err.Error())
		os.Exit(exitCode)
	}
	exitCode++
	defer stack.Stop()

	client, err := stack.Attach()
	if err != nil {
		log.Error(err.Error())
		os.Exit(exitCode)
	}
	exitCode++
	if err := client.Call(nil, "demo_addPssPeer", []byte{0x01, 0x02}); err != nil {
		log.Error(err.Error())
		os.Exit(exitCode)
	}
	exitCode++

	sigC := make(chan os.Signal)
	signal.Notify(sigC, syscall.SIGINT)
	<-sigC
}
