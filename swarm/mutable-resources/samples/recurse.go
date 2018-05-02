package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/swarm/storage"

	colorable "github.com/mattn/go-colorable"
)

var (
	dir = flag.String("d", "", "datadir")
)

func init() {
	flag.Parse()
	if *dir == "" {
		var ok bool
		home, ok := os.LookupEnv("HOME")
		if !ok {
			panic("unknown datadir")
		}
		*dir = fmt.Sprintf("%s/.ethereum", home)
	}

	log.PrintOrigins(true)
	log.Root().SetHandler(log.LvlFilterHandler(log.LvlTrace, log.StreamHandler(colorable.NewColorableStderr(), log.TerminalFormat(true))))
}

func main() {

	name := flag.Arg(0)

	backends := []accounts.Backend{
		keystore.NewKeyStore(fmt.Sprintf("%s/keystore", *dir), keystore.StandardScryptN, keystore.StandardScryptP),
	}
	accman := accounts.NewManager(backends...)
	var chunkdir string
	//var bzzkey string
	var resourceSigner storage.ResourceSigner
OUTER:
	for _, w := range accman.Wallets() {
		for _, a := range w.Accounts() {
			trydir := fmt.Sprintf("%s/swarm/bzz-%x/chunks", *dir, a.Address)
			if f, err := os.Open(trydir); err == nil {
				f.Close()
				chunkdir = trydir
				//bzzkey = a.Address.Hex()
				resourceSigner = &signer{
					keystore: backends[0].(*keystore.KeyStore),
					account:  a,
				}
				break OUTER
			}

		}
	}
	if chunkdir == "" {
		log.Error("no chunkdir found")
		return
	}
	log.Info("using chunkdir", "dir", chunkdir)
	lstoreparams := storage.NewDefaultLocalStoreParams()
	lstoreparams.ChunkDbPath = chunkdir
	lstore, err := storage.NewLocalStore(lstoreparams, nil)
	if err != nil {
		log.Error(err.Error())
		return
	}
	//
	//	kp := network.NewKadParams()
	//	to := network.NewKademlia(
	//		common.FromHex(bzzkey),
	//		kp,
	//	)
	//
	//	db := storage.NewDBAPI(lstore)
	//	delivery := stream.NewDelivery(to, db)
	//
	//	streamer := streamer.NewRegistry(
	//	dpaChunkStore := storage.NewNetStore(lstore, delivery, db, nil, &stream.RegistryOptions{
	//		DoRetrieve: true,
	//		SyncUpdateDelay: time.Second * 10,
	//	})
	defer lstore.Close()

	rhparams := &storage.ResourceHandlerParams{}
	rhparams.Signer = resourceSigner
	rhparams.EthClient = storage.NewBlockEstimator()
	rh, err := storage.NewResourceHandler(rhparams)
	if err != nil {
		log.Error(err.Error())
		return
	}
	rh.SetStore(lstore)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rsrc, err := rh.LookupLatestByName(ctx, name, true, &storage.ResourceLookupParams{})
	if err != nil {
		log.Error(err.Error())
		return
	}
	_ = rsrc
}

// implements storage.ResourceSigner
type signer struct {
	keystore *keystore.KeyStore
	account  accounts.Account
}

func (s *signer) Sign(h common.Hash) (signature storage.Signature, err error) {
	signaturebytes, err := s.keystore.SignHash(s.account, h.Bytes())
	if err != nil {
		return signature, err
	}
	copy(signature[:], signaturebytes)
	return signature, nil
}
