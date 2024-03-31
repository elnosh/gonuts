package wallet

import (
	"context"
	"fmt"
	"github.com/elnosh/gonuts/cashurpc"
	"github.com/elnosh/gonuts/mint/rpc"
)

func GetActiveKeysets(ctx context.Context, mintURL string) (*cashurpc.KeysResponse, error) {
	client, err := createMintClient(mintURL)
	if err != nil {
		return nil, err
	}
	resp, err := client.Keys(ctx, &cashurpc.KeysRequest{})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func GetAllKeysets(ctx context.Context, mintURL string) (*cashurpc.KeysResponse, error) {
	client, err := createMintClient(mintURL)
	if err != nil {
		return nil, err
	}
	resp, err := client.KeySets(ctx, &cashurpc.KeysRequest{})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func PostMintQuoteBolt11(ctx context.Context, mintURL string, mintQuoteRequest *cashurpc.PostMintQuoteBolt11Request) (
	*cashurpc.PostMintQuoteBolt11Response, error) {
	client, err := createMintClient(mintURL)
	if err != nil {
		return nil, err
	}
	resp, err := client.MintQuote(ctx, mintQuoteRequest)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func GetMintQuoteState(ctx context.Context, mintURL, quoteId string) (*cashurpc.PostMintQuoteBolt11Response, error) {
	client, err := createMintClient(mintURL)
	if err != nil {
		return nil, err
	}
	resp, err := client.MintQuoteState(ctx,
		&cashurpc.GetQuoteBolt11StateRequest{QuoteId: quoteId})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func PostMintBolt11(ctx context.Context, mintURL string, mintRequest *cashurpc.PostMintBolt11Request) (
	*cashurpc.PostMintBolt11Response, error) {
	client, err := createMintClient(mintURL)
	if err != nil {
		return nil, err
	}
	resp, err := client.Mint(ctx, mintRequest)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func PostSwap(ctx context.Context, mintURL string, swapRequest *cashurpc.SwapRequest) (*cashurpc.SwapResponse, error) {
	client, err := createMintClient(mintURL)
	if err != nil {
		return nil, err
	}
	resp, err := client.Swap(ctx, swapRequest)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func PostMeltQuoteBolt11(ctx context.Context, mintURL string, meltQuoteRequest *cashurpc.PostMeltQuoteBolt11Request) (
	*cashurpc.PostMeltQuoteBolt11Response, error) {
	client, err := createMintClient(mintURL)
	if err != nil {
		return nil, err
	}
	resp, err := client.MeltQuote(ctx, meltQuoteRequest)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func PostMeltBolt11(ctx context.Context, mintURL string, meltRequest *cashurpc.PostMeltBolt11Request) (
	*cashurpc.PostMeltBolt11Response, error) {
	client, err := createMintClient(mintURL)
	if err != nil {
		return nil, err
	}
	resp, err := client.Melt(ctx, meltRequest)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func createMintClient(mintURL string) (cashurpc.MintClient, error) {
	conn, err := rpc.CreateGrpcClient(mintURL, true)
	if err != nil {
		return nil, fmt.Errorf("could not create gRPC client: %w", err)
	}
	return cashurpc.NewMintClient(conn), nil
}
