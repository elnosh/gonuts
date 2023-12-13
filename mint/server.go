package mint

import (
	"encoding/json"
	"log"
	"log/slog"
	"net/http"

	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut01"
	"github.com/elnosh/gonuts/cashu/nuts/nut02"
	"github.com/elnosh/gonuts/cashu/nuts/nut04"
	"github.com/elnosh/gonuts/config"
	"github.com/elnosh/gonuts/crypto"
	"github.com/gorilla/mux"
)

func writeErr(rw http.ResponseWriter, err cashu.Error) {
	errRes, _ := json.Marshal(err)
	rw.WriteHeader(400)
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
	r.HandleFunc("/v1/mint/quote/{method}", ms.requestMint).Methods(http.MethodPost)
	r.HandleFunc("/v1/mint/quote/{method}/{quote_id}", ms.getQuoteState).Methods(http.MethodGet)
	r.HandleFunc("/v1/mint/{method}", ms.mintTokens).Methods(http.MethodPost)
	//r.HandleFunc("/mint", ms.postMint).Methods(http.MethodPost)

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
		writeErr(rw, cashu.KeysetsErr)
		return
	}

	rw.Write(jsonRes)
	return
}

func (ms *MintServer) getKeysetsList(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("Content-Type", "application/json")

	getKeysetsResponse := ms.buildAllKeysetsResponse()
	jsonRes, err := json.Marshal(getKeysetsResponse)
	if err != nil {
		writeErr(rw, cashu.KeysetsErr)
		return
	}

	rw.Write(jsonRes)
	return
}

func (ms *MintServer) getKeysetById(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(req)

	id, ok := vars["id"]
	if !ok {
		http.Error(rw, "please specify a keyset ID", http.StatusBadRequest)
		return
	}

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
		writeErr(rw, cashu.KeysetsErr)
		return
	}

	rw.Write(jsonRes)
}

func (ms *MintServer) requestMint(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(req)
	method, ok := vars["method"]
	if !ok {
		writeErr(rw, cashu.PaymentMethodNotSpecified)
		return
	}

	if method != "bolt11" {
		writeErr(rw, cashu.PaymentMethodNotSupported)
		return
	}

	// check for invalid input
	var mintReq nut04.PostMintQuoteBolt11Request
	err := json.NewDecoder(req.Body).Decode(&mintReq)
	if err != nil {
		writeErr(rw, cashu.StandardErr)
		return
	}

	if mintReq.Unit != "sat" {
		writeErr(rw, cashu.UnitNotSupported)
		return
	}

	invoice, err := ms.mint.RequestInvoice(mintReq.Amount)
	if err != nil {
		writeErr(rw, cashu.Error{Detail: err.Error(), Code: 1010})
		return
	}

	reqMintResponse := nut04.PostMintQuoteBolt11Response{Quote: invoice.Id,
		Request: invoice.PaymentRequest, Paid: invoice.Settled, Expiry: invoice.Expiry}
	jsonRes, err := json.Marshal(reqMintResponse)
	if err != nil {
		writeErr(rw, cashu.StandardErr)
		return
	}

	rw.Write(jsonRes)
}

func (ms *MintServer) getQuoteState(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(req)
	method, ok := vars["method"]
	if !ok {
		writeErr(rw, cashu.PaymentMethodNotSpecified)
		return
	}

	if method != "bolt11" {
		writeErr(rw, cashu.PaymentMethodNotSupported)
		return
	}

	quoteId, ok := vars["quote_id"]
	if !ok {
		writeErr(rw, cashu.QuoteIdNotSpecified)
		return
	}

	invoice := ms.mint.GetInvoice(quoteId)
	if invoice == nil {
		writeErr(rw, cashu.InvoiceNotExist)
		return
	}

	invoice.Settled = ms.mint.LightningClient.InvoiceSettled(invoice.PaymentHash)
	// save invoice

	reqMintResponse := nut04.PostMintQuoteBolt11Response{Quote: invoice.Id,
		Request: invoice.PaymentRequest, Paid: invoice.Settled, Expiry: invoice.Expiry}
	jsonRes, err := json.Marshal(reqMintResponse)
	if err != nil {
		writeErr(rw, cashu.StandardErr)
		return
	}

	rw.Write(jsonRes)
}

func (ms *MintServer) mintTokens(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(req)
	method, ok := vars["method"]
	if !ok {
		writeErr(rw, cashu.PaymentMethodNotSpecified)
		return
	}

	if method != "bolt11" {
		writeErr(rw, cashu.PaymentMethodNotSupported)
		return
	}

	var mintReq nut04.PostMintBolt11Response
	err := json.NewDecoder(req.Body).Decode(&mintReq)
	if err != nil {
		writeErr(rw, cashu.StandardErr)
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

		response := cashu.PostMintResponse{Promises: blindedSignatures}
		json.NewEncoder(rw).Encode(response)
		return
	} else {
		if invoice.Redeemed {
			writeErr(rw, cashu.InvoiceTokensIssued)
			return
		}
	}
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
