package wallet

import (
	cashurpc "buf.build/gen/go/cashu/rpc/protocolbuffers/go"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"slices"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint/lightning"
	"github.com/elnosh/gonuts/wallet/storage"
)

type Wallet struct {
	db storage.DB

	// default mint
	currentMint *walletMint
	// array of mints that have been trusted
	mints map[string]walletMint

	proofs           []*cashurpc.Proof
	domainSeparation bool
}

type walletMint struct {
	mintURL string
	// active keysets from mint
	activeKeysets map[string]crypto.Keyset
}

// get mint keysets
func mintInfo(ctx context.Context, mintURL string) (*walletMint, error) {
	activeKeysets, err := GetMintActiveKeysets(ctx, mintURL)
	if err != nil {
		return nil, fmt.Errorf("error getting active keysets from mint: %v", err)
	}
	return &walletMint{mintURL, activeKeysets}, nil
}

func (w *Wallet) addMint(ctx context.Context, mint string) (*walletMint, error) {
	url, err := url.Parse(mint)
	if err != nil {
		return nil, fmt.Errorf("invalid mint url: %v", err)
	}
	mintURL := url.String()

	mintInfo, err := mintInfo(ctx, mintURL)
	if err != nil {
		return nil, err
	}

	for _, keyset := range mintInfo.activeKeysets {
		w.db.SaveKeyset(&keyset)
	}
	w.mints[mintURL] = *mintInfo

	return mintInfo, nil
}

func InitStorage(path string) (storage.DB, error) {
	// bolt db atm
	return storage.InitBolt(path)
}

func LoadWallet(ctx context.Context, config Config) (*Wallet, error) {
	db, err := InitStorage(config.WalletPath)
	if err != nil {
		return nil, fmt.Errorf("InitStorage: %v", err)
	}

	wallet := &Wallet{db: db}
	wallet.mints = wallet.getWalletMints()

	currentMint, err := mintInfo(ctx, config.CurrentMintURL)
	if err != nil {
		return nil, fmt.Errorf("error getting keysets from mint: %v", err)
	}
	wallet.currentMint = currentMint

	_, ok := wallet.mints[config.CurrentMintURL]
	if !ok {
		for _, keyset := range currentMint.activeKeysets {
			db.SaveKeyset(&keyset)
		}
	}
	wallet.mints[config.CurrentMintURL] = *currentMint

	wallet.proofs = wallet.db.GetProofs()
	wallet.domainSeparation = true

	return wallet, nil
}

func GetMintActiveKeysets(ctx context.Context, mintURL string) (map[string]crypto.Keyset, error) {
	keysetsResponse, err := GetAllKeysets(ctx, mintURL)
	if err != nil {
		return nil, fmt.Errorf("error getting active keyset from mint: %v", err)
	}

	activeKeysets := make(map[string]crypto.Keyset)
	// convert keysetRes.Keys to map[uint64]KeyPair
	for _, keysetRes := range keysetsResponse.Keysets {
		// convert keysetRes.Keys to map[uint64]KeyPair
		keys := make(map[uint64]crypto.KeyPair)
		for key, value := range keysetRes.Keys {
			publicKeyBytes, err := hex.DecodeString(value)
			if err != nil {
				return nil, fmt.Errorf("error decoding private key: %v", err)
			}
			publicKey, err := secp256k1.ParsePubKey(publicKeyBytes)
			if err != nil {
				return nil, fmt.Errorf("error decoding private key: %v", err)
			}
			keys[key] = crypto.KeyPair{
				PublicKey: publicKey,
			}
		}
		keyset := crypto.Keyset{
			Id:      keysetRes.Id,
			MintURL: mintURL,
			Unit:    keysetRes.Unit,
			Active:  keysetRes.Active,
			Keys:    keys,
		}
		activeKeysets[keyset.Id] = keyset
	}
	return activeKeysets, nil
}
func (w *Wallet) GetBalance() uint64 {
	return cashu.Amount(w.proofs)
}

func (w *Wallet) GetBalanceByMints() map[string]uint64 {
	mintsBalances := make(map[string]uint64)

	for _, mint := range w.mints {
		var mintBalance uint64 = 0

		for _, keyset := range mint.activeKeysets {
			proofs := w.db.GetProofsByKeysetId(keyset.Id)
			mintBalance += cashu.Amount(proofs)
		}
		mintsBalances[mint.mintURL] = mintBalance
	}

	return mintsBalances
}

