# 🚀 Eth Mini Indexer (2026)

> **Real-time Ethereum Block Explorer & Data Pipeline built with Go.**

이 프로젝트는 이더리움 메인넷의 데이터를 실시간으로 수집하고, 관계형 데이터베이스에 인덱싱하여 사용자에게 직관적인 대시보드를 제공하는 풀스택 데이터 인덱서입니다.

[](https://go.dev/)
[](https://www.docker.com/)
[](https://opensource.org/licenses/MIT)

-----

## ✨ Key Features

  * **Real-time Indexing:** 12초마다 생성되는 이더리움 새 블록을 감지하고 즉시 데이터를 추출합니다.
  * **Automatic Back-filling:** 시스템 중단 시 발생한 블록 공백(Gap)을 재시작 시 자동으로 감지하여 동기화합니다.
  * **Transaction Sender Recovery:** 서명 데이터(v, r, s)로부터 `From` 주소를 복구하는 최신 고성능 로직(`types.Sender`)을 포함합니다.
  * **WebSocket Live Alerts:** 새 블록이 발견되면 브라우저 새로고침 없이 즉시 알림을 푸시합니다.
  * **Human-readable UI:** Wei 단위를 ETH로 자동 변환하고 Checksum 주소 형식을 지원하는 대시보드를 제공합니다.

-----

## 🏗️ Architecture

1.  **Ingestion:** Infura RPC를 통해 이더리움 메인넷 데이터 수집.
2.  **Processing:** Go-Ethereum 라이브러리를 사용한 데이터 가공 및 서명 복구.
3.  **Persistence:** SQLite3를 활용한 트랜잭션 데이터 영구 저장 (GORM 활용).
4.  **Serving:** Gin Gonic 프레임워크 기반의 RESTful API 서빙.
5.  **Visualization:** Tailwind CSS 및 Vanilla JS 기반의 실시간 모니터링 대시보드.

-----

## 🛠️ Tech Stack

  * **Backend:** Golang (1.21+)
  * **Database:** SQLite 3 (with GORM)
  * **API:** Gin Gonic
  * **Frontend:** HTML5, Tailwind CSS, JavaScript (Vanilla)
  * **Infrastructure:** Docker, Docker-Compose

-----

## 🚀 Getting Started

### Prerequisites

  * [Docker](https://www.docker.com/) & [Docker Compose](https://docs.docker.com/compose/)
  * [Infura](https://www.infura.io/) API Key (Ethereum Mainnet)

### Installation & Run

1.  **Repository Clone**

    ```bash
    git clone https://github.com/your-username/eth-mini-indexer.git
    cd eth-mini-indexer
    ```

2.  **Environment Setup**
    `docker-compose.yml` 파일의 `ETH_RPC_URL` 환경변수에 본인의 Infura 키를 입력합니다.

    ```yaml
    ETH_RPC_URL: https://mainnet.infura.io/v3/YOUR_INFURA_KEY
    ```

3.  **Docker Up**

    ```bash
    docker-compose up -d --build
    ```

4.  **Access**

      * **Dashboard:** `http://localhost:3000`
      * **API Endpoints:** `http://localhost:8080/api/transactions`

-----

## 📝 Troubleshooting Records

### 1\. 401 Unauthorized (RPC Connection)

  * **Issue:** Infura 연결 시 인증 에러 발생.
  * **Fix:** Docker 환경변수 주입 형식을 수정하고, `ethclient.Dial` 시 URL 포맷을 검증하여 해결했습니다.

### 2\. Transaction Sender Recovery Issues

  * **Issue:** 최신 `go-ethereum` 버전에서 `AsMessage` 메서드 삭제됨.
  * **Fix:** `types.Sender`와 `types.LatestSignerForChainID`를 활용하여 더 빠르고 정확한 주소 복구 로직으로 업데이트했습니다.

-----

## 🤝 Contributing

이 프로젝트는 교육적 목적으로 제작되었습니다. 새로운 기능 제안이나 버그 리포트는 언제나 환영합니다\!