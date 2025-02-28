package testutils

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	mathrand "math/rand/v2"
	"net"
	"net/http"
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
	"github.com/elnosh/btc-docker-test/cln"
	"github.com/elnosh/btc-docker-test/lnd"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut01"
	"github.com/elnosh/gonuts/cashu/nuts/nut04"
	"github.com/elnosh/gonuts/cashu/nuts/nut10"
	"github.com/elnosh/gonuts/cashu/nuts/nut11"
	"github.com/elnosh/gonuts/cashu/nuts/nut14"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint"
	"github.com/elnosh/gonuts/mint/lightning"
	"github.com/elnosh/gonuts/wallet"
	"github.com/elnosh/gonuts/wallet/client"
	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/lnrpc/invoicesrpc"
	"github.com/lightningnetwork/lnd/macaroons"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc/credentials"
	"gopkg.in/macaroon.v2"
)

const (
	NUM_BLOCKS int64 = 110
)

type LightningBackend interface {
	Info() (*NodeInfo, error)
	Synced() (bool, error)
	NewAddress() (btcutil.Address, error)
	ConnectToPeer(peer *Peer) error
	OpenChannel(to *Peer, amount uint64) error
	PayInvoice(string) error
	CreateInvoice(amount uint64) (*Invoice, error)
	LookupInvoice(hash string) (*Invoice, error)
	CreateHodlInvoice(amount uint64, hash string) (*Invoice, error)
	// CLN does not support HODL invoices (unless using a plugin)
	// passing invoice and payer as a hack. Payer will pay the invoice
	// just like a regular invoice.
	SettleHodlInvoice(preimage string, invoice string, payer *CLNBackend) error
}

type Peer struct {
	Pubkey string
	Addr   string
}

type NodeInfo struct {
	Pubkey string
	Addr   string
}

type Invoice struct {
	PaymentRequest string
	Hash           string
	Preimage       string
}

type LndBackend struct {
	*lnd.Lnd
}

func (lndContainer *LndBackend) Info() (*NodeInfo, error) {
	ctx := context.Background()
	infoResponse, err := lndContainer.Client.GetInfo(ctx, &lnrpc.GetInfoRequest{})
	if err != nil {
		return nil, err
	}
	return &NodeInfo{
		Pubkey: infoResponse.IdentityPubkey,
		Addr:   lndContainer.ContainerIP + ":" + lnd.LND_P2P_PORT,
	}, nil
}

func (lndContainer *LndBackend) Synced() (bool, error) {
	ctx := context.Background()
	infoResponse, err := lndContainer.Client.GetInfo(ctx, &lnrpc.GetInfoRequest{})
	if err != nil {
		return false, err
	}
	if infoResponse.SyncedToChain {
		return true, nil
	}
	return false, nil
}

func (lndContainer *LndBackend) NewAddress() (btcutil.Address, error) {
	ctx := context.Background()
	addressResponse, err := lndContainer.Client.NewAddress(ctx, &lnrpc.NewAddressRequest{Type: 0})
	if err != nil {
		return nil, err
	}

	address, err := btcutil.DecodeAddress(addressResponse.Address, &chaincfg.RegressionNetParams)
	if err != nil {
		return nil, err
	}

	return address, nil
}

func (lndContainer *LndBackend) ConnectToPeer(peer *Peer) error {
	toLightningAddress := lnrpc.LightningAddress{
		Pubkey: peer.Pubkey,
		Host:   peer.Addr,
	}
	connectPeerRequest := lnrpc.ConnectPeerRequest{
		Addr: &toLightningAddress,
		Perm: false,
	}

	ctx := context.Background()
	_, err := lndContainer.Client.ConnectPeer(ctx, &connectPeerRequest)
	if err != nil {
		return err
	}

	return nil
}

func (lndContainer *LndBackend) OpenChannel(to *Peer, amount uint64) error {
	toPubkeyBytes, err := hex.DecodeString(to.Pubkey)
	if err != nil {
		return err
	}
	openChannelRequest := lnrpc.OpenChannelRequest{
		NodePubkey:         toPubkeyBytes,
		LocalFundingAmount: int64(amount),
		PushSat:            int64(amount / 2),
	}

	ctx := context.Background()
	_, err = lndContainer.Client.OpenChannelSync(ctx, &openChannelRequest)
	if err != nil {
		return err
	}

	return nil
}

