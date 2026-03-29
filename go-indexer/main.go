package main

import (
	"context"
	"log"
	"math"
	"math/big"
	"net/http"
	"os"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Transaction 데이터 모델 (Value를 가독성 있게 float64로 저장하거나 별도 필드 추가 가능)
type Transaction struct {
	ID          uint    `gorm:"primaryKey" json:"id"`
	Hash        string  `gorm:"uniqueIndex" json:"hash"`
	FromAddress string  `gorm:"index" json:"from_address"` // 인덱스 추가
	ToAddress   string  `gorm:"index" json:"to_address"`   // 인덱스 추가
	Value       string  `json:"raw_value"`                 // JSON 필드명이 raw_value 임에 주의!
	EthValue    float64 `json:"eth_value"`                 // 이미 백엔드에서 계산된 값
	BlockNumber uint64  `json:"block_number"`
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

	// 3. 백그라운드 인덱서 실행
	go processBlock(client, db)

	// 4. API 서버 설정
	r := gin.Default()
	r.Use(cors.Default())
	r.GET("/api/transactions", func(c *gin.Context) {
		var txs []Transaction
		limit := 50

		address := c.Query("address")
		if address != "" {
			// 사용자가 소문자로 입력해도 DB의 체크섬 주소와 매칭되도록 변환
			address = toChecksumAddr(address)
		}

		blockNum := c.Query("block")

		if address == "" && blockNum == "" {
			limit = 10
		}

		query := db.Order("block_number desc").Limit(limit)

		// 1. 지갑 주소 필터링 (From 또는 To에 포함된 경우)
		if address != "" {
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

func processBlock(client *ethclient.Client, db *gorm.DB) {
	log.Println("🚀 블록 감시 및 자동 백필 루프 가동 시작...")

	for {
		// 1. [이론: 상태 확인] DB에서 가장 최신 저장된 블록 가져오기
		var lastStoredTx Transaction
		db.Order("block_number desc").First(&lastStoredTx)

		lastStoredBlock := lastStoredTx.BlockNumber

		// 2. [이론: 목표 확인] 네트워크의 실제 최신 블록 번호 확인
		header, err := client.HeaderByNumber(context.Background(), nil)
		if err != nil {
			log.Printf("❌ 네트워크 헤더 확인 에러: %v", err)
			time.Sleep(10 * time.Second)
			continue
		}
		latestBlock := header.Number.Uint64()

		// 첫 실행 시 DB가 비어있다면 현재 블록의 바로 이전부터 시작하도록 설정
		if lastStoredBlock == 0 {
			lastStoredBlock = latestBlock - 1
		}

		// 3. [이론: 백필 실행] DB와 네트워크 사이의 공백(Gap) 메우기
		if lastStoredBlock < latestBlock {
			gap := latestBlock - lastStoredBlock
			log.Printf("📥 공백 발견: #%d ~ #%d (총 %d개 블록 동기화 필요)", lastStoredBlock+1, latestBlock, gap)

			for i := lastStoredBlock + 1; i <= latestBlock; i++ {
				err := fetchAndSaveBlock(client, db, i)
				if err != nil {
					log.Printf("⚠️ 블록 #%d 동기화 중 오류 발생: %v", i, err)
					// 오류 발생 시 다음 루프에서 해당 번호부터 다시 시도하도록 break
					break
				}
				// RPC 노드의 Rate Limit을 고려해 아주 짧은 휴식 (옵션)
				time.Sleep(500 * time.Millisecond)
			}
		}

		// 4. [이론: 실시간 대기] 다음 블록 생성을 기다림 (이더리움 약 12초 주기)
		time.Sleep(12 * time.Second)
	}
}

func fetchAndSaveBlock(client *ethclient.Client, db *gorm.DB, blockNum uint64) error {
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

		if newTx.EthValue > 100 { // 100 ETH 이상 고액 거래
			log.Printf("🐋 고래 출현! [Hash: %s] [Value: %.2f ETH]", newTx.Hash, newTx.EthValue)
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
