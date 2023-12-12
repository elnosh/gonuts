package mint

import (
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut01"
	"github.com/elnosh/gonuts/cashu/nuts/nut02"
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
	r.HandleFunc("/mint", ms.requestMint).Methods(http.MethodGet)
	r.HandleFunc("/mint", ms.postMint).Methods(http.MethodPost)

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
	amount := req.URL.Query().Get("amount")
	if amount == "" {
		http.Error(rw, "specify an amount", http.StatusBadRequest)
		return
	}

	satsAmount, err := strconv.ParseInt(amount, 10, 64)
	if err != nil {
		http.Error(rw, "invalid amount", http.StatusBadRequest)
		return
	}

	invoice, err := ms.mint.RequestInvoice(satsAmount)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
	}

	reqMintResponse := cashu.RequestMintResponse{PaymentRequest: invoice.PaymentRequest, Hash: invoice.Id}
	jsonRes, err := json.Marshal(reqMintResponse)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}

	rw.Write(jsonRes)
}

func (ms *MintServer) postMint(rw http.ResponseWriter, req *http.Request) {
	hash := req.URL.Query().Get("hash")
	if hash == "" {
		http.Error(rw, "specify hash", http.StatusBadRequest)
		return
	}

	invoice := ms.mint.GetInvoice(hash)
	if invoice == nil {
		http.Error(rw, "invoice not found", http.StatusNotFound)
		return
	}

	if !invoice.Settled {
		settled := ms.mint.LightningClient.InvoiceSettled(invoice.PaymentHash)
		if !settled {
			http.Error(rw, "invoice has not been paid", http.StatusBadRequest)
			return
		}

		var mintRequest cashu.PostMintRequest
		err := json.NewDecoder(req.Body).Decode(&mintRequest)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}

		blindedSignatures, err := cashu.SignBlindedMessages(mintRequest.Outputs, &ms.mint.ActiveKeysets[0])
		if err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
		invoice.Settled = true
		invoice.Redeemed = true
		ms.mint.SaveInvoice(*invoice)

		response := cashu.PostMintResponse{Promises: blindedSignatures}
		rw.Header().Set("Content-Type", "application/json")
		json.NewEncoder(rw).Encode(response)
		return
	} else {
		if invoice.Redeemed {
			http.Error(rw, "tokens have already been minted", http.StatusBadRequest)
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
