package demo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// BlocksHandler returns a handler that queries eRPC for basic chain data.
func BlocksHandler(erpcURL string) http.HandlerFunc {
	client := &http.Client{Timeout: 10 * time.Second}

	return func(w http.ResponseWriter, r *http.Request) {
		type result struct {
			key string
			val json.RawMessage
			err error
		}

		methods := []struct {
			key    string
			method string
			params string
		}{
			{"blockNumber", "eth_blockNumber", "[]"},
			{"gasPrice", "eth_gasPrice", "[]"},
			{"chainId", "eth_chainId", "[]"},
		}

		results := make(chan result, len(methods))
		for _, m := range methods {
			go func(key, method, params string) {
				val, err := rpcCall(client, erpcURL, method, params)
				results <- result{key, val, err}
			}(m.key, m.method, m.params)
		}

		data := make(map[string]any)
		var errs []string
		for range methods {
			res := <-results
			if res.err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", res.key, res.err))
				continue
			}
			data[res.key] = res.val
		}

		// Fetch latest block details using the block number we just got.
		if bn, ok := data["blockNumber"]; ok {
			bnBytes, _ := json.Marshal(bn)
			blockNum := string(bytes.Trim(bnBytes, `"`))
			params := fmt.Sprintf(`[%q, false]`, blockNum)
			block, err := rpcCall(client, erpcURL, "eth_getBlockByNumber", params)
			if err != nil {
				errs = append(errs, fmt.Sprintf("block: %v", err))
			} else {
				data["latestBlock"] = block
			}
		}

		if len(errs) > 0 {
			data["errors"] = errs
		}

		respond(w, r, "blocks", data)
	}
}

// rpcCall executes a JSON-RPC 2.0 call and returns the result field.
func rpcCall(client *http.Client, url, method, params string) (json.RawMessage, error) {
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q,"params":%s}`, method, params)
	resp, err := client.Post(url, "application/json", bytes.NewBufferString(body))
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	var rpcResp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &rpcResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return rpcResp.Result, nil
}
