# ── Étape 1 : compilation ─────────────────────────────────────────────────────
FROM golang:1.23-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o server .

# ── Étape 2 : image finale (Alpine minimal) ───────────────────────────────────
FROM alpine:3.20

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

COPY --from=builder /app/server .

ENV PORT=3000
ENV NODE_ENV=production

EXPOSE 3000

CMD ["./server"]
