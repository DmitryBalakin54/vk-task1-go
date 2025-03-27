FROM golang:1.24-alpine

WORKDIR /app

# Устанавливаем зависимости для сборки
RUN apk add --no-cache git gcc musl-dev pkgconfig openssl-dev

# Сначала копируем только файлы модулей
COPY go.mod go.sum ./

# Скачиваем зависимости
RUN go mod download

# Копируем остальные файлы
COPY . .

# Собираем приложение
RUN go mod tidy && go build -o main .

CMD ["./main"]