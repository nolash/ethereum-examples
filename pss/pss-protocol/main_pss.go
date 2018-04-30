package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/node"
	"github.com/ethereum/go-ethereum/p2p"
	swarmapi "github.com/ethereum/go-ethereum/swarm/api"
	//	"github.com/ethereum/go-ethereum/swarm/network"

	"./bzz"
	"./service"
)

const (
	ipcName        = "pssdemo.ipc"
	lockFilename   = ".pssdemo-lock"
	maxDifficulty  = 23
	minDifficulty  = 12
	submitInterval = time.Millisecond * 50
)

var (
	loglevel = flag.Int("l", 3, "loglevel")
	port     = flag.Int("p", 30499, "p2p port")
	bzzport  = flag.String("b", "8555", "bzz port")
	enode    = flag.String("e", "", "enode to connect to")
	httpapi  = flag.String("a", "localhost:8545", "http api")
)

func init() {
	flag.Parse()
	log.Root().SetHandler(log.CallerFileHandler(log.LvlFilterHandler(log.Lvl(*loglevel), (log.StreamHandler(os.Stderr, log.TerminalFormat(true))))))
}

func main() {

	datadir, err := ioutil.TempDir("", "pssmailboxdemo-")
	if err != nil {
		log.Error("dir create fail", "err", err)
		return
	}
	defer os.RemoveAll(datadir)

	cfg := &node.DefaultConfig
	cfg.P2P.ListenAddr = fmt.Sprintf(":%d", *port)
	cfg.P2P.EnableMsgEvents = true
	cfg.IPCPath = ipcName

	httpspec := strings.Split(*httpapi, ":")
	httpport, err := strconv.ParseInt(httpspec[1], 10, 0)
	if err != nil {
		log.Error("node create fail", "err", err)
		return
	}

	if *httpapi != "" {
		cfg.HTTPHost = httpspec[0]
		cfg.HTTPPort = int(httpport)
		cfg.HTTPModules = []string{"demo", "admin", "pss"}
	}
	cfg.DataDir = datadir

	stack, err := node.New(cfg)
	if err != nil {
		log.Error("node create fail", "err", err)
		return
	}

	// create the demo service, but now we don't register it directly
	// so we avoid the protocol running on the direct connected peers
	svc := service.NewDemoService(20, 3, time.Second)

	// create the pss service that wraps the demo protocol
	privkey, err := crypto.GenerateKey()
	if err != nil {
		log.Error(err.Error())
		return
	}
	bzzCfg := swarmapi.NewConfig()
	bzzCfg.SyncEnabled = false
	bzzCfg.Port = *bzzport
	bzzCfg.Path = datadir
	bzzCfg.HiveParams.Discovery = true
	bzzCfg.Init(privkey)

	bzzSvc, err := bzz.NewPssService(bzzCfg, svc)
	if err != nil {
		log.Error(err.Error())
		return
	}

	if err := stack.Register(func(ctx *node.ServiceContext) (node.Service, error) {
		return bzzSvc, nil
	}); err != nil {
		log.Error(err.Error())
		return
	}

	if err := stack.Start(); err != nil {
		log.Error(err.Error())
		return
	}
	defer stack.Stop()

	// determine whether we are worker or moocher
	var central string
	var centralKey string
	lockfile, err := os.Open(lockFilename)
	if err == nil {
		defer lockfile.Close()
		in, err := ioutil.ReadAll(lockfile)
		if err != nil {
			log.Error("lockfile set but cant read")
			return
		}
		out := strings.Split(string(in), "|")
		central = out[0]
		centralKey = string(out[1])
		lockfile.Close()
	} else {
		lockfile, err = os.Create(lockFilename)
		if err != nil {
			log.Error("lock create fail", "err", err)
			return
		}
		defer os.RemoveAll(lockfile.Name())
		defer lockfile.Close()
		me := stack.Server().Self().String()

		buf := bytes.NewBufferString(me)
		buf.Write([]byte("|"))
		buf.Write([]byte(bzzCfg.PublicKey))
		c, err := lockfile.Write(buf.Bytes())
		if err != nil || c != buf.Len() {
			log.Error("lock write fail", "err", err)
			return
		}
		lockfile.Close()
	}

	// get the rpc
	client, err := stack.Attach()
	if err != nil {
		log.Error("get rpc fail", "err", err)
		return
	}
	defer client.Close()

	// if a moocher, connect to the worker
	// protocol will start, and start a ticker submitting jobs
	// notice that we now do this with the pss add call
	if len(central) != 0 {
		log.Info("connecting", "peer", central)
		err := client.Call(nil, "admin_addPeer", string(central))
		if err != nil {
			log.Error("addpeer fail", "err", err)
			return
		}
		log.Info("connecting pss peer", "peer", "0x")
		time.Sleep(time.Millisecond * 250)
		var peers []p2p.PeerInfo
		err = client.Call(&peers, "admin_peers")
		if err != nil {
			log.Error("peerinfo fail", "err", err)
			return
		}
		// assume first is the worker
		bzzaddr, ok := peers[0].Protocols["hive"].(map[string]interface{})
		if !ok {
			log.Error("no bzzaddr on peer")
			return
		}
		err = client.Call(nil, "demops_addPeer", centralKey, bzzaddr["OAddr"])
		if err != nil {
			log.Error("pss addpeer fail", "err", err)
			return
		}

	}

	sigC := make(chan os.Signal)
	signal.Notify(sigC, syscall.SIGINT)

	if !svc.IsWorker() {
		go func() {
			t := time.NewTicker(submitInterval)
			for {
				select {
				case <-t.C:
					data := make([]byte, 64)
					rand.Read(data)
					difficulty := rand.Intn(maxDifficulty-minDifficulty) + minDifficulty
					var id uint64
					err := client.Call(&id, "demo_submit", data, difficulty)
					if err != nil {
						log.Warn("job not accepted", "err", err)
					} else {
						log.Info("job submitted", "id", id)
					}
				case <-sigC:
					t.Stop()
					return
				}
			}

		}()
	}

	<-sigC
}
