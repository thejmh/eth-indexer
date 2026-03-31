package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/big"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	alertThreshold float64 = 100.0
	thresholdMu    sync.RWMutex
	upgrader       = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
)

type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
}

// --- WebSocket Hub 정의 ---
type Hub struct {
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
	mu         sync.Mutex
}

func newHub() *Hub {
	return &Hub{
		broadcast:  make(chan []byte),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		clients:    make(map[*Client]bool),
	}
}

func (h *Hub) run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client] = true
		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
		case message := <-h.broadcast:
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
		}
	}
}

// Transaction 데이터 모델 (Value를 가독성 있게 float64로 저장하거나 별도 필드 추가 가능)
type Transaction struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Hash        string    `gorm:"uniqueIndex" json:"hash"`
	FromAddress string    `gorm:"index" json:"from_address"` // 인덱스 추가
	ToAddress   string    `gorm:"index" json:"to_address"`   // 인덱스 추가
	Value       string    `json:"raw_value"`                 // JSON 필드명이 raw_value 임에 주의!
	EthValue    float64   `json:"eth_value"`                 // 이미 백엔드에서 계산된 값
	BlockNumber uint64    `json:"block_number"`
	CreatedAt   time.Time `gorm:"index" json:"created_at"` // [추가] 인덱싱된 시간
}

func main() {
	// 1. DB 초기화
	db, err := gorm.Open(sqlite.Open("indexer.db"), &gorm.Config{})
	if err != nil {
		log.Fatal("DB 연결 실패:", err)
	}
	db.AutoMigrate(&Transaction{})

	// 2. RPC 클라이언트 설정
	rpcURL := os.Getenv("ETH_RPC_URL")
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		log.Fatal("RPC 연결 실패:", err)
	}

	// 웹소켓 허브 초기화 및 실행
	hub := newHub()
	go hub.run()

	// 3. 백그라운드 인덱서 실행
	// 인덱서 실행 시 hub 전달
	go processBlock(client, db, hub)

	// 데이터 클리너 실행
	go startDataCleaner(db)

	// 4. API 서버 설정
	r := gin.Default()
	r.Use(cors.Default())

	// 고래 알림 API
	r.GET("/api/currentSet", func(c *gin.Context) {
		c.JSON(http.StatusOK, alertThreshold)
	})

	// 고래 알림 설정 API
	r.POST("/api/settings", func(c *gin.Context) {
		var in struct {
			Threshold float64 `json:"threshold"`
		}
		if err := c.BindJSON(&in); err == nil {
			thresholdMu.Lock()
			alertThreshold = in.Threshold
			thresholdMu.Unlock()
			c.JSON(200, gin.H{"status": "ok"})
		}
	})

	// 웹소켓 엔드포인트
	r.GET("/ws", func(c *gin.Context) {
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		client := &Client{hub: hub, conn: conn, send: make(chan []byte, 256)}
		hub.register <- client
		go func() {
			defer func() { hub.unregister <- client; conn.Close() }()
			for {
				_, message, err := conn.ReadMessage()
				if err != nil {
					break
				}
				hub.broadcast <- message
			}
		}()
		for msg := range client.send {
			conn.WriteMessage(websocket.TextMessage, msg)
		}
	})

	r.GET("/api/transactions", func(c *gin.Context) {
		var txs []Transaction
		limit := 50
		address := c.Query("address")
		blockNum := c.Query("block")

		if address == "" && blockNum == "" {
			limit = 10
		}

		query := db.Order("block_number desc").Limit(limit)

		// 1. 지갑 주소 필터링 (From 또는 To에 포함된 경우)
		if address != "" {
			address = toChecksumAddr(address)
			// 주소는 대소문자 구분 없이 검색하기 위해 체크섬 처리를 하거나 OR 조건을 씁니다.
			query = query.Where("from_address = ? OR to_address = ?", address, address)
		}

		// 2. 특정 블록 번호 필터링
		if blockNum != "" {
			query = query.Where("block_number = ?", blockNum)
		}

		if err := query.Find(&txs).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "데이터 조회 실패"})
			return
		}
		c.JSON(http.StatusOK, txs)
	})
	r.Run(":8080")
}