func (w *Wallet) RequestMint(ctx context.Context, amount uint64) (*cashurpc.PostMintQuoteBolt11Response, error) {
	mintRequest := &cashurpc.PostMintQuoteBolt11Request{Amount: amount, Unit: "sat"}
	mintResponse, err := PostMintQuoteBolt11(ctx, w.currentMint.mintURL, mintRequest)
	if err != nil {
		return nil, err
	}

	invoice := lightning.Invoice{Id: mintResponse.Quote,
		PaymentRequest: mintResponse.Request, Amount: amount,
		Expiry: mintResponse.Expiry}

	err = w.db.SaveInvoice(invoice)
	if err != nil {
		return nil, err
	}

	return mintResponse, nil
}

func (w *Wallet) CheckQuotePaid(ctx context.Context, quoteId string) bool {
	mintQuote, err := GetMintQuoteState(ctx, w.currentMint.mintURL, quoteId)
	if err != nil {
		return false
	}

	return mintQuote.Paid
}

func (w *Wallet) MintTokens(ctx context.Context, quoteId string) ([]*cashurpc.Proof, error) {
	mintQuote, err := GetMintQuoteState(ctx, w.currentMint.mintURL, quoteId)
	if err != nil {
		return nil, err
	}
	if false {
		return nil, errors.New("invoice not paid")
	}

	invoice := w.db.GetInvoice(mintQuote.Request)
	if invoice == nil {
		return nil, errors.New("invoice not found")
	}

	activeKeyset := w.GetActiveSatKeyset()
	blindedMessages, secrets, rs, err := w.CreateBlindedMessages(invoice.Amount, activeKeyset)
	if err != nil {
		return nil, fmt.Errorf("error creating blinded messages: %v", err)
	}

	postMintRequest := &cashurpc.PostMintBolt11Request{Quote: quoteId, Outputs: blindedMessages}
	mintResponse, err := PostMintBolt11(ctx, w.currentMint.mintURL, postMintRequest)
	if err != nil {
		return nil, err
	}

	// unblind the signatures from the promises and build the proofs
	proofs, err := w.ConstructProofs(mintResponse.Signatures, secrets, rs, &activeKeyset)
	if err != nil {
		return nil, fmt.Errorf("error constructing proofs: %v", err)
	}
	fmt.Println("sig")
	fmt.Println(mintResponse.Signatures)
	// store proofs in db
	err = w.saveProofs(proofs)
	if err != nil {
		return nil, fmt.Errorf("error storing proofs: %v", err)
	}
	fmt.Println(proofs)
	return proofs, nil
}

func (w *Wallet) Send(ctx context.Context, amount uint64, mintURL string) (*cashurpc.TokenV3, error) {
	proofsToSend, err := w.getProofsForAmount(ctx, amount, mintURL)
	if err != nil {
		return nil, err
	}

	token := cashu.NewToken(proofsToSend, mintURL, "sat")
	return &token, nil
}