func (lndContainer *LndBackend) PayInvoice(invoice string) error {
	ctx := context.Background()
	sendPaymentRequest := lnrpc.SendRequest{
		PaymentRequest: invoice,
	}
	response, _ := lndContainer.Client.SendPaymentSync(ctx, &sendPaymentRequest)
	if len(response.PaymentError) > 0 {
		return errors.New(response.PaymentError)
	}

	return nil
}

func (lndContainer *LndBackend) CreateInvoice(amount uint64) (*Invoice, error) {
	ctx := context.Background()
	invoice := lnrpc.Invoice{Value: int64(amount)}
	addInvoiceResponse, err := lndContainer.Client.AddInvoice(ctx, &invoice)
	if err != nil {
		return nil, err
	}
	return &Invoice{
		PaymentRequest: addInvoiceResponse.PaymentRequest,
		Hash:           hex.EncodeToString(addInvoiceResponse.RHash),
	}, nil
}

func (lndContainer *LndBackend) CreateHodlInvoice(amount uint64, hash string) (*Invoice, error) {
	paymentHash, err := hex.DecodeString(hash)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	hodlInvoice := invoicesrpc.AddHoldInvoiceRequest{Hash: paymentHash, Value: int64(amount)}
	addHodlInvoiceRes, err := lndContainer.InvoicesClient.AddHoldInvoice(ctx, &hodlInvoice)
	if err != nil {
		return nil, err
	}

	return &Invoice{
		PaymentRequest: addHodlInvoiceRes.PaymentRequest,
		Hash:           hash,
	}, nil
}

// NOTE: invoice and payer are not used. Those are an ugly hack for CLN
func (lndContainer *LndBackend) SettleHodlInvoice(preimage string, invoice string, payer *CLNBackend) error {
	preimageBytes, err := hex.DecodeString(preimage)
	if err != nil {
		return err
	}

	settleHodlInvoice := invoicesrpc.SettleInvoiceMsg{Preimage: preimageBytes}
	ctx := context.Background()
	_, err = lndContainer.InvoicesClient.SettleInvoice(ctx, &settleHodlInvoice)
	if err != nil {
		return err
	}

	return nil
}

func (lndContainer *LndBackend) LookupInvoice(hash string) (*Invoice, error) {
	ctx := context.Background()
	paymentHash, err := hex.DecodeString(hash)
	if err != nil {
		return nil, err
	}

	invoice, err := lndContainer.Client.LookupInvoice(ctx, &lnrpc.PaymentHash{RHash: paymentHash})
	if err != nil {
		return nil, err
	}
	return &Invoice{
		PaymentRequest: invoice.PaymentRequest,
		Hash:           hex.EncodeToString(invoice.RHash),
		Preimage:       hex.EncodeToString(invoice.RPreimage),
	}, nil
}

type CLNBackend struct {
	*cln.CLN
	client *http.Client
	url    string
}

func NewCLNBackend(cln *cln.CLN) *CLNBackend {
	return &CLNBackend{
		CLN:    cln,
		client: &http.Client{},
		url:    fmt.Sprintf("%s/v1", "http://"+cln.Host+":"+cln.RestPort),
	}
}