// [유틸리티] Wei 문자열을 ETH로 변환
func weiToEther(weiStr string) float64 {
	wei := new(big.Int)
	wei.SetString(weiStr, 10)

	f := new(big.Float).SetInt(wei)
	// 10^18로 나누기
	ethValue := new(big.Float).Quo(f, big.NewFloat(math.Pow10(18)))

	result, _ := ethValue.Float64()
	return result
}

// [유틸리티] 주소를 Checksum 형식으로 변환 (EIP-55)
func toChecksumAddr(addr string) string {
	if addr == "" || addr == "unknown" {
		return addr
	}
	// common.HexToAddress가 주소 형식을 검증하고 .Hex()가 체크섬을 적용함
	return common.HexToAddress(addr).Hex()
}

// --- API 핸들러: 설정 가져오기 및 저장 ---
func setupSettingRoutes(r *gin.Engine) {
	// 현재 설정값 조회
	r.GET("/api/settings", func(c *gin.Context) {
		thresholdMu.RLock() // 읽기 잠금
		defer thresholdMu.RUnlock()
		c.JSON(http.StatusOK, gin.H{"threshold": alertThreshold})
	})

	// 새로운 설정값 저장
	r.POST("/api/settings", func(c *gin.Context) {
		var input struct {
			Threshold float64 `json:"threshold"`
		}
		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "잘못된 입력값입니다."})
			return
		}

		thresholdMu.Lock() // 쓰기 잠금
		alertThreshold = input.Threshold
		thresholdMu.Unlock()

		log.Printf("⚙️  알림 임계값이 %.2f ETH로 변경되었습니다.", input.Threshold)
		c.JSON(http.StatusOK, gin.H{"message": "설정이 저장되었습니다.", "threshold": alertThreshold})
	})
}

// --- 인덱서 로직 내 알림 체크 기능 ---
func checkWhaleAlert(tx Transaction, hub *Hub) {
	thresholdMu.RLock()
	currentThreshold := alertThreshold
	thresholdMu.RUnlock()

	// [핵심 로직] 저장된 트랜잭션 금액이 설정된 임계값 이상인지 검사
	if tx.EthValue >= currentThreshold {
		log.Printf("🚨 고래 감지! [%.2f ETH] Hash: %s", tx.EthValue, tx.Hash)

		// 웹소켓을 통해 프론트엔드로 특수 이벤트 전송
		whaleEvent := map[string]interface{}{
			"type":    "WHALE_ALERT",
			"value":   tx.EthValue,
			"from":    tx.FromAddress,
			"to":      tx.ToAddress,
			"hash":    tx.Hash,
			"message": fmt.Sprintf("🐋 %.2f ETH 고액 거래가 발생했습니다!", tx.EthValue),
		}

		msgBytes, _ := json.Marshal(whaleEvent)
		hub.broadcast <- msgBytes // Hub를 통해 연결된 모든 클라이언트에 전송
	}
}

// 1시간마다 깨어나서 24시간 전 데이터를 삭제하는 함수
func startDataCleaner(db *gorm.DB) {
	log.Println("🧹 데이터 클리너(TTL 24h) 가동 시작...")
	for {
		// 현재 시간 기준 24시간 전 시점 계산
		threshold := time.Now().Add(-24 * time.Hour)
		// 24시간보다 이전에 생성된 데이터 삭제
		result := db.Where("created_at < ?", threshold).Delete(&Transaction{})

		if result.RowsAffected > 0 {
			log.Printf("♻️  TTL 정리 완료: 오래된 트랜잭션 %d건 삭제됨", result.RowsAffected)
		}

		// 1시간 대기 후 다시 확인
		time.Sleep(1 * time.Hour)
	}
}