// Receives Cashu token. If swap is true, it will swap the funds to the configured default mint.
// If false, it will add the proofs from the trusted mint.
func (w *Wallet) Receive(ctx context.Context, token *cashurpc.TokenV3, swap bool) (uint64, error) {
	if swap {
		trustedMintProofs, err := w.swapToTrusted(ctx, token)
		if err != nil {
			return 0, fmt.Errorf("error swapping token to trusted mint: %v", err)
		}
		return cashu.Amount(trustedMintProofs), nil
	} else {
		proofsToSwap := make([]*cashurpc.Proof, 0)
		for _, tokenProof := range token.Token {
			proofsToSwap = append(proofsToSwap, tokenProof.Proofs...)
		}

		tokenMintURL := "localhost:3339"

		mint, err := w.addMint(ctx, tokenMintURL)
		if err != nil {
			return 0, err
		}

		var activeSatKeyset crypto.Keyset
		// for now just taking first keyset val from map and assign it as sat keyset
		for _, k := range mint.activeKeysets {
			activeSatKeyset = k
			break
		}

		outputs, secrets, rs, err := w.CreateBlindedMessages(totalAmount(token), activeSatKeyset)
		if err != nil {
			return 0, fmt.Errorf("CreateBlindedMessages: %v", err)
		}

		swapRequest := &cashurpc.SwapRequest{Inputs: proofsToSwap, Outputs: outputs}
		swapResponse, err := PostSwap(ctx, tokenMintURL, swapRequest)
		if err != nil {
			return 0, err
		}

		proofs, err := w.ConstructProofs(swapResponse.Signatures, secrets, rs, &activeSatKeyset)
		if err != nil {
			return 0, fmt.Errorf("wallet.ConstructProofs: %v", err)
		}

		w.saveProofs(proofs)
		return cashu.Amount(proofs), nil
	}
}
func totalAmount(token *cashurpc.TokenV3) uint64 {
	var total uint64
	for _, baseToken := range token.Token {
		for _, proof := range baseToken.Proofs {
			total += proof.Amount
		}
	}
	return total
}
func (w *Wallet) swapToTrusted(ctx context.Context, token *cashurpc.TokenV3) ([]*cashurpc.Proof, error) {
	invoicePct := 0.99
	tokenAmount := totalAmount(token)
	tokenMintURL := token.Token[0].Mint
	amount := float64(tokenAmount) * invoicePct

	proofsToSwap := make([]*cashurpc.Proof, 0)
	for _, tokenProof := range token.Token {
		proofsToSwap = append(proofsToSwap, tokenProof.Proofs...)
	}

	var mintResponse *cashurpc.PostMintQuoteBolt11Response
	var meltQuoteResponse *cashurpc.PostMeltQuoteBolt11Response
	var err error

	for {
		mintResponse, err = w.RequestMint(ctx, uint64(amount))
		if err != nil {
			return nil, fmt.Errorf("error requesting mint: %v", err)
		}

		meltRequest := &cashurpc.PostMeltQuoteBolt11Request{Request: mintResponse.Request, Unit: "sat"}
		meltQuoteResponse, err = PostMeltQuoteBolt11(ctx, tokenMintURL, meltRequest)
		if err != nil {
			return nil, fmt.Errorf("error with melt request: %v", err)
		}

		if meltQuoteResponse.Amount+meltQuoteResponse.FeeReserve > tokenAmount {
			invoicePct -= 0.01
			amount *= invoicePct
		} else {
			break
		}
	}

	meltBolt11Request := &cashurpc.PostMeltBolt11Request{Quote: meltQuoteResponse.Quote, Inputs: proofsToSwap}
	meltBolt11Response, err := PostMeltBolt11(ctx, tokenMintURL, meltBolt11Request)
	if err != nil {
		return nil, fmt.Errorf("error melting token: %v", err)
	}

	if meltBolt11Response.Paid {
		proofs, err := w.MintTokens(ctx, mintResponse.Quote)
		if err != nil {
			return nil, fmt.Errorf("error minting tokens: %v", err)
		}
		return proofs, nil
	} else {
		return nil, errors.New("mint could not pay lightning invoice")
	}
}

func (w *Wallet) Melt(ctx context.Context, invoice string, mint string) (*cashurpc.PostMeltBolt11Response, error) {
	selectedMint, ok := w.mints[mint]
	if !ok {
		return nil, errors.New("mint does not exist")
	}

	meltRequest := &cashurpc.PostMeltQuoteBolt11Request{Request: invoice, Unit: "sat"}
	meltQuoteResponse, err := PostMeltQuoteBolt11(ctx, selectedMint.mintURL, meltRequest)
	if err != nil {
		return nil, err
	}

	amountNeeded := meltQuoteResponse.Amount + meltQuoteResponse.FeeReserve
	proofs, err := w.getProofsForAmount(ctx, amountNeeded, mint)
	if err != nil {
		return nil, err
	}

	meltBolt11Request := &cashurpc.PostMeltBolt11Request{Quote: meltQuoteResponse.Quote, Inputs: proofs}
	meltBolt11Response, err := PostMeltBolt11(ctx, selectedMint.mintURL, meltBolt11Request)
	if err != nil {
		return nil, err
	}

	// only delete proofs after invoice has been paid
	if meltBolt11Response.Paid {
		for _, proof := range proofs {
			w.db.DeleteProof(proof.Secret)
		}
	}

	return meltBolt11Response, nil
}

func (w *Wallet) GetProofsByMint(mintURL string) ([]*cashurpc.Proof, error) {
	selectedMint, ok := w.mints[mintURL]
	if !ok {
		return nil, errors.New("mint does not exist")
	}

	proofs := make([]*cashurpc.Proof, 0)
	for _, keyset := range selectedMint.activeKeysets {
		keysetProofs := w.db.GetProofsByKeysetId(keyset.Id)
		proofs = append(proofs, keysetProofs...)
	}

	return proofs, nil
}

