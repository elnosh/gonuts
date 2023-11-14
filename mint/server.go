package mint

import (
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"strings"

	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint/config"
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

	server := &http.Server{
		Addr:    "127.0.0.1:3338",
		Handler: r,
	}

	ms.httpServer = server
}

func (ms *MintServer) getPublicKeyset(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("Content-Type", "application/json")

	publicKeyset := ms.mint.Keyset.DerivePublic()

	jsonKeyset, err := json.Marshal(publicKeyset)
	if err != nil {
		rw.WriteHeader(http.StatusInternalServerError)
		rw.Write([]byte("unable to serve keys"))
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
		rw.WriteHeader(http.StatusInternalServerError)
		rw.Write([]byte("unable to serve keys"))
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
		rw.WriteHeader(http.StatusInternalServerError)
		rw.Write([]byte("unable to serve keysets"))
		return
	}

	rw.Write(jsonRes)
	return
}
