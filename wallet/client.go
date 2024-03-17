package wallet

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/elnosh/gonuts/cashu"
)

func httpPost(url, contentType string, body io.Reader) (*http.Response, error) {
	resp, err := http.Post(url, contentType, body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == 400 {
		var errResponse cashu.Error
		err = json.NewDecoder(resp.Body).Decode(&errResponse)
		if err != nil {
			return nil, fmt.Errorf("could not decode error response from mint: %v", err)
		}
		return nil, errResponse
	}

	return resp, nil
}
