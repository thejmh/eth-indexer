# 1. 빌드 스테이지
FROM golang:1.25-alpine AS builder

# SQLite 빌드를 위한 필수 도구 설치
RUN apk add --no-cache gcc musl-dev

WORKDIR /app

# [핵심] 로컬에 있는 장부 파일들을 먼저 복사합니다.
# 이미 로컬에 go.mod가 있으므로 여기서 init을 할 필요가 없습니다.
COPY go.mod go.sum ./

# 장부에 적힌 패키지들을 다운로드합니다.
RUN go mod download

# 나머지 소스 코드를 복사합니다.
COPY . .

# 빌드 실행
RUN go build -o main .

# 2. 실행 스테이지
FROM alpine:latest
RUN apk add --no-cache ca-certificates libc6-compat

WORKDIR /root/
# 빌드된 결과물만 가져옵니다.
COPY --from=builder /app/main .

# 실행
CMD ["./main"]
