package mint

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/elnosh/gonuts/config"
	"github.com/elnosh/gonuts/crypto"
	"github.com/gorilla/mux"
)

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

	r.HandleFunc("/keys", ms.getPublicKeyset).Methods("GET")
	r.HandleFunc("/keys/{id}", ms.getKeysetById).Methods("GET")
	r.HandleFunc("/keysets", ms.getKeysetsList).Methods("GET")
	r.HandleFunc("/mint", ms.requestMint).Methods("GET")

	server := &http.Server{
		Addr:    "127.0.0.1:3338",
		Handler: r,
	}

	ms.httpServer = server
}

var KeysErrMsg = "unable to serve keys"

func (ms *MintServer) getPublicKeyset(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("Content-Type", "application/json")

	publicKeyset := ms.mint.Keyset.DerivePublic()

	jsonKeyset, err := json.Marshal(publicKeyset)
	if err != nil {
		http.Error(rw, KeysErrMsg, http.StatusInternalServerError)
		return
	}

	rw.Write(jsonKeyset)
	return
}

func (ms *MintServer) getKeysetById(rw http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)

	id, ok := vars["id"]
	if !ok {
		http.Error(rw, "please specify a keyset ID", http.StatusBadRequest)
		return
	}
	id = strings.ReplaceAll(strings.ReplaceAll(id, "_", "/"), "-", "+")

	var keyset *crypto.Keyset
	for _, ks := range ms.mint.Keysets {
		if ks.Id == id {
			keyset = ks
		}
	}

	if keyset == nil {
		http.Error(rw, "keyset does not exist", http.StatusNotFound)
		return
	}

	jsonRes, err := json.Marshal(keyset.DerivePublic())
	rw.Header().Set("Content-Type", "application/json")
	if err != nil {
		http.Error(rw, KeysErrMsg, http.StatusInternalServerError)
		return
	}

	rw.Write(jsonRes)
}

type KeysetsResponse struct {
	KeysetIds []string `json:"keysets"`
}

func (ms *MintServer) getKeysetsList(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("Content-Type", "application/json")

	keysetRes := KeysetsResponse{KeysetIds: ms.mint.KeysetList()}

	jsonRes, err := json.Marshal(keysetRes)
	if err != nil {
		http.Error(rw, "unable to serve keysets", http.StatusInternalServerError)
		return
	}

	rw.Write(jsonRes)
	return
}

type RequestMintResponse struct {
	PaymentRequest string `json:"pr"`
	Hash           string `json:"hash"`
}

func (ms *MintServer) requestMint(rw http.ResponseWriter, req *http.Request) {
	// check value exists and that is a valid number
	amount := req.URL.Query().Get("amount")

	satsAmount, err := strconv.ParseInt(amount, 10, 64)
	if err != nil {
		http.Error(rw, "invalid amount", http.StatusBadRequest)
		return
	}

	randomBytes := make([]byte, 32)
	_, err = rand.Read(randomBytes)
	if err != nil {
		http.Error(rw, "unable to create invoice", http.StatusInternalServerError)
		return
	}

	hash := sha256.Sum256(randomBytes)
	hashStr := hex.EncodeToString(hash[:])

	pr, err := ms.mint.LightningClient.CreateInvoice(satsAmount)
	if err != nil {
		errMsg := "error creating invoice: " + err.Error()
		http.Error(rw, errMsg, http.StatusInternalServerError)
		return
	}

	reqMintResponse := RequestMintResponse{PaymentRequest: pr, Hash: hashStr}
	jsonRes, err := json.Marshal(reqMintResponse)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}

	rw.Write(jsonRes)
}
