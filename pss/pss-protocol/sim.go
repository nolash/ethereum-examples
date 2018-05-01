package main

import (
	"context"
	"math/rand"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/node"
	"github.com/ethereum/go-ethereum/p2p/discover"
	"github.com/ethereum/go-ethereum/p2p/simulations"
	"github.com/ethereum/go-ethereum/p2p/simulations/adapters"

	colorable "github.com/mattn/go-colorable"

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
	log.PrintOrigins(true)
	log.Root().SetHandler(log.LvlFilterHandler(log.LvlTrace, log.StreamHandler(colorable.NewColorableStderr(), log.TerminalFormat(true))))

	maxDifficulty = defaultMaxDifficulty
	minDifficulty = defaultMinDifficulty
	maxTime = defaultMaxTime
	maxJobs = defaultMaxJobs

	adapters.RegisterServices(newServices())
}

func main() {
	a := adapters.NewSimAdapter(newServices())
	//	a, err := adapters.NewDockerAdapter()
	//	if err != nil {
	//		log.Crit(err.Error())
	//	}

	n := simulations.NewNetwork(a, &simulations.NetworkConfig{
		ID:             "protocol-demo",
		DefaultService: "demo",
	})
	defer n.Shutdown()

	var nids []discover.NodeID
	for i := 0; i < 2; i++ {
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

	for i, nid := range nids {
		if i == 0 {
			continue
		}
		go func() {
			client, err := n.GetNode(nid).Client()
			if err != nil {
				return
			}
			err = client.Call(nil, "demo_setDifficulty", 0)
			if err != nil {
				return
			}
			tick := time.NewTicker(time.Millisecond * 1000)
			for {
				select {
				case e := <-events:
					if e.Type == simulations.EventTypeMsg {
						log.Info("got message", "e", e)
						continue
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

	http.ListenAndServe(":8888", simulations.NewServer(n))

	return
}

func newServices() adapters.Services {
	return adapters.Services{
		"demo": func(node *adapters.ServiceContext) (node.Service, error) {
			return service.NewDemoService(maxDifficulty, maxJobs, maxTime), nil
		},
	}
}