func (w *Wallet) getProofsForAmount(ctx context.Context, amount uint64, mintURL string) ([]*cashurpc.Proof, error) {
	selectedMint, ok := w.mints[mintURL]
	if !ok {
		return nil, errors.New("mint does not exist")
	}

	balanceByMints := w.GetBalanceByMints()
	balance := balanceByMints[mintURL]
	if balance < amount {
		return nil, errors.New("not enough funds in selected mint")
	}

	// use proofs from inactive keysets first
	activeKeysetProofs := make([]*cashurpc.Proof, 0)
	inactiveKeysetProofs := make([]*cashurpc.Proof, 0)
	mintProofs, err := w.GetProofsByMint(mintURL)
	if err != nil {
		return nil, err
	}

	for _, proof := range mintProofs {
		isInactive := false
		for _, inactiveKeyset := range selectedMint.activeKeysets {
			if proof.Id == inactiveKeyset.Id && !inactiveKeyset.Active {
				isInactive = true
				break
			}
		}

		if isInactive {
			inactiveKeysetProofs = append(inactiveKeysetProofs, proof)
		} else {
			activeKeysetProofs = append(activeKeysetProofs, proof)
		}
	}

	selectedProofs := make([]*cashurpc.Proof, 0)
	var currentProofsAmount uint64 = 0
	addKeysetProofs := func(proofs []*cashurpc.Proof) {
		if currentProofsAmount < amount {
			for _, proof := range proofs {
				selectedProofs = append(selectedProofs, proof)
				currentProofsAmount += proof.Amount

				if currentProofsAmount == amount {
					for _, proofToDelete := range selectedProofs {
						w.db.DeleteProof(proofToDelete.Secret)
					}
				} else if currentProofsAmount > amount {
					break
				}

			}
		}
	}

	addKeysetProofs(inactiveKeysetProofs)
	addKeysetProofs(activeKeysetProofs)

	var activeSatKeyset crypto.Keyset
	for _, k := range selectedMint.activeKeysets {
		activeSatKeyset = k
		break
	}
	// blinded messages for send amount
	send, secrets, rs, err := w.CreateBlindedMessages(amount, activeSatKeyset)
	if err != nil {
		return nil, err
	}

	// blinded messages for change amount
	change, changeSecrets, changeRs, err := w.CreateBlindedMessages(currentProofsAmount-amount, activeSatKeyset)
	if err != nil {
		return nil, err
	}

	blindedMessages := make(cashu.BlindedMessages, len(send))
	copy(blindedMessages, send)
	blindedMessages = append(blindedMessages, change...)
	secrets = append(secrets, changeSecrets...)
	rs = append(rs, changeRs...)

	// sort messages, secrets and rs
	for i := 0; i < len(blindedMessages)-1; i++ {
		for j := i + 1; j < len(blindedMessages); j++ {
			if blindedMessages[i].Amount > blindedMessages[j].Amount {
				// Swap blinded messages
				blindedMessages[i], blindedMessages[j] = blindedMessages[j], blindedMessages[i]

				// Swap secrets
				secrets[i], secrets[j] = secrets[j], secrets[i]

				// Swap rs
				rs[i], rs[j] = rs[j], rs[i]
			}
		}
	}

	swapRequest := &cashurpc.SwapRequest{Inputs: selectedProofs, Outputs: blindedMessages}
	swapResponse, err := PostSwap(ctx, selectedMint.mintURL, swapRequest)
	if err != nil {
		return nil, err
	}

	for _, proof := range selectedProofs {
		w.db.DeleteProof(proof.Secret)
	}

	proofs, err := w.ConstructProofs(swapResponse.Signatures, secrets, rs, &activeSatKeyset)
	if err != nil {
		return nil, fmt.Errorf("wallet.ConstructProofs: %v", err)
	}

	proofsToSend := make([]*cashurpc.Proof, len(send))
	for i, sendmsg := range send {
		for j, proof := range proofs {
			if sendmsg.Amount == proof.Amount {
				proofsToSend[i] = proof
				proofs = slices.Delete(proofs, j, j+1)
				break
			}
		}
	}

	// remaining proofs are change proofs to save to db
	w.saveProofs(proofs)
	return proofsToSend, nil
}

