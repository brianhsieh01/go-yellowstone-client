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

// 错误类型
type ErrorType string

const (
	ErrorTypeNonExistent ErrorType = "区块不存在"
	ErrorTypeInvalid     ErrorType = "时间戳无效"
	ErrorTypeRPCFailed   ErrorType = "RPC调用失败"
)

// 错误信息结构
type ErrorInfo struct {
	ErrorType ErrorType
	Message   string
	Timestamp time.Time
}

// 缓存结构
var (
	blockTimeCache      = make(map[uint64]time.Time)
	blockTimeErrorCache = make(map[uint64]ErrorInfo)
	cacheMu             sync.RWMutex
	requestGroup        singleflight.Group // 用于 Single Flight 模式
)

// GetBlockTime 从 Helius RPC 获取指定区块的时间戳，使用内存缓存和 Single Flight 模式
func GetBlockTime(slot uint64) (time.Time, error) {
	// 使用 Single Flight 模式获取区块时间
	key := fmt.Sprintf("blocktime-%d", slot)
	result, err, _ := requestGroup.Do(key, func() (interface{}, error) {
		// 在回调函数中检查缓存
		cacheMu.RLock()
		if t, found := blockTimeCache[slot]; found {
			cacheMu.RUnlock()
			return t, nil
		}
		if errInfo, found := blockTimeErrorCache[slot]; found {
			cacheMu.RUnlock()
			return time.Time{}, fmt.Errorf("%s: %s", errInfo.ErrorType, errInfo.Message)
		}
		cacheMu.RUnlock()

		// 缓存中没有，调用 RPC
		client := &http.Client{Timeout: 5 * time.Second}
		heliusEndpoint := os.Getenv("HELIUS_RPC_ENDPOINT")
		if heliusEndpoint == "" {
			errorInfo := ErrorInfo{
				ErrorType: ErrorTypeRPCFailed,
				Message:   "未设置 HELIUS_RPC_ENDPOINT 环境变量",
				Timestamp: time.Now(),
			}
			cacheMu.Lock()
			blockTimeErrorCache[slot] = errorInfo
			cacheMu.Unlock()
			return time.Time{}, fmt.Errorf("%s: %s", errorInfo.ErrorType, errorInfo.Message)
		}

		reqBody := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"getBlockTime","params":[%d]}`, slot)
		req, err := http.NewRequest("POST", heliusEndpoint, bytes.NewBufferString(reqBody))
		if err != nil {
			errorInfo := ErrorInfo{
				ErrorType: ErrorTypeRPCFailed,
				Message:   fmt.Sprintf("创建 HTTP 请求失败: %v", err),
				Timestamp: time.Now(),
			}
			cacheMu.Lock()
			blockTimeErrorCache[slot] = errorInfo
			cacheMu.Unlock()
			return time.Time{}, fmt.Errorf("%s: %s", errorInfo.ErrorType, errorInfo.Message)
		}

		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			errorInfo := ErrorInfo{
				ErrorType: ErrorTypeRPCFailed,
				Message:   fmt.Sprintf("发送 RPC 请求失败: %v", err),
				Timestamp: time.Now(),
			}
			cacheMu.Lock()
			blockTimeErrorCache[slot] = errorInfo
			cacheMu.Unlock()
			return time.Time{}, fmt.Errorf("%s: %s", errorInfo.ErrorType, errorInfo.Message)
		}
		defer resp.Body.Close()

		var result struct {
			Result int64 `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			errorInfo := ErrorInfo{
				ErrorType: ErrorTypeRPCFailed,
				Message:   fmt.Sprintf("解析 RPC 响应失败: %v", err),
				Timestamp: time.Now(),
			}
			cacheMu.Lock()
			blockTimeErrorCache[slot] = errorInfo
			cacheMu.Unlock()
			return time.Time{}, fmt.Errorf("%s: %s", errorInfo.ErrorType, errorInfo.Message)
		}

		// 检查是否有错误响应
		if result.Error != nil {
			errorInfo := ErrorInfo{
				ErrorType: ErrorTypeNonExistent,
				Message:   fmt.Sprintf("RPC 错误: %s (代码: %d)", result.Error.Message, result.Error.Code),
				Timestamp: time.Now(),
			}
			cacheMu.Lock()
			blockTimeErrorCache[slot] = errorInfo
			cacheMu.Unlock()
			return time.Time{}, fmt.Errorf("%s: %s", errorInfo.ErrorType, errorInfo.Message)
		}

		cacheMu.Lock()
		defer cacheMu.Unlock()

		if result.Result <= 0 {
			// 记录错误区块到缓存
			errorInfo := ErrorInfo{
				ErrorType: ErrorTypeInvalid,
				Message:   fmt.Sprintf("获取到无效的区块时间戳: %d", result.Result),
				Timestamp: time.Now(),
			}
			blockTimeErrorCache[slot] = errorInfo
			return time.Time{}, fmt.Errorf("%s: %s", errorInfo.ErrorType, errorInfo.Message)
		}

		// 将结果存入缓存
		t := time.Unix(result.Result, 0)
		blockTimeCache[slot] = t
		return t, nil
	})

	if err != nil {
		return time.Time{}, err
	}

	return result.(time.Time), nil
}

// ClearBlockTimeCache 清除区块时间缓存
func ClearBlockTimeCache() {
	cacheMu.Lock()
	blockTimeCache = make(map[uint64]time.Time)
	blockTimeErrorCache = make(map[uint64]ErrorInfo)
	cacheMu.Unlock()
}

// GetCacheStats 获取缓存统计信息
func GetCacheStats() (int, int) {
	cacheMu.RLock()
	successCount := len(blockTimeCache)
	errorCount := len(blockTimeErrorCache)
	cacheMu.RUnlock()
	return successCount, errorCount
}

// GetErrorDetails 获取错误区块的详细信息
func GetErrorDetails() map[uint64]ErrorInfo {
	cacheMu.RLock()
	defer cacheMu.RUnlock()

	// 创建一个副本，避免外部修改
	result := make(map[uint64]ErrorInfo, len(blockTimeErrorCache))
	for slot, info := range blockTimeErrorCache {
		result[slot] = info
	}
	return result
}
