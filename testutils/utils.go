package testutils

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	btcdocker "github.com/elnosh/btc-docker-test"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut04"
	"github.com/elnosh/gonuts/cashu/nuts/nut10"
	"github.com/elnosh/gonuts/cashu/nuts/nut11"
	"github.com/elnosh/gonuts/cashu/nuts/nut14"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint"
	"github.com/elnosh/gonuts/mint/lightning"
	"github.com/elnosh/gonuts/wallet"
	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/macaroons"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc/credentials"
	"gopkg.in/macaroon.v2"
)

const (
	NUM_BLOCKS int64 = 110
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
	mintRes, err := wallet.RequestMint(amount, wallet.CurrentMint())
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

func MintConfig(
	lnd *btcdocker.Lnd,
	port string,
	derivationPathIdx uint32,
	dbpath string,
	dbMigrationPath string,
	inputFeePpk uint,
	limits mint.MintLimits,
) (*mint.Config, error) {
	if err := os.MkdirAll(dbpath, 0750); err != nil {
		return nil, err
	}

	var lightningClient lightning.Client
	if lnd != nil {
		var err error
		lightningClient, err = LndClient(lnd, dbpath)
		if err != nil {
			return nil, err
		}
	} else {
		lightningClient = &lightning.FakeBackend{}
	}

	timeout := time.Second * 2
	mintConfig := &mint.Config{
		DerivationPathIdx: derivationPathIdx,
		Port:              port,
		MintPath:          dbpath,
		DBMigrationPath:   dbMigrationPath,
		InputFeePpk:       inputFeePpk,
		Limits:            limits,
		LightningClient:   lightningClient,
		LogLevel:          mint.Disable,
		MeltTimeout:       &timeout,
	}

	return mintConfig, nil
}

func LndClient(lnd *btcdocker.Lnd, dbpath string) (*lightning.LndClient, error) {
	if err := os.MkdirAll(dbpath, 0750); err != nil {
		return nil, err
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

	creds, err := credentials.NewClientTLSFromFile(filepath.Join(nodeDir, "/tls.cert"), "")
	if err != nil {
		return nil, err
	}

	macaroonBytes, err := os.ReadFile(macaroonPath)
	if err != nil {
		return nil, fmt.Errorf("error reading macaroon: os.ReadFile %v", err)
	}

	macaroon := &macaroon.Macaroon{}
	if err = macaroon.UnmarshalBinary(macaroonBytes); err != nil {
		return nil, fmt.Errorf("unable to decode macaroon: %v", err)
	}
	macarooncreds, err := macaroons.NewMacaroonCredential(macaroon)
	if err != nil {
		return nil, fmt.Errorf("error setting macaroon creds: %v", err)
	}
	lndConfig := lightning.LndConfig{
		GRPCHost: lnd.Host + ":" + lnd.GrpcPort,
		Cert:     creds,
		Macaroon: macarooncreds,
	}
	lndClient, err := lightning.SetupLndClient(lndConfig)
	if err != nil {
		return nil, fmt.Errorf("error setting LND client: %v", err)
	}

	return lndClient, nil
}

func CreateTestMint(
	lnd *btcdocker.Lnd,
	dbpath string,
	dbMigrationPath string,
	inputFeePpk uint,
	limits mint.MintLimits,
) (*mint.Mint, error) {
	config, err := MintConfig(lnd, "", 0, dbpath, dbMigrationPath, inputFeePpk, limits)
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
	port string,
	derivationPathIdx uint32,
	dbpath string,
	dbMigrationPath string,
	inputFeePpk uint,
) (*mint.MintServer, error) {
	config, err := MintConfig(lnd, port, derivationPathIdx, dbpath, dbMigrationPath, inputFeePpk, mint.MintLimits{})
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

func CreateBlindedMessages(amount uint64, keyset crypto.MintKeyset) (cashu.BlindedMessages, []string, []*secp256k1.PrivateKey, error) {
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
	secrets []string, rs []*secp256k1.PrivateKey, keyset *crypto.MintKeyset) (cashu.Proofs, error) {

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

		r := hex.EncodeToString(rs[i].Serialize())
		proof := cashu.Proof{
			Amount: blindedSignature.Amount,
			Secret: secrets[i],
			C:      Cstr,
			Id:     blindedSignature.Id,
			DLEQ: &cashu.DLEQProof{
				E: blindedSignature.DLEQ.E,
				S: blindedSignature.DLEQ.S,
				R: r,
			},
		}

		proofs[i] = proof
	}

	return proofs, nil
}

func GetBlindedSignatures(amount uint64, mint *mint.Mint, payer *btcdocker.Lnd) (
	cashu.BlindedMessages,
	[]string,
	[]*secp256k1.PrivateKey,
	cashu.BlindedSignatures,
	error) {

	mintQuoteRequest := nut04.PostMintQuoteBolt11Request{Amount: amount, Unit: cashu.Sat.String()}
	mintQuoteResponse, err := mint.RequestMintQuote(mintQuoteRequest)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("error requesting mint quote: %v", err)
	}

	keyset := mint.GetActiveKeyset()
	blindedMessages, secrets, rs, err := CreateBlindedMessages(amount, keyset)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("error creating blinded message: %v", err)
	}

	ctx := context.Background()
	//pay invoice
	sendPaymentRequest := lnrpc.SendRequest{
		PaymentRequest: mintQuoteResponse.PaymentRequest,
	}
	response, _ := payer.Client.SendPaymentSync(ctx, &sendPaymentRequest)
	if len(response.PaymentError) > 0 {
		return nil, nil, nil, nil, fmt.Errorf("error paying invoice: %v", response.PaymentError)
	}

	mintTokensRequest := nut04.PostMintBolt11Request{
		Quote:   mintQuoteResponse.Id,
		Outputs: blindedMessages,
	}
	blindedSignatures, err := mint.MintTokens(mintTokensRequest)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("got unexpected error minting tokens: %v", err)
	}

	return blindedMessages, secrets, rs, blindedSignatures, nil
}

