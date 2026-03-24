package noop

import (
	"context"

	"github.com/chenhg5/cc-connect/core"
)

func init() {
	core.RegisterPlatform("noop", New)
}

type Platform struct{}

func New(opts map[string]any) (core.Platform, error) {
	_ = opts
	return &Platform{}, nil
}

func (p *Platform) Name() string { return "noop" }

func (p *Platform) Start(handler core.MessageHandler) error {
	_ = handler
	return nil
}

func (p *Platform) Reply(ctx context.Context, replyCtx any, content string) error {
	_ = ctx
	_ = replyCtx
	_ = content
	return nil
}

func (p *Platform) Send(ctx context.Context, replyCtx any, content string) error {
	_ = ctx
	_ = replyCtx
	_ = content
	return nil
}

func (p *Platform) Stop() error { return nil }
