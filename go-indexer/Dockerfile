FROM golang:1.21-alpine AS builder

# 빌드에 필요한 도구 설치
RUN apk add --no-cache gcc musl-dev linux-headers

WORKDIR /app
COPY . .
RUN go mod init eth-indexer && go mod tidy
# RUN go build -o indexer . # 실제 코드 작성 후 활성화

CMD ["go", "run", "main.go"]
