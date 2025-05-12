package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"go-yellowstone-client/pkg/solana"
	"log"
	"os"
	"strings"
	"sync"
	"time"

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

func handleAccountUpdate(update *pb.SubscribeUpdate) {
	taskChan <- update.GetTransaction()
	// go handleTransactionUpdate(update.GetTransaction())
	// switch u := update.GetUpdateOneof().(type) {
	// case *pb.SubscribeUpdate_Block:
	// 	handleBlockUpdate(u.Block)
	// case *pb.SubscribeUpdate_Slot:
	// 	handleSlotUpdate(u.Slot)
	// case *pb.SubscribeUpdate_Transaction:
	// 	handleTransactionUpdate(u.Transaction)
	// }
}

func handleBlockUpdate(block *pb.SubscribeUpdateBlock) {
	go func() {
		log.Printf("\n=== Block Details ===\n")
		log.Printf("Slot: %d\n", block.GetSlot())
		log.Printf("Parent Slot: %d\n", block.GetParentSlot())
		log.Printf("Blockhash: %s\n", block.GetBlockhash())
		log.Printf("Previous Blockhash: %s\n", block.GetParentBlockhash())
		// fmt.Printf("Slot: %d\n", block.GetSlot())
		// fmt.Printf("BlockTime: %s\n", block.GetBlockTime())

		// blockTime := time.Unix(block.GetBlockTime().GetTimestamp(), 0)
		// fmt.Printf("TimeDifference: %v\n", time.Now().Sub(blockTime))

		if txs := block.GetTransactions(); len(txs) > 0 {
			log.Printf("\n=== Block Transactions ===\n")
			log.Printf("Transaction Count: %d\n", len(txs))

			for i, tx := range txs {
				if txInfo := tx.GetTransaction(); txInfo != nil {
					log.Printf("\nTransaction %d:\n", i+1)
					if signatures := txInfo.GetSignatures(); len(signatures) > 0 {
						log.Printf("  Signature: %s\n", base58.Encode(signatures[0]))
					}

					if meta := tx.GetMeta(); meta != nil {
						log.Printf("  Status: %v\n", meta.GetErr() == nil)
						log.Printf("  Fee: %d lamports\n", meta.GetFee())
					}
				}
			}
		}

		if rewards := block.GetRewards(); rewards != nil {
			if rewardList := rewards.GetRewards(); len(rewardList) > 0 {
				log.Printf("\n=== Block Rewards ===\n")
				for _, reward := range rewardList {
					pubkey := []byte(reward.GetPubkey())
					log.Printf("  Account: %s\n", base58.Encode(pubkey))
					log.Printf("  Lamports: %d\n", reward.GetLamports())
					log.Printf("  Post Balance: %d\n", reward.GetPostBalance())
					log.Printf("  Reward Type: %d\n", reward.GetRewardType())
					log.Printf("  Commission: %s\n", reward.GetCommission())
					log.Printf("  ---\n")
				}
			}
		}
		log.Printf("\n%s\n", strings.Repeat("=", 50))
	}()

}

func handleSlotUpdate(slot *pb.SubscribeUpdateSlot) {
	log.Printf("\n=== Slot Update ===\n")
	log.Printf("Slot: %d\n", slot.GetSlot())

	if parent := slot.GetParent(); parent > 0 {
		log.Printf("Parent Slot: %d\n", parent)
	}

	log.Printf("Status: %v\n", slot.GetStatus())

	if deadError := slot.GetDeadError(); deadError != "" {
		log.Printf("Dead Error: %s\n", deadError)
	}

	log.Printf("\n%s\n", strings.Repeat("=", 50))
}

