package main

import (
	"context"
	"log"
	"math/big" // big 패키지 추가 확인
	"net/http"
	"os" // 환경변수 읽기 위해 추가
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// 데이터 모델 정의
type Transaction struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	Hash        string `gorm:"uniqueIndex" json:"hash"`
	FromAddress string `json:"from_address"`
	ToAddress   string `json:"to_address"`
	Value       string `json:"value"`
	BlockNumber uint64 `json:"block_number"`
}

func main() {
	// [A] DB 설정
	db, err := gorm.Open(sqlite.Open("indexer.db"), &gorm.Config{})
	if err != nil {
		log.Fatal("DB 연결 실패:", err)
	}
	db.AutoMigrate(&Transaction{})

	// [B] 환경변수에서 Infura RPC URL 읽기
	// docker-compose.yml의 ETH_RPC_URL 변수를 가져옵니다.
	rpcURL := os.Getenv("ETH_RPC_URL")
	if rpcURL == "" {
		log.Fatal("❌ 에러: ETH_RPC_URL 환경변수가 설정되지 않았습니다.")
	}

	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		log.Fatal("RPC 연결 실패:", err)
	}
	log.Printf("🔗 연결 성공: %s", rpcURL)

	// [C] 백그라운드 인덱싱 시작
	go func() {
		log.Println("🚀 인덱서 가동 중...")
		for {
			header, err := client.HeaderByNumber(context.Background(), nil)
			if err != nil {
				log.Println("블록 확인 에러:", err)
				time.Sleep(5 * time.Second)
				continue
			}

			processBlock(client, db, header.Number.Uint64())
			time.Sleep(12 * time.Second)
		}
	}()

	// [D] API 서버 설정 (Gin)
	r := gin.Default()
	r.Use(cors.Default())

	r.GET("/api/transactions", func(c *gin.Context) {
		var txs []Transaction
		db.Order("block_number desc").Limit(50).Find(&txs)
		c.JSON(http.StatusOK, txs)
	})

	log.Println("📡 API 서버 실행 중 (Port 8080)")
	r.Run(":8080")
}

// processBlock 함수는 이전과 동일하며 big.Int 처리를 위해 상단 import 확인 필요
func processBlock(client *ethclient.Client, db *gorm.DB, blockNum uint64) {
	block, err := client.BlockByNumber(context.Background(), new(big.Int).SetUint64(blockNum))
	if err != nil {
		log.Println("블록 읽기 실패:", err)
		return
	}

	for _, tx := range block.Transactions() {
		from, _ := client.TransactionSender(context.Background(), tx, block.Hash(), 0)

		toAddr := ""
		if tx.To() != nil {
			toAddr = tx.To().Hex()
		}

		newTx := Transaction{
			Hash:        tx.Hash().Hex(),
			FromAddress: from.Hex(),
			ToAddress:   toAddr,
			Value:       tx.Value().String(),
			BlockNumber: block.NumberU64(),
		}

		db.Where(Transaction{Hash: newTx.Hash}).FirstOrCreate(&newTx)
	}
	log.Printf("✅ 블록 #%d 인덱싱 완료", blockNum)
}
