FROM golang:1.19-alpine

RUN apk add --no-cache \
    gcc \
    musl-dev \
    pkgconfig \
    openssl-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o mattermost-vote-bot .

CMD ["./mattermost-vote-bot"]