func NewBlindedMessage(id string, amount uint64, B_ *secp256k1.PublicKey) *cashurpc.BlindedMessage {
	B_str := hex.EncodeToString(B_.SerializeCompressed())
	return &cashurpc.BlindedMessage{Amount: amount, B_: B_str, Id: id}
}

// returns Blinded messages, secrets - [][]byte, and list of r
func (w *Wallet) CreateBlindedMessages(amount uint64, keyset crypto.Keyset) (cashu.BlindedMessages, []string, []*secp256k1.PrivateKey, error) {
	splitAmounts := cashu.AmountSplit(amount)
	splitLen := len(splitAmounts)

	blindedMessages := make(cashu.BlindedMessages, splitLen)
	secrets := make([]string, splitLen)
	rs := make([]*secp256k1.PrivateKey, splitLen)

	for i, amt := range splitAmounts {
		// create random secret
		secretBytes := make([]byte, 32)
		_, err := rand.Read(secretBytes)
		if err != nil {
			return nil, nil, nil, err
		}
		secret := hex.EncodeToString(secretBytes)

		// generate new private key r
		r, err := secp256k1.GeneratePrivateKey()
		if err != nil {
			return nil, nil, nil, err
		}

		var B_ *secp256k1.PublicKey
		if w.domainSeparation {
			B_, r, err = crypto.BlindMessageDomainSeparated(secret, r)
			if err != nil {
				return nil, nil, nil, err
			}
		} else {
			B_, r = crypto.BlindMessage(secret, r)
		}
		blindedMessage := NewBlindedMessage(keyset.Id, amt, B_)
		blindedMessages[i] = blindedMessage
		secrets[i] = secret
		rs[i] = r
	}

	return blindedMessages, secrets, rs, nil
}

func (w *Wallet) ConstructProofs(blindedSignatures cashu.BlindedSignatures,
	secrets []string, rs []*secp256k1.PrivateKey, keyset *crypto.Keyset) ([]*cashurpc.Proof, error) {

	if len(blindedSignatures) != len(secrets) && len(blindedSignatures) != len(rs) {
		return nil, errors.New("lengths do not match")
	}

	proofs := make([]*cashurpc.Proof, len(blindedSignatures))
	for i, blindedSignature := range blindedSignatures {
		C_bytes, err := hex.DecodeString(blindedSignature.C_)
		if err != nil {
			return nil, err
		}
		C_, err := secp256k1.ParsePubKey(C_bytes)
		if err != nil {
			return nil, err
		}

		K := keyset.Keys[blindedSignature.Amount].PublicKey
		C := crypto.UnblindSignature(C_, rs[i], K)
		Cstr := hex.EncodeToString(C.SerializeCompressed())

		proof := &cashurpc.Proof{Amount: blindedSignature.Amount,
			Secret: secrets[i], C: Cstr, Id: blindedSignature.Id}

		proofs[i] = proof
	}

	return proofs, nil
}

func (w *Wallet) GetActiveSatKeyset() crypto.Keyset {
	var activeKeyset crypto.Keyset
	for _, keyset := range w.currentMint.activeKeysets {
		if keyset.Unit == "sat" {
			activeKeyset = keyset
			break
		}
	}
	return activeKeyset
}

func (w *Wallet) getWalletMints() map[string]walletMint {
	walletMints := make(map[string]walletMint)

	keysets := w.db.GetKeysets()
	for k, mintKeysets := range keysets {
		activeKeysets := make(map[string]crypto.Keyset)
		for _, keyset := range mintKeysets {
			activeKeysets[keyset.Id] = keyset
		}
		walletMints[k] = walletMint{mintURL: k, activeKeysets: activeKeysets}
	}

	return walletMints
}

func (w *Wallet) CurrentMint() string {
	return w.currentMint.mintURL
}

func (w *Wallet) TrustedMints() map[string]walletMint {
	return w.mints
}

func (w *Wallet) saveProofs(proofs []*cashurpc.Proof) error {
	for _, proof := range proofs {
		err := w.db.SaveProof(proof)
		if err != nil {
			return err
		}
	}
	w.proofs = append(w.proofs, proofs...)
	return nil
}

func (w *Wallet) GetInvoice(pr string) *lightning.Invoice {
	return w.db.GetInvoice(pr)
}