func GetValidProofsForAmount(amount uint64, mint *mint.Mint, payer *btcdocker.Lnd) (cashu.Proofs, error) {
	keyset := mint.GetActiveKeyset()
	_, secrets, rs, blindedSignatures, err := GetBlindedSignatures(amount, mint, payer)
	if err != nil {
		return nil, fmt.Errorf("error generating blinded signatures: %v", err)
	}

	proofs, err := ConstructProofs(blindedSignatures, secrets, rs, &keyset)
	if err != nil {
		return nil, fmt.Errorf("error constructing proofs: %v", err)
	}

	return proofs, nil
}

func BlindedMessagesFromSpendingCondition(
	splitAmounts []uint64,
	keysetId string,
	spendingCondition nut10.SpendingCondition,
) (
	cashu.BlindedMessages,
	[]string,
	[]*secp256k1.PrivateKey,
	error,
) {
	splitLen := len(splitAmounts)
	blindedMessages := make(cashu.BlindedMessages, splitLen)
	secrets := make([]string, splitLen)
	rs := make([]*secp256k1.PrivateKey, splitLen)
	for i, amt := range splitAmounts {
		r, err := secp256k1.GeneratePrivateKey()
		if err != nil {
			return nil, nil, nil, err
		}

		secret, err := nut10.NewSecretFromSpendingCondition(spendingCondition)
		if err != nil {
			return nil, nil, nil, err
		}

		B_, r, err := crypto.BlindMessage(secret, r)
		if err != nil {
			return nil, nil, nil, err
		}

		blindedMessages[i] = cashu.NewBlindedMessage(keysetId, amt, B_)
		secrets[i] = secret
		rs[i] = r
	}

	return blindedMessages, secrets, rs, nil
}

func GetProofsWithSpendingCondition(
	amount uint64,
	spendingCondition nut10.SpendingCondition,
	mint *mint.Mint,
	payer *btcdocker.Lnd,
) (cashu.Proofs, error) {
	mintQuoteRequest := nut04.PostMintQuoteBolt11Request{Amount: amount, Unit: cashu.Sat.String()}
	mintQuoteResponse, err := mint.RequestMintQuote(mintQuoteRequest)
	if err != nil {
		return nil, fmt.Errorf("error requesting mint quote: %v", err)
	}

	keyset := mint.GetActiveKeyset()

	split := cashu.AmountSplit(amount)
	blindedMessages, secrets, rs, err := BlindedMessagesFromSpendingCondition(split, keyset.Id, spendingCondition)
	if err != nil {
		return nil, fmt.Errorf("error creating blinded message: %v", err)
	}

	ctx := context.Background()
	//pay invoice
	sendPaymentRequest := lnrpc.SendRequest{
		PaymentRequest: mintQuoteResponse.PaymentRequest,
	}
	response, _ := payer.Client.SendPaymentSync(ctx, &sendPaymentRequest)
	if len(response.PaymentError) > 0 {
		return nil, fmt.Errorf("error paying invoice: %v", response.PaymentError)
	}

	mintTokensRequest := nut04.PostMintBolt11Request{
		Quote:   mintQuoteResponse.Id,
		Outputs: blindedMessages,
	}
	blindedSignatures, err := mint.MintTokens(mintTokensRequest)
	if err != nil {
		return nil, fmt.Errorf("got unexpected error minting tokens: %v", err)
	}

	proofs, err := ConstructProofs(blindedSignatures, secrets, rs, &keyset)
	if err != nil {
		return nil, fmt.Errorf("error constructing proofs: %v", err)
	}

	return proofs, nil
}

