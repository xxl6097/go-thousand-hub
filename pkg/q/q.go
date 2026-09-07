package q

import (
	"context"
	"errors"
	"log"

	"github.com/xxl6097/go-thousand-hub/internal/agent"
	"github.com/xxl6097/go-thousand-hub/pkg/qjt"
)

func NewQJT(opts *qjt.Options, ctx context.Context) error {
	if opts == nil {
		return errors.New("opts is nil")
	}
	if err := agent.New(*opts).Run(ctx); err != nil {
		log.Printf("agent 退出: %v", err)
		return err
	}
	return nil
}