func handleTransactionUpdate(tx *pb.SubscribeUpdateTransaction) {
	log.Printf("Transaction Update:\n")
	slot := tx.GetSlot()
	log.Printf("  Slot: %d\n", slot)

	currentTime := time.Now().UTC()
	// fmt.Printf("  Current Time: %s\n", currentTime.Format("Jan 2, 2006 15:04:05 +UTC"))

	blockTime, err := solana.GetBlockTime(slot)
	if err == nil {
		fmt.Printf("Slot: %d, Block Time: %s, Difference: %v\n", slot, blockTime.Format("Jan 2, 2006 15:04:05 +UTC"), currentTime.Sub(blockTime))
	} else {
		log.Printf("  Failed to get block time: %v\n", err)
	}

	txInfo := tx.GetTransaction()
	if txInfo != nil {
		// Print signature if available
		if signature := txInfo.GetSignature(); len(signature) > 0 {
			log.Printf("  Signature: %s\n", base58.Encode(signature))
		}

		// Print if it's a vote transaction
		log.Printf("  Is Vote: %v\n", txInfo.GetIsVote())

		// Print transaction index
		log.Printf("  Transaction Index: %d\n", txInfo.GetIndex())

		// Print status and metadata
		if meta := txInfo.GetMeta(); meta != nil {
			log.Printf("  Status: %v\n", meta.GetErr() == nil)
			log.Printf("  Fee: %d lamports\n", meta.GetFee())
		}
	}
}

func boolPtr(b bool) *bool {
	return &b
}

var taskChan = make(chan *pb.SubscribeUpdateTransaction, 5000)

func handleTask(tc <-chan *pb.SubscribeUpdateTransaction) {
	for tx := range tc {
		handleTransactionUpdate(tx)
	}
}

func main() {
	// Load environment variables
	if err := godotenv.Load(); err != nil {
		log.Println("Warning: Failed to load .env file, using system environment variables")
	}

	// Get configuration from environment variables
	endpoint := os.Getenv("SOLANA_GRPC_ENDPOINT")
	apiToken := os.Getenv("SOLANA_API_TOKEN")

	// Check if necessary configurations exist
	if endpoint == "" || apiToken == "" {
		log.Fatal("Error: Missing required environment variables SOLANA_GRPC_ENDPOINT or SOLANA_API_TOKEN")
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

	// wg := sync.WaitGroup{}
	for i := 0; i < 100; i++ {
		// wg.Add(1)
		go handleTask(taskChan)
	}

	// // Create subscription request for blocks and slots
	// blocks := make(map[string]*pb.SubscribeRequestFilterBlocks)
	// blocks["blocks"] = &pb.SubscribeRequestFilterBlocks{

	// 	AccountInclude: []string{
	// 		"TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA", // Token Program
	// 		"11111111111111111111111111111111",            // System Program
	// 		"So11111111111111111111111111111111111111112", // Wrapped SOL
	// 	},
	// 	IncludeTransactions: boolPtr(true),
	// 	IncludeAccounts:     boolPtr(true),
	// 	IncludeEntries:      boolPtr(false),
	// }
	// _ = blocks

	// slots := make(map[string]*pb.SubscribeRequestFilterSlots)
	// slots["slots"] = &pb.SubscribeRequestFilterSlots{
	// 	FilterByCommitment: boolPtr(true),
	// }
	// _ = slots

	// commitment := pb.CommitmentLevel_CONFIRMED
	req := &pb.SubscribeRequest{
		// Blocks:     blocks,
		Transactions: transactions,
		// Slots:        slots,
		// Commitment:   &commitment,
	}

	log.Println("Starting block and slot monitoring...")
	log.Println("Monitoring blocks for Token Program, System Program, and Wrapped SOL activities...")

	// Connect and handle updates
	if err := manager.Connect(ctx, req); err != nil {
		log.Fatal(err)
	}

	// Add periodic output of cache statistics
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				successCount, errorCount := solana.GetCacheStats()
				fmt.Printf("Block time cache statistics - Success cache: %d, Error cache: %d\n", successCount, errorCount)
			}
		}
	}()

	// Keep the main goroutine running
	select {}
}
