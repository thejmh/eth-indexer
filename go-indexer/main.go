package main

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Transaction 데이터 모델
type Transaction struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	Hash        string `gorm:"uniqueIndex" json:"hash"`
	FromAddress string `json:"from_address"`
	ToAddress   string `json:"to_address"`
	Value       string `json:"value"`
	BlockNumber uint64 `json:"block_number"`
}

func main() {
	// 1. DB 초기화 (SQLite)
	db, err := gorm.Open(sqlite.Open("indexer.db"), &gorm.Config{})
	if err != nil {
		log.Fatal("DB 연결 실패:", err)
	}
	db.AutoMigrate(&Transaction{})

	// 2. 이더리움 클라이언트 설정 (RPC)
	rpcURL := os.Getenv("ETH_RPC_URL")
	if rpcURL == "" {
		log.Fatal("ETH_RPC_URL 환경변수가 설정되지 않았습니다.")
	}
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		log.Fatal("RPC 연결 실패:", err)
	}
	log.Printf("🔗 노드 연결 성공: %s", rpcURL)

	// 3. 백그라운드 인덱싱 루프 시작 (processBlock 실행)
	go processBlock(client, db)

	// 4. API 서버 설정 (Gin)
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

// [함수 1] processBlock: 고수준 관리자 (흐름 제어)
func processBlock(client *ethclient.Client, db *gorm.DB) {
	log.Println("🚀 블록 감시 루프 가동 시작...")
	var lastProcessedBlock uint64

	for {
		// 최신 블록 넘버 확인
		header, err := client.HeaderByNumber(context.Background(), nil)
		if err != nil {
			log.Println("❌ 블록 헤더 확인 에러:", err)
			time.Sleep(12 * time.Second)
			continue
		}

		currentBlock := header.Number.Uint64()

		// 새로운 블록이 발견되었을 때만 실행
		if currentBlock > lastProcessedBlock {
			log.Printf("🔍 새 블록 발견: #%d", currentBlock)

			// 실제 작업 수행자(fetchAndSaveBlock) 호출
			err := fetchAndSaveBlock(client, db, currentBlock)
			if err != nil {
				log.Printf("⚠️ 블록 #%d 처리 중 오류: %v", currentBlock, err)
			} else {
				lastProcessedBlock = currentBlock
			}
		}

		// 이더리움 블록 생성 주기(약 12초)에 맞춰 대기
		time.Sleep(12 * time.Second)
	}
}

// [함수 2] fetchAndSaveBlock: 저수준 작업자 (데이터 추출 및 저장)
func fetchAndSaveBlock(client *ethclient.Client, db *gorm.DB, blockNum uint64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// 1. 블록 데이터 전체 가져오기
	block, err := client.BlockByNumber(ctx, new(big.Int).SetUint64(blockNum))
	if err != nil {
		return fmt.Errorf("block fetch error: %v", err)
	}

	// 2. 체인 ID 확인 (Sender 주소 복구용)
	chainID, err := client.NetworkID(ctx)
	if err != nil {
		return fmt.Errorf("network id error: %v", err)
	}
	signer := types.LatestSignerForChainID(chainID)

	// 3. 트랜잭션 루프 처리
	for _, tx := range block.Transactions() {
		// From 주소 복구 (최신 방식)
		from, err := types.Sender(signer, tx)
		fromAddr := "unknown"
		if err == nil {
			fromAddr = from.Hex()
		}

		toAddr := ""
		if tx.To() != nil {
			toAddr = tx.To().Hex()
		}

		newTx := Transaction{
			Hash:        tx.Hash().Hex(),
			FromAddress: fromAddr,
			ToAddress:   toAddr,
			Value:       tx.Value().String(),
			BlockNumber: blockNum,
		}

		// 4. DB 저장 (Upsert: 중복 해시는 무시)
		db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "hash"}},
			DoNothing: true,
		}).Create(&newTx)
	}

	log.Printf("✅ 블록 #%d 인덱싱 완료 (TX 수: %d)", blockNum, len(block.Transactions()))
	return nil
}
