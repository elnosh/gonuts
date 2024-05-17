package testutils

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	btcdocker "github.com/elnosh/btc-docker-test"
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
