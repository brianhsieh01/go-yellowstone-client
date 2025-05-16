package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"go-yellowstone-client/pkg/definition"
	"log"
	"os"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/joho/godotenv"
	"github.com/mr-tron/base58"
	pb "github.com/rpcpool/yellowstone-grpc/examples/golang/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

type GrpcStreamManager struct {
	mu                   sync.Mutex
	conn                 *grpc.ClientConn
	client               pb.GeyserClient
	stream               pb.Geyser_SubscribeClient
	isConnected          bool
	reconnectAttempts    int
	maxReconnectAttempts int
	reconnectInterval    time.Duration
	dataHandler          func(*pb.SubscribeUpdate)
	xToken               string
}

func NewGrpcStreamManager(endpoint string, xToken string, dataHandler func(*pb.SubscribeUpdate)) (*GrpcStreamManager, error) {
	// Create gRPC connection with interceptor for x-token
	ctx := metadata.NewOutgoingContext(
		context.Background(),
		metadata.New(map[string]string{"x-token": xToken}),
	)

	// Configure TLS
	config := &tls.Config{
		InsecureSkipVerify: true,
	}

	conn, err := grpc.DialContext(ctx, endpoint,
		grpc.WithTransportCredentials(credentials.NewTLS(config)),
		grpc.WithInitialWindowSize(1<<30),
		grpc.WithInitialConnWindowSize(1<<30),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallSendMsgSize(64*1024*1024),
			grpc.MaxCallRecvMsgSize(64*1024*1024),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %v", err)
	}

	return &GrpcStreamManager{
		conn:                 conn,
		client:               pb.NewGeyserClient(conn),
		isConnected:          false,
		reconnectAttempts:    0,
		maxReconnectAttempts: 10,
		reconnectInterval:    5 * time.Second,
		dataHandler:          dataHandler,
		xToken:               xToken,
	}, nil
}

func (m *GrpcStreamManager) Connect(ctx context.Context, req *pb.SubscribeRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	log.Println("Attempting to connect...")

	// Add x-token to context
	ctx = metadata.NewOutgoingContext(
		ctx,
		metadata.New(map[string]string{"x-token": m.xToken}),
	)

	stream, err := m.client.Subscribe(ctx)
	if err != nil {
		log.Printf("Failed to subscribe: %v", err)
		return m.reconnect(ctx, req)
	}

	if err := stream.Send(req); err != nil {
		log.Printf("Failed to send request: %v", err)
		return m.reconnect(ctx, req)
	}

	m.stream = stream
	m.isConnected = true
	m.reconnectAttempts = 0
	log.Println("Connection established")

	// Start ping goroutine
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !m.isConnected {
					return
				}
				pingReq := &pb.SubscribeRequest{
					Ping: &pb.SubscribeRequestPing{Id: 1},
				}
				if err := m.stream.Send(pingReq); err != nil {
					log.Printf("Ping failed: %v", err)
					m.handleDisconnect(ctx, req)
					return
				}
			}
		}
	}()

	// Process updates
	go func() {
		for {
			update, err := m.stream.Recv()
			if err != nil {
				log.Printf("Stream error: %v", err)
				m.handleDisconnect(ctx, req)
				return
			}

			m.dataHandler(update)
		}
	}()

	return nil
}

func (m *GrpcStreamManager) reconnect(ctx context.Context, req *pb.SubscribeRequest) error {
	if m.reconnectAttempts >= m.maxReconnectAttempts {
		return fmt.Errorf("max reconnection attempts reached")
	}

	m.reconnectAttempts++
	log.Printf("Reconnecting... Attempt %d", m.reconnectAttempts)

	backoff := m.reconnectInterval * time.Duration(min(m.reconnectAttempts, 5))
	time.Sleep(backoff)

	return m.Connect(ctx, req)
}

func (m *GrpcStreamManager) handleDisconnect(ctx context.Context, req *pb.SubscribeRequest) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.isConnected {
		return
	}

	m.isConnected = false
	if err := m.reconnect(ctx, req); err != nil {
		log.Printf("Failed to reconnect: %v", err)
	}
}

