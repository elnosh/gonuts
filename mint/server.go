package mint

import (
	"encoding/json"
	"log"
	"log/slog"
	"net/http"

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

func SetupMintServer(config config.Config) *MintServer {
	mint := SetupMint(config)
	logger := slog.Default()
	mintServer := &MintServer{mint: mint, logger: logger}
	mintServer.setupHttpServer()
	return mintServer
}

func (ms *MintServer) setupHttpServer() {
	r := mux.NewRouter()

	r.HandleFunc("/keys", ms.handleKeys)

	server := &http.Server{
		Addr:    "127.0.0.1:3338",
		Handler: r,
	}

	ms.httpServer = server
}

func (ms *MintServer) handleKeys(rw http.ResponseWriter, req *http.Request) {
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
