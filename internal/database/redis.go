package database

import (
	"context"
	"os"

	"github.com/redis/go-redis/v9"
)

func ConnectRedis() (*redis.Client, error) {
	opts := &redis.Options{Addr: "localhost:6379"}
	if url := os.Getenv("REDIS_URL"); url != "" {
		parsed, err := redis.ParseURL(url)
		if err != nil {
			return nil, err
		}
		opts = parsed
	}

	client := redis.NewClient(opts)

	_, err := client.Ping(context.Background()).Result()
	if err != nil {
		return nil, err
	}

	return client, nil
}