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
	log.Println("🚀 블록 감시 및 자동 백필 루프 가동...")

	for {
		// 1. DB에서 가장 마지막으로 저장된 블록 번호 가져오기
		var lastStoredBlock Transaction
		db.Order("block_number desc").First(&lastStoredBlock)

		// DB가 비어있다면 현재 블록부터 시작 (혹은 특정 과거 블록 지정 가능)
		startBlock := lastStoredBlock.BlockNumber

		// 2. 네트워크의 최신 블록 번호 확인
		header, err := client.HeaderByNumber(context.Background(), nil)
		if err != nil {
			log.Println("❌ 최신 헤더 확인 에러:", err)
			time.Sleep(5 * time.Second)
			continue
		}
		latestBlock := header.Number.Uint64()

		// 첫 실행 시 초기값 설정
		if startBlock == 0 {
			startBlock = latestBlock - 1
		}

		// 3. 공백 메우기 (Back-filling)
		// DB에 저장된 마지막 블록 다음 번호부터 최신 블록까지 순회
		if startBlock < latestBlock {
			log.Printf("📥 공백 발견: #%d ~ #%d (총 %d개 블록)", startBlock+1, latestBlock, latestBlock-startBlock)

			for i := startBlock + 1; i <= latestBlock; i++ {
				err := fetchAndSaveBlock(client, db, i)
				if err != nil {
					log.Printf("⚠️ 블록 #%d 동기화 중 오류: %v", i, err)
					// 오류 발생 시 해당 지점부터 다시 시도하기 위해 루프 탈출
					break
				}
				// 팁: 너무 빨리 요청하면 RPC에서 차단될 수 있으니 아주 짧은 휴식 추가 가능
				time.Sleep(1000 * time.Millisecond)
			}
		}

		// 4. 다음 체크까지 대기 (실시간 감시 주기)
		time.Sleep(10 * time.Second)
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
