package server

import (
	"github.com/xxl6097/go-thousand-hub/internal/server"
	"github.com/xxl6097/go-thousand-hub/pkg/server/m"
)

func RunServer(cfg *m.Config) error {
	s, err := server.New(cfg)
	if err != nil {
		return err
	}
	return s.Run()
}
