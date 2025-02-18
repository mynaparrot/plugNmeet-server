package factory

import (
	"fmt"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"
	"strings"
)

func NewNatsConnection(appCnf *config.AppConfig) error {
	info := appCnf.NatsInfo
	var opt nats.Option
	var err error

	if info.Nkey != nil {
		opt, err = nKeyOptionFromSeedText(*info.Nkey)
		if err != nil {
			return err
		}
	} else {
		opt = nats.UserInfo(info.User, info.Password)
	}

	nc, err := nats.Connect(strings.Join(info.NatsUrls, ","), opt)
	if err != nil {
		return err
	}
	appCnf.NatsConn = nc

	js, err := jetstream.New(nc)
	if err != nil {
		return err
	}
	appCnf.JetStream = js

	return nil
}

func nKeyOptionFromSeedText(seedText string) (nats.Option, error) {
	kp, err := nKeyPairFromSeed(seedText)
	if err != nil {
		return nil, err
	}
	// Wipe our key on exit.
	defer kp.Wipe()

	pub, err := kp.PublicKey()
	if err != nil {
		return nil, err
	}
	if !nkeys.IsValidPublicUserKey(pub) {
		return nil, fmt.Errorf("nats: Not a valid nkey user seed")
	}
	sigCB := func(nonce []byte) ([]byte, error) {
		return sigHandler(nonce, seedText)
	}
	return nats.Nkey(pub, sigCB), nil
}

func sigHandler(nonce []byte, seed string) ([]byte, error) {
	kp, err := nKeyPairFromSeed(seed)
	if err != nil {
		return nil, err
	}
	// Wipe our key on exit.
	defer kp.Wipe()

	sig, _ := kp.Sign(nonce)
	return sig, nil
}

func nKeyPairFromSeed(seedText string) (nkeys.KeyPair, error) {
	contents := []byte(seedText)
	defer wipeSlice(contents)
	return jwt.ParseDecoratedNKey(contents)
}

// Wipe slice with 'x', for clearing contents of creds or nkey seed file.
func wipeSlice(buf []byte) {
	for i := range buf {
		buf[i] = 'x'
	}
}