func (m *GrpcStreamManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.isConnected = false
	if m.conn != nil {
		return m.conn.Close()
	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func boolPtr(b bool) *bool {
	return &b
}

var taskChan = make(chan *pb.SubscribeUpdateTransaction, 5000)
var blockChan = make(chan *pb.SubscribeUpdateBlock, 1000)

func handleTask(tc <-chan *pb.SubscribeUpdateTransaction) {
	for tx := range tc {
		handleTransactionUpdate(tx)
	}
}

func handleBlockTask(bc <-chan *pb.SubscribeUpdateBlock) {
	for block := range bc {
		handleBlockUpdate(block)
	}
}

func handleAccountUpdate(update *pb.SubscribeUpdate) {
	switch u := update.GetUpdateOneof().(type) {
	case *pb.SubscribeUpdate_Transaction:
		taskChan <- u.Transaction
	case *pb.SubscribeUpdate_Block:
		blockChan <- u.Block
	}
}

func handleBlockUpdate(block *pb.SubscribeUpdateBlock) {
	slot := block.GetSlot()
	currentTime := time.Now().UnixMicro()
	blockTime := block.GetBlockTime()

	fmt.Printf("[Block] Slot: %d, Time Now: %d, BlockTime: %v\n", slot, currentTime, blockTime)

	// if blockTime := block.GetBlockTime(); blockTime != nil {
	// 	fmt.Printf("Slot: %d, Time Now: %d, [Block] \n",
	// 		slot,
	// 		currentTime,
	// 	)
	// } else {
	// 	fmt.Printf("Slot: %d, BlockTime: 未提供\n", slot)
	// }
}

func handleTransactionUpdate(tx *pb.SubscribeUpdateTransaction) {
	slot := tx.GetSlot()
	currentTime := time.Now().UnixMicro()
	fmt.Printf("[交易] Slot: %d, 当前时间: %d\n", slot, currentTime)

	txInfo := tx.GetTransaction()
	if txInfo == nil {
		fmt.Println("交易信息为空")
		return
	}

	// 创建 JSON 结构
	jsonTx := definition.ConfirmedTransactionJSON{}

	// 处理 Transaction 部分
	transaction := txInfo.GetTransaction()
	if transaction != nil {
		// 处理签名
		signatures := []string{}
		for _, sig := range transaction.GetSignatures() {
			signatures = append(signatures, base58.Encode(sig))
		}
		jsonTx.Transaction.Signatures = signatures

		// 处理消息
		message := transaction.GetMessage()
		if message != nil {
			// 处理消息头
			header := message.GetHeader()
			if header != nil {
				jsonTx.Transaction.Message.Header.NumRequiredSignatures = header.GetNumRequiredSignatures()
				jsonTx.Transaction.Message.Header.NumReadonlySignedAccounts = header.GetNumReadonlySignedAccounts()
				jsonTx.Transaction.Message.Header.NumReadonlyUnsignedAccounts = header.GetNumReadonlyUnsignedAccounts()
			}

			// 处理账户公钥
			accountKeys := []string{}
			for _, key := range message.GetAccountKeys() {
				accountKeys = append(accountKeys, base58.Encode(key))
			}
			jsonTx.Transaction.Message.AccountKeys = accountKeys

			// 处理最近的区块哈希
			jsonTx.Transaction.Message.RecentBlockhash = base58.Encode(message.GetRecentBlockhash())

			// 处理指令
			instructions := []definition.CompiledInstructionJSON{}
			for _, instr := range message.GetInstructions() {
				instruction := definition.CompiledInstructionJSON{
					ProgramIdIndex: instr.GetProgramIdIndex(),
					Data:           base58.Encode(instr.GetData()),
				}

				// 解析账户索引
				accounts := []uint8{}
				for _, acc := range instr.GetAccounts() {
					accounts = append(accounts, uint8(acc))
				}
				instruction.Accounts = accounts

				instructions = append(instructions, instruction)
			}
			jsonTx.Transaction.Message.Instructions = instructions

			// 处理版本化交易信息
			jsonTx.Transaction.Message.Versioned = message.GetVersioned()

			// 处理地址查找表
			lookups := []definition.AddressTableLookupJSON{}
			for _, lookup := range message.GetAddressTableLookups() {
				lookupJSON := definition.AddressTableLookupJSON{
					AccountKey:      base58.Encode(lookup.GetAccountKey()),
					WritableIndexes: fmt.Sprintf("%x", lookup.GetWritableIndexes()),
					ReadonlyIndexes: fmt.Sprintf("%x", lookup.GetReadonlyIndexes()),
				}
				lookups = append(lookups, lookupJSON)
			}
			if len(lookups) > 0 {
				jsonTx.Transaction.Message.AddressTableLookups = lookups
			}
		}
	}

	// 处理 Meta 部分
	meta := txInfo.GetMeta()
	if meta != nil {
		// 处理错误
		if err := meta.GetErr(); err != nil {
			jsonTx.Meta.Err = &definition.TransactionErrorJSON{
				Err: fmt.Sprintf("%x", err.GetErr()),
			}
		}

		// 处理费用
		jsonTx.Meta.Fee = meta.GetFee()

		// 处理余额
		jsonTx.Meta.PreBalances = meta.GetPreBalances()
		jsonTx.Meta.PostBalances = meta.GetPostBalances()

		// 处理内部指令
		innerInstructions := []definition.InnerInstructionsJSON{}
		jsonTx.Meta.InnerInstructionsNone = meta.GetInnerInstructionsNone()
		if !jsonTx.Meta.InnerInstructionsNone {
			for _, innerInstrs := range meta.GetInnerInstructions() {
				innerInstructionsJSON := definition.InnerInstructionsJSON{
					Index: innerInstrs.GetIndex(),
				}

				instructions := []definition.InnerInstructionJSON{}
				for _, instr := range innerInstrs.GetInstructions() {
					instruction := definition.InnerInstructionJSON{
						ProgramIdIndex: instr.GetProgramIdIndex(),
						Data:           base58.Encode(instr.GetData()),
					}

					// 处理账户索引
					accounts := []uint8{}
					for _, acc := range instr.GetAccounts() {
						accounts = append(accounts, uint8(acc))
					}
					instruction.Accounts = accounts

					// 处理堆栈高度
					if instr.StackHeight != nil {
						stackHeight := instr.GetStackHeight()
						instruction.StackHeight = &stackHeight
					}

					instructions = append(instructions, instruction)
				}
				innerInstructionsJSON.Instructions = instructions
				innerInstructions = append(innerInstructions, innerInstructionsJSON)
			}
		}
		if len(innerInstructions) > 0 {
			jsonTx.Meta.InnerInstructions = innerInstructions
		}

		// 处理日志消息
		jsonTx.Meta.LogMessagesNone = meta.GetLogMessagesNone()
		if !jsonTx.Meta.LogMessagesNone {
			jsonTx.Meta.LogMessages = meta.GetLogMessages()
		}

		// 处理代币余额
		preTokenBalances := []definition.TokenBalanceJSON{}
		for _, balance := range meta.GetPreTokenBalances() {
			tokenBalance := definition.TokenBalanceJSON{
				AccountIndex: balance.GetAccountIndex(),
				Mint:         balance.GetMint(),
				Owner:        balance.GetOwner(),
				ProgramId:    balance.GetProgramId(),
			}

			// 处理 UI 代币金额
			uiAmount := balance.GetUiTokenAmount()
			if uiAmount != nil {
				tokenBalance.UiTokenAmount = definition.UiTokenAmountJSON{
					UiAmount:       uiAmount.GetUiAmount(),
					Decimals:       uiAmount.GetDecimals(),
					Amount:         uiAmount.GetAmount(),
					UiAmountString: uiAmount.GetUiAmountString(),
				}
			}

			preTokenBalances = append(preTokenBalances, tokenBalance)
		}
		if len(preTokenBalances) > 0 {
			jsonTx.Meta.PreTokenBalances = preTokenBalances
		}

		postTokenBalances := []definition.TokenBalanceJSON{}
		for _, balance := range meta.GetPostTokenBalances() {
			tokenBalance := definition.TokenBalanceJSON{
				AccountIndex: balance.GetAccountIndex(),
				Mint:         balance.GetMint(),
				Owner:        balance.GetOwner(),
				ProgramId:    balance.GetProgramId(),
			}

			// 处理 UI 代币金额
			uiAmount := balance.GetUiTokenAmount()
			if uiAmount != nil {
				tokenBalance.UiTokenAmount = definition.UiTokenAmountJSON{
					UiAmount:       uiAmount.GetUiAmount(),
					Decimals:       uiAmount.GetDecimals(),
					Amount:         uiAmount.GetAmount(),
					UiAmountString: uiAmount.GetUiAmountString(),
				}
			}

			postTokenBalances = append(postTokenBalances, tokenBalance)
		}
		if len(postTokenBalances) > 0 {
			jsonTx.Meta.PostTokenBalances = postTokenBalances
		}

		// 处理奖励
		rewards := []definition.RewardJSON{}
		for _, reward := range meta.GetRewards() {
			rewardJSON := definition.RewardJSON{
				Pubkey:      reward.GetPubkey(),
				Lamports:    reward.GetLamports(),
				PostBalance: reward.GetPostBalance(),
				RewardType:  reward.GetRewardType().String(),
				Commission:  reward.GetCommission(),
			}
			rewards = append(rewards, rewardJSON)
		}
		if len(rewards) > 0 {
			jsonTx.Meta.Rewards = rewards
		}

		// 处理加载的地址
		writableAddresses := []string{}
		for _, addr := range meta.GetLoadedWritableAddresses() {
			writableAddresses = append(writableAddresses, base58.Encode(addr))
		}
		if len(writableAddresses) > 0 {
			jsonTx.Meta.LoadedWritableAddresses = writableAddresses
		}

		readonlyAddresses := []string{}
		for _, addr := range meta.GetLoadedReadonlyAddresses() {
			readonlyAddresses = append(readonlyAddresses, base58.Encode(addr))
		}
		if len(readonlyAddresses) > 0 {
			jsonTx.Meta.LoadedReadonlyAddresses = readonlyAddresses
		}

		// 处理返回数据
		jsonTx.Meta.ReturnDataNone = meta.GetReturnDataNone()
		if !jsonTx.Meta.ReturnDataNone && meta.GetReturnData() != nil {
			returnData := meta.GetReturnData()
			jsonTx.Meta.ReturnData = &definition.ReturnDataJSON{
				ProgramId: base58.Encode(returnData.GetProgramId()),
				Data:      base58.Encode(returnData.GetData()),
			}
		}

		// 处理计算单元消耗
		if meta.ComputeUnitsConsumed != nil {
			computeUnits := meta.GetComputeUnitsConsumed()
			jsonTx.Meta.ComputeUnitsConsumed = &computeUnits
		}
	}

	// 将结构转换为 JSON 并打印
	jsonData, err := sonic.MarshalIndent(jsonTx, "", "  ")
	if err != nil {
		fmt.Printf("JSON 转换错误: %v\n", err)
		return
	}

	fmt.Printf("交易 JSON 数据:\n%s\n", string(jsonData))
}

func main() {
	// 加載環境變數
	if err := godotenv.Load(); err != nil {
		log.Println("警告: 無法加載 .env 文件，將使用系統環境變數")
	}

	// 從環境變數獲取配置
	endpoint := os.Getenv("SOLANA_GRPC_ENDPOINT")
	apiToken := os.Getenv("SOLANA_API_TOKEN")

	// 檢查必要的配置是否存在
	if endpoint == "" || apiToken == "" {
		log.Fatal("錯誤: 缺少必要的環境變數 SOLANA_GRPC_ENDPOINT 或 SOLANA_API_TOKEN")
	}
	ctx := context.Background()

	// Create manager with data handler and x-token
	manager, err := NewGrpcStreamManager(
		endpoint,
		apiToken,
		handleAccountUpdate,
	)
	if err != nil {
		log.Fatal(err)
	}
	defer manager.Close()

	transactions := make(map[string]*pb.SubscribeRequestFilterTransactions)
	transactions["transactions"] = &pb.SubscribeRequestFilterTransactions{
		Vote:   boolPtr(false),
		Failed: boolPtr(false),
	}

	// 启动交易处理goroutines
	for i := 0; i < 100; i++ {
		go handleTask(taskChan)
	}

	// 启动区块处理goroutines
	for i := 0; i < 10; i++ {
		go handleBlockTask(blockChan)
	}

	// 创建订阅请求，包括区块和交易
	blocks := make(map[string]*pb.SubscribeRequestFilterBlocks)
	blocks["blocks"] = &pb.SubscribeRequestFilterBlocks{
		// 不需要指定 AccountInclude，因为我们不关心特定账户
		// AccountInclude: []string{
		// 	"TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA", // Token Program
		// 	"11111111111111111111111111111111",            // System Program
		// 	"So11111111111111111111111111111111111111112", // Wrapped SOL
		// },
		IncludeTransactions: boolPtr(false), // 设置为 false，不包含交易数据
		IncludeAccounts:     boolPtr(false), // 设置为 false，不包含账户数据
		IncludeEntries:      boolPtr(false), // 设置为 false，不包含条目数据
	}

	// 移除 slots 相关配置
	commitment := pb.CommitmentLevel_CONFIRMED
	req := &pb.SubscribeRequest{
		Blocks:       blocks,
		Transactions: transactions,
		Commitment:   &commitment,
	}

	log.Println("Starting block monitoring...")
	log.Println("Monitoring blocks for Token Program, System Program, and Wrapped SOL activities...")

	// Connect and handle updates
	if err := manager.Connect(ctx, req); err != nil {
		log.Fatal(err)
	}

	// Keep the main goroutine running
	select {}
}
