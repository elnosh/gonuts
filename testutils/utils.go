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
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	NUM_BLOCKS    int64 = 110
	BOLT11_METHOD       = "bolt11"
	SAT_UNIT            = "sat"
)

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

func FundCashuWallet(ctx context.Context, wallet *wallet.Wallet, lnd *btcdocker.Lnd, amount uint64) error {
	mintRes, err := wallet.RequestMint(amount)
	if err != nil {
		return fmt.Errorf("error requesting mint: %v", err)
	}

	//pay invoice
	sendPaymentRequest := lnrpc.SendRequest{
		PaymentRequest: mintRes.Request,
	}
	response, _ := lnd.Client.SendPaymentSync(ctx, &sendPaymentRequest)
	if len(response.PaymentError) > 0 {
		return fmt.Errorf("error paying invoice: %v", response.PaymentError)
	}

	_, err = wallet.MintTokens(mintRes.Quote)
	if err != nil {
		return fmt.Errorf("got unexpected error: %v", err)
	}

	return nil
}

func mintConfig(
	lnd *btcdocker.Lnd,
	key string,
	port string,
	dbpath string,
	inputFeePpk uint,
) (*mint.Config, error) {
	if err := os.MkdirAll(dbpath, 0750); err != nil {
		return nil, err
	}
	mintConfig := &mint.Config{
		PrivateKey:     key,
		DerivationPath: "0/0/0",
		Port:           port,
		DBPath:         dbpath,
		InputFeePpk:    inputFeePpk,
	}
	nodeDir := lnd.LndDir

	macaroonPath := filepath.Join(dbpath, "/admin.macaroon")
	file, err := os.Create(macaroonPath)
	if err != nil {
		return nil, fmt.Errorf("error creating macaroon file: %v", err)
	}

	_, err = file.Write(lnd.AdminMacaroon)
	if err != nil {
		return nil, fmt.Errorf("error writing to macaroon file: %v", err)
	}

	os.Setenv("LIGHTNING_BACKEND", "Lnd")
	os.Setenv("LND_GRPC_HOST", lnd.Host+":"+lnd.GrpcPort)
	os.Setenv("LND_CERT_PATH", filepath.Join(nodeDir, "/tls.cert"))
	os.Setenv("LND_MACAROON_PATH", macaroonPath)

	return mintConfig, nil
}

func CreateTestMint(
	lnd *btcdocker.Lnd,
	key string,
	dbpath string,
	inputFeePpk uint,
) (*mint.Mint, error) {
	config, err := mintConfig(lnd, key, "", dbpath, inputFeePpk)
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
	inputFeePpk uint,
) (*mint.MintServer, error) {
	config, err := mintConfig(lnd, key, port, dbpath, inputFeePpk)
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

func ConstructProofs(blindedSignatures cashu.BlindedSignatures,
	secrets []string, rs []*secp256k1.PrivateKey, keyset *crypto.Keyset) (cashu.Proofs, error) {

	if len(blindedSignatures) != len(secrets) || len(blindedSignatures) != len(rs) {
		return nil, errors.New("lengths do not match")
	}

	proofs := make(cashu.Proofs, len(blindedSignatures))
	for i, blindedSignature := range blindedSignatures {
		C_bytes, err := hex.DecodeString(blindedSignature.C_)
		if err != nil {
			return nil, err
		}
		C_, err := secp256k1.ParsePubKey(C_bytes)
		if err != nil {
			return nil, err
		}

		keyp, ok := keyset.Keys[blindedSignature.Amount]
		if !ok {
			return nil, errors.New("key not found")
		}

		C := crypto.UnblindSignature(C_, rs[i], keyp.PublicKey)
		Cstr := hex.EncodeToString(C.SerializeCompressed())

		proof := cashu.Proof{Amount: blindedSignature.Amount,
			Secret: secrets[i], C: Cstr, Id: blindedSignature.Id}

		proofs[i] = proof
	}

	return proofs, nil
}

func GetValidProofsForAmount(amount uint64, mint *mint.Mint, payer *btcdocker.Lnd) (cashu.Proofs, error) {
	mintQuoteResponse, err := mint.RequestMintQuote(BOLT11_METHOD, amount, SAT_UNIT)
	if err != nil {
		return nil, fmt.Errorf("error requesting mint quote: %v", err)
	}

	var keyset crypto.Keyset
	for _, k := range mint.ActiveKeysets {
		keyset = k
		break
	}

	blindedMessages, secrets, rs, err := CreateBlindedMessages(amount, keyset)
	if err != nil {
		return nil, fmt.Errorf("error creating blinded message: %v", err)
	}

	ctx := context.Background()
	//pay invoice
	sendPaymentRequest := lnrpc.SendRequest{
		PaymentRequest: mintQuoteResponse.Request,
	}
	response, _ := payer.Client.SendPaymentSync(ctx, &sendPaymentRequest)
	if len(response.PaymentError) > 0 {
		return nil, fmt.Errorf("error paying invoice: %v", response.PaymentError)
	}

	blindedSignatures, err := mint.MintTokens(BOLT11_METHOD, mintQuoteResponse.Quote, blindedMessages)
	if err != nil {
		return nil, fmt.Errorf("got unexpected error minting tokens: %v", err)
	}

	proofs, err := ConstructProofs(blindedSignatures, secrets, rs, &keyset)
	if err != nil {
		return nil, fmt.Errorf("error constructing proofs: %v", err)
	}

	return proofs, nil
}

func Fees(proofs cashu.Proofs, mint string) (uint, error) {
	keysetResponse, err := wallet.GetAllKeysets(mint)
	if err != nil {
		return 0, err
	}

	feePpk := keysetResponse.Keysets[0].InputFeePpk
	var fees uint = 0
	for i := 0; i < len(proofs); i++ {
		fees += feePpk
	}
	return (fees + 999) / 1000, nil
}

type NutshellMintContainer struct {
	testcontainers.Container
	Host string
}

func CreateNutshellMintContainer(ctx context.Context) (*NutshellMintContainer, error) {
	req := testcontainers.ContainerRequest{
		Image:        "cashubtc/nutshell:0.15.3",
		ExposedPorts: []string{"3338"},
		Cmd: []string{
			"poetry",
			"run",
			"mint",
		},
		Env: map[string]string{
			"MINT_LISTEN_HOST":        "0.0.0.0",
			"MINT_LISTEN_PORT":        "3338",
			"MINT_BACKEND_BOLT11_SAT": "FakeWallet",
			"MINT_PRIVATE_KEY":        "secretkey",
		},
		WaitingFor: wait.ForListeningPort("3338"),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, err
	}

	ip, err := container.Host(ctx)
	if err != nil {
		return nil, err
	}

	mappedPort, err := container.MappedPort(ctx, "3338")
	if err != nil {
		return nil, err
	}

	nutshellHost := "http://" + ip + ":" + mappedPort.Port()
	nutshellContainer := &NutshellMintContainer{
		Container: container,
		Host:      nutshellHost,
	}

	return nutshellContainer, nil
}