func processBlock(client *ethclient.Client, db *gorm.DB, hub *Hub) {
	log.Println("🚀 블록 감시 및 자동 백필 루프 가동 시작...")

	for {
		// 1. [백필 핵심] DB에서 현재 저장된 가장 높은 블록 번호 조회
		var lastTx Transaction
		// 만약 데이터가 없으면 lastTx.BlockNumber는 0이 됨
		db.Order("block_number desc").First(&lastTx)
		lastStoredBlock := lastTx.BlockNumber

		// 2. 네트워크의 실제 최신 블록 번호 확인
		header, err := client.HeaderByNumber(context.Background(), nil)
		if err != nil {
			log.Printf("❌ 네트워크 헤더 확인 에러: %v", err)
			time.Sleep(10 * time.Second)
			continue
		}
		latestBlock := header.Number.Uint64()

		// 3. [초기화] DB가 완전히 비어있다면 현재 블록부터 시작 (혹은 원하는 지점)
		if lastStoredBlock == 0 {
			lastStoredBlock = latestBlock - 1
		}

		// 4. [백필 로직] 저장된 블록과 최신 블록 사이에 차이가 있다면 실행
		if lastStoredBlock < latestBlock {
			gap := latestBlock - lastStoredBlock
			log.Printf("📥 공백 발견: #%d ~ #%d (총 %d개 블록 동기화 중...)", lastStoredBlock+1, latestBlock, gap)

			for i := lastStoredBlock + 1; i <= latestBlock; i++ {
				err := fetchAndSaveBlock(client, db, i, hub)
				if err != nil {
					log.Printf("⚠️ 블록 #%d 동기화 실패: %v", i, err)
					break // 에러 시 다음 메인 루프에서 다시 시도
				}

				// 저장 성공 시 웹소켓 알림 전송
				syncMsg, _ := json.Marshal(map[string]interface{}{"type": "NEW_BLOCK", "number": latestBlock})
				hub.broadcast <- syncMsg

				// RPC 노드 부하 방지용 짧은 휴식
				time.Sleep(100 * time.Millisecond)
			}
		}

		// 5. 다음 블록 생성을 기다림 (이더리움 평균 12초)
		time.Sleep(10 * time.Second)
	}
}

func fetchAndSaveBlock(client *ethclient.Client, db *gorm.DB, blockNum uint64, hub *Hub) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	block, err := client.BlockByNumber(ctx, new(big.Int).SetUint64(blockNum))
	if err != nil {
		return err
	}

	chainID, _ := client.NetworkID(context.Background())
	signer := types.LatestSignerForChainID(chainID)

	for _, tx := range block.Transactions() {
		from, _ := types.Sender(signer, tx)

		// 1. 기본 변수 초기화
		var toAddr string

		// 2. [핵심] Nil 체크 로직 도입
		if tx.To() == nil {
			// 수신자가 없는 경우 = 스마트 컨트랙트 배포 트랜잭션
			toAddr = "Contract Creation"
		} else {
			// 수신자가 있는 경우 = 일반 송금 또는 컨트랙트 실행
			toAddr = toChecksumAddr(tx.To().Hex())
		}

		newTx := Transaction{
			Hash:        tx.Hash().Hex(),
			FromAddress: toChecksumAddr(from.Hex()),
			ToAddress:   toAddr, // 안전하게 할당된 주소 사용
			Value:       tx.Value().String(),
			EthValue:    weiToEther(tx.Value().String()),
			BlockNumber: blockNum,
		}

		// 고래 알림 체크
		thresholdMu.RLock()
		limit := alertThreshold
		thresholdMu.RUnlock()

		if newTx.EthValue >= limit { // 기준 ETH 이상 고액 거래
			log.Printf("🐋 고래 출현! [Hash: %s] [Value: %.2f ETH]", newTx.Hash, newTx.EthValue)

			event, _ := json.Marshal(map[string]interface{}{
				"type": "WHALE_ALERT", "value": newTx.EthValue, "hash": tx.Hash().Hex(),
			})
			hub.broadcast <- event
		}

		// DB 저장
		db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "hash"}},
			DoNothing: true,
		}).Create(&newTx)
	}
	log.Printf("✅ Block #%d indexed with human-readable data", blockNum)
	return nil
}
