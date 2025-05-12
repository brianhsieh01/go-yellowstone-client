package solana

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// Cache structure
var (
	blockTimeCache      = make(map[uint64]time.Time)
	blockTimeErrorCache = make(map[uint64]struct{})
	cacheMu             sync.RWMutex
	requestGroup        singleflight.Group // Used for Single Flight pattern
)

// GetBlockTime retrieves the timestamp of a specified block from Helius RPC, using memory cache and Single Flight pattern
func GetBlockTime(slot uint64) (time.Time, error) {
	// Using Single Flight pattern to get block time
	key := fmt.Sprintf("blocktime-%d", slot)
	result, err, _ := requestGroup.Do(key, func() (interface{}, error) {
		// Check cache in callback function
		cacheMu.RLock()
		if t, found := blockTimeCache[slot]; found {
			cacheMu.RUnlock()
			return t, nil
		}
		if _, found := blockTimeErrorCache[slot]; found {
			cacheMu.RUnlock()
			return time.Time{}, fmt.Errorf("known invalid block timestamp")
		}
		cacheMu.RUnlock()

		// Not in cache, call RPC
		client := &http.Client{Timeout: 5 * time.Second}
		heliusEndpoint := os.Getenv("HELIUS_RPC_ENDPOINT")
		if heliusEndpoint == "" {
			return time.Time{}, fmt.Errorf("HELIUS_RPC_ENDPOINT environment variable not set")
		}

		reqBody := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"getBlockTime","params":[%d]}`, slot)
		req, err := http.NewRequest("POST", heliusEndpoint, bytes.NewBufferString(reqBody))
		if err != nil {
			return time.Time{}, fmt.Errorf("failed to create HTTP request: %v", err)
		}

		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return time.Time{}, fmt.Errorf("failed to send RPC request: %v", err)
		}
		defer resp.Body.Close()

		var result struct {
			Result int64 `json:"result"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return time.Time{}, fmt.Errorf("failed to parse RPC response: %v", err)
		}

		cacheMu.Lock()
		defer cacheMu.Unlock()

		if result.Result <= 0 {
			// Record error block to cache
			blockTimeErrorCache[slot] = struct{}{}
			return time.Time{}, fmt.Errorf("received invalid block timestamp")
		}

		// Store result in cache
		t := time.Unix(result.Result, 0)
		blockTimeCache[slot] = t
		return t, nil
	})

	if err != nil {
		return time.Time{}, err
	}

	return result.(time.Time), nil
}

// ClearBlockTimeCache clears the block time cache
func ClearBlockTimeCache() {
	cacheMu.Lock()
	blockTimeCache = make(map[uint64]time.Time)
	blockTimeErrorCache = make(map[uint64]struct{})
	cacheMu.Unlock()
}

// GetCacheStats returns cache statistics
func GetCacheStats() (int, int) {
	cacheMu.RLock()
	successCount := len(blockTimeCache)
	errorCount := len(blockTimeErrorCache)
	cacheMu.RUnlock()
	return successCount, errorCount
}
