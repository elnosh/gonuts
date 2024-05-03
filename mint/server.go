package mint

import (
	"buf.build/gen/go/cashu/rpc/grpc-ecosystem/gateway/v2/cashu/cashuv1gateway"
	"buf.build/gen/go/cashu/rpc/grpc/go/cashuv1grpc"
	cashurpc "buf.build/gen/go/cashu/rpc/protocolbuffers/go"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/elnosh/gonuts/mint/rpc"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/crypto"
)

const bolt11 = "bolt11"

type Server struct {
	cashuv1grpc.UnimplementedMintServer
	rpc    *rpc.Server
	mint   *Mint
	logger *slog.Logger
}

func StartMintServer(server *Server) error {
	server.rpc = rpc.NewServer(
		rpc.WithServiceHandlerFromEndpointRegistration(cashuv1gateway.RegisterMintHandlerFromEndpoint),
	)
	server.rpc.RegisterService(server.rpc.GRPC, &cashuv1grpc.Mint_ServiceDesc, server)

	return server.rpc.Serve()
}

func SetupMintServer(config Config) (*Server, error) {
	mint, err := LoadMint(config)
	if err != nil {
		return nil, err
	}

	logger := getLogger()
	mintServer := &Server{mint: mint, logger: logger}
	//mintServer.setupHttpServer()
	return mintServer, nil
}

func getLogger() *slog.Logger {
	replacer := func(groups []string, a slog.Attr) slog.Attr {
		if a.Key == slog.SourceKey {
			source := a.Value.Any().(*slog.Source)
			source.File = filepath.Base(source.File)
			source.Function = filepath.Base(source.Function)
		}
		return a
	}

	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{AddSource: true, ReplaceAttr: replacer}))
}

func (ms *Server) LogInfo(format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	ms.logger.Info(msg)
}

func (ms *Server) Keys(ctx context.Context, request *cashurpc.KeysRequest) (*cashurpc.KeysResponse, error) {
	return buildKeysResponse(ms.mint.Keysets), nil

}

func (ms *Server) KeySets(ctx context.Context, request *cashurpc.KeysRequest) (*cashurpc.KeysResponse, error) {
	//TODO implement me
	return ms.buildAllKeysetsResponse(), nil

}

func (ms *Server) Swap(ctx context.Context, request *cashurpc.SwapRequest) (*cashurpc.SwapResponse, error) {
	response, err := ms.mint.Swap(request.Inputs, request.Outputs)
	if err != nil {
		return nil, err
	}
	return &cashurpc.SwapResponse{
		Signatures: response,
	}, nil
}

func (ms *Server) MintQuoteState(ctx context.Context, request *cashurpc.GetQuoteBolt11StateRequest) (*cashurpc.PostMintQuoteBolt11Response, error) {
	response, err := ms.mint.GetMintQuoteState(bolt11, request.QuoteId)
	if err != nil {
		return nil, err
	}
	return response, nil
}

func (ms *Server) MintQuote(ctx context.Context, request *cashurpc.PostMintQuoteBolt11Request) (*cashurpc.PostMintQuoteBolt11Response, error) {
	return ms.mint.RequestMintQuote(bolt11, request.Amount, request.Unit)

}

func (ms *Server) Mint(ctx context.Context, request *cashurpc.PostMintBolt11Request) (*cashurpc.PostMintBolt11Response, error) {
	signatures, err := ms.mint.MintTokens(bolt11, request.Quote, request.Outputs)
	if err != nil {
		return nil, err
	}
	return &cashurpc.PostMintBolt11Response{
		Signatures: signatures,
	}, nil
}

func (ms *Server) MeltQuoteState(ctx context.Context, request *cashurpc.GetQuoteBolt11StateRequest) (*cashurpc.PostMeltQuoteBolt11Response, error) {
	melt, err := ms.mint.GetMeltQuoteState(bolt11, request.QuoteId)
	if err != nil {
		return nil, err
	}
	return melt.PostMeltQuoteBolt11Response, nil
}

func (ms *Server) MeltQuote(ctx context.Context, request *cashurpc.PostMeltQuoteBolt11Request) (*cashurpc.PostMeltQuoteBolt11Response, error) {
	melt, err := ms.mint.MeltRequest(bolt11, request.Request, request.Unit)
	if err != nil {
		return nil, err
	}
	return melt.PostMeltQuoteBolt11Response, nil
}

func (ms *Server) Melt(ctx context.Context, request *cashurpc.PostMeltBolt11Request) (*cashurpc.PostMeltBolt11Response, error) {
	melt, err := ms.mint.MeltTokens(bolt11, request.Quote, request.Inputs)
	if err != nil {
		return nil, err
	}
	return melt, nil
}

func (ms *Server) Info(ctx context.Context, request *cashurpc.InfoRequest) (*cashurpc.InfoResponse, error) {
	return ms.mint.MintInfo, nil
}

func (ms *Server) CheckState(ctx context.Context, request *cashurpc.PostCheckStateRequest) (*cashurpc.PostCheckStateResponse, error) {
	//TODO implement me
	panic("implement me")
}

func buildKeysResponse(keysets map[string]crypto.Keyset) *cashurpc.KeysResponse {
	keysResponse := &cashurpc.KeysResponse{}

	for _, keyset := range keysets {
		if !keyset.Active {
			continue
		}
		pks := keyset.DerivePublic()
		keyRes := &cashurpc.Keyset{Id: keyset.Id, Unit: keyset.Unit, Keys: pks}
		keysResponse.Keysets = append(keysResponse.Keysets, keyRes)
	}

	return keysResponse
}

func (ms *Server) buildAllKeysetsResponse() *cashurpc.KeysResponse {
	keysetsResponse := &cashurpc.KeysResponse{}

	for _, keyset := range ms.mint.Keysets {
		keysetRes := &cashurpc.Keyset{Id: keyset.Id, Unit: keyset.Unit, Active: keyset.Active}
		keysetsResponse.Keysets = append(keysetsResponse.Keysets, keysetRes)
	}

	return keysetsResponse
}

func decodeJsonReqBody(req *http.Request, dst any) error {
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
			return cashu.EmptyBodyErr

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
