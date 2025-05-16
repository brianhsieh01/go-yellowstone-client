package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
)

type SlotData struct {
	MinTransactionTime int64
	BlockTime          int64
	HasBlock           bool
	HasTransaction     bool
}

func main() {
	//
	// 打開文件
	file, err := os.Open("../../test.txt")
	if err != nil {
		fmt.Printf("無法打開文件: %v\n", err)
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	// 正則表達式來匹配交易和區塊行
	reTransaction := regexp.MustCompile(`\[Transaction\] Slot: (\d+), Time Now: (\d+)`)
	reBlock := regexp.MustCompile(`\[Block\] Slot: (\d+), Time Now: (\d+)`)

	// 存儲每個 Slot 的資料
	slotsData := make(map[int]*SlotData)

	// 讀取文件的每一行
	for scanner.Scan() {
		line := scanner.Text()

		// 匹配交易行
		if matches := reTransaction.FindStringSubmatch(line); len(matches) >= 3 {
			slot, _ := strconv.Atoi(matches[1])
			timeNow, _ := strconv.ParseInt(matches[2], 10, 64)

			// 如果這個 Slot 尚未在 map 中，則新增
			if _, exists := slotsData[slot]; !exists {
				slotsData[slot] = &SlotData{
					MinTransactionTime: timeNow,
					HasTransaction:     true,
				}
			} else if timeNow < slotsData[slot].MinTransactionTime {
				// 更新最小交易時間
				slotsData[slot].MinTransactionTime = timeNow
				slotsData[slot].HasTransaction = true
			}
		}

		// 匹配區塊行
		if matches := reBlock.FindStringSubmatch(line); len(matches) >= 3 {
			slot, _ := strconv.Atoi(matches[1])
			blockTime, _ := strconv.ParseInt(matches[2], 10, 64)

			// 如果這個 Slot 尚未在 map 中，則新增
			if _, exists := slotsData[slot]; !exists {
				slotsData[slot] = &SlotData{
					BlockTime: blockTime,
					HasBlock:  true,
				}
			} else {
				// 更新區塊時間
				slotsData[slot].BlockTime = blockTime
				slotsData[slot].HasBlock = true
			}
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("讀取文件時出錯: %v\n", err)
		return
	}

	// 計算時間差異並輸出結果
	var totalTimeDiff int64
	var count int

	fmt.Println("Slot\t最小 Transaction 時間\tBlock 時間\t時間差異(微秒)\t時間差異(毫秒)")
	fmt.Println("---------------------------------------------------------------------------------")

	for slot, data := range slotsData {
		if data.HasBlock && data.HasTransaction {
			timeDiff := data.BlockTime - data.MinTransactionTime
			timeDiffMs := float64(timeDiff) / 1000.0
			fmt.Printf("%d\t%d\t%d\t%d\t%.2f\n", slot, data.MinTransactionTime, data.BlockTime, timeDiff, timeDiffMs)
			totalTimeDiff += timeDiff
			count++
		}
	}

	// 計算平均時間差異
	if count > 0 {
		avgTimeDiff := float64(totalTimeDiff) / float64(count)
		avgTimeDiffMs := avgTimeDiff / 1000.0

		fmt.Printf("\n總計有 %d 個 Slot 同時包含 Transaction 和 Block\n", count)
		fmt.Printf("平均時間差異: %.2f µs (%.2f ms)\n", avgTimeDiff, avgTimeDiffMs)
	} else {
		fmt.Println("\n沒有找到同時包含 Transaction 和 Block 的 Slot")
	}
}
