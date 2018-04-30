package bzz

import (
	"fmt"
	"net"
	"path/filepath"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/discover"
	"github.com/ethereum/go-ethereum/rpc"
	swarmapi "github.com/ethereum/go-ethereum/swarm/api"
	"github.com/ethereum/go-ethereum/swarm/network"
	"github.com/ethereum/go-ethereum/swarm/network/stream"
	"github.com/ethereum/go-ethereum/swarm/pss"
	"github.com/ethereum/go-ethereum/swarm/state"
	"github.com/ethereum/go-ethereum/swarm/storage"

	"../protocol"
	"../service"
)

type PssService struct {
	bzz         *network.Bzz
	lstore      *storage.LocalStore
	ps          *pss.Pss
	pssProtocol *pss.Protocol
	Topic       *pss.Topic
	streamer    *stream.Registry
	demo        *service.DemoService
	rh          *storage.ResourceHandler
}

func NewPssService(cfg *swarmapi.Config, demo *service.DemoService) (*PssService, error) {
	var err error

	// master parameters
	self := &PssService{}
	privkey := cfg.ShiftPrivateKey()
	kp := network.NewKadParams()
	to := network.NewKademlia(
		common.FromHex(cfg.BzzKey),
		kp,
	)

	nodeID, err := discover.HexID(cfg.NodeID)
	addr := &network.BzzAddr{
		OAddr: common.FromHex(cfg.BzzKey),
		UAddr: []byte(discover.NewNode(nodeID, net.IP{127, 0, 0, 1}, 30303, 30303).String()),
	}

	// storage
	lstoreparams := storage.NewDefaultLocalStoreParams()
	lstoreparams.Init(filepath.Join(cfg.Path, "chunk"))
	self.lstore, err = storage.NewLocalStore(lstoreparams, nil)
	if err != nil {
		return nil, fmt.Errorf("lstore fail: %v", err)
	}

	// resource handler
	rhparams := &storage.ResourceHandlerParams{
		QueryMaxPeriods: &storage.ResourceLookupParams{
			Limit: false,
		},
		Signer: &storage.GenericResourceSigner{
			PrivKey: privkey,
		},
		//EthClient: storage.NewBlockEstimator(),
	}
	self.rh, err = storage.NewResourceHandler(rhparams)
	if err != nil {
		return nil, fmt.Errorf("resource fail: %v", err)
	}
	self.lstore.Validators = []storage.ChunkValidator{self.rh}

	// sync/stream
	stateStore, err := state.NewDBStore(filepath.Join(cfg.Path, "state-store.db"))
	if err != nil {
		return nil, fmt.Errorf("statestore fail: %v", err)
	}
	db := storage.NewDBAPI(self.lstore)
	delivery := stream.NewDelivery(to, db)

	self.streamer = stream.NewRegistry(addr, delivery, db, stateStore, &stream.RegistryOptions{
		DoSync:     false,
		DoRetrieve: true,
	})

	// pss
	pssparams := pss.NewPssParams()
	pssparams.Init(privkey)
	self.ps, err = pss.NewPss(to, pssparams)
	if err != nil {
		return nil, err
	}

	// bzz protocol
	bzzconfig := &network.BzzConfig{
		OverlayAddr:  addr.OAddr,
		UnderlayAddr: addr.UAddr,
		HiveParams:   cfg.HiveParams,
	}
	self.bzz = network.NewBzz(bzzconfig, to, stateStore, stream.Spec, self.streamer.Run)

	// the demoservice
	underlyingProtocol := demo.Protocols()[0]
	topic := pss.BytesToTopic([]byte(protocol.Spec.Name))
	self.Topic = &topic
	self.pssProtocol, err = pss.RegisterProtocol(self.ps, self.Topic, protocol.Spec, &underlyingProtocol, &pss.ProtocolParams{true, true})
	if err != nil {
		return nil, fmt.Errorf("register pss protocol fail: %v", err)
	}
	self.ps.Register(self.Topic, self.pssProtocol.Handle)
	self.demo = demo
	return self, nil
}

func (self *PssService) Protocols() (protos []p2p.Protocol) {
	//protos = append(protos, self.bzz.Protocols()...)
	protos = append(protos, self.bzz.Protocols()[0])
	protos = append(protos, self.bzz.Protocols()[1])
	protos = append(protos, self.ps.Protocols()...)
	return
}

func (self *PssService) APIs() []rpc.API {
	apis := []rpc.API{
		{
			Namespace: "demops",
			Version:   "1.0",
			Service:   newPssServiceAPI(self),
			Public:    true,
		},
	}
	apis = append(apis, self.bzz.APIs()...)
	apis = append(apis, self.ps.APIs()...)
	apis = append(apis, self.demo.APIs()...)
	return apis
}

func (self *PssService) Start(srv *p2p.Server) error {
	newaddr := self.bzz.UpdateLocalAddr([]byte(srv.Self().String()))
	log.Warn("Updated bzz local addr", "oaddr", fmt.Sprintf("%x", newaddr.OAddr), "uaddr", fmt.Sprintf("%s", newaddr.UAddr))
	err := self.bzz.Start(srv)
	if err != nil {
		return err
	}
	//self.streamer.Start(srv)
	self.ps.Start(srv)
	self.demo.Start(srv)
	return nil
}

func (self *PssService) Stop() error {
	self.demo.Stop()
	self.ps.Stop()
	//self.streamer.Stop()
	self.lstore.Close()
	return nil
}

// api to interact with pss protocol
// TODO: change protocol methods so we only have to use pss api here and remove this structure
type PssServiceAPI struct {
	service *PssService
	api     *pss.API
}

func newPssServiceAPI(svc *PssService) *PssServiceAPI {
	return &PssServiceAPI{
		service: svc,
		api:     pss.NewAPI(svc.ps),
	}
}

func (self *PssServiceAPI) AddPeer(pubKey hexutil.Bytes, addr pss.PssAddress) error {

	// add the public key to the pss address book
	err := self.api.SetPeerPublicKey(pubKey, *self.service.Topic, addr)
	if err != nil {
		return err
	}

	// register the underlying protocol as pss protocol
	// and start running it on the peer
	var nid discover.NodeID
	copy(nid[:], addr)
	self.service.pssProtocol.AddPeer(p2p.NewPeer(nid, string(pubKey), []p2p.Cap{}), *self.service.Topic, true, common.ToHex(pubKey))
	log.Info(fmt.Sprintf("adding peer %x to demoservice protocol", pubKey))
	return nil
}