func (clnContainer *CLNBackend) Post(url string, body interface{}) (*http.Response, error) {
	var jsonBody []byte
	if body != nil {
		var err error
		jsonBody, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Rune", clnContainer.Rune)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	return clnContainer.client.Do(req)
}

type InfoResponse struct {
	Id                 string `json:"id"`
	BitcoindSyncWarn   string `json:"warning_bitcoind_sync"`
	LightningdSyncWarn string `json:"warning_lightningd_sync"`
}

func (clnContainer *CLNBackend) Info() (*NodeInfo, error) {
	resp, err := clnContainer.Post(clnContainer.url+"/getinfo", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var infoResponse InfoResponse
	if err := json.Unmarshal(bodyBytes, &infoResponse); err != nil {
		return nil, err
	}

	return &NodeInfo{
		Pubkey: infoResponse.Id,
		Addr:   clnContainer.ContainerIP + ":" + cln.CLN_P2P_PORT,
	}, nil
}

func (clnContainer *CLNBackend) Synced() (bool, error) {
	resp, err := clnContainer.Post(clnContainer.url+"/getinfo", nil)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}

	var infoResponse InfoResponse
	if err := json.Unmarshal(bodyBytes, &infoResponse); err != nil {
		return false, err
	}

	if len(infoResponse.BitcoindSyncWarn) > 0 || len(infoResponse.LightningdSyncWarn) > 0 {
		return false, nil
	}

	return true, nil
}

func (clnContainer *CLNBackend) NewAddress() (btcutil.Address, error) {
	body := map[string]string{
		"addresstype": "bech32",
	}

	resp, err := clnContainer.Post(clnContainer.url+"/newaddr", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var response struct {
		Bech32 string `json:"bech32"`
	}
	if err := json.Unmarshal(bodyBytes, &response); err != nil {
		return nil, err
	}

	address, err := btcutil.DecodeAddress(response.Bech32, &chaincfg.RegressionNetParams)
	if err != nil {
		return nil, err
	}
	return address, nil
}

func (clnContainer *CLNBackend) ConnectToPeer(peer *Peer) error {
	id := fmt.Sprintf("%s@%s", peer.Pubkey, peer.Addr)
	body := map[string]string{
		"id": id,
	}

	resp, err := clnContainer.Post(clnContainer.url+"/connect", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var connectResponse struct {
		Id string `json:"id"`
	}
	if err := json.Unmarshal(bodyBytes, &connectResponse); err != nil {
		return err
	}

	if len(connectResponse.Id) == 0 {
		return errors.New("could not connect to peer")
	}

	return nil
}

func (clnContainer *CLNBackend) OpenChannel(to *Peer, amount uint64) error {
	body := map[string]any{
		"id":     to.Pubkey,
		"amount": amount,
	}

	resp, err := clnContainer.Post(clnContainer.url+"/fundchannel", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var fundChannelResponse struct {
		Tx string `json:"tx"`
	}
	if err := json.Unmarshal(bodyBytes, &fundChannelResponse); err != nil {
		return err
	}
	if len(fundChannelResponse.Tx) == 0 {
		return errors.New("could not open channel")
	}

	return nil
}

func (clnContainer *CLNBackend) PayInvoice(invoice string) error {
	body := map[string]string{
		"bolt11": invoice,
	}

	resp, err := clnContainer.Post(clnContainer.url+"/pay", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var payResponse struct {
		PaymentPreimage string `json:"payment_preimage"`
	}
	if err := json.Unmarshal(bodyBytes, &payResponse); err != nil {
		return err
	}
	if len(payResponse.PaymentPreimage) == 0 {
		return errors.New("payment failed")
	}

	return nil
}

func (clnContainer *CLNBackend) CreateInvoice(amount uint64) (*Invoice, error) {
	body := map[string]any{
		"amount":      amount * 1000,
		"label":       time.Now().Unix(),
		"description": "test",
	}

	resp, err := clnContainer.Post(clnContainer.url+"/invoice", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var invoiceResponse struct {
		Bolt11      string `json:"bolt11"`
		PaymentHash string `json:"payment_hash"`
	}
	if err := json.Unmarshal(bodyBytes, &invoiceResponse); err != nil {
		return nil, err
	}
	if len(invoiceResponse.Bolt11) == 0 {
		return nil, errors.New("could not create invoice")
	}

	return &Invoice{
		PaymentRequest: invoiceResponse.Bolt11,
		Hash:           invoiceResponse.PaymentHash,
	}, nil
}

func (clnContainer *CLNBackend) CreateHodlInvoice(amount uint64, hash string) (*Invoice, error) {
	return clnContainer.CreateInvoice(amount)
}

func (clnContainer *CLNBackend) SettleHodlInvoice(preimage string, invoice string, payer *CLNBackend) error {
	if err := payer.PayInvoice(invoice); err != nil {
		return err
	}
	return nil
}

func (clnContainer *CLNBackend) LookupInvoice(hash string) (*Invoice, error) {
	body := map[string]string{
		"payment_hash": hash,
	}

	resp, err := clnContainer.Post(clnContainer.url+"/listinvoices", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var listInvoicesResponse struct {
		Invoices []struct {
			Bolt11      string `json:"bolt11"`
			PaymentHash string `json:"payment_hash"`
			Preimage    string `json:"payment_preimage"`
		} `json:"invoices"`
	}
	if err := json.Unmarshal(bodyBytes, &listInvoicesResponse); err != nil {
		return nil, err
	}
	if len(listInvoicesResponse.Invoices) == 0 {
		return nil, errors.New("could not lookup invoice")
	}

	return &Invoice{
		PaymentRequest: listInvoicesResponse.Invoices[0].Bolt11,
		Hash:           listInvoicesResponse.Invoices[0].PaymentHash,
		Preimage:       listInvoicesResponse.Invoices[0].Preimage,
	}, nil
}

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

func FundNode(ctx context.Context, bitcoind *btcdocker.Bitcoind, lightningNode LightningBackend) error {
	address, err := lightningNode.NewAddress()
	if err != nil {
		return fmt.Errorf("error generating address: %v", err)
	}

	_, err = bitcoind.Client.GenerateToAddress(NUM_BLOCKS, address, nil)
	if err != nil {
		return err
	}

	time.Sleep(time.Second * 2)
	if err := SyncNode(lightningNode); err != nil {
		return err
	}

	return nil
}

func OpenChannel(
	ctx context.Context,
	bitcoind *btcdocker.Bitcoind,
	from LightningBackend,
	to LightningBackend,
	amount uint64,
) error {
	toInfo, err := to.Info()
	if err != nil {
		return fmt.Errorf("error getting node info: %v", err)
	}
	peer := &Peer{
		Pubkey: toInfo.Pubkey,
		Addr:   toInfo.Addr,
	}

	if err := from.ConnectToPeer(peer); err != nil {
		return fmt.Errorf("error connecting to peer: %v", err)
	}

	if err := from.OpenChannel(peer, amount); err != nil {
		return fmt.Errorf("error opening channel: %v", err)
	}

	if err := MineBlocks(bitcoind, 6); err != nil {
		return fmt.Errorf("error generating new blocks: %v", err)
	}
	time.Sleep(time.Second * 2)
	if err := SyncNode(from); err != nil {
		return err
	}

	return nil
}

func SyncNode(node LightningBackend) error {
	for range 50 {
		synced, err := node.Synced()
		if err != nil {
			return fmt.Errorf("could not get node info: %v", err)
		}
		if synced {
			return nil
		}
		time.Sleep(time.Millisecond * 500)
	}
	return errors.New("could not sync node")
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

func FundCashuWallet(ctx context.Context, wallet *wallet.Wallet, backend LightningBackend, amount uint64) error {
	mintRes, err := wallet.RequestMint(amount, wallet.CurrentMint())
	if err != nil {
		return fmt.Errorf("error requesting mint: %v", err)
	}

	if backend != nil {
		if err := backend.PayInvoice(mintRes.Request); err != nil {
			return fmt.Errorf("error paying invoice: %v", err)
		}
	}

	_, err = wallet.MintTokens(mintRes.Quote)
	if err != nil {
		return fmt.Errorf("got unexpected error: %v", err)
	}

	return nil
}

func MintConfig(
	backend lightning.Client,
	port int,
	derivationPathIdx uint32,
	dbpath string,
	inputFeePpk uint,
	limits mint.MintLimits,
) (*mint.Config, error) {
	if err := os.MkdirAll(dbpath, 0750); err != nil {
		return nil, err
	}

	timeout := time.Second * 2
	mintConfig := &mint.Config{
		DerivationPathIdx: derivationPathIdx,
		Port:              port,
		MintPath:          dbpath,
		InputFeePpk:       inputFeePpk,
		Limits:            limits,
		LightningClient:   backend,
		EnableMPP:         true,
		LogLevel:          mint.Disable,
		MeltTimeout:       &timeout,
	}

	return mintConfig, nil
}

func LndClient(lnd *lnd.Lnd) (*lightning.LndClient, error) {
	creds, err := credentials.NewClientTLSFromFile(filepath.Join(lnd.LndDir, "/tls.cert"), "")
	if err != nil {
		return nil, err
	}

	macaroon := &macaroon.Macaroon{}
	if err = macaroon.UnmarshalBinary(lnd.AdminMacaroon); err != nil {
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
	backend lightning.Client,
	dbpath string,
	inputFeePpk uint,
	limits mint.MintLimits,
) (*mint.Mint, error) {
	config, err := MintConfig(backend, 0, 0, dbpath, inputFeePpk, limits)
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
	backend lightning.Client,
	port int,
	derivationPathIdx uint32,
	dbpath string,
	inputFeePpk uint,
) (*mint.MintServer, error) {
	config, err := MintConfig(backend, port, derivationPathIdx, dbpath, inputFeePpk, mint.MintLimits{})
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

func CreateBlindedMessages(amount uint64, keysetId string) (cashu.BlindedMessages, []string, []*secp256k1.PrivateKey, error) {
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

		blindedMessage := newBlindedMessage(keysetId, amt, B_)
		blindedMessages[i] = blindedMessage
		secrets[i] = secret
		rs[i] = r
	}

	return blindedMessages, secrets, rs, nil
}

func ConstructProofs(blindedSignatures cashu.BlindedSignatures,
	secrets []string, rs []*secp256k1.PrivateKey, keyset nut01.Keyset) (cashu.Proofs, error) {

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

		publicKey, ok := keyset.Keys[blindedSignature.Amount]
		if !ok {
			return nil, errors.New("key not found")
		}

		C := crypto.UnblindSignature(C_, rs[i], publicKey)
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

func GetBlindedSignatures(amount uint64, mint *mint.Mint, payer LightningBackend) (
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
	blindedMessages, secrets, rs, err := CreateBlindedMessages(amount, keyset.Id)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("error creating blinded message: %v", err)
	}

	if err := payer.PayInvoice(mintQuoteResponse.PaymentRequest); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("error paying invoice: %v", err)
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

func GetValidProofsForAmount(amount uint64, mint *mint.Mint, payer LightningBackend) (cashu.Proofs, error) {
	keyset := mint.GetActiveKeyset()
	_, secrets, rs, blindedSignatures, err := GetBlindedSignatures(amount, mint, payer)
	if err != nil {
		return nil, fmt.Errorf("error generating blinded signatures: %v", err)
	}

	proofs, err := ConstructProofs(blindedSignatures, secrets, rs, keyset)
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
	payer LightningBackend,
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

	//pay invoice
	if err := payer.PayInvoice(mintQuoteResponse.PaymentRequest); err != nil {
		return nil, fmt.Errorf("error paying invoice: %v", err)
	}

	mintTokensRequest := nut04.PostMintBolt11Request{
		Quote:   mintQuoteResponse.Id,
		Outputs: blindedMessages,
	}
	blindedSignatures, err := mint.MintTokens(mintTokensRequest)
	if err != nil {
		return nil, fmt.Errorf("got unexpected error minting tokens: %v", err)
	}

	proofs, err := ConstructProofs(blindedSignatures, secrets, rs, keyset)
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
	keysetResponse, err := client.GetAllKeysets(mint)
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

func CreateNutshellMintContainer(ctx context.Context, inputFeePpk int, lnd *lnd.Lnd) (*NutshellMintContainer, error) {
	envMap := map[string]string{
		"MINT_LISTEN_HOST":   "0.0.0.0",
		"MINT_LISTEN_PORT":   "3338",
		"MINT_INPUT_FEE_PPK": strconv.Itoa(inputFeePpk),
		"MINT_PRIVATE_KEY":   generateRandomString(32),
	}

	started := true
	containerMacaroonPath := "/admin.macaroon"
	containerTLSCert := "/tls.cert"
	if lnd != nil {
		envMap["MINT_BACKEND_BOLT11_SAT"] = "LndRPCWallet"
		envMap["MINT_LND_RPC_ENDPOINT"] = lnd.ContainerIP + ":" + "10009"
		envMap["MINT_LND_RPC_CERT"] = containerTLSCert
		envMap["MINT_LND_RPC_MACAROON"] = containerMacaroonPath
		started = false
	} else {
		envMap["MINT_BACKEND_BOLT11_SAT"] = "FakeWallet"
	}

	req := testcontainers.ContainerRequest{
		//Image:        "cashubtc/nutshell:0.16.5",
		Image:        "cashubtc/nutshell:latest",
		ExposedPorts: []string{"3338"},
		Cmd: []string{
			"poetry",
			"run",
			"mint",
		},
		Env:        envMap,
		WaitingFor: wait.ForListeningPort("3338"),
	}

	if lnd != nil {
		req.Networks = []string{lnd.Network}
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          started,
	})
	if err != nil {
		return nil, err
	}

	if !started {
		tlsCert := filepath.Join(lnd.LndDir, "tls.cert")
		if err := container.CopyToContainer(ctx, lnd.AdminMacaroon, containerMacaroonPath, 0777); err != nil {
			return nil, err
		}
		if err := container.CopyFileToContainer(ctx, tlsCert, containerTLSCert, 0777); err != nil {
			return nil, err
		}
		if err := container.Start(ctx); err != nil {
			return nil, err
		}
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

func GetAvailablePort() (int, error) {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func generateRandomString(length int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = letters[mathrand.IntN(len(letters))]
	}
	return string(b)
}

func GenerateRandomBytes() ([]byte, error) {
	randomBytes := make([]byte, 32)
	_, err := rand.Read(randomBytes)
	if err != nil {
		return nil, err
	}
	return randomBytes[:], nil
}
