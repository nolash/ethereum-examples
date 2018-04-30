package main

import (
	"context"
	"math/rand"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/node"
	"github.com/ethereum/go-ethereum/p2p/discover"
	"github.com/ethereum/go-ethereum/p2p/simulations"
	"github.com/ethereum/go-ethereum/p2p/simulations/adapters"

	"./service"
)

const (
	defaultMaxDifficulty = 24
	defaultMinDifficulty = 8
	defaultMaxTime       = time.Second * 10
	defaultMaxJobs       = 100
)

var (
	maxDifficulty uint8
	minDifficulty uint8
	maxTime       time.Duration
	maxJobs       int
)

func init() {
	log.Root().SetHandler(log.LvlFilterHandler(log.LvlTrace, log.StderrHandler))

	maxDifficulty = defaultMaxDifficulty
	minDifficulty = defaultMinDifficulty
	maxTime = defaultMaxTime
	maxJobs = defaultMaxJobs

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
		DefaultService: "demo",
	})
	defer n.Shutdown()

	var nids []discover.NodeID
	for i := 0; i < 5; i++ {
		c := adapters.RandomNodeConfig()
		nod, err := n.NewNodeWithConfig(c)
		if err != nil {
			log.Error(err.Error())
			return
		}
		nids = append(nids, nod.ID())
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	if err := simulations.Up(ctx, n, nids, simulations.UpModeStar); err != nil {
		log.Error(err.Error())
		return
	}

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
			go func() {
				defer func() {
					trigger <- nid
				}()
				client, err := n.GetNode(nid).Client()
				if err != nil {
					return
				}
				if i != 0 {
					err := client.Call(nil, "demo_setDifficulty", 0)
					if err != nil {
						return
					}
				}
				tick := time.NewTicker(time.Millisecond * 500)
				for {
					select {
					case e := <-events:
						if e.Type == simulations.EventTypeMsg {
							log.Info("got message", "e", e)
						}

					case <-quitC:
						return
					case <-tick.C:
					}
					data := make([]byte, 64)
					rand.Read(data)
					difficulty := rand.Intn(int(maxDifficulty-minDifficulty)) + int(minDifficulty)

					var id uint64
					err := client.Call(&id, "demo_submit", data, difficulty)
					if err != nil {
						log.Warn("job not accepted", "err", err)
					} else {
						log.Info("job submitted", "id", id)
					}
				}
			}()
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

func newServices() adapters.Services {
	return adapters.Services{
		"demo": func(node *adapters.ServiceContext) (node.Service, error) {
			return service.NewDemoService(maxDifficulty, maxJobs, maxTime), nil
		},
	}
}
