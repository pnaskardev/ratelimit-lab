package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

var redisClient *redis.Client = nil

func InitCache() error {

	rdb := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "", // no password
		DB:       0,  // use default DB
		Protocol: 2,
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(time.Second*5))
	defer cancel()

	// Check By Pinging the instance
	_, err := rdb.Ping(ctx).Result()
	if err != nil {
		return err
	}

	redisClient = rdb

	return nil
}
