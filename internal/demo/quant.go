package demo

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"time"
)

// QuantHandler returns a handler that performs chain analysis using eRPC data.
// Unlike a simple RPC passthrough, it fetches multiple data points, computes
// derived metrics (gas statistics, tx volume), and formats a structured report.
func QuantHandler(erpcURL string) http.HandlerFunc {
	client := &http.Client{Timeout: 15 * time.Second}

	return func(w http.ResponseWriter, r *http.Request) {
		report := make(map[string]any)
		var errs []string

		// Fetch current chain state.
		chainID, err := rpcCall(client, erpcURL, "eth_chainId", "[]")
		if err != nil {
			errs = append(errs, fmt.Sprintf("chainId: %v", err))
		} else {
			report["chainId"] = chainID
		}

		blockNumRaw, err := rpcCall(client, erpcURL, "eth_blockNumber", "[]")
		if err != nil {
			errs = append(errs, fmt.Sprintf("blockNumber: %v", err))
			respond(w, r, "quant", map[string]any{"errors": errs})
			return
		}
		report["latestBlockNumber"] = blockNumRaw

		blockNum := hexToUint64(trimQuotes(blockNumRaw))

		// Fetch the last 5 blocks to compute gas statistics.
		const sampleSize = 5
		type blockInfo struct {
			Number       string `json:"number"`
			Timestamp    string `json:"timestamp"`
			GasUsed      string `json:"gasUsed"`
			GasLimit     string `json:"gasLimit"`
			BaseFee      string `json:"baseFeePerGas"`
			Transactions int    `json:"transactionCount"`
		}

		blocks := make([]blockInfo, 0, sampleSize)
		var totalTxs int
		var gasPrices []*big.Int

		for i := 0; i < sampleSize && blockNum-uint64(i) > 0; i++ {
			num := fmt.Sprintf("0x%x", blockNum-uint64(i))
			raw, err := rpcCall(client, erpcURL, "eth_getBlockByNumber", fmt.Sprintf(`[%q, false]`, num))
			if err != nil {
				errs = append(errs, fmt.Sprintf("block %s: %v", num, err))
				continue
			}

			var block struct {
				Number       string   `json:"number"`
				Timestamp    string   `json:"timestamp"`
				GasUsed      string   `json:"gasUsed"`
				GasLimit     string   `json:"gasLimit"`
				BaseFee      string   `json:"baseFeePerGas"`
				Transactions []string `json:"transactions"`
			}
			if err := json.Unmarshal(raw, &block); err != nil {
				errs = append(errs, fmt.Sprintf("decode block %s: %v", num, err))
				continue
			}

			txCount := len(block.Transactions)
			totalTxs += txCount
			blocks = append(blocks, blockInfo{
				Number:       block.Number,
				Timestamp:    block.Timestamp,
				GasUsed:      block.GasUsed,
				GasLimit:     block.GasLimit,
				BaseFee:      block.BaseFee,
				Transactions: txCount,
			})

			if block.BaseFee != "" {
				if fee := hexToBigInt(block.BaseFee); fee != nil {
					gasPrices = append(gasPrices, fee)
				}
			}
		}

		report["recentBlocks"] = blocks

		// Compute gas statistics.
		if len(gasPrices) > 0 {
			stats := computeGasStats(gasPrices)
			report["gasAnalysis"] = stats
		}

		// Transaction volume.
		report["txVolume"] = map[string]any{
			"totalTransactions": totalTxs,
			"blocksAnalyzed":   len(blocks),
			"avgTxPerBlock":    safeDivFloat(float64(totalTxs), float64(len(blocks))),
		}

		// Utilization: avg gasUsed/gasLimit across sampled blocks.
		var totalUsed, totalLimit uint64
		for _, b := range blocks {
			totalUsed += hexToUint64(b.GasUsed)
			totalLimit += hexToUint64(b.GasLimit)
		}
		if totalLimit > 0 {
			pct := float64(totalUsed) / float64(totalLimit) * 100
			report["gasUtilization"] = map[string]any{
				"percentage": fmt.Sprintf("%.1f%%", pct),
				"status":     utilizationLabel(pct),
			}
		}

		if len(errs) > 0 {
			report["errors"] = errs
		}

		respond(w, r, "quant", report)
	}
}

type gasStats struct {
	MinGwei string `json:"minGwei"`
	MaxGwei string `json:"maxGwei"`
	AvgGwei string `json:"avgGwei"`
	Samples int    `json:"samples"`
}

func computeGasStats(prices []*big.Int) gasStats {
	if len(prices) == 0 {
		return gasStats{}
	}

	min := new(big.Int).Set(prices[0])
	max := new(big.Int).Set(prices[0])
	sum := new(big.Int)

	for _, p := range prices {
		if p.Cmp(min) < 0 {
			min.Set(p)
		}
		if p.Cmp(max) > 0 {
			max.Set(p)
		}
		sum.Add(sum, p)
	}

	avg := new(big.Int).Div(sum, big.NewInt(int64(len(prices))))

	return gasStats{
		MinGwei: weiToGwei(min),
		MaxGwei: weiToGwei(max),
		AvgGwei: weiToGwei(avg),
		Samples: len(prices),
	}
}

func weiToGwei(wei *big.Int) string {
	gwei := new(big.Float).SetInt(wei)
	gwei.Quo(gwei, new(big.Float).SetInt64(1_000_000_000))
	return gwei.Text('f', 4)
}

func hexToUint64(s string) uint64 {
	if len(s) > 2 && s[:2] == "0x" {
		s = s[2:]
	}
	v, _ := strconv.ParseUint(s, 16, 64)
	return v
}

func hexToBigInt(s string) *big.Int {
	if len(s) > 2 && s[:2] == "0x" {
		s = s[2:]
	}
	v := new(big.Int)
	v.SetString(s, 16)
	return v
}

func trimQuotes(raw json.RawMessage) string {
	s := string(raw)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func safeDivFloat(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}

func utilizationLabel(pct float64) string {
	switch {
	case pct > 90:
		return "congested"
	case pct > 70:
		return "busy"
	case pct > 40:
		return "moderate"
	default:
		return "low"
	}
}
