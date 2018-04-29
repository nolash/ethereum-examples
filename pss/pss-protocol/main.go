package main

import (
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

	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/node"

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

	svc := service.NewDemoService(19, 6, time.Second*10)
	if err := stack.Register(func(ctx *node.ServiceContext) (node.Service, error) {
		return svc, nil
	}); err != nil {
		log.Error(err.Error())
		return
	}
	if err := stack.Start(); err != nil {
		log.Error(err.Error())
		return
	}
	defer stack.Stop()

	var central []byte
	lockfile, err := os.Open(lockFilename)
	if err == nil {
		defer lockfile.Close()
		central, err = ioutil.ReadAll(lockfile)
		if err != nil {
			log.Error("lockfile set but cant read")
			return
		}
		svc.SetDifficulty(0)
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
		c, err := lockfile.Write([]byte(me))
		if err != nil || c != len(me) {
			log.Error("lock write fail", "err", err)
			return
		}
		lockfile.Close()
	}

	client, err := stack.Attach()
	if err != nil {
		log.Error("get rpc fail", "err", err)
		return
	}
	defer client.Close()

	if len(central) != 0 {
		log.Info("connecting", "peer", central)
		err := client.Call(nil, "admin_addPeer", string(central))
		if err != nil {
			log.Error("addpeer fail", "err", err)
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
