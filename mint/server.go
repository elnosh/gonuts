package mint

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut01"
	"github.com/elnosh/gonuts/cashu/nuts/nut02"
	"github.com/elnosh/gonuts/cashu/nuts/nut03"
	"github.com/elnosh/gonuts/cashu/nuts/nut04"
	"github.com/elnosh/gonuts/config"
	"github.com/elnosh/gonuts/crypto"
	"github.com/gorilla/mux"
)

func writeErr(rw http.ResponseWriter, err cashu.Error) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(400)
	errRes, _ := json.Marshal(err)
	rw.Write(errRes)
}

type MintServer struct {
	httpServer *http.Server
	mint       *Mint
	logger     *slog.Logger
}

func StartMintServer(server *MintServer) {
	server.logger.Info("mint server listening on: " + server.httpServer.Addr)
	log.Fatal(server.httpServer.ListenAndServe())
}

func SetupMintServer(config config.Config) (*MintServer, error) {
	mint, err := LoadMint(config)
	if err != nil {
		return nil, err
	}
	logger := slog.Default()
	mintServer := &MintServer{mint: mint, logger: logger}
	mintServer.setupHttpServer()
	return mintServer, nil
}

func (ms *MintServer) setupHttpServer() {
	r := mux.NewRouter()

	r.HandleFunc("/v1/keys", ms.getActiveKeysets).Methods(http.MethodGet)
	r.HandleFunc("/v1/keysets", ms.getKeysetsList).Methods(http.MethodGet)
	r.HandleFunc("/v1/keys/{id}", ms.getKeysetById).Methods(http.MethodGet)
	r.HandleFunc("/v1/mint/quote/{method}", ms.mintRequest).Methods(http.MethodPost)
	r.HandleFunc("/v1/mint/quote/{method}/{quote_id}", ms.getQuoteState).Methods(http.MethodGet)
	r.HandleFunc("/v1/mint/{method}", ms.mintTokens).Methods(http.MethodPost)
	r.HandleFunc("/v1/swap", ms.swapRequest).Methods(http.MethodPost)

	server := &http.Server{
		Addr:    "127.0.0.1:3338",
		Handler: r,
	}

	ms.httpServer = server
}

func (ms *MintServer) getActiveKeysets(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("Content-Type", "application/json")

	getKeysResponse := buildKeysResponse(ms.mint.ActiveKeysets)
	jsonRes, err := json.Marshal(getKeysResponse)
	if err != nil {
		writeErr(rw, cashu.StandardErr)
		return
	}

	rw.Write(jsonRes)
}

func (ms *MintServer) getKeysetsList(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("Content-Type", "application/json")

	getKeysetsResponse := ms.buildAllKeysetsResponse()
	jsonRes, err := json.Marshal(getKeysetsResponse)
	if err != nil {
		writeErr(rw, cashu.StandardErr)
		return
	}

	rw.Write(jsonRes)
}

