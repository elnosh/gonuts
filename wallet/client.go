package wallet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut01"
	"github.com/elnosh/gonuts/cashu/nuts/nut02"
	"github.com/elnosh/gonuts/cashu/nuts/nut03"
	"github.com/elnosh/gonuts/cashu/nuts/nut04"
	"github.com/elnosh/gonuts/cashu/nuts/nut05"
)

func GetActiveKeysets(mintURL string) (*nut01.GetKeysResponse, error) {
	resp, err := get(mintURL + "/v1/keys")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var keysetRes *nut01.GetKeysResponse
	err = json.NewDecoder(resp.Body).Decode(&keysetRes)
	if err != nil {
		return nil, fmt.Errorf("json.Decode: %v", err)
	}

	return keysetRes, nil
}

func GetAllKeysets(mintURL string) (*nut02.GetKeysetsResponse, error) {
	resp, err := get(mintURL + "/v1/keysets")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var keysetsRes *nut02.GetKeysetsResponse
	err = json.NewDecoder(resp.Body).Decode(&keysetsRes)
	if err != nil {
		return nil, fmt.Errorf("json.Decode: %v", err)
	}

	return keysetsRes, nil
}

func GetKeysetById(mintURL, id string) (*nut01.GetKeysResponse, error) {
	resp, err := get(mintURL + "/v1/keys/" + id)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var keysetRes *nut01.GetKeysResponse
	err = json.NewDecoder(resp.Body).Decode(&keysetRes)
	if err != nil {
		return nil, fmt.Errorf("json.Decode: %v", err)
	}

	return keysetRes, nil
}

func PostMintQuoteBolt11(mintURL string, mintQuoteRequest nut04.PostMintQuoteBolt11Request) (
	*nut04.PostMintQuoteBolt11Response, error) {
	requestBody, err := json.Marshal(mintQuoteRequest)
	if err != nil {
		return nil, fmt.Errorf("json.Marshal: %v", err)
	}

	resp, err := httpPost(mintURL+"/v1/mint/quote/bolt11", "application/json", bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var reqMintResponse *nut04.PostMintQuoteBolt11Response
	err = json.NewDecoder(resp.Body).Decode(&reqMintResponse)
	if err != nil {
		return nil, fmt.Errorf("json.Decode: %v", err)
	}

	return reqMintResponse, nil
}

func GetMintQuoteState(mintURL, quoteId string) (*nut04.PostMintQuoteBolt11Response, error) {
	resp, err := get(mintURL + "/v1/mint/quote/bolt11/" + quoteId)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var mintQuoteResponse *nut04.PostMintQuoteBolt11Response
	err = json.NewDecoder(resp.Body).Decode(&mintQuoteResponse)
	if err != nil {
		return nil, fmt.Errorf("json.Decode: %v", err)
	}

	return mintQuoteResponse, nil
}

func PostMintBolt11(mintURL string, mintRequest nut04.PostMintBolt11Request) (
	*nut04.PostMintBolt11Response, error) {
	requestBody, err := json.Marshal(mintRequest)
	if err != nil {
		return nil, fmt.Errorf("json.Marshal: %v", err)
	}

	resp, err := httpPost(mintURL+"/v1/mint/bolt11", "application/json", bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var reqMintResponse *nut04.PostMintBolt11Response
	err = json.NewDecoder(resp.Body).Decode(&reqMintResponse)
	if err != nil {
		return nil, fmt.Errorf("json.Decode: %v", err)
	}

	return reqMintResponse, nil
}

func PostSwap(mintURL string, swapRequest nut03.PostSwapRequest) (*nut03.PostSwapResponse, error) {
	requestBody, err := json.Marshal(swapRequest)
	if err != nil {
		return nil, fmt.Errorf("json.Marshal: %v", err)
	}

	resp, err := httpPost(mintURL+"/v1/swap", "application/json", bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var swapResponse *nut03.PostSwapResponse
	err = json.NewDecoder(resp.Body).Decode(&swapResponse)
	if err != nil {
		return nil, fmt.Errorf("json.Decode: %v", err)
	}

	return swapResponse, nil
}

func PostMeltQuoteBolt11(mintURL string, meltQuoteRequest nut05.PostMeltQuoteBolt11Request) (
	*nut05.PostMeltQuoteBolt11Response, error) {

	requestBody, err := json.Marshal(meltQuoteRequest)
	if err != nil {
		return nil, fmt.Errorf("json.Marshal: %v", err)
	}

	resp, err := httpPost(mintURL+"/v1/melt/quote/bolt11", "application/json", bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var meltQuoteResponse *nut05.PostMeltQuoteBolt11Response
	err = json.NewDecoder(resp.Body).Decode(&meltQuoteResponse)
	if err != nil {
		return nil, fmt.Errorf("json.Decode: %v", err)
	}

	return meltQuoteResponse, nil
}

func PostMeltBolt11(mintURL string, meltRequest nut05.PostMeltBolt11Request) (
	*nut05.PostMeltBolt11Response, error) {

	requestBody, err := json.Marshal(meltRequest)
	if err != nil {
		return nil, fmt.Errorf("json.Marshal: %v", err)
	}

	resp, err := httpPost(mintURL+"/v1/melt/bolt11", "application/json", bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var meltResponse *nut05.PostMeltBolt11Response
	err = json.NewDecoder(resp.Body).Decode(&meltResponse)
	if err != nil {
		return nil, fmt.Errorf("json.Decode: %v", err)
	}

	return meltResponse, nil
}

func get(url string) (*http.Response, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}

	return parse(resp)
}

func httpPost(url, contentType string, body io.Reader) (*http.Response, error) {
	resp, err := http.Post(url, contentType, body)
	if err != nil {
		return nil, err
	}

	return parse(resp)
}

func parse(response *http.Response) (*http.Response, error) {
	if response.StatusCode == 400 {
		var errResponse cashu.Error
		err := json.NewDecoder(response.Body).Decode(&errResponse)
		if err != nil {
			return nil, fmt.Errorf("could not decode error response from mint: %v", err)
		}
		return nil, errResponse
	}

	return response, nil
}