func AddP2PKWitnessToInputs(inputs cashu.Proofs, signingKeys []*btcec.PrivateKey) (cashu.Proofs, error) {
	for i, proof := range inputs {
		hash := sha256.Sum256([]byte(proof.Secret))
		signatures := make([]string, len(signingKeys))

		for j, key := range signingKeys {
			signature, err := schnorr.Sign(key, hash[:])
			if err != nil {
				return nil, err
			}
			sig := hex.EncodeToString(signature.Serialize())
			signatures[j] = sig
		}

		p2pkWitness := nut11.P2PKWitness{Signatures: signatures}
		witness, err := json.Marshal(p2pkWitness)
		if err != nil {
			return nil, err
		}

		proof.Witness = string(witness)
		inputs[i] = proof
	}

	return inputs, nil
}

func AddP2PKWitnessToOutputs(
	outputs cashu.BlindedMessages,
	signingKeys []*btcec.PrivateKey,
) (cashu.BlindedMessages, error) {
	for i, output := range outputs {
		msgToSign, err := hex.DecodeString(output.B_)
		if err != nil {
			return nil, err
		}
		hash := sha256.Sum256(msgToSign)
		signatures := make([]string, len(signingKeys))

		for j, key := range signingKeys {
			signature, err := schnorr.Sign(key, hash[:])
			if err != nil {
				return nil, err
			}
			sig := hex.EncodeToString(signature.Serialize())
			signatures[j] = sig
		}

		p2pkWitness := nut11.P2PKWitness{Signatures: signatures}
		witness, err := json.Marshal(p2pkWitness)
		if err != nil {
			return nil, err
		}
		output.Witness = string(witness)
		outputs[i] = output
	}

	return outputs, nil
}

// it will add signatures if signingKey is not nil
func AddHTLCWitnessToInputs(inputs cashu.Proofs, preimage string, signingKey *btcec.PrivateKey) (cashu.Proofs, error) {
	for i, proof := range inputs {
		htlcWitness := nut14.HTLCWitness{Preimage: preimage}

		if signingKey != nil {
			hash := sha256.Sum256([]byte(proof.Secret))
			signature, err := schnorr.Sign(signingKey, hash[:])
			if err != nil {
				return nil, err
			}
			sig := hex.EncodeToString(signature.Serialize())
			htlcWitness.Signatures = []string{sig}
		}

		witness, err := json.Marshal(htlcWitness)
		if err != nil {
			return nil, err
		}

		proof.Witness = string(witness)
		inputs[i] = proof
	}

	return inputs, nil
}

// it will add signatures if signingKey is not nil
func AddHTLCWitnessToOutputs(outputs cashu.BlindedMessages, preimage string, signingKey *btcec.PrivateKey) (cashu.BlindedMessages, error) {
	for i, output := range outputs {
		htlcWitness := nut14.HTLCWitness{Preimage: preimage}

		if signingKey != nil {
			msgToSign, err := hex.DecodeString(output.B_)
			if err != nil {
				return nil, err
			}
			hash := sha256.Sum256(msgToSign)
			signature, err := schnorr.Sign(signingKey, hash[:])
			if err != nil {
				return nil, err
			}
			sig := hex.EncodeToString(signature.Serialize())
			htlcWitness.Signatures = []string{sig}
		}

		witness, err := json.Marshal(htlcWitness)
		if err != nil {
			return nil, err
		}

		output.Witness = string(witness)
		outputs[i] = output
	}

	return outputs, nil
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

func CreateNutshellMintContainer(ctx context.Context, inputFeePpk int) (*NutshellMintContainer, error) {
	req := testcontainers.ContainerRequest{
		Image:        "cashubtc/nutshell:0.16.0",
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
			"MINT_INPUT_FEE_PPK":      strconv.Itoa(inputFeePpk),
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

func GenerateRandomBytes() ([]byte, error) {
	randomBytes := make([]byte, 32)
	_, err := rand.Read(randomBytes)
	if err != nil {
		return nil, err
	}
	return randomBytes[:], nil
}
