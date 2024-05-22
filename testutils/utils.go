package testutils

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	btcdocker "github.com/elnosh/btc-docker-test"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint"
	"github.com/elnosh/gonuts/wallet"
	"github.com/lightningnetwork/lnd/lnrpc"
)

const NUM_BLOCKS int64 = 110

func MineBlocks(bitcoind *btcdocker.Bitcoind, numBlocks int64) error {
	address, err := bitcoind.Client.GetNewAddress("")
	if err != nil {
		return fmt.Errorf("error getting new address: %v", err)
	}

	_, err = bitcoind.Client.GenerateToAddress(numBlocks, address, nil)
	if err != nil {
		return err
	}

	return nil
}

func FundLndNode(ctx context.Context, bitcoind *btcdocker.Bitcoind, lnd *btcdocker.Lnd) error {
	addressResponse, err := lnd.Client.NewAddress(ctx, &lnrpc.NewAddressRequest{Type: 0})
	if err != nil {
		return fmt.Errorf("error generating address: %v", err)
	}

	address, err := btcutil.DecodeAddress(addressResponse.Address, &chaincfg.RegressionNetParams)
	if err != nil {
		return fmt.Errorf("error generating address: %v", err)
	}

	_, err = bitcoind.Client.GenerateToAddress(NUM_BLOCKS, address, nil)
	if err != nil {
		return err
	}

	time.Sleep(time.Second * 2)
	err = SyncLndNode(ctx, lnd)
	if err != nil {
		return err
	}

	return nil
}

func OpenChannel(
	ctx context.Context,
	bitcoind *btcdocker.Bitcoind,
	from *btcdocker.Lnd,
	to *btcdocker.Lnd,
	amount int64,
) error {
	infoResponse, err := to.Client.GetInfo(ctx, &lnrpc.GetInfoRequest{})
	if err != nil {
		return fmt.Errorf("error getting information about node: %v", err)
	}

	toPubkey := infoResponse.IdentityPubkey
	toPubkeyBytes, err := hex.DecodeString(toPubkey)
	if err != nil {
		return fmt.Errorf("error decoding node pubkey: %v", err)
	}

	toLightningAddress := lnrpc.LightningAddress{
		Pubkey: toPubkey,
		Host:   to.ContainerIP + ":" + btcdocker.LND_P2P_PORT,
	}
	connectPeerRequest := lnrpc.ConnectPeerRequest{
		Addr: &toLightningAddress,
		Perm: false,
	}
	_, err = from.Client.ConnectPeer(ctx, &connectPeerRequest)
	if err != nil {
		return fmt.Errorf("error connecting to peer: %v", err)
	}

	openChannelRequest := lnrpc.OpenChannelRequest{
		NodePubkey:         toPubkeyBytes,
		LocalFundingAmount: amount,
		PushSat:            amount / 2,
	}

	_, err = from.Client.OpenChannelSync(ctx, &openChannelRequest)
	if err != nil {
		return err
	}

	if err = MineBlocks(bitcoind, 6); err != nil {
		return fmt.Errorf("error generating new blocks: %v", err)
	}
	time.Sleep(time.Second * 2)
	err = SyncLndNode(ctx, from)
	if err != nil {
		return err
	}

	return nil
}

func SyncLndNode(ctx context.Context, lnd *btcdocker.Lnd) error {
	for range 50 {
		infoResponse, err := lnd.Client.GetInfo(ctx, &lnrpc.GetInfoRequest{})
		if err != nil {
			return fmt.Errorf("error getting node info: %v", err)
		}
		if infoResponse.SyncedToChain {
			return nil
		}
		time.Sleep(time.Millisecond * 500)
	}

	return errors.New("could not sync LND")
}

func CreateTestWallet(walletpath, defaultMint string) (*wallet.Wallet, error) {
	if err := os.MkdirAll(walletpath, 0750); err != nil {
		return nil, err
	}
	walletConfig := wallet.Config{
		WalletPath:     walletpath,
		CurrentMintURL: defaultMint,
	}
	testWallet, err := wallet.LoadWallet(walletConfig)
	if err != nil {
		return nil, err
	}

	return testWallet, nil
}

func mintConfig(lnd *btcdocker.Lnd, key, port, dbpath string) (*mint.Config, error) {
	if err := os.MkdirAll(dbpath, 0750); err != nil {
		return nil, err
	}
	mintConfig := &mint.Config{
		PrivateKey:     key,
		DerivationPath: "0/0/0",
		Port:           port,
		DBPath:         dbpath,
	}
	nodeDir := lnd.LndDir

	os.Setenv("LIGHTNING_BACKEND", "Lnd")
	os.Setenv("LND_REST_HOST", "https://"+lnd.Host+":"+lnd.RestPort)
	os.Setenv("LND_CERT_PATH", filepath.Join(nodeDir, "/tls.cert"))
	os.Setenv("LND_MACAROON_PATH", filepath.Join(nodeDir, "/data/chain/bitcoin/regtest/admin.macaroon"))

	return mintConfig, nil
}

func CreateTestMint(
	lnd *btcdocker.Lnd,
	key string,
	port string,
	dbpath string,
) (*mint.Mint, error) {
	config, err := mintConfig(lnd, key, port, dbpath)
	if err != nil {
		return nil, err
	}

	mint, err := mint.LoadMint(*config)
	if err != nil {
		return nil, err
	}
	return mint, nil
}

func CreateTestMintServer(
	lnd *btcdocker.Lnd,
	key string,
	port string,
	dbpath string,
) (*mint.MintServer, error) {
	config, err := mintConfig(lnd, key, port, dbpath)
	if err != nil {
		return nil, err
	}

	mintServer, err := mint.SetupMintServer(*config)
	if err != nil {
		return nil, err
	}

	return mintServer, nil
}

func newBlindedMessage(id string, amount uint64, B_ *secp256k1.PublicKey) cashu.BlindedMessage {
	B_str := hex.EncodeToString(B_.SerializeCompressed())
	return cashu.BlindedMessage{Amount: amount, B_: B_str, Id: id}
}

func CreateBlindedMessages(amount uint64, keyset crypto.Keyset) (cashu.BlindedMessages, []string, []*secp256k1.PrivateKey, error) {
	splitAmounts := cashu.AmountSplit(amount)
	splitLen := len(splitAmounts)

	blindedMessages := make(cashu.BlindedMessages, splitLen)
	secrets := make([]string, splitLen)
	rs := make([]*secp256k1.PrivateKey, splitLen)

	for i, amt := range splitAmounts {
		// generate new private key r
		r, err := secp256k1.GeneratePrivateKey()
		if err != nil {
			return nil, nil, nil, err
		}

		var B_ *secp256k1.PublicKey
		var secret string
		// generate random secret until it finds valid point
		for {
			secretBytes := make([]byte, 32)
			_, err = rand.Read(secretBytes)
			if err != nil {
				return nil, nil, nil, err
			}
			secret = hex.EncodeToString(secretBytes)
			B_, r, err = crypto.BlindMessage(secret, r)
			if err == nil {
				break
			}
		}

		blindedMessage := newBlindedMessage(keyset.Id, amt, B_)
		blindedMessages[i] = blindedMessage
		secrets[i] = secret
		rs[i] = r
	}

	return blindedMessages, secrets, rs, nil
}