func (ms *MintServer) getKeysetById(rw http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	id := vars["id"]

	var keyset *crypto.Keyset
	for _, ks := range ms.mint.Keysets {
		if ks.Id == id {
			keyset = &ks
		}
	}

	if keyset == nil {
		writeErr(rw, cashu.KeysetNotExist)
		return
	}

	getKeysResponse := buildKeysResponse([]crypto.Keyset{*keyset})
	jsonRes, err := json.Marshal(getKeysResponse)
	if err != nil {
		writeErr(rw, cashu.StandardErr)
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	rw.Write(jsonRes)
}

func (ms *MintServer) mintRequest(rw http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	method := vars["method"]

	if method != "bolt11" {
		writeErr(rw, cashu.PaymentMethodNotSupported)
		return
	}

	var mintReq nut04.PostMintQuoteBolt11Request
	cashuErr := decodeJsonReqBody(req, &mintReq)
	if cashuErr != nil {
		writeErr(rw, *cashuErr)
		return
	}

	if mintReq.Unit != "sat" {
		writeErr(rw, cashu.UnitNotSupported)
		return
	}

	invoice, err := ms.mint.RequestInvoice(mintReq.Amount)
	if err != nil {
		writeErr(rw, cashu.Error{Detail: err.Error(), Code: cashu.InvoiceErrCode})
		return
	}

	reqMintResponse := nut04.PostMintQuoteBolt11Response{Quote: invoice.Id,
		Request: invoice.PaymentRequest, Paid: invoice.Settled, Expiry: invoice.Expiry}
	jsonRes, err := json.Marshal(reqMintResponse)
	if err != nil {
		writeErr(rw, cashu.StandardErr)
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	rw.Write(jsonRes)
}

func (ms *MintServer) getQuoteState(rw http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	method := vars["method"]
	if method != "bolt11" {
		writeErr(rw, cashu.PaymentMethodNotSupported)
		return
	}

	quoteId := vars["quote_id"]
	invoice := ms.mint.GetInvoice(quoteId)
	if invoice == nil {
		writeErr(rw, cashu.InvoiceNotExist)
		return
	}

	settled := ms.mint.LightningClient.InvoiceSettled(invoice.PaymentHash)
	if settled != invoice.Settled {
		invoice.Settled = settled
		ms.mint.SaveInvoice(*invoice)
	}

	reqMintResponse := nut04.PostMintQuoteBolt11Response{Quote: invoice.Id,
		Request: invoice.PaymentRequest, Paid: settled, Expiry: invoice.Expiry}
	jsonRes, err := json.Marshal(reqMintResponse)
	if err != nil {
		writeErr(rw, cashu.StandardErr)
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	rw.Write(jsonRes)
}

func (ms *MintServer) mintTokens(rw http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	method := vars["method"]
	if method != "bolt11" {
		writeErr(rw, cashu.PaymentMethodNotSupported)
		return
	}

	var mintReq nut04.PostMintBolt11Request
	cashuErr := decodeJsonReqBody(req, &mintReq)
	if cashuErr != nil {
		writeErr(rw, *cashuErr)
		return
	}

	invoice := ms.mint.GetInvoice(mintReq.Quote)
	if invoice == nil {
		http.Error(rw, "invoice not found", http.StatusNotFound)
		return
	}

	if !invoice.Settled {
		settled := ms.mint.LightningClient.InvoiceSettled(invoice.PaymentHash)
		if !settled {
			writeErr(rw, cashu.InvoiceNotPaid)
			return
		}

		var totalAmount uint64 = 0
		for _, message := range mintReq.Outputs {
			totalAmount += message.Amount
		}

		if totalAmount > invoice.Amount {
			writeErr(rw, cashu.OutputsOverInvoice)
			return
		}

		blindedSignatures, err := cashu.SignBlindedMessages(mintReq.Outputs, &ms.mint.ActiveKeysets[0])
		if err != nil {
			writeErr(rw, cashu.StandardErr)
			return
		}
		invoice.Settled = true
		invoice.Redeemed = true
		ms.mint.SaveInvoice(*invoice)

		signatures := nut04.PostMintBolt11Response{Signatures: blindedSignatures}
		rw.Header().Set("Content-Type", "application/json")
		json.NewEncoder(rw).Encode(signatures)
		return
	} else {
		if invoice.Redeemed {
			writeErr(rw, cashu.InvoiceTokensIssued)
			return
		}
	}
}

func (ms *MintServer) swapRequest(rw http.ResponseWriter, req *http.Request) {
	var swapReq nut03.PostSwapRequest
	cashuErr := decodeJsonReqBody(req, &swapReq)
	if cashuErr != nil {
		writeErr(rw, *cashuErr)
		return
	}

	// check that proofs are valid
	for _, proof := range swapReq.Inputs {
		dbProof := ms.mint.GetProof(proof.Secret)
		if dbProof != nil {
			writeErr(rw, cashu.ProofAlreadyUsed)
			return
		}
		secret, err := hex.DecodeString(proof.Secret)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}

		var privateKey []byte
		for _, kp := range ms.mint.ActiveKeysets[0].KeyPairs {
			if kp.Amount == proof.Amount {
				privateKey = kp.PrivateKey
			}
		}
		k := secp256k1.PrivKeyFromBytes(privateKey)

		Cbytes, err := hex.DecodeString(proof.C)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}

		C, err := secp256k1.ParsePubKey(Cbytes)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}

		if !crypto.Verify(secret, k, C) {
			writeErr(rw, cashu.InvalidProof)
			return
		}
	}

	// if verification complete, sign blinded messages and add used proofs to db
	blindedSignatures, err := cashu.SignBlindedMessages(swapReq.Outputs, &ms.mint.ActiveKeysets[0])
	if err != nil {
		writeErr(rw, cashu.StandardErr)
		return
	}

	signatures := nut03.PostSwapResponse{Signatures: blindedSignatures}

	for _, proof := range swapReq.Inputs {
		ms.mint.SaveProof(proof)
	}

	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(signatures)
}

func buildKeysResponse(keysets []crypto.Keyset) nut01.GetKeysResponse {
	keysResponse := nut01.GetKeysResponse{}

	for _, keyset := range keysets {
		pks := keyset.DerivePublic()
		keyRes := nut01.Keyset{Id: keyset.Id, Unit: keyset.Unit, Keys: pks}
		keysResponse.Keysets = append(keysResponse.Keysets, keyRes)
	}

	return keysResponse
}

func (ms *MintServer) buildAllKeysetsResponse() nut02.GetKeysetResponse {
	keysetsResponse := nut02.GetKeysetResponse{}

	for _, keyset := range ms.mint.Keysets {
		keysetRes := nut02.Keyset{Id: keyset.Id, Unit: keyset.Unit, Active: keyset.Active}
		keysetsResponse.Keysets = append(keysetsResponse.Keysets, keysetRes)
	}

	return keysetsResponse
}

func decodeJsonReqBody(req *http.Request, dst any) *cashu.Error {
	ct := req.Header.Get("Content-Type")
	if ct != "" {
		mediaType := strings.ToLower(strings.Split(ct, ";")[0])
		if mediaType != "application/json" {
			ctError := cashu.BuildCashuError("Content-Type header is not application/json", cashu.StandardErrCode)
			return ctError
		}
	}

	dec := json.NewDecoder(req.Body)
	// error if unknown field is specified in the json req body
	dec.DisallowUnknownFields()

	err := dec.Decode(&dst)
	if err != nil {
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		var cashuErr *cashu.Error

		switch {
		case errors.As(err, &syntaxErr):
			msg := fmt.Sprintf("bad json at %d", syntaxErr.Offset)
			cashuErr = cashu.BuildCashuError(msg, cashu.StandardErrCode)

		case errors.As(err, &typeErr):
			msg := fmt.Sprintf("invalid %v for field %q", typeErr.Value, typeErr.Field)
			cashuErr = cashu.BuildCashuError(msg, cashu.StandardErrCode)

		case errors.Is(err, io.EOF):
			return &cashu.EmptyBody

		case strings.HasPrefix(err.Error(), "json: unknown field "):
			invalidField := strings.TrimPrefix(err.Error(), "json: unknown field ")
			msg := fmt.Sprintf("Request body contains unknown field %s", invalidField)
			cashuErr = cashu.BuildCashuError(msg, cashu.StandardErrCode)

		default:
			cashuErr = cashu.BuildCashuError(err.Error(), cashu.StandardErrCode)
		}
		return cashuErr
	}

	return nil
}
