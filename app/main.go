package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/tarantool/go-tarantool"
)

func main() {
	addr := os.Getenv("TARANTOOL_ADDR")
	if addr == "" {
		addr = "tarantool:3301"
	}

	conn, err := tarantool.Connect(addr, tarantool.Opts{
		User:    "guest",
		Timeout: 5 * time.Second,
	})
	if err != nil {
		log.Fatalf("Connection error: %v", err)
	}
	defer conn.Close()

	// Тестовые операции
	_, err = conn.Insert("users", []interface{}{1, "Alice"})
	if err != nil {
		log.Printf("Insert error: %v", err)
	}

	resp, err := conn.Select("users", "primary", 0, 1, tarantool.IterEq, []interface{}{1})
	if err != nil {
		log.Printf("Select error: %v", err)
	} else {
		fmt.Printf("User data: %+v\n", resp.Data)
	}

	// Бесконечный цикл для поддержания работы контейнера
	for {
		time.Sleep(10 * time.Second)
	}
}
