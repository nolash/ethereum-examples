package main

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/rand"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/node"
	"github.com/ethereum/go-ethereum/p2p/discover"
	"github.com/ethereum/go-ethereum/p2p/simulations"
	"github.com/ethereum/go-ethereum/p2p/simulations/adapters"
	"github.com/ethereum/go-ethereum/rpc"
	swarmapi "github.com/ethereum/go-ethereum/swarm/api"
	"github.com/ethereum/go-ethereum/swarm/pss"

	colorable "github.com/mattn/go-colorable"

	"./bzz"
	"./protocol"
	"./resource"
	"./service"
)

const (
	defaultMaxDifficulty   = 24
	defaultMinDifficulty   = 8
	defaultMaxTime         = time.Second * 10
	defaultMaxJobs         = 100
	defaultResourceApiHost = "http://localhost:8500"
)

var (
	maxDifficulty uint8
	minDifficulty uint8
	maxTime       time.Duration
	maxJobs       int
	privateKeys   map[discover.NodeID]*ecdsa.PrivateKey
)

func init() {
	log.PrintOrigins(true)
	log.Root().SetHandler(log.LvlFilterHandler(log.LvlDebug, log.StreamHandler(colorable.NewColorableStderr(), log.TerminalFormat(true))))

	maxDifficulty = defaultMaxDifficulty
	minDifficulty = defaultMinDifficulty
	maxTime = defaultMaxTime
	maxJobs = defaultMaxJobs

	privateKeys = make(map[discover.NodeID]*ecdsa.PrivateKey)
	adapters.RegisterServices(newServices())
}

func main() {
	a := adapters.NewSimAdapter(newServices())
	//a, err := adapters.NewDockerAdapter("")
	//	if err != nil {
	//		log.Crit(err.Error())
	//	}

	n := simulations.NewNetwork(a, &simulations.NetworkConfig{
		ID:             "protocol-demo",
		DefaultService: "bzz",
	})
	defer n.Shutdown()

	var nids []discover.NodeID
	for i := 0; i < 10; i++ {
		c := adapters.RandomNodeConfig()
		nod, err := n.NewNodeWithConfig(c)
		if err != nil {
			log.Error(err.Error())
			return
		}
		nids = append(nids, nod.ID())
		privateKeys[nod.ID()], err = crypto.GenerateKey()
		if err != nil {
			log.Error(err.Error())
			return
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	if err := simulations.Up(ctx, n, nids, simulations.UpModeStar); err != nil {
		log.Error(err.Error())
		return
	}

	err := connectPssPeers(n, nids)
	if err != nil {
		log.Error(err.Error())
		return
	}

	// the fucking healthy stuff
	time.Sleep(time.Second * 1)

	quitC := make(chan struct{})
	trigger := make(chan discover.NodeID)
	events := make(chan *simulations.Event)
	sub := n.Events().Subscribe(events)
	// event sink on quit
	defer func() {
		sub.Unsubscribe()
		close(quitC)
		select {
		case <-events:
		default:
		}
		return
	}()

	action := func(ctx context.Context) error {
		for i, nid := range nids {
			if i == 0 {
				go func() {
					trigger <- nid
				}()
				continue
			}
			client, err := n.GetNode(nid).Client()
			if err != nil {
				return err
			}

			err = client.Call(nil, "demo_setDifficulty", 0)
			if err != nil {
				return err
			}

			go func(nid discover.NodeID) {
				tick := time.NewTicker(time.Millisecond * 500)
				for {
					select {
					case e := <-events:
						if e.Type == simulations.EventTypeMsg {
							continue
						}

					case <-quitC:
						trigger <- nid
						return
					case <-ctx.Done():
						trigger <- nid
						return
					case <-tick.C:
					}
					data := make([]byte, 64)
					rand.Read(data)
					difficulty := rand.Intn(int(maxDifficulty-minDifficulty)) + int(minDifficulty)

					var id protocol.ID
					err := client.Call(&id, "demo_submit", data, difficulty)
					if err != nil {
						log.Warn("job not accepted", "err", err)
					} else {
						log.Info("job submitted", "id", id, "nid", nid)
					}
				}
			}(nid)
		}
		return nil
	}
	check := func(ctx context.Context, nid discover.NodeID) (bool, error) {
		select {
		case <-ctx.Done():
		default:
		}
		return true, nil
	}

	ctx, cancel = context.WithTimeout(context.Background(), time.Second*60)
	defer cancel()
	sim := simulations.NewSimulation(n)
	step := sim.Run(ctx, &simulations.Step{
		Action:  action,
		Trigger: nil,
		Expect: &simulations.Expectation{
			Nodes: nids,
			Check: check,
		},
	})
	if step.Error != nil {
		log.Error(step.Error.Error())
	}
	return
}

func connectPssPeers(n *simulations.Network, nids []discover.NodeID) error {
	var pivotBaseAddr string
	var pivotPubKeyHex string
	var pivotClient *rpc.Client
	topic := pss.BytesToTopic([]byte(fmt.Sprintf("%s:%d", protocol.Spec.Name, protocol.Spec.Version)))
	for i, nid := range nids {
		client, err := n.GetNode(nid).Client()
		if err != nil {
			return err
		}
		var baseAddr string
		err = client.Call(&baseAddr, "pss_baseAddr")
		if err != nil {
			return err
		}
		pubkey := privateKeys[nid].PublicKey
		pubkeybytes := crypto.FromECDSAPub(&pubkey)
		pubkeyhex := common.ToHex(pubkeybytes)
		if i == 0 {
			pivotBaseAddr = baseAddr
			pivotPubKeyHex = pubkeyhex
			pivotClient = client
		} else {
			err = client.Call(nil, "pss_setPeerPublicKey", pivotPubKeyHex, common.ToHex(topic[:]), pivotBaseAddr)
			if err != nil {
				return err
			}
			err = pivotClient.Call(nil, "pss_setPeerPublicKey", pubkeyhex, common.ToHex(topic[:]), baseAddr)
			if err != nil {
				return err
			}
			err = client.Call(nil, "pss_addPeer", topic, pivotPubKeyHex, pivotBaseAddr)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func newServices() adapters.Services {
	return adapters.Services{
		"bzz": func(node *adapters.ServiceContext) (node.Service, error) {
			resourceapi := resource.NewClient(defaultResourceApiHost, fmt.Sprintf("%x.mutable.test", node.Config.ID[:]))
			params := service.NewDemoParams(resourceapi.ResourceSinkFunc())
			params.MaxJobs = maxJobs
			params.MaxTimePerJob = maxTime
			params.MaxDifficulty = maxDifficulty

			// create the pss service that wraps the demo protocol
			svc, err := service.NewDemo(params)
			if err != nil {
				return nil, err
			}
			bzzCfg := swarmapi.NewConfig()
			bzzCfg.SyncEnabled = false
			//bzzCfg.Port = *bzzport
			//bzzCfg.Path = node.ServiceContext.
			bzzCfg.HiveParams.Discovery = true
			bzzCfg.Init(privateKeys[node.Config.ID])

			bzzSvc, err := bzz.NewBzzService(bzzCfg)
			if err != nil {
				return nil, err
			}
			bzzSvc.RegisterPssProtocol(svc)
			return bzzSvc, nil
		},
	}
}